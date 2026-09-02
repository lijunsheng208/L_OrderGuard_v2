package mcp

import "encoding/json"

const ProtocolVersion = "2025-06-18"

// Request 表示 MCP 使用的 JSON-RPC 请求。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 表示 MCP 使用的 JSON-RPC 响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 表示协议层错误。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool 描述一个可发现的 MCP 工具及其输入契约。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"inputSchema"`
	Annotations ToolAnnotations `json:"annotations"`
}

// ToolAnnotations 声明工具的读写与幂等属性。
type ToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
}

// ToolCallParams 表示 tools/call 的参数。
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolCallResult 表示标准 MCP 工具调用结果。
type ToolCallResult struct {
	Content           []Content `json:"content"`
	StructuredContent Envelope  `json:"structuredContent"`
	IsError           bool      `json:"isError,omitempty"`
}

// Content 表示 MCP 返回的内容块。
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Envelope 是 OrderGuard 所有工具共享的业务返回结构。
type Envelope struct {
	Success     bool       `json:"success"`
	Data        any        `json:"data,omitempty"`
	Error       *ToolError `json:"error,omitempty"`
	EvidenceID  string     `json:"evidence_id,omitempty"`
	Source      string     `json:"source"`
	CollectedAt string     `json:"collected_at"`
}

// ToolError 表示稳定、可供调用方判断的工具错误。
type ToolError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}
