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
	observabilitytools "github.com/lijunsheng/orderguard/internal/mcptools/observability"
	"github.com/redis/go-redis/v9"
)

// main 启动可观测性 MCP Server。
func main() {
	ctx := context.Background()
	pool, err := database.OpenPostgres(ctx, config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: config.RedisAddress()})
	defer redisClient.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		log.Fatal(err)
	}
	profile := mcp.Profiles()["observability"]
	store := observabilitytools.NewStore(pool, redisClient, config.EventStream())
	server := mcp.NewServer(
		profile, slog.Default(), mcp.WithToolHandlers(store.Handlers()),
		mcp.WithAuditor(mcpaudit.NewPostgresAuditor(pool)), mcp.WithToolTimeout(3*time.Second),
	)
	app.RunHTTP(profile.Name, ":8080", server.Handler())
}
