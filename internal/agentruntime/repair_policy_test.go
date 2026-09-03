package agentruntime

import "testing"

func TestRepairActionForRootCause(t *testing.T) {
	cases := map[string]string{
		"PAYMENT_EVENT_NOT_CREATED":         "REBUILD_OUTBOX_EVENT",
		"PAYMENT_EVENT_NOT_PUBLISHED":       "RETRY_OUTBOX_PUBLISH",
		"INVENTORY_EVENT_NOT_CONSUMED":      "RETRY_INVENTORY_CONSUMER",
		"INVENTORY_DEDUCTION_FAILED":        "RETRY_INVENTORY_CONSUMER",
		"INVENTORY_DEDUCTION_NOT_PERSISTED": "RECONCILE_INVENTORY_STATE",
	}
	for root, want := range cases {
		got, ok := RepairActionForRootCause(root)
		if !ok || got != want {
			t.Errorf("%s: got %s, %v", root, got, ok)
		}
	}
	if _, ok := RepairActionForRootCause("NO_CONFIRMED_ROOT_CAUSE"); ok {
		t.Fatal("inconclusive diagnosis must not be executable")
	}
}

func TestRemediationArgumentsMatchToolSchemas(t *testing.T) {
	tests := []struct {
		action string
		keys   []string
	}{
		{"REBUILD_OUTBOX_EVENT", []string{"order_id"}},
		{"RETRY_OUTBOX_PUBLISH", []string{"event_id"}},
		{"RETRY_INVENTORY_CONSUMER", []string{"event_id"}},
		{"RETRY_INVENTORY_DEDUCTION", []string{"order_id", "items", "idempotency_key"}},
		{"RECONCILE_INVENTORY_STATE", []string{"order_id"}},
	}
	for _, test := range tests {
		args, err := remediationArguments(test.action, "O-1", "evt-1", "key-1", []map[string]any{{"sku_id": "SKU1", "quantity": 1}})
		if err != nil {
			t.Fatalf("%s: %v", test.action, err)
		}
		if len(args) != len(test.keys) {
			t.Fatalf("%s: arguments=%v", test.action, args)
		}
		for _, key := range test.keys {
			if _, ok := args[key]; !ok {
				t.Fatalf("%s: missing argument %s", test.action, key)
			}
		}
	}
}
