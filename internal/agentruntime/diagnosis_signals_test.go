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

func TestDiagnosticSignalsRejectConflictingEvidence(t *testing.T) {
	got := diagnoseSignals(
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "NOT_DEDUCTED"}),
		testEvidence("get_outbox_status", map[string]any{"publish_status": "PUBLISHED"}),
		testEvidence("get_event_record", map[string]any{"found": true}),
		testEvidence("get_trace", map[string]any{"service": "inventory-service", "operation": "deduct_inventory", "signals": []any{
			map[string]any{"status": "OK", "message": "inventory deduction success"},
			map[string]any{"status": "ERROR", "message": "inventory deduction rollback failed"},
		}}),
	)
	if got.RootCause != "NO_CONFIRMED_ROOT_CAUSE" {
		t.Fatalf("got %s", got.RootCause)
	}
}

func TestDiagnosticSignalsRejectDirtyInventoryData(t *testing.T) {
	got := diagnoseSignals(
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "CORRUPTED_UNKNOWN", "data_quality": "DIRTY"}),
	)
	if got.RootCause != "NO_CONFIRMED_ROOT_CAUSE" {
		t.Fatalf("got %s", got.RootCause)
	}
}

func TestEvidenceRequiresInconclusiveChecksAllEvidence(t *testing.T) {
	evidence := []Evidence{
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "NOT_DEDUCTED"}),
		testEvidence("get_trace", map[string]any{"service": "inventory-service", "operation": "deduct_inventory", "signals": []any{
			map[string]any{"status": "OK", "message": "inventory deduction success"},
			map[string]any{"status": "ERROR", "message": "inventory deduction rollback"},
		}}),
	}
	if !evidenceRequiresInconclusive(evidence) {
		t.Fatal("all evidence must be considered when detecting conflicts")
	}
}

func TestValidateDiagnosisRootCauseChecksCandidateWithoutReplacingIt(t *testing.T) {
	evidence := []Evidence{
		testEvidence("get_payment_status", map[string]any{"status": "SUCCESS"}),
		testEvidence("get_inventory_status", map[string]any{"status": "NOT_DEDUCTED"}),
		testEvidence("get_outbox_status", map[string]any{"found": true, "publish_status": "PENDING"}),
	}
	result := Diagnosis{
		RootCause: "PAYMENT_EVENT_NOT_PUBLISHED", Confidence: 0.9,
		EvidenceIDs: []string{"get_payment_status", "get_inventory_status", "get_outbox_status"},
	}
	validation := ValidateDiagnosisRootCause(result, evidence)
	if !validation.Valid {
		t.Fatalf("validation rejected supported root cause: %v", validation.Issues)
	}

	result.RootCause = "INVENTORY_DEDUCTION_NOT_PERSISTED"
	validation = ValidateDiagnosisRootCause(result, evidence)
	if validation.Valid {
		t.Fatal("validation accepted root cause without consumer success evidence")
	}
	if result.RootCause != "INVENTORY_DEDUCTION_NOT_PERSISTED" {
		t.Fatalf("validator replaced agent root cause with %s", result.RootCause)
	}
}

func TestValidateDiagnosisRootCauseUsesCitedEvidenceOnly(t *testing.T) {
	evidence := []Evidence{
		testEvidence("payment", map[string]any{"status": "SUCCESS"}),
		testEvidence("outbox", map[string]any{"found": true, "publish_status": "PENDING"}),
	}
	evidence[0].ToolName = "get_payment_status"
	evidence[1].ToolName = "get_outbox_status"
	result := Diagnosis{
		RootCause: "PAYMENT_EVENT_NOT_PUBLISHED", Confidence: 0.9,
		EvidenceIDs: []string{"payment"},
	}
	validation := ValidateDiagnosisRootCause(result, evidence)
	if validation.Valid {
		t.Fatal("validation used an uncited outbox evidence")
	}
}
