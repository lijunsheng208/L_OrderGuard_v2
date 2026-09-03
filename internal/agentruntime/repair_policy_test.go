package agentruntime

import "testing"

func TestRepairActionForRootCause(t *testing.T) {
	cases := map[string]string{
		"PAYMENT_EVENT_NOT_PUBLISHED":       "RETRY_OUTBOX_PUBLISH",
		"INVENTORY_EVENT_NOT_CONSUMED":      "RETRY_INVENTORY_CONSUMER",
		"INVENTORY_DEDUCTION_FAILED":        "RETRY_INVENTORY_DEDUCTION",
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
