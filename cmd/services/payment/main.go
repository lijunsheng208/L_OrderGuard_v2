package main

import (
	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/service"
)

// main 启动支付服务骨架。
func main() {
	app.RunHTTP("payment-service", ":8080", service.Handler("payment-service"))
}
