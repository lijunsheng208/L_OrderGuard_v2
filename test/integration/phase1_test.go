package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/eventbus"
	"github.com/lijunsheng/orderguard/internal/payment"
	"github.com/redis/go-redis/v9"
)

// TestPhase1NormalFlowAndDuplicateDelivery 验证正常链路及重复投递幂等。
func TestPhase1NormalFlowAndDuplicateDelivery(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("set INTEGRATION=1 to run phase 1 integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	databaseURL := env(
		"DATABASE_URL",
		"postgres://orderguard:orderguard@localhost:55432/orderguard?sslmode=disable",
	)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	redisClient := redis.NewClient(&redis.Options{
		Addr: env("REDIS_ADDR", "localhost:56379"),
	})
	defer redisClient.Close()

	suffix := time.Now().UnixNano()
	orderID := fmt.Sprintf("IT-O-%d", suffix)
	paymentID := fmt.Sprintf("IT-P-%d", suffix)
	skuID := fmt.Sprintf("IT-SKU-%d", suffix)
	_, err = pool.Exec(ctx, `
		INSERT INTO inventory.stocks (sku_id, available, reserved, version)
		VALUES ($1, 20, 0, 1)`, skuID,
	)
	if err != nil {
		t.Fatal(err)
	}

	postJSON(t, "http://localhost:8081/demo/orders", map[string]any{
		"id": orderID, "user_id": "integration-user",
		"items": []map[string]any{{
			"sku_id": skuID, "quantity": 2, "unit_price": 19900,
		}},
	}, http.StatusCreated)
	postJSON(t, "http://localhost:8082/demo/orders/"+orderID+"/pay", map[string]any{
		"payment_id": paymentID, "amount": 39800,
	}, http.StatusOK)

	var event payment.Event
	waitFor(t, 5*time.Second, func() bool {
		var payload []byte
		var status string
		err := pool.QueryRow(ctx, `
			SELECT payload, publish_status FROM payments.outbox_events
			WHERE event_id = $1`, "evt_"+paymentID,
		).Scan(&payload, &status)
		if err != nil || status != "PUBLISHED" {
			return false
		}
		return json.Unmarshal(payload, &event) == nil
	})

	stream := eventbus.NewRedisStream(
		redisClient, "orderguard.events", "inventory", "integration-publisher",
	)
	for index := 0; index < 9; index++ {
		if err := stream.Publish(ctx, event.EventID, event); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, 10*time.Second, func() bool {
		var count, deliveryCount int
		err := pool.QueryRow(ctx, `
			SELECT count(*), COALESCE(MAX(delivery_count), 0)
			FROM inventory.consumed_events WHERE event_id = $1`,
			event.EventID,
		).Scan(&count, &deliveryCount)
		return err == nil && count == 1 && deliveryCount == 10
	})
	assertPhase1State(t, ctx, pool, orderID, paymentID, skuID, event.EventID)
}

// assertPhase1State 核对阶段 1 的数据库最终状态。
func assertPhase1State(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orderID string,
	paymentID string,
	skuID string,
	eventID string,
) {
	t.Helper()
	var orderStatus, paymentStatus, outboxStatus string
	var paymentCount, outboxCount, deductionCount, consumedCount int
	var deliveryCount, available int
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT status FROM orders.orders WHERE id = $1),
			(SELECT status FROM payments.payments WHERE id = $2),
			(SELECT publish_status FROM payments.outbox_events WHERE event_id = $3),
			(SELECT count(*) FROM payments.payments WHERE order_id = $1),
			(SELECT count(*) FROM payments.outbox_events WHERE aggregate_id = $1),
			(SELECT count(*) FROM inventory.deductions WHERE order_id = $1),
			(SELECT count(*) FROM inventory.consumed_events WHERE event_id = $3),
			(SELECT delivery_count FROM inventory.consumed_events WHERE event_id = $3),
			(SELECT available FROM inventory.stocks WHERE sku_id = $4)`,
		orderID, paymentID, eventID, skuID,
	).Scan(
		&orderStatus, &paymentStatus, &outboxStatus,
		&paymentCount, &outboxCount, &deductionCount, &consumedCount,
		&deliveryCount, &available,
	)
	if err != nil {
		t.Fatal(err)
	}
	if orderStatus != "PAID" || paymentStatus != "SUCCESS" || outboxStatus != "PUBLISHED" {
		t.Fatalf(
			"statuses order=%s payment=%s outbox=%s",
			orderStatus, paymentStatus, outboxStatus,
		)
	}
	if paymentCount != 1 || outboxCount != 1 || deductionCount != 1 || consumedCount != 1 {
		t.Fatalf(
			"counts payment=%d outbox=%d deduction=%d consumed=%d",
			paymentCount, outboxCount, deductionCount, consumedCount,
		)
	}
	if deliveryCount != 10 {
		t.Fatalf("delivery count = %d, want 10", deliveryCount)
	}
	if available != 18 {
		t.Fatalf("available stock = %d, want 18", available)
	}
}

// postJSON 发送 JSON 请求并检查 HTTP 状态码。
func postJSON(t *testing.T, url string, body any, expectedStatus int) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		message, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s status=%d body=%s", url, response.StatusCode, message)
	}
}

// waitFor 等待异步链路达到预期状态。
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

// env 读取集成测试配置。
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
