package agentruntime

import (
	"errors"
	"testing"

	"github.com/lijunsheng/orderguard/internal/mcp"
)

// TestDecodeModelJSONExtractsWrappedObject 验证模型添加说明文字时仍能安全提取 JSON。
func TestDecodeModelJSONExtractsWrappedObject(t *testing.T) {
	var value struct {
		Summary string `json:"summary"`
	}
	content := "All six steps have been completed. Here is the result:\n`json\n{\"summary\":\"ok\"}\n`"
	if err := decodeModelJSON(content, &value); err != nil {
		t.Fatal(err)
	}
	if value.Summary != "ok" {
		t.Fatalf("summary = %q", value.Summary)
	}
}

// TestValidateTransition 验证阶段 3 状态机只允许声明的迁移。
func TestValidateTransition(t *testing.T) {
	if err := ValidateTransition(StatusCreated, StatusPlanning); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(StatusCreated, StatusEvidenceCollected); err == nil {
		t.Fatal("invalid transition was accepted")
	}
	if err := ValidateTransition(StatusEvidenceCollected, StatusNoAnomaly); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(StatusDiagnosing, StatusInvestigating); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(StatusDiagnosing, StatusEvidenceCollected); err != nil {
		t.Fatal(err)
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
