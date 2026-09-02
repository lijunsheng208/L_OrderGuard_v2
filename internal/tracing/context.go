package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type contextKey struct{}

// Middleware 接收或生成 trace_id，并将其写入请求上下文和响应头。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		traceID := strings.TrimSpace(request.Header.Get("X-Trace-ID"))
		if traceID == "" {
			traceID = NewID()
		}
		w.Header().Set("X-Trace-ID", traceID)
		ctx := context.WithValue(request.Context(), contextKey{}, traceID)
		next.ServeHTTP(w, request.WithContext(ctx))
	})
}

// ID 返回上下文中的 trace_id，不存在时生成一个新值。
func ID(ctx context.Context) string {
	if value, ok := ctx.Value(contextKey{}).(string); ok && value != "" {
		return value
	}
	return NewID()
}

// NewID 生成本地可追踪的 trace_id。
func NewID() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "trace-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "trace-" + hex.EncodeToString(random)
}
