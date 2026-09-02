package main

import (
	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/service"
)

// main 启动对账服务骨架。
func main() {
	app.RunHTTP("reconciliation-service", ":8080", service.Handler("reconciliation-service"))
}
