package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/database"
	"github.com/lijunsheng/orderguard/internal/knowledge"
	"github.com/lijunsheng/orderguard/internal/mcp"
	"github.com/lijunsheng/orderguard/internal/mcpaudit"
	knowledgetools "github.com/lijunsheng/orderguard/internal/mcptools/knowledge"
)

// main 启动知识检索 MCP Server。
func main() {
	db, err := database.OpenPostgres(context.Background(), config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	store, err := knowledge.NewStore()
	if err != nil {
		log.Fatal(err)
	}
	profile := mcp.Profiles()["knowledge"]
	server := mcp.NewServer(profile, slog.Default(), mcp.WithToolHandlers(knowledgetools.StoreHandlers(store)), mcp.WithAuditor(mcpaudit.NewPostgresAuditor(db)), mcp.WithToolTimeout(2*time.Second))
	app.RunHTTP(profile.Name, ":8080", server.Handler())
}
