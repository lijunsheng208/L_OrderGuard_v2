package main

import (
	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/service"
)

// main 启动订单服务骨架。
func main() {
	app.RunHTTP("order-service", ":8080", service.Handler("order-service"))
}
