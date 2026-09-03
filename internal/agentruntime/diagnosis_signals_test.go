package agentruntime

import (
	"encoding/json"
	"testing"
)

func testEvidence(tool string, data any) Evidence {
	b, _ := json.Marshal(data)
	return Evidence{EvidenceID: tool, ToolName: tool, Data: b}
}

func diagnoseSignals(items ...Evidence) Diagnosis {
	s := diagnosticSignals{}
	for _, item := range items {
		s.observe(item)
	}
	return s.diagnosis([]string{"evidence"})
}

func TestDiagnosticSignalsClassifyPublishStages(t *testing.T) {
	base := []Evidence{
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "NOT_DEDUCTED"}),
	}
	tests := []struct {
		name, want string
		evidence   []Evidence
	}{
		{"outbox missing", "PAYMENT_EVENT_NOT_CREATED", []Evidence{testEvidence("get_outbox_status", map[string]any{"found": false})}},
		{"outbox pending", "PAYMENT_EVENT_NOT_PUBLISHED", []Evidence{testEvidence("get_outbox_status", map[string]any{"publish_status": "PENDING"})}},
		{"published event unavailable", "EVENT_NOT_AVAILABLE_AFTER_PUBLISH", []Evidence{testEvidence("get_outbox_status", map[string]any{"publish_status": "PUBLISHED"}), testEvidence("get_event_record", map[string]any{"found": false})}},
	}
	for _, tc := range tests {
		evidence := append([]Evidence{}, base...)
		evidence = append(evidence, tc.evidence...)
		got := diagnoseSignals(evidence...)
		if got.RootCause != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got.RootCause, tc.want)
		}
	}
}

func TestDiagnosticSignalsDoNotSubstringMatchNotDeducted(t *testing.T) {
	got := diagnoseSignals(
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "NOT_DEDUCTED"}),
		testEvidence("get_outbox_status", map[string]any{"publish_status": "PUBLISHED"}),
		testEvidence("get_event_record", map[string]any{"found": true}),
	)
	if got.RootCause == "NO_ISSUE" {
		t.Fatal("NOT_DEDUCTED must not be classified as DEDUCTED")
	}
}

func TestDiagnosticSignalsClassifyPersistenceConflict(t *testing.T) {
	got := diagnoseSignals(
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "NOT_DEDUCTED"}),
		testEvidence("get_outbox_status", map[string]any{"publish_status": "PUBLISHED"}),
		testEvidence("get_event_record", map[string]any{"found": true}),
		testEvidence("get_trace", map[string]any{"service": "inventory-service", "operation": "deduct_inventory", "status": "OK"}),
	)
	if got.RootCause != "INVENTORY_DEDUCTION_NOT_PERSISTED" {
		t.Fatalf("got %s", got.RootCause)
	}
}
