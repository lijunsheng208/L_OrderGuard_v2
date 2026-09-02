package main

import (
	"log/slog"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

// main 启动可观测性 MCP Server。
func main() {
	profile := mcp.Profiles()["observability"]
	server := mcp.NewServer(profile, slog.Default())
	app.RunHTTP(profile.Name, ":8080", server.Handler())
}
