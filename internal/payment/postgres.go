package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/eventbus"
)

var (
	ErrInvalidPayment  = errors.New("invalid payment")
	ErrOrderNotFound   = errors.New("order not found")
	ErrOrderNotPayable = errors.New("order is not payable")
	ErrAmountMismatch  = errors.New("payment amount does not match order amount")
)

// Repository 使用 PostgreSQL 保存支付记录和 Outbox。
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository 创建支付 Repository。
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Pay 原子写入支付成功记录、订单状态和 Outbox 事件。
func (r *Repository) Pay(
	ctx context.Context,
	paymentID string,
	orderID string,
	amount int64,
) (Payment, error) {
	if paymentID == "" || orderID == "" || amount < 0 {
		return Payment{}, ErrInvalidPayment
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Payment{}, fmt.Errorf("begin payment: %w", err)
	}
	defer tx.Rollback(ctx)

	var orderAmount int64
	var orderStatus string
	err = tx.QueryRow(ctx, `
		SELECT amount, status FROM orders.orders WHERE id = $1 FOR UPDATE`, orderID,
	).Scan(&orderAmount, &orderStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrOrderNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("lock order: %w", err)
	}
	if orderStatus != "PENDING_PAYMENT" && orderStatus != "PAID" {
		return Payment{}, ErrOrderNotPayable
	}
	if orderAmount != amount {
		return Payment{}, ErrAmountMismatch
	}

	existing, found, err := getPaymentTx(ctx, tx, orderID)
	if err != nil {
		return Payment{}, err
	}
	if found {
		return existing, tx.Commit(ctx)
	}
	items, err := getOrderItemsTx(ctx, tx, orderID)
	if err != nil {
		return Payment{}, err
	}

	now := time.Now().UTC()
	created := Payment{
		ID: paymentID, OrderID: orderID, Amount: amount,
		Status: Success, PaidAt: now,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO payments.payments (id, order_id, amount, status, paid_at)
		VALUES ($1, $2, $3, 'SUCCESS', $4)`,
		created.ID, created.OrderID, created.Amount, created.PaidAt,
	)
	if err != nil {
		return Payment{}, fmt.Errorf("insert payment: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE orders.orders
		SET status = 'PAID', version = version + 1, updated_at = now()
		WHERE id = $1 AND status = 'PENDING_PAYMENT'`, orderID,
	)
	if err != nil {
		return Payment{}, fmt.Errorf("mark order paid: %w", err)
	}

	event := Event{
		EventID: "evt_" + paymentID, EventType: "payment.succeeded",
		AggregateID: orderID, OccurredAt: now,
		Producer: "payment-service", Data: created, Items: items,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return Payment{}, fmt.Errorf("encode payment event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO payments.outbox_events
			(event_id, aggregate_id, event_type, payload, publish_status)
		VALUES ($1, $2, $3, $4, 'PENDING')`,
		event.EventID, event.AggregateID, event.EventType, payload,
	)
	if err != nil {
		return Payment{}, fmt.Errorf("insert outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit payment: %w", err)
	}
	return created, nil
}

// Get 返回订单对应的支付记录。
func (r *Repository) Get(ctx context.Context, orderID string) (Payment, error) {
	var result Payment
	err := r.db.QueryRow(ctx, `
		SELECT id, order_id, amount, status, paid_at
		FROM payments.payments WHERE order_id = $1`, orderID,
	).Scan(&result.ID, &result.OrderID, &result.Amount, &result.Status, &result.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrOrderNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("query payment: %w", err)
	}
	return result, nil
}

// GetOutboxStatus 返回订单最新支付事件的发布状态。
func (r *Repository) GetOutboxStatus(ctx context.Context, orderID string) (string, error) {
	var status string
	err := r.db.QueryRow(ctx, `
		SELECT publish_status FROM payments.outbox_events
		WHERE aggregate_id = $1 AND event_type = 'payment.succeeded'
		ORDER BY created_at DESC LIMIT 1`, orderID,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrOrderNotFound
	}
	return status, err
}

// PendingOutbox 返回待发布事件。
func (r *Repository) PendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := r.db.Query(ctx, `
		SELECT payload, publish_status
		FROM payments.outbox_events
		WHERE publish_status = 'PENDING'
		ORDER BY created_at LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending outbox: %w", err)
	}
	defer rows.Close()

	result := make([]OutboxEvent, 0)
	for rows.Next() {
		var item OutboxEvent
		var payload []byte
		if err := rows.Scan(&payload, &item.PublishStatus); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		if err := json.Unmarshal(payload, &item.Event); err != nil {
			return nil, fmt.Errorf("decode outbox event: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// PublishPending 发布一批 Outbox 事件并更新发布状态。
func (r *Repository) PublishPending(
	ctx context.Context,
	stream *eventbus.RedisStream,
	limit int,
) (int, error) {
	events, err := r.PendingOutbox(ctx, limit)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, item := range events {
		if err := stream.Publish(ctx, item.EventID, item.Event); err != nil {
			return published, err
		}
		_, err := r.db.Exec(ctx, `
			UPDATE payments.outbox_events
			SET publish_status = 'PUBLISHED', attempts = attempts + 1, published_at = now()
			WHERE event_id = $1 AND publish_status = 'PENDING'`, item.EventID,
		)
		if err != nil {
			return published, fmt.Errorf("mark outbox published: %w", err)
		}
		published++
	}
	return published, nil
}

// getPaymentTx 在支付事务中查询已有支付。
func getPaymentTx(ctx context.Context, tx pgx.Tx, orderID string) (Payment, bool, error) {
	var result Payment
	err := tx.QueryRow(ctx, `
		SELECT id, order_id, amount, status, paid_at
		FROM payments.payments WHERE order_id = $1`, orderID,
	).Scan(&result.ID, &result.OrderID, &result.Amount, &result.Status, &result.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, false, nil
	}
	if err != nil {
		return Payment{}, false, fmt.Errorf("query existing payment: %w", err)
	}
	return result, true, nil
}

// getOrderItemsTx 在支付事务中读取可信订单商品项。
func getOrderItemsTx(ctx context.Context, tx pgx.Tx, orderID string) ([]EventItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT sku_id, quantity FROM orders.order_items
		WHERE order_id = $1 ORDER BY sku_id`, orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("query payment event items: %w", err)
	}
	defer rows.Close()
	items := make([]EventItem, 0)
	for rows.Next() {
		var item EventItem
		if err := rows.Scan(&item.SKUID, &item.Quantity); err != nil {
			return nil, fmt.Errorf("scan payment event item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
