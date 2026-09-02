package main

import (
	"context"
	"log"
	"log/slog"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/database"
	"github.com/lijunsheng/orderguard/internal/eventbus"
	"github.com/lijunsheng/orderguard/internal/inventory"
	"github.com/redis/go-redis/v9"
)

// main 启动库存服务骨架。
func main() {
	pool, err := database.OpenPostgres(context.Background(), config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: config.RedisAddress()})
	defer redisClient.Close()
	repository := inventory.NewRepository(pool)
	stream := eventbus.NewRedisStream(
		redisClient, config.EventStream(), "inventory", "inventory-consumer-1",
	)
	consumer := inventory.NewConsumer(stream, repository)
	go func() {
		if err := consumer.Run(context.Background()); err != nil {
			slog.Error("inventory consumer stopped", "error", err)
		}
	}()
	app.RunHTTP("inventory-service", ":8080", inventory.NewHandler(repository))
}
