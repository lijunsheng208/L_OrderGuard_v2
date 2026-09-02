package business

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip 调用测试定义的 HTTP 响应函数。
func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// TestHandlersReadBusinessServices 验证四个工具调用正确的服务只读接口。
func TestHandlersReadBusinessServices(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"path":"` + request.URL.Path + `"}`)),
		}, nil
	})}
	handlers := Handlers(Config{
		OrderServiceURL: "http://service.local", PaymentServiceURL: "http://service.local",
		InventoryServiceURL: "http://service.local", HTTPClient: client,
	})
	wants := map[string]string{
		"get_order_snapshot":      "/orders/O-1/snapshot",
		"get_payment_status":      "/payments/O-1",
		"get_inventory_status":    "/inventory/O-1/status",
		"get_order_state_history": "/orders/O-1/history",
	}
	for name, wantPath := range wants {
		output, err := handlers[name](context.Background(), map[string]any{"order_id": "O-1"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data := output.Data.(map[string]any)
		if data["path"] != wantPath {
			t.Fatalf("%s path = %v, want %s", name, data["path"], wantPath)
		}
	}
}

// TestHandlerMapsNotFound 验证业务服务 404 映射为 NOT_FOUND。
func TestHandlerMapsNotFound(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"code":"ORDER_NOT_FOUND"}`)),
		}, nil
	})}
	handler := Handlers(Config{
		OrderServiceURL: "http://service.local", HTTPClient: client,
	})["get_order_snapshot"]
	_, err := handler(context.Background(), map[string]any{"order_id": "missing"})
	var executionErr *mcp.ExecutionError
	if !errors.As(err, &executionErr) || executionErr.Code != "NOT_FOUND" {
		t.Fatalf("unexpected error: %v", err)
	}
}
