package remediation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

// Handlers 将库存服务的幂等扣减接口适配为 remediation-mcp 工具。
func Handlers(inventoryURL string) map[string]mcp.ToolHandler {
	client := &http.Client{}
	return map[string]mcp.ToolHandler{"deduct_inventory_once": func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
		orderID, _ := args["order_id"].(string)
		payload, _ := json.Marshal(map[string]any{"items": args["items"], "idempotency_key": args["idempotency_key"]})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(inventoryURL, "/")+"/inventory/"+orderID+"/deduct-once", bytes.NewReader(payload))
		if err != nil {
			return mcp.ToolOutput{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return mcp.ToolOutput{}, &mcp.ExecutionError{Code: "UPSTREAM_UNAVAILABLE", Message: err.Error(), Retryable: true}
		}
		defer resp.Body.Close()
		var result any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		if resp.StatusCode >= 300 {
			return mcp.ToolOutput{}, &mcp.ExecutionError{Code: "REPAIR_REJECTED", Message: fmt.Sprint(result)}
		}
		return mcp.ToolOutput{Data: result, Source: "inventory-service", EvidencePrefix: "repair"}, nil
	}}
}
