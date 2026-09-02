package main

import (
	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/service"
)

// main 启动库存服务骨架。
func main() {
	app.RunHTTP("inventory-service", ":8080", service.Handler("inventory-service"))
}
