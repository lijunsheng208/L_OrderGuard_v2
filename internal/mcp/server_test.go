package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type handlerTransport struct {
	handler http.Handler
}

// RoundTrip 在内存中把客户端请求交给 MCP HTTP Handler。
func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

// TestClientDiscoversAndCallsReadTool 验证阶段 0 的端到端 MCP 验收路径。
func TestClientDiscoversAndCallsReadTool(t *testing.T) {
	profile := Profiles()["business"]
	handler := NewServer(
		profile,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
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
