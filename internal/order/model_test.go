package order

import "testing"

// TestNewCalculatesAmount 验证订单总金额由可信商品项计算。
func TestNewCalculatesAmount(t *testing.T) {
	result, err := New("O1001", "U1001", []Item{
		{SKUID: "SKU1", Quantity: 2, UnitPrice: 19900},
		{SKUID: "SKU2", Quantity: 1, UnitPrice: 9900},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Amount != 49700 {
		t.Fatalf("amount = %d, want 49700", result.Amount)
	}
	if result.Status != PendingPayment {
		t.Fatalf("status = %s, want %s", result.Status, PendingPayment)
	}
}

// TestNewRejectsDuplicateSKU 验证数据库主键冲突在进入持久化前即可发现。
func TestNewRejectsDuplicateSKU(t *testing.T) {
	_, err := New("O1001", "U1001", []Item{
		{SKUID: "SKU1", Quantity: 1, UnitPrice: 100},
		{SKUID: "SKU1", Quantity: 1, UnitPrice: 100},
	})
	if err == nil {
		t.Fatal("duplicate SKU was accepted")
	}
}
