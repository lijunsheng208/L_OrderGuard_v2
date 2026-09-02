package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type handlerTransport struct {
	handler http.Handler
}

type memoryAuditor struct {
	mu      sync.Mutex
	records []ToolCallAudit
}

// RoundTrip 在内存中把客户端请求交给 MCP HTTP Handler。
func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

// Record 在线程安全的内存切片中保存审计记录。
func (auditor *memoryAuditor) Record(_ context.Context, record ToolCallAudit) error {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	auditor.records = append(auditor.records, record)
	return nil
}

// snapshot 返回当前审计记录副本。
func (auditor *memoryAuditor) snapshot() []ToolCallAudit {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	return append([]ToolCallAudit(nil), auditor.records...)
}

// TestClientDiscoversAndCallsReadTool 验证阶段 0 的端到端 MCP 验收路径。
func TestClientDiscoversAndCallsReadTool(t *testing.T) {
	profile := Profiles()["business"]
	handler := NewServer(
		profile,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithToolHandler("get_order_snapshot", func(
			_ context.Context,
			arguments map[string]any,
		) (ToolOutput, error) {
			return ToolOutput{
				Data:   map[string]any{"order_id": arguments["order_id"], "status": "PAID"},
				Source: "order-service", EvidencePrefix: "order",
			}, nil
		}),
	).Handler()

	client := NewClient("http://mcp.local/mcp", &http.Client{
		Timeout:   time.Second,
		Transport: handlerTransport{handler: handler},
	})
	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("tool count = %d, want 4", len(tools))
	}

	result, err := client.CallTool(ctx, "get_order_snapshot", map[string]any{
		"order_id": "O1001",
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.StructuredContent.Success || result.IsError {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.StructuredContent.EvidenceID == "" {
		t.Fatal("evidence_id is empty")
	}
}

// TestToolTimeoutMapsToStableError 验证 Handler 超时返回工具层错误而非 JSON-RPC 错误。
func TestToolTimeoutMapsToStableError(t *testing.T) {
	server := NewServer(
		Profiles()["business"], slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithToolTimeout(10*time.Millisecond),
		WithToolHandler("get_order_snapshot", func(
			ctx context.Context,
			_ map[string]any,
		) (ToolOutput, error) {
			<-ctx.Done()
			return ToolOutput{}, ctx.Err()
		}),
	)
	result := callTestTool(t, server, "get_order_snapshot")
	if !result.IsError || result.StructuredContent.Error.Code != "UPSTREAM_TIMEOUT" {
		t.Fatalf("unexpected timeout result: %+v", result)
	}
}

// TestExecutionErrorAndAudit 验证稳定错误映射并审计成功和失败调用。
func TestExecutionErrorAndAudit(t *testing.T) {
	auditor := &memoryAuditor{}
	server := NewServer(
		Profiles()["business"], slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuditor(auditor),
		WithToolHandler("get_order_snapshot", func(
			_ context.Context,
			_ map[string]any,
		) (ToolOutput, error) {
			return ToolOutput{Data: map[string]any{"status": "PAID"}}, nil
		}),
		WithToolHandler("get_payment_status", func(
			_ context.Context,
			_ map[string]any,
		) (ToolOutput, error) {
			return ToolOutput{}, &ExecutionError{Code: "NOT_FOUND", Message: "payment not found"}
		}),
	)
	success := callTestTool(t, server, "get_order_snapshot")
	failure := callTestTool(t, server, "get_payment_status")
	if !success.StructuredContent.Success || !failure.IsError {
		t.Fatalf("success=%+v failure=%+v", success, failure)
	}
	if failure.StructuredContent.Error.Code != "NOT_FOUND" {
		t.Fatalf("error code = %s", failure.StructuredContent.Error.Code)
	}
	records := auditor.snapshot()
	if len(records) != 2 || !records[0].Envelope.Success || records[1].Envelope.Success {
		t.Fatalf("unexpected audit records: %+v", records)
	}
}

// TestAuditReceivesRuntimeMetadata 验证 Runtime Header 被传入 MCP 调用审计。
func TestAuditReceivesRuntimeMetadata(t *testing.T) {
	auditor := &memoryAuditor{}
	server := NewServer(
		Profiles()["business"], slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuditor(auditor),
		WithToolHandler("get_order_snapshot", func(
			_ context.Context,
			_ map[string]any,
		) (ToolOutput, error) {
			return ToolOutput{Data: map[string]any{"status": "PAID"}}, nil
		}),
	)
	client := NewClient("http://mcp.local/mcp", &http.Client{
		Transport: handlerTransport{handler: server.Handler()},
	})
	_, err := client.CallToolWithMetadata(
		context.Background(), "get_order_snapshot", map[string]any{"order_id": "O1001"},
		RequestMetadata{
			RunID: "run-1", AgentStepID: "step-1", TraceID: "trace-1",
			ToolCallID: "call-1", Caller: "investigator-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	records := auditor.snapshot()
	if len(records) != 1 || records[0].Metadata.ToolCallID != "call-1" ||
		records[0].Metadata.RunID != "run-1" {
		t.Fatalf("unexpected audit metadata: %+v", records)
	}
}

// callTestTool 在内存 HTTP Server 上调用一个订单参数工具。
func callTestTool(t *testing.T, server *Server, toolName string) ToolCallResult {
	t.Helper()
	client := NewClient("http://mcp.local/mcp", &http.Client{
		Transport: handlerTransport{handler: server.Handler()},
	})
	result, err := client.CallTool(context.Background(), toolName, map[string]any{"order_id": "O1001"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestProfilesKeepWriteToolIsolated 验证受控写工具不泄漏到只读 Server。
func TestProfilesKeepWriteToolIsolated(t *testing.T) {
	profiles := Profiles()
	for key, profile := range profiles {
		for _, tool := range profile.Tools {
			if key == "remediation" && tool.Annotations.ReadOnlyHint {
				t.Fatalf("remediation tool %q marked read-only", tool.Name)
			}
			if key != "remediation" && !tool.Annotations.ReadOnlyHint {
				t.Fatalf("tool %q in %s is not read-only", tool.Name, key)
			}
		}
	}
	if len(profiles["remediation"].Tools) != 1 {
		t.Fatal("remediation-mcp must expose exactly one phase 0 tool")
	}
}

// TestValidateArgumentsRejectsInvalidInput 验证输入 Schema 的必填、额外字段和嵌套类型约束。
func TestValidateArgumentsRejectsInvalidInput(t *testing.T) {
	businessTool := Profiles()["business"].Tools[0]
	if err := validateArguments(businessTool, map[string]any{}); err == nil {
		t.Fatal("missing order_id was accepted")
	}
	if err := validateArguments(businessTool, map[string]any{
		"order_id": "O1001", "extra": true,
	}); err == nil {
		t.Fatal("additional field was accepted")
	}

	remediationTool := Profiles()["remediation"].Tools[0]
	err := validateArguments(remediationTool, map[string]any{
		"order_id":         "O1001",
		"items":            []any{map[string]any{"sku_id": "SKU-1", "quantity": 0.0}},
		"idempotency_key":  "idem-1",
		"evidence_version": "v1",
	})
	if err == nil {
		t.Fatal("zero quantity was accepted")
	}
}
