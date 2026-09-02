package business

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/lijunsheng/orderguard/internal/httpx"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

const maxResponseBytes = 1 << 20

// Config 保存业务 MCP 访问三个业务服务所需的依赖。
type Config struct {
	OrderServiceURL     string
	PaymentServiceURL   string
	InventoryServiceURL string
	HTTPClient          *http.Client
}

// Handlers 创建四个只读业务工具 Handler。
func Handlers(config Config) map[string]mcp.ToolHandler {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return map[string]mcp.ToolHandler{
		"get_order_snapshot": serviceHandler(
			client, config.OrderServiceURL, "/orders/%s/snapshot", "order-service", "order",
		),
		"get_payment_status": serviceHandler(
			client, config.PaymentServiceURL, "/payments/%s", "payment-service", "payment",
		),
		"get_inventory_status": serviceHandler(
			client, config.InventoryServiceURL, "/inventory/%s/status", "inventory-service", "inventory",
		),
		"get_order_state_history": serviceHandler(
			client, config.OrderServiceURL, "/orders/%s/history", "order-service", "order-history",
		),
	}
}

// serviceHandler 创建按 order_id 调用业务服务的通用 Handler。
func serviceHandler(
	client *http.Client,
	baseURL string,
	pathFormat string,
	source string,
	prefix string,
) mcp.ToolHandler {
	return func(ctx context.Context, arguments map[string]any) (mcp.ToolOutput, error) {
		orderID := arguments["order_id"].(string)
		endpoint := strings.TrimRight(baseURL, "/") + fmt.Sprintf(
			pathFormat, url.PathEscape(orderID),
		)
		data, err := getJSON(ctx, client, endpoint)
		if err != nil {
			return mcp.ToolOutput{}, err
		}
		return mcp.ToolOutput{Data: data, Source: source, EvidencePrefix: prefix}, nil
	}
}

// getJSON 请求有界 JSON 响应并映射上游错误。
func getJSON(ctx context.Context, client *http.Client, endpoint string) (any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &mcp.ExecutionError{Code: "INTERNAL", Message: "cannot create upstream request"}
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, &mcp.ExecutionError{
			Code: "UPSTREAM_UNAVAILABLE", Message: "business service is unavailable", Retryable: true,
		}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &mcp.ExecutionError{
			Code: "UPSTREAM_UNAVAILABLE", Message: "cannot read business service response", Retryable: true,
		}
	}
	if len(body) > maxResponseBytes {
		return nil, &mcp.ExecutionError{Code: "UPSTREAM_INVALID_RESPONSE", Message: "business service response is too large"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, mapHTTPError(response.StatusCode, body)
	}
	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, &mcp.ExecutionError{Code: "UPSTREAM_INVALID_RESPONSE", Message: "business service returned invalid JSON"}
	}
	return result, nil
}

// mapHTTPError 将业务 HTTP 状态映射为稳定的 MCP 工具错误。
func mapHTTPError(status int, body []byte) error {
	var upstream httpx.ErrorResponse
	_ = json.Unmarshal(body, &upstream)
	details := map[string]any{"http_status": status}
	if upstream.Code != "" {
		details["upstream_code"] = upstream.Code
	}
	switch status {
	case http.StatusNotFound:
		return &mcp.ExecutionError{Code: "NOT_FOUND", Message: "requested business record was not found", Details: details}
	case http.StatusBadRequest:
		return &mcp.ExecutionError{Code: "INVALID_ARGUMENT", Message: "business service rejected the query", Details: details}
	default:
		return &mcp.ExecutionError{
			Code: "UPSTREAM_UNAVAILABLE", Message: "business service query failed",
			Retryable: status >= http.StatusInternalServerError, Details: details,
		}
	}
}
