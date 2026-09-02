package main

import (
	"log/slog"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/mcp"
	remediationtools "github.com/lijunsheng/orderguard/internal/mcptools/remediation"
)

// main 启动受控修复 MCP Server。
func main() {
	profile := mcp.Profiles()["remediation"]
	server := mcp.NewServer(profile, slog.Default(), mcp.WithToolHandlers(remediationtools.Handlers(config.InventoryServiceURL())))
	app.RunHTTP(profile.Name, ":8080", server.Handler())
}
