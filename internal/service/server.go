package service

import (
	"encoding/json"
	"net/http"
)

// Handler 返回阶段 0 业务服务的健康检查与信息路由。
func Handler(name string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok", "service": name, "phase": "skeleton",
		})
	})
	return mux
}
