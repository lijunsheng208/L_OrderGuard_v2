package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Server 实现阶段 0 所需的 MCP Streamable HTTP 端点。
type Server struct {
	profile ServerProfile
	logger  *slog.Logger
}

// NewServer 创建一个使用指定工具配置的 MCP Server。
func NewServer(profile ServerProfile, logger *slog.Logger) *Server {
	return &Server{profile: profile, logger: logger}
}

// Handler 返回 MCP Server 的 HTTP 路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /mcp", s.handleMCP)
	return mux
}

// handleHealth 返回服务存活信息。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "service": s.profile.Name,
	})
}

// handleMCP 解析并分发 JSON-RPC 请求。
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var request Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&request); err != nil {
		s.writeRPCError(w, nil, -32700, "parse error", nil)
		return
	}

	if request.JSONRPC != "2.0" || request.Method == "" {
		s.writeRPCError(w, request.ID, -32600, "invalid request", nil)
		return
	}

	if len(request.ID) == 0 {
		// MCP 通知没有响应体。
		w.WriteHeader(http.StatusAccepted)
		return
	}

	s.logger.Info("mcp request", "server", s.profile.Name, "method", request.Method)
	s.dispatch(w, request)
}

// dispatch 执行 MCP 方法并生成协议响应。
func (s *Server) dispatch(w http.ResponseWriter, request Request) {
	switch request.Method {
	case "initialize":
		s.writeResult(w, request.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]string{
				"name": s.profile.Name, "version": s.profile.Version,
			},
		})
	case "ping":
		s.writeResult(w, request.ID, map[string]any{})
	case "tools/list":
		s.writeResult(w, request.ID, map[string]any{"tools": s.profile.Tools})
	case "tools/call":
		s.handleToolCall(w, request)
	default:
		s.writeRPCError(w, request.ID, -32601, "method not found", nil)
	}
}

// handleToolCall 校验工具参数并返回阶段 0 契约结果。
func (s *Server) handleToolCall(w http.ResponseWriter, request Request) {
	var params ToolCallParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.writeRPCError(w, request.ID, -32602, "invalid params", nil)
		return
	}

	tool, ok := s.findTool(params.Name)
	if !ok {
		s.writeRPCError(w, request.ID, -32602, "unknown tool", map[string]any{"tool": params.Name})
		return
	}
	if err := validateArguments(tool, params.Arguments); err != nil {
		s.writeRPCError(w, request.ID, -32602, "invalid tool arguments", map[string]any{
			"tool": params.Name, "reason": err.Error(),
		})
		return
	}

	envelope := s.executeContractStub(tool, params.Arguments)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		s.writeRPCError(w, request.ID, -32603, "internal error", nil)
		return
	}

	s.writeResult(w, request.ID, ToolCallResult{
		Content:           []Content{{Type: "text", Text: string(encoded)}},
		StructuredContent: envelope,
		IsError:           !envelope.Success,
	})
}

// executeContractStub 执行阶段 0 唯一的冒烟查询，其余工具明确返回未实现。
func (s *Server) executeContractStub(tool Tool, arguments map[string]any) Envelope {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if s.profile.Name == "business-mcp" && tool.Name == "get_order_snapshot" {
		return Envelope{
			Success: true,
			Data: map[string]any{
				"order_id": arguments["order_id"],
				"status":   "UNKNOWN",
				"phase":    "CONTRACT_ONLY",
			},
			EvidenceID:  newEvidenceID("order"),
			Source:      "business-mcp-contract",
			CollectedAt: now,
		}
	}

	return Envelope{
		Success: false,
		Error: &ToolError{
			Code:      "PHASE_NOT_IMPLEMENTED",
			Message:   "tool implementation is outside phase 0",
			Retryable: false,
			Details:   map[string]any{"tool": tool.Name},
		},
		Source:      s.profile.Name,
		CollectedAt: now,
	}
}

// findTool 按名称查找当前 Server 暴露的工具。
func (s *Server) findTool(name string) (Tool, bool) {
	for _, tool := range s.profile.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

// validateArguments 校验阶段 0 Schema 中冻结的必填字段和基础类型。
func validateArguments(tool Tool, arguments map[string]any) error {
	if arguments == nil {
		arguments = map[string]any{}
	}
	return validateObject("arguments", arguments, tool.InputSchema)
}

// validateValue 递归校验当前契约使用的 JSON Schema 类型。
func validateValue(name string, value any, definition map[string]any) error {
	switch definition["type"] {
	case "string":
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("field %q must be a non-empty string", name)
		}
		if definition["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339, text); err != nil {
				return fmt.Errorf("field %q must be an RFC 3339 timestamp", name)
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			return fmt.Errorf("field %q must be a non-empty array", name)
		}
		itemSchema, _ := definition["items"].(map[string]any)
		for index, item := range items {
			if err := validateValue(fmt.Sprintf("%s[%d]", name, index), item, itemSchema); err != nil {
				return err
			}
		}
	case "integer":
		number, ok := value.(float64)
		minimum, _ := definition["minimum"].(int)
		if !ok || number != float64(int64(number)) || number < float64(minimum) {
			return fmt.Errorf("field %q must be an integer", name)
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("field %q must be an object", name)
		}
		return validateObject(name, object, definition)
	}
	return nil
}

// validateObject 校验对象字段、必填项和额外字段限制。
func validateObject(name string, object map[string]any, schema map[string]any) error {
	properties := schemaProperties(schema)
	for fieldName, value := range object {
		definition, ok := properties[fieldName]
		if !ok {
			return fmt.Errorf("unexpected field %q", fieldName)
		}
		fieldSchema, _ := definition.(map[string]any)
		if err := validateValue(fieldName, value, fieldSchema); err != nil {
			return err
		}
	}

	required, _ := schema["required"].([]string)
	for _, fieldName := range required {
		if _, ok := object[fieldName]; !ok {
			return fmt.Errorf("missing required field %q in %s", fieldName, name)
		}
	}
	return nil
}

// schemaProperties 兼容内部命名类型并返回 Schema 字段定义。
func schemaProperties(schema map[string]any) map[string]any {
	switch properties := schema["properties"].(type) {
	case fields:
		return map[string]any(properties)
	case map[string]any:
		return properties
	default:
		return map[string]any{}
	}
}

// newEvidenceID 创建无需外部依赖的证据标识。
func newEvidenceID(prefix string) string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("ev-%s-%d", prefix, time.Now().UnixNano())
	}
	return "ev-" + prefix + "-" + hex.EncodeToString(random)
}

// writeResult 写入成功的 JSON-RPC 响应。
func (s *Server) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, Response{JSONRPC: "2.0", ID: id, Result: result})
}

// writeRPCError 写入失败的 JSON-RPC 响应。
func (s *Server) writeRPCError(
	w http.ResponseWriter,
	id json.RawMessage,
	code int,
	message string,
	data any,
) {
	writeJSON(w, http.StatusOK, Response{
		JSONRPC: "2.0", ID: id,
		Error: &RPCError{Code: code, Message: message, Data: data},
	})
}

// writeJSON 写入 JSON HTTP 响应。
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.Error("write response", "error", err)
	}
}
