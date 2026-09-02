package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

// main 发现工具并调用一个只读工具，用于阶段 0 验收。
func main() {
	endpoint := flag.String("endpoint", "http://localhost:8091/mcp", "MCP endpoint")
	orderID := flag.String("order-id", "O1001", "smoke-test order ID")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(*endpoint, &http.Client{Timeout: 5 * time.Second})
	if err := client.Initialize(ctx); err != nil {
		fail(err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		fail(err)
	}
	result, err := client.CallTool(ctx, "get_order_snapshot", map[string]any{
		"order_id": *orderID,
	})
	if err != nil {
		fail(err)
	}

	output := map[string]any{
		"discovered_tools": len(tools),
		"called_tool":      "get_order_snapshot",
		"result":           result.StructuredContent,
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
}

// fail 输出错误并以非零状态结束客户端。
func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
