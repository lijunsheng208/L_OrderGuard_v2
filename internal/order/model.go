package order

import (
	"errors"
	"time"
)

var (
	ErrInvalidOrder  = errors.New("invalid order")
	ErrOrderNotFound = errors.New("order not found")
)

// Status 表示订单状态。
type Status string

const (
	PendingPayment Status = "PENDING_PAYMENT"
	Paid           Status = "PAID"
)

// Item 表示订单商品项，单价单位为分。
type Item struct {
	SKUID     string `json:"sku_id"`
	Quantity  int    `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
}

// Order 表示订单聚合。
type Order struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Amount         int64     `json:"amount"`
	Status         Status    `json:"status"`
	ShipmentStatus string    `json:"shipment_status"`
	Items          []Item    `json:"items"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
}

// New 校验输入并创建待支付订单。
func New(id, userID string, items []Item) (Order, error) {
	if id == "" || userID == "" || len(items) == 0 {
		return Order{}, ErrInvalidOrder
	}
	seen := make(map[string]struct{}, len(items))
	var amount int64
	for _, item := range items {
		if item.SKUID == "" || item.Quantity <= 0 || item.UnitPrice < 0 {
			return Order{}, ErrInvalidOrder
		}
		if _, exists := seen[item.SKUID]; exists {
			return Order{}, ErrInvalidOrder
		}
		seen[item.SKUID] = struct{}{}
		amount += int64(item.Quantity) * item.UnitPrice
	}
	return Order{
		ID: id, UserID: userID, Amount: amount,
		Status: PendingPayment, ShipmentStatus: "NOT_SHIPPED",
		Items: append([]Item(nil), items...), Version: 1, CreatedAt: time.Now().UTC(),
	}, nil
}
