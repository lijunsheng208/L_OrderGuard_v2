package inventory

import "time"

// Status 表示库存扣减状态。
type Status string

const Deducted Status = "DEDUCTED"

const NotDeducted Status = "NOT_DEDUCTED"

// Item 表示待扣减商品。
type Item struct {
	SKUID    string `json:"sku_id"`
	Quantity int    `json:"quantity"`
}

// Stock 表示 SKU 当前库存。
type Stock struct {
	SKUID     string `json:"sku_id"`
	Available int    `json:"available"`
	Reserved  int    `json:"reserved"`
	Version   int64  `json:"version"`
}

// Deduction 表示一次库存扣减记录。
type Deduction struct {
	OrderID        string    `json:"order_id"`
	SKUID          string    `json:"sku_id"`
	Quantity       int       `json:"quantity"`
	IdempotencyKey string    `json:"idempotency_key"`
	Status         Status    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// OrderStatus 汇总订单维度的库存扣减证据。
type OrderStatus struct {
	OrderID                  string      `json:"order_id"`
	Status                   Status      `json:"status"`
	Deductions               []Deduction `json:"deductions"`
	SuccessfulDeductionCount int         `json:"successful_deduction_count"`
	EventDeliveryCount       int         `json:"event_delivery_count"`
}
