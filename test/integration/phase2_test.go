package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

// TestPhase2CollectsEvidenceWithoutModel 验证不接模型也能通过九个只读工具完成证据采集。
func TestPhase2CollectsEvidenceWithoutModel(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("set INTEGRATION=1 to run phase 2 integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, env(
		"DATABASE_URL",
		"postgres://orderguard:orderguard@localhost:55432/orderguard?sslmode=disable",
	))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	suffix := time.Now().UnixNano()
	orderID := fmt.Sprintf("IT-P2-O-%d", suffix)
	paymentID := fmt.Sprintf("IT-P2-P-%d", suffix)
	skuID := fmt.Sprintf("IT-P2-SKU-%d", suffix)
	traceID := fmt.Sprintf("IT-P2-TRACE-%d", suffix)
	_, err = pool.Exec(ctx, `
		INSERT INTO inventory.stocks (sku_id, available, reserved, version)
		VALUES ($1, 20, 0, 1)`, skuID,
	)
	if err != nil {
		t.Fatal(err)
	}
	postJSONWithTrace(t, "http://localhost:8081/demo/orders", traceID, map[string]any{
		"id": orderID, "user_id": "phase2-user",
		"items": []map[string]any{{"sku_id": skuID, "quantity": 2, "unit_price": 19900}},
	}, http.StatusCreated)
	postJSONWithTrace(
		t, "http://localhost:8082/demo/orders/"+orderID+"/pay", traceID,
		map[string]any{"payment_id": paymentID, "amount": 39800}, http.StatusOK,
	)
	waitFor(t, 10*time.Second, func() bool {
		var deductions int
		var outbox string
		err := pool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM inventory.deductions WHERE order_id = $1),
				(SELECT publish_status FROM payments.outbox_events WHERE aggregate_id = $1)`,
			orderID,
		).Scan(&deductions, &outbox)
		return err == nil && deductions == 1 && outbox == "PUBLISHED"
	})

	httpClient := &http.Client{Timeout: 5 * time.Second}
	businessClient := mcp.NewClient("http://localhost:8091/mcp", httpClient)
	observabilityClient := mcp.NewClient("http://localhost:8092/mcp", httpClient)
	assertToolCount(t, ctx, businessClient, 4)
	assertToolCount(t, ctx, observabilityClient, 5)

	evidenceIDs := make([]string, 0, 9)
	orderData := callEvidence(t, ctx, businessClient, "get_order_snapshot", map[string]any{"order_id": orderID}, &evidenceIDs)
	paymentData := callEvidence(t, ctx, businessClient, "get_payment_status", map[string]any{"order_id": orderID}, &evidenceIDs)
	inventoryData := callEvidence(t, ctx, businessClient, "get_inventory_status", map[string]any{"order_id": orderID}, &evidenceIDs)
	historyData := callEvidence(t, ctx, businessClient, "get_order_state_history", map[string]any{"order_id": orderID}, &evidenceIDs)
	if orderData["status"] != "PAID" || paymentData["status"] != "SUCCESS" || inventoryData["status"] != "DEDUCTED" {
		t.Fatalf("unexpected business evidence: order=%v payment=%v inventory=%v", orderData, paymentData, inventoryData)
	}
	if history, ok := historyData["history"].([]any); !ok || len(history) != 2 {
		t.Fatalf("unexpected state history: %v", historyData)
	}

	now := time.Now().UTC()
	logsData := callEvidence(t, ctx, observabilityClient, "search_service_logs", map[string]any{
		"service": "payment-service", "order_id": orderID, "keyword": "payment",
	}, &evidenceIDs)
	traceData := callEvidence(t, ctx, observabilityClient, "get_trace", map[string]any{"trace_id": traceID}, &evidenceIDs)
	metricData := callEvidence(t, ctx, observabilityClient, "get_metric", map[string]any{
		"service": "payment-service", "metric": "operations_total",
		"from": now.Add(-time.Minute).Format(time.RFC3339), "to": now.Add(time.Minute).Format(time.RFC3339),
	}, &evidenceIDs)
	eventData := callEvidence(t, ctx, observabilityClient, "get_event_record", map[string]any{
		"order_id": orderID, "event_type": "payment.succeeded",
	}, &evidenceIDs)
	outboxData := callEvidence(t, ctx, observabilityClient, "get_outbox_status", map[string]any{"order_id": orderID}, &evidenceIDs)
	if logsData["count"].(float64) < 1 || traceData["found"] != true || metricData["value"].(float64) < 1 {
		t.Fatalf("unexpected observability evidence: logs=%v trace=%v metric=%v", logsData, traceData, metricData)
	}
	if eventData["found"] != true || outboxData["publish_status"] != "PUBLISHED" {
		t.Fatalf("unexpected delivery evidence: event=%v outbox=%v", eventData, outboxData)
	}

	var audited int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM mcp.tool_calls WHERE evidence_id = ANY($1)`, evidenceIDs,
	).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != len(evidenceIDs) {
		t.Fatalf("audited calls = %d, want %d", audited, len(evidenceIDs))
	}
}

// TestPhase2TreatsMissingSignalsAsEvidence 验证已知订单缺少事件时返回成功的否定证据。
func TestPhase2TreatsMissingSignalsAsEvidence(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("set INTEGRATION=1 to run phase 2 integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano()
	orderID := fmt.Sprintf("IT-P2-UNPAID-%d", suffix)
	postJSONWithTrace(t, "http://localhost:8081/demo/orders", "trace-"+orderID, map[string]any{
		"id": orderID, "user_id": "phase2-user",
		"items": []map[string]any{{"sku_id": "SKU1", "quantity": 1, "unit_price": 1}},
	}, http.StatusCreated)
	client := mcp.NewClient("http://localhost:8092/mcp", &http.Client{Timeout: 5 * time.Second})
	evidenceIDs := make([]string, 0, 2)
	event := callEvidence(t, ctx, client, "get_event_record", map[string]any{
		"order_id": orderID, "event_type": "payment.succeeded",
	}, &evidenceIDs)
	outbox := callEvidence(t, ctx, client, "get_outbox_status", map[string]any{
		"order_id": orderID,
	}, &evidenceIDs)
	if event["found"] != false || outbox["found"] != false {
		t.Fatalf("missing evidence should be successful: event=%v outbox=%v", event, outbox)
	}
}

// assertToolCount 初始化 MCP Server 并核对工具发现数量。
func assertToolCount(t *testing.T, ctx context.Context, client *mcp.Client, want int) {
	t.Helper()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != want {
		t.Fatalf("tool count = %d, want %d", len(tools), want)
	}
}

// callEvidence 调用工具并校验统一证据元数据。
func callEvidence(
	t *testing.T,
	ctx context.Context,
	client *mcp.Client,
	name string,
	arguments map[string]any,
	evidenceIDs *[]string,
) map[string]any {
	t.Helper()
	result, err := client.CallTool(ctx, name, arguments)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	envelope := result.StructuredContent
	if result.IsError || !envelope.Success || envelope.EvidenceID == "" || envelope.Source == "" {
		t.Fatalf("invalid %s evidence: %+v", name, result)
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.CollectedAt); err != nil {
		t.Fatalf("%s collected_at: %v", name, err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !ok || len(data) == 0 {
		t.Fatalf("%s data is not a non-empty object: %#v", name, envelope.Data)
	}
	*evidenceIDs = append(*evidenceIDs, envelope.EvidenceID)
	return data
}

// postJSONWithTrace 发送携带固定 Trace ID 的 JSON 请求。
func postJSONWithTrace(
	t *testing.T,
	endpoint string,
	traceID string,
	body any,
	expectedStatus int,
) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Trace-ID", traceID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		t.Fatalf("POST %s status=%d", endpoint, response.StatusCode)
	}
}
