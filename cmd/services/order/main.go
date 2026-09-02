package main

import (
	"context"
	"log"

	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/database"
	"github.com/lijunsheng/orderguard/internal/order"
)

// main 启动订单服务骨架。
func main() {
	pool, err := database.OpenPostgres(context.Background(), config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	repository := order.NewRepository(pool)
	app.RunHTTP("order-service", ":8080", order.NewHandler(repository))
}
