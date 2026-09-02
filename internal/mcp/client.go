package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Client 是用于阶段 0 验收的轻量 MCP Streamable HTTP 客户端。
type Client struct {
	endpoint   string
	httpClient *http.Client
	nextID     int
}

// NewClient 创建 MCP 客户端。
func NewClient(endpoint string, httpClient *http.Client) *Client {
	return &Client{endpoint: endpoint, httpClient: httpClient, nextID: 1}
}

// Initialize 完成 MCP 初始化握手。
func (c *Client) Initialize(ctx context.Context) error {
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "orderguard-smoke-client", "version": "0.1.0"},
	}, &result)
	if err != nil {
		return err
	}
	if result.ProtocolVersion == "" {
		return fmt.Errorf("server returned an empty protocol version")
	}
	return nil
}

// ListTools 发现 Server 暴露的工具。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool 调用指定 MCP 工具。
func (c *Client) CallTool(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (ToolCallResult, error) {
	var result ToolCallResult
	err := c.call(ctx, "tools/call", ToolCallParams{
		Name: name, Arguments: arguments,
	}, &result)
	return result, err
}

// call 发送一次 JSON-RPC 请求并解析响应。
func (c *Client) call(ctx context.Context, method string, params any, target any) error {
	id := c.nextID
	c.nextID++
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()

	var rpcResponse struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&rpcResponse); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if rpcResponse.Error != nil {
		return fmt.Errorf("mcp error %d: %s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}
	if err := json.Unmarshal(rpcResponse.Result, target); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}
