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
func Handlers(inventoryURL string, paymentURLs ...string) map[string]mcp.ToolHandler {
	client := &http.Client{}
	result := map[string]mcp.ToolHandler{"deduct_inventory_once": func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
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
	result["retry_inventory_deduction"] = result["deduct_inventory_once"]
	result["retry_inventory_consumer"] = inventoryAction(client, inventoryURL, "/inventory/events/%s/redeliver")
	result["reconcile_inventory_state"] = inventoryAction(client, inventoryURL, "/inventory/%s/reconcile")
	if len(paymentURLs) > 0 {
		result["retry_outbox_publish"] = inventoryAction(client, paymentURLs[0], "/outbox/%s/retry")
		result["rebuild_outbox_event"] = inventoryAction(client, paymentURLs[0], "/outbox/orders/%s/rebuild")
	}
	return result
}

func inventoryAction(client *http.Client, baseURL, path string) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (mcp.ToolOutput, error) {
		id, _ := args["event_id"].(string)
		if id == "" {
			id, _ = args["order_id"].(string)
		}
		method := http.MethodPost
		if strings.HasSuffix(path, "/reconcile") {
			method = http.MethodGet
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+fmt.Sprintf(path, id), nil)
		if err != nil {
			return mcp.ToolOutput{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return mcp.ToolOutput{}, err
		}
		defer resp.Body.Close()
		var data any
		_ = json.NewDecoder(resp.Body).Decode(&data)
		if resp.StatusCode >= 300 {
			return mcp.ToolOutput{}, &mcp.ExecutionError{Code: "REPAIR_REJECTED", Message: fmt.Sprint(data)}
		}
		return mcp.ToolOutput{Data: data, Source: "remediation", EvidencePrefix: "repair"}, nil
	}
}
