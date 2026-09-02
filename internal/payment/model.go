package payment

import "time"

// Status 表示支付状态。
type Status string

const Success Status = "SUCCESS"

// Payment 表示支付记录。
type Payment struct {
	ID      string    `json:"id"`
	OrderID string    `json:"order_id"`
	Amount  int64     `json:"amount"`
	Status  Status    `json:"status"`
	PaidAt  time.Time `json:"paid_at"`
}

// EventItem 表示支付事件携带的库存扣减项。
type EventItem struct {
	SKUID    string `json:"sku_id"`
	Quantity int    `json:"quantity"`
}

// Event 表示支付成功领域事件。
type Event struct {
	EventID     string      `json:"event_id"`
	EventType   string      `json:"event_type"`
	AggregateID string      `json:"aggregate_id"`
	OccurredAt  time.Time   `json:"occurred_at"`
	Producer    string      `json:"producer"`
	TraceID     string      `json:"trace_id"`
	Data        Payment     `json:"data"`
	Items       []EventItem `json:"items"`
}

// OutboxEvent 表示待发布的 Outbox 事件。
type OutboxEvent struct {
	Event
	PublishStatus string     `json:"publish_status"`
	Attempts      int        `json:"attempts"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}
