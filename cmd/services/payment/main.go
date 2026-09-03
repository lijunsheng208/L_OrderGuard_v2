package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/database"
	"github.com/lijunsheng/orderguard/internal/eventbus"
	"github.com/lijunsheng/orderguard/internal/payment"
	"github.com/redis/go-redis/v9"
)

// main 启动支付服务骨架。
func main() {
	pool, err := database.OpenPostgres(context.Background(), config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: config.RedisAddress()})
	defer redisClient.Close()
	repository := payment.NewRepository(pool)
	stream := eventbus.NewRedisStream(
		redisClient, config.EventStream(), "inventory", "payment-publisher",
	)
	go publishOutbox(context.Background(), repository, stream)
	app.RunHTTP("payment-service", ":8080", payment.NewHandler(repository, stream))
}

// publishOutbox 定时把已提交的支付 Outbox 事件发布到 Redis。
func publishOutbox(
	ctx context.Context,
	repository *payment.Repository,
	stream *eventbus.RedisStream,
) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := repository.PublishPending(ctx, stream, 100); err != nil {
				slog.Error("publish outbox", "error", err)
			}
		}
	}
}
