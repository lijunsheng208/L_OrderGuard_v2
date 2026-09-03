package agentruntime

import (
	"encoding/json"
)

// RepairActionForRootCause maps only confirmed, executable causes to actions.
func RepairActionForRootCause(rootCause string) (string, bool) {
	switch rootCause {
	case "PAYMENT_EVENT_NOT_PUBLISHED":
		return "RETRY_OUTBOX_PUBLISH", true
	case "INVENTORY_EVENT_NOT_CONSUMED":
		return "RETRY_INVENTORY_CONSUMER", true
	case "INVENTORY_DEDUCTION_FAILED":
		return "RETRY_INVENTORY_DEDUCTION", true
	case "INVENTORY_DEDUCTION_NOT_PERSISTED":
		return "RECONCILE_INVENTORY_STATE", true
	default:
		return "", false
	}
}

func remediationToolForAction(action string) string {
	switch action {
	case "RETRY_OUTBOX_PUBLISH":
		return "retry_outbox_publish"
	case "RETRY_INVENTORY_CONSUMER":
		return "retry_inventory_consumer"
	case "RETRY_INVENTORY_DEDUCTION":
		return "retry_inventory_deduction"
	case "RECONCILE_INVENTORY_STATE":
		return "reconcile_inventory_state"
	default:
		return ""
	}
}

func eventIDFromEvidence(evidence []Evidence) string {
	for _, item := range evidence {
		if item.ToolName != "get_outbox_status" {
			continue
		}
		var data map[string]any
		if json.Unmarshal(item.Data, &data) == nil {
			if id, ok := data["event_id"].(string); ok && id != "" {
				return id
			}
		}
	}
	return ""
}
