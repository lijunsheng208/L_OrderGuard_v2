package inventory

import "testing"

// TestNewHandlerRegistersRoutes 验证库存路由不存在 ServeMux 模式冲突。
func TestNewHandlerRegistersRoutes(t *testing.T) {
	handler := NewHandler(nil)
	if handler == nil {
		t.Fatal("handler is nil")
	}
}
