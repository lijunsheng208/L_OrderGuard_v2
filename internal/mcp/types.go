package mcp

import (
	"context"
	"encoding/json"
	"time"
)

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

// ToolOutput 是工具 Handler 返回的原始数据和证据来源。
type ToolOutput struct {
	Data           any
	Source         string
	EvidencePrefix string
}

// ToolHandler 执行一个已通过 Schema 校验的 MCP 工具。
type ToolHandler func(context.Context, map[string]any) (ToolOutput, error)

// ExecutionError 表示工具执行阶段的稳定业务错误。
type ExecutionError struct {
	Code      string
	Message   string
	Retryable bool
	Details   map[string]any
}

// Error 返回工具执行错误信息。
func (e *ExecutionError) Error() string {
	return e.Message
}

// ToolCallAudit 保存一次 MCP 工具执行的审计数据。
type ToolCallAudit struct {
	RequestID  string
	ServerName string
	ToolName   string
	Metadata   RequestMetadata
	Arguments  map[string]any
	Envelope   Envelope
	Duration   time.Duration
	CalledAt   time.Time
}

// RequestMetadata 保存 Runtime 传递给 MCP Server 的调用关联信息。
type RequestMetadata struct {
	RunID       string
	AgentStepID string
	TraceID     string
	ToolCallID  string
	Caller      string
}

// Auditor 定义 MCP 工具调用审计写入能力。
type Auditor interface {
	Record(context.Context, ToolCallAudit) error
}
