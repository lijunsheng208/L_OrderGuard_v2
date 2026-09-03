package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
)

// RepairActionForRootCause maps only confirmed, executable causes to actions.
func RepairActionForRootCause(rootCause string) (string, bool) {
	switch rootCause {
	case "PAYMENT_EVENT_NOT_CREATED":
		return "REBUILD_OUTBOX_EVENT", true
	case "PAYMENT_EVENT_NOT_PUBLISHED":
		return "RETRY_OUTBOX_PUBLISH", true
	case "INVENTORY_EVENT_NOT_CONSUMED":
		return "RETRY_INVENTORY_CONSUMER", true
	case "INVENTORY_DEDUCTION_FAILED":
		// A consumer-side processing error must replay the original event first;
		// direct deduction would bypass the failed consumer and can duplicate work.
		return "RETRY_INVENTORY_CONSUMER", true
	case "INVENTORY_DEDUCTION_NOT_PERSISTED":
		return "RECONCILE_INVENTORY_STATE", true
	default:
		return "", false
	}
}

func remediationToolForAction(action string) string {
	switch action {
	case "REBUILD_OUTBOX_EVENT":
		return "rebuild_outbox_event"
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

// remediationArguments builds the exact argument object accepted by each MCP
// tool. MCP schemas reject unrelated fields through additionalProperties=false.
func remediationArguments(action, orderID, eventID, idempotencyKey string, items []map[string]any) (map[string]any, error) {
	switch action {
	case "RETRY_OUTBOX_PUBLISH", "RETRY_INVENTORY_CONSUMER":
		if eventID == "" {
			return nil, fmt.Errorf("%s requires event_id", action)
		}
		return map[string]any{"event_id": eventID}, nil
	case "RETRY_INVENTORY_DEDUCTION":
		if orderID == "" || idempotencyKey == "" || len(items) == 0 {
			return nil, errors.New("RETRY_INVENTORY_DEDUCTION requires order_id, items and idempotency_key")
		}
		return map[string]any{"order_id": orderID, "items": items, "idempotency_key": idempotencyKey}, nil
	case "REBUILD_OUTBOX_EVENT", "RECONCILE_INVENTORY_STATE":
		if orderID == "" {
			return nil, fmt.Errorf("%s requires order_id", action)
		}
		return map[string]any{"order_id": orderID}, nil
	default:
		return nil, fmt.Errorf("unknown remediation action %s", action)
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
