package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Signal 表示可供阶段 2 查询的结构化日志或 Trace 片段。
type Signal struct {
	TraceID    string
	OrderID    string
	Service    string
	Type       string
	Operation  string
	Status     string
	Message    string
	Attributes map[string]any
	StartedAt  time.Time
	FinishedAt time.Time
}

type executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// RecordTx 在现有业务事务中写入结构化观测信号。
func RecordTx(ctx context.Context, tx pgx.Tx, signal Signal) error {
	return record(ctx, tx, signal)
}

// Record 使用连接池写入结构化观测信号。
func Record(ctx context.Context, pool *pgxpool.Pool, signal Signal) error {
	return record(ctx, pool, signal)
}

// record 将结构化信号持久化到统一观测表。
func record(ctx context.Context, target executor, signal Signal) error {
	attributes, err := json.Marshal(signal.Attributes)
	if err != nil {
		return fmt.Errorf("encode signal attributes: %w", err)
	}
	if signal.StartedAt.IsZero() {
		signal.StartedAt = time.Now().UTC()
	}
	if signal.FinishedAt.IsZero() {
		signal.FinishedAt = signal.StartedAt
	}
	_, err = target.Exec(ctx, `
		INSERT INTO observability.signals (
			trace_id, order_id, service_name, signal_type, operation,
			status, message, attributes, started_at, finished_at
		) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9, $10)`,
		signal.TraceID, signal.OrderID, signal.Service, signal.Type,
		signal.Operation, signal.Status, signal.Message, attributes,
		signal.StartedAt, signal.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("insert observability signal: %w", err)
	}
	return nil
}
