package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/payment"
)

var ErrInsufficientStock = errors.New("insufficient stock")

// Repository 在事务中处理库存扣减和消费幂等。
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository 创建库存 Repository。
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Consume 使用事件 ID 幂等消费支付成功事件。
func (r *Repository) Consume(
	ctx context.Context,
	event payment.Event,
) ([]Deduction, error) {
	if event.EventID == "" || event.AggregateID == "" || len(event.Items) == 0 {
		return nil, errors.New("invalid payment event")
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin inventory deduction: %w", err)
	}
	defer tx.Rollback(ctx)

	var consumed bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM inventory.consumed_events WHERE event_id = $1
		)`, event.EventID,
	).Scan(&consumed)
	if err != nil {
		return nil, fmt.Errorf("query consumed event: %w", err)
	}
	if consumed {
		_, err := tx.Exec(ctx, `
			UPDATE inventory.consumed_events
			SET delivery_count = delivery_count + 1
			WHERE event_id = $1`, event.EventID,
		)
		if err != nil {
			return nil, fmt.Errorf("count duplicate delivery: %w", err)
		}
		result, err := getDeductions(ctx, tx, event.AggregateID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit duplicate delivery: %w", err)
		}
		return result, nil
	}

	for _, item := range event.Items {
		if item.SKUID == "" || item.Quantity <= 0 {
			return nil, errors.New("invalid inventory item")
		}
		var available int
		err := tx.QueryRow(ctx, `
			SELECT available FROM inventory.stocks
			WHERE sku_id = $1 FOR UPDATE`, item.SKUID,
		).Scan(&available)
		if err != nil {
			return nil, fmt.Errorf("lock stock %s: %w", item.SKUID, err)
		}
		if available < item.Quantity {
			return nil, ErrInsufficientStock
		}
	}

	result := make([]Deduction, 0, len(event.Items))
	for _, item := range event.Items {
		_, err := tx.Exec(ctx, `
			UPDATE inventory.stocks
			SET available = available - $1, version = version + 1, updated_at = now()
			WHERE sku_id = $2`, item.Quantity, item.SKUID,
		)
		if err != nil {
			return nil, fmt.Errorf("deduct stock %s: %w", item.SKUID, err)
		}
		deduction := Deduction{
			OrderID: event.AggregateID, SKUID: item.SKUID,
			Quantity:       item.Quantity,
			IdempotencyKey: event.EventID + ":" + item.SKUID,
			Status:         Deducted, CreatedAt: time.Now().UTC(),
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO inventory.deductions
				(order_id, sku_id, quantity, idempotency_key, status, created_at)
			VALUES ($1, $2, $3, $4, 'DEDUCTED', $5)`,
			deduction.OrderID, deduction.SKUID, deduction.Quantity,
			deduction.IdempotencyKey, deduction.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("insert deduction: %w", err)
		}
		result = append(result, deduction)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode consumption result: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO inventory.consumed_events (event_id, consumer, result)
		VALUES ($1, 'inventory-service', $2)`, event.EventID, encoded,
	)
	if err != nil {
		return nil, fmt.Errorf("record consumed event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit inventory deduction: %w", err)
	}
	return result, nil
}

// GetDeductions 查询订单的成功扣减记录。
func (r *Repository) GetDeductions(
	ctx context.Context,
	orderID string,
) ([]Deduction, error) {
	return getDeductions(ctx, r.db, orderID)
}

// GetStock 查询 SKU 库存快照。
func (r *Repository) GetStock(ctx context.Context, skuID string) (Stock, error) {
	var result Stock
	err := r.db.QueryRow(ctx, `
		SELECT sku_id, available, reserved, version
		FROM inventory.stocks WHERE sku_id = $1`, skuID,
	).Scan(&result.SKUID, &result.Available, &result.Reserved, &result.Version)
	return result, err
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// getDeductions 使用连接池或事务查询库存扣减。
func getDeductions(
	ctx context.Context,
	query queryer,
	orderID string,
) ([]Deduction, error) {
	rows, err := query.Query(ctx, `
		SELECT order_id, sku_id, quantity, idempotency_key, status, created_at
		FROM inventory.deductions WHERE order_id = $1 ORDER BY sku_id`, orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("query deductions: %w", err)
	}
	defer rows.Close()
	result := make([]Deduction, 0)
	for rows.Next() {
		var item Deduction
		if err := rows.Scan(
			&item.OrderID, &item.SKUID, &item.Quantity,
			&item.IdempotencyKey, &item.Status, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan deduction: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
