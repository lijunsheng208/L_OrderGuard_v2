package agentruntime

import (
	"errors"
	"testing"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

// TestValidateTransition 验证阶段 3 状态机只允许声明的迁移。
func TestValidateTransition(t *testing.T) {
	if err := ValidateTransition(StatusCreated, StatusPlanning); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(StatusCreated, StatusEvidenceCollected); err == nil {
		t.Fatal("invalid transition was accepted")
	}
}

// TestToolGuardEnforcesOrderAndBudgets 验证订单边界、重复次数和总调用预算。
func TestToolGuardEnforcesOrderAndBudgets(t *testing.T) {
	definition := mcp.Tool{
		Name:        "get_order_snapshot",
		Annotations: mcp.ToolAnnotations{ReadOnlyHint: true},
	}
	guard := NewToolGuard("O-1", 2, 1)
	if err := guard.Authorize(definition, map[string]any{"order_id": "O-1"}); err != nil {
		t.Fatal(err)
	}
	if err := guard.Authorize(definition, map[string]any{"order_id": "O-1"}); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("duplicate call error = %v", err)
	}
	if err := guard.Authorize(definition, map[string]any{"order_id": "O-2"}); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("cross-order call error = %v", err)
	}
}
