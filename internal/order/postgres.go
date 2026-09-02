package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/telemetry"
	"github.com/lijunsheng/orderguard/internal/tracing"
)

// Repository 使用 PostgreSQL 保存订单。
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository 创建订单 Repository。
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Create 在一个事务中写入订单和商品项。
func (r *Repository) Create(
	ctx context.Context,
	id string,
	userID string,
	items []Item,
) (Order, error) {
	created, err := New(id, userID, items)
	if err != nil {
		return Order{}, err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, fmt.Errorf("begin create order: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO orders.orders
			(id, user_id, amount, status, shipment_status, version, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		created.ID, created.UserID, created.Amount, created.Status,
		created.ShipmentStatus, created.Version, created.CreatedAt,
	)
	if err != nil {
		return Order{}, fmt.Errorf("insert order: %w", err)
	}
	for _, item := range created.Items {
		_, err = tx.Exec(ctx, `
			INSERT INTO orders.order_items (order_id, sku_id, quantity, unit_price)
			VALUES ($1, $2, $3, $4)`,
			created.ID, item.SKUID, item.Quantity, item.UnitPrice,
		)
		if err != nil {
			return Order{}, fmt.Errorf("insert order item: %w", err)
		}
	}
	traceID := tracing.ID(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO orders.state_history (
			order_id, version, from_status, to_status, reason, trace_id, changed_at
		) VALUES ($1, $2, NULL, $3, 'ORDER_CREATED', $4, $5)`,
		created.ID, created.Version, created.Status, traceID, created.CreatedAt,
	)
	if err != nil {
		return Order{}, fmt.Errorf("insert initial order history: %w", err)
	}
	if err := telemetry.RecordTx(ctx, tx, telemetry.Signal{
		TraceID: traceID, OrderID: created.ID, Service: "order-service",
		Type: "LOG", Operation: "create_order", Status: "OK",
		Message: "order created", Attributes: map[string]any{"version": created.Version},
		StartedAt: created.CreatedAt, FinishedAt: time.Now().UTC(),
	}); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("commit create order: %w", err)
	}
	return created, nil
}

// GetHistory 返回订单按版本排序的状态变更历史。
func (r *Repository) GetHistory(ctx context.Context, id string) ([]StateChange, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM orders.orders WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("query order existence: %w", err)
	}
	if !exists {
		return nil, ErrOrderNotFound
	}
	rows, err := r.db.Query(ctx, `
		SELECT version, from_status, to_status, reason, COALESCE(trace_id, ''), changed_at
		FROM orders.state_history WHERE order_id = $1 ORDER BY version`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("query order history: %w", err)
	}
	defer rows.Close()
	result := make([]StateChange, 0)
	for rows.Next() {
		var change StateChange
		if err := rows.Scan(
			&change.Version, &change.FromStatus, &change.ToStatus,
			&change.Reason, &change.TraceID, &change.ChangedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order history: %w", err)
		}
		result = append(result, change)
	}
	return result, rows.Err()
}

// Get 返回订单和商品项快照。
func (r *Repository) Get(ctx context.Context, id string) (Order, error) {
	var result Order
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, amount, status, shipment_status, version, created_at
		FROM orders.orders WHERE id = $1`, id,
	).Scan(
		&result.ID, &result.UserID, &result.Amount, &result.Status,
		&result.ShipmentStatus, &result.Version, &result.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("query order: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT sku_id, quantity, unit_price
		FROM orders.order_items WHERE order_id = $1 ORDER BY sku_id`, id,
	)
	if err != nil {
		return Order{}, fmt.Errorf("query order items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.SKUID, &item.Quantity, &item.UnitPrice); err != nil {
			return Order{}, fmt.Errorf("scan order item: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}
