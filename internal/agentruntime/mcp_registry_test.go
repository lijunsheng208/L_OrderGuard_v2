package agentruntime

import "testing"

func TestInvestigationToolBoundary(t *testing.T) {
	allowed := []string{
		"get_order_snapshot", "get_payment_status", "get_inventory_status",
		"get_order_state_history", "search_service_logs", "get_trace",
		"get_metric", "get_event_record", "get_outbox_status",
	}
	for _, name := range allowed {
		if !isInvestigationTool(name) {
			t.Fatalf("expected investigation tool %s to be allowed", name)
		}
	}
	denied := []string{
		"search_runbook", "search_historical_incidents", "get_service_topology",
		"get_state_machine", "get_repair_policy", "reconcile_inventory_state",
	}
	for _, name := range denied {
		if isInvestigationTool(name) {
			t.Fatalf("expected non-investigation tool %s to be denied", name)
		}
	}
}
