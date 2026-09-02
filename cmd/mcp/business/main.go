package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/database"
	"github.com/lijunsheng/orderguard/internal/mcp"
	"github.com/lijunsheng/orderguard/internal/mcpaudit"
	businesstools "github.com/lijunsheng/orderguard/internal/mcptools/business"
)

// main 启动业务查询 MCP Server。
func main() {
	pool, err := database.OpenPostgres(context.Background(), config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	profile := mcp.Profiles()["business"]
	handlers := businesstools.Handlers(businesstools.Config{
		OrderServiceURL: config.OrderServiceURL(), PaymentServiceURL: config.PaymentServiceURL(),
		InventoryServiceURL: config.InventoryServiceURL(),
	})
	server := mcp.NewServer(
		profile, slog.Default(), mcp.WithToolHandlers(handlers),
		mcp.WithAuditor(mcpaudit.NewPostgresAuditor(pool)), mcp.WithToolTimeout(2*time.Second),
	)
	app.RunHTTP(profile.Name, ":8080", server.Handler())
}
