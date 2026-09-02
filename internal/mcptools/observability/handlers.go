package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/mcp"
	"github.com/redis/go-redis/v9"
)

const eventStream = "orderguard.events"

// Store 保存可观测性工具使用的受控基础设施连接。
type Store struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	stream string
}

// Signal 是结构化日志和 Trace 的查询结果。
type Signal struct {
	ID         int64          `json:"id"`
	TraceID    string         `json:"trace_id"`
	OrderID    *string        `json:"order_id,omitempty"`
	Service    string         `json:"service"`
	Type       string         `json:"signal_type"`
	Operation  string         `json:"operation"`
	Status     string         `json:"status"`
	Message    string         `json:"message"`
	Attributes map[string]any `json:"attributes"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
}

// NewStore 创建可观测性查询 Store。
func NewStore(db *pgxpool.Pool, redisClient *redis.Client, stream string) *Store {
	if stream == "" {
		stream = eventStream
	}
	return &Store{db: db, redis: redisClient, stream: stream}
}

// Handlers 创建五个只读可观测性工具 Handler。
func (s *Store) Handlers() map[string]mcp.ToolHandler {
	return map[string]mcp.ToolHandler{
		"search_service_logs": s.searchServiceLogs,
		"get_trace":           s.getTrace,
		"get_metric":          s.getMetric,
		"get_event_record":    s.getEventRecord,
		"get_outbox_status":   s.getOutboxStatus,
	}
}

// searchServiceLogs 按服务、订单和可选关键词检索有界日志集合。
func (s *Store) searchServiceLogs(ctx context.Context, arguments map[string]any) (mcp.ToolOutput, error) {
	service := arguments["service"].(string)
	orderID := arguments["order_id"].(string)
	keyword, _ := arguments["keyword"].(string)
	rows, err := s.db.Query(ctx, `
		SELECT id, trace_id, order_id, service_name, signal_type, operation,
			status, message, attributes, started_at, finished_at
		FROM observability.signals
		WHERE signal_type = 'LOG' AND service_name = $1 AND order_id = $2
			AND ($3 = '' OR message ILIKE '%' || $3 || '%' OR operation ILIKE '%' || $3 || '%')
		ORDER BY started_at DESC LIMIT 100`, service, orderID, keyword,
	)
	if err != nil {
		return mcp.ToolOutput{}, databaseError("search service logs", err)
	}
	defer rows.Close()
	signals, err := scanSignals(rows)
	if err != nil {
		return mcp.ToolOutput{}, err
	}
	return mcp.ToolOutput{
		Data:   map[string]any{"service": service, "order_id": orderID, "logs": signals, "count": len(signals)},
		Source: "observability-postgres", EvidencePrefix: "logs",
	}, nil
}

// getTrace 返回同一 trace_id 下按时间排序的所有结构化信号。
func (s *Store) getTrace(ctx context.Context, arguments map[string]any) (mcp.ToolOutput, error) {
	traceID := arguments["trace_id"].(string)
	rows, err := s.db.Query(ctx, `
		SELECT id, trace_id, order_id, service_name, signal_type, operation,
			status, message, attributes, started_at, finished_at
		FROM observability.signals WHERE trace_id = $1
		ORDER BY started_at, id LIMIT 500`, traceID,
	)
	if err != nil {
		return mcp.ToolOutput{}, databaseError("get trace", err)
	}
	defer rows.Close()
	signals, err := scanSignals(rows)
	if err != nil {
		return mcp.ToolOutput{}, err
	}
	return mcp.ToolOutput{
		Data:   map[string]any{"trace_id": traceID, "found": len(signals) > 0, "signals": signals},
		Source: "observability-postgres", EvidencePrefix: "trace",
	}, nil
}

// getMetric 对固定白名单指标执行时间范围聚合。
func (s *Store) getMetric(ctx context.Context, arguments map[string]any) (mcp.ToolOutput, error) {
	service := arguments["service"].(string)
	metric := arguments["metric"].(string)
	from, err := time.Parse(time.RFC3339, arguments["from"].(string))
	if err != nil {
		return mcp.ToolOutput{}, invalidArgument("from must be RFC3339")
	}
	to, err := time.Parse(time.RFC3339, arguments["to"].(string))
	if err != nil {
		return mcp.ToolOutput{}, invalidArgument("to must be RFC3339")
	}
	if from.After(to) || to.Sub(from) > 31*24*time.Hour {
		return mcp.ToolOutput{}, invalidArgument("metric range must be ordered and no longer than 31 days")
	}
	query := ""
	switch metric {
	case "operations_total":
		query = "SELECT count(*)::double precision FROM observability.signals WHERE service_name = $1 AND started_at BETWEEN $2 AND $3"
	case "operations_failed_total":
		query = "SELECT count(*)::double precision FROM observability.signals WHERE service_name = $1 AND status <> 'OK' AND started_at BETWEEN $2 AND $3"
	case "operation_duration_ms_avg":
		query = "SELECT COALESCE(avg(EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000), 0)::double precision FROM observability.signals WHERE service_name = $1 AND started_at BETWEEN $2 AND $3"
	default:
		return mcp.ToolOutput{}, invalidArgument("unsupported metric")
	}
	var value float64
	if err := s.db.QueryRow(ctx, query, service, from, to).Scan(&value); err != nil {
		return mcp.ToolOutput{}, databaseError("get metric", err)
	}
	return mcp.ToolOutput{
		Data:   map[string]any{"service": service, "metric": metric, "from": from, "to": to, "value": value},
		Source: "observability-postgres", EvidencePrefix: "metric",
	}, nil
}

// getEventRecord 通过订单事件索引精确读取 Redis Stream 记录。
func (s *Store) getEventRecord(ctx context.Context, arguments map[string]any) (mcp.ToolOutput, error) {
	orderID := arguments["order_id"].(string)
	eventType := arguments["event_type"].(string)
	if err := s.requireOrder(ctx, orderID); err != nil {
		return mcp.ToolOutput{}, err
	}
	streamID, err := s.redis.HGet(
		ctx, "orderguard:event-index:"+orderID, "type:"+eventType,
	).Result()
	if errors.Is(err, redis.Nil) {
		return eventOutput(orderID, eventType, false, nil), nil
	}
	if err != nil {
		return mcp.ToolOutput{}, &mcp.ExecutionError{
			Code: "UPSTREAM_UNAVAILABLE", Message: "redis event index is unavailable", Retryable: true,
		}
	}
	entries, err := s.redis.XRangeN(ctx, s.stream, streamID, streamID, 1).Result()
	if err != nil {
		return mcp.ToolOutput{}, &mcp.ExecutionError{
			Code: "UPSTREAM_UNAVAILABLE", Message: "redis event stream is unavailable", Retryable: true,
		}
	}
	if len(entries) == 0 {
		return eventOutput(orderID, eventType, false, nil), nil
	}
	var payload any
	if err := json.Unmarshal([]byte(fmt.Sprint(entries[0].Values["payload"])), &payload); err != nil {
		return mcp.ToolOutput{}, &mcp.ExecutionError{Code: "UPSTREAM_INVALID_RESPONSE", Message: "redis event payload is invalid"}
	}
	record := map[string]any{
		"stream_id": entries[0].ID, "event_id": fmt.Sprint(entries[0].Values["event_id"]), "payload": payload,
	}
	return eventOutput(orderID, eventType, true, record), nil
}

// getOutboxStatus 查询支付 Outbox 中最新的支付成功事件。
func (s *Store) getOutboxStatus(ctx context.Context, arguments map[string]any) (mcp.ToolOutput, error) {
	orderID := arguments["order_id"].(string)
	var eventID, eventType, status string
	var attempts int
	var payload json.RawMessage
	var createdAt time.Time
	var publishedAt *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT event_id, event_type, payload, publish_status, attempts, created_at, published_at
		FROM payments.outbox_events WHERE aggregate_id = $1
		ORDER BY created_at DESC LIMIT 1`, orderID,
	).Scan(&eventID, &eventType, &payload, &status, &attempts, &createdAt, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := s.requireOrder(ctx, orderID); err != nil {
			return mcp.ToolOutput{}, err
		}
		return mcp.ToolOutput{
			Data:   map[string]any{"order_id": orderID, "found": false},
			Source: "payments-postgres", EvidencePrefix: "outbox",
		}, nil
	}
	if err != nil {
		return mcp.ToolOutput{}, databaseError("get outbox status", err)
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return mcp.ToolOutput{}, databaseError("decode outbox payload", err)
	}
	return mcp.ToolOutput{
		Data: map[string]any{
			"order_id": orderID, "found": true, "event_id": eventID, "event_type": eventType,
			"publish_status": status, "attempts": attempts, "created_at": createdAt,
			"published_at": publishedAt, "payload": decoded,
		},
		Source: "payments-postgres", EvidencePrefix: "outbox",
	}, nil
}

// requireOrder 区分未知订单与已知订单下缺失的观测证据。
func (s *Store) requireOrder(ctx context.Context, orderID string) error {
	var exists bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM orders.orders WHERE id = $1)`, orderID,
	).Scan(&exists); err != nil {
		return databaseError("query order existence", err)
	}
	if !exists {
		return &mcp.ExecutionError{
			Code: "NOT_FOUND", Message: "order was not found", Details: map[string]any{"order_id": orderID},
		}
	}
	return nil
}

// scanSignals 将数据库行转换为稳定的结构化信号。
func scanSignals(rows pgx.Rows) ([]Signal, error) {
	result := make([]Signal, 0)
	for rows.Next() {
		var signal Signal
		var attributes []byte
		if err := rows.Scan(
			&signal.ID, &signal.TraceID, &signal.OrderID, &signal.Service, &signal.Type,
			&signal.Operation, &signal.Status, &signal.Message, &attributes,
			&signal.StartedAt, &signal.FinishedAt,
		); err != nil {
			return nil, databaseError("scan observability signal", err)
		}
		if err := json.Unmarshal(attributes, &signal.Attributes); err != nil {
			return nil, databaseError("decode observability attributes", err)
		}
		result = append(result, signal)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate observability signals", err)
	}
	return result, nil
}

// eventOutput 创建存在或缺失事件都可作为证据的成功结果。
func eventOutput(orderID, eventType string, found bool, record any) mcp.ToolOutput {
	data := map[string]any{"order_id": orderID, "event_type": eventType, "found": found}
	if record != nil {
		data["record"] = record
	}
	return mcp.ToolOutput{Data: data, Source: "redis-streams", EvidencePrefix: "event"}
}

// invalidArgument 创建不可重试的参数错误。
func invalidArgument(message string) error {
	return &mcp.ExecutionError{Code: "INVALID_ARGUMENT", Message: message}
}

// databaseError 隐藏数据库实现细节并保留操作上下文。
func databaseError(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return &mcp.ExecutionError{
		Code: "UPSTREAM_UNAVAILABLE", Message: operation + " failed", Retryable: true,
		Details: map[string]any{"upstream": "postgresql"},
	}
}
