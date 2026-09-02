package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lijunsheng/orderguard/internal/agentruntime"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

type phase3ScriptedModel struct {
	tools []*schema.ToolInfo
}

// Generate 为 Planner 返回固定计划，并按已有工具结果驱动 Investigator ReAct。
func (m *phase3ScriptedModel) Generate(
	_ context.Context,
	input []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	if len(input) > 0 && strings.Contains(input[0].Content, "OrderGuard Planner") {
		var request struct {
			OrderID string `json:"order_id"`
		}
		_ = json.Unmarshal([]byte(input[len(input)-1].Content), &request)
		plan := agentruntime.Plan{
			Goal: "collect order delivery evidence",
			Steps: []agentruntime.PlanStep{
				{ID: "step-1", Purpose: "order", Tool: "get_order_snapshot", Args: map[string]any{"order_id": request.OrderID}},
				{ID: "step-2", Purpose: "payment", Tool: "get_payment_status", Args: map[string]any{"order_id": request.OrderID}},
				{ID: "step-3", Purpose: "inventory", Tool: "get_inventory_status", Args: map[string]any{"order_id": request.OrderID}},
				{ID: "step-4", Purpose: "event", Tool: "get_event_record", Args: map[string]any{"order_id": request.OrderID, "event_type": "payment.succeeded"}},
			},
		}
		encoded, _ := json.Marshal(plan)
		return schema.AssistantMessage(string(encoded), nil), nil
	}
	return m.investigatorResponse(input)
}

// Stream 在模型无关测试中未使用流式生成。
func (m *phase3ScriptedModel) Stream(
	_ context.Context,
	_ []*schema.Message,
	_ ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errorsNew("scripted model does not stream")
}

// WithTools 返回绑定工具定义的独立脚本模型。
func (m *phase3ScriptedModel) WithTools(
	tools []*schema.ToolInfo,
) (model.ToolCallingChatModel, error) {
	return &phase3ScriptedModel{tools: tools}, nil
}

// investigatorResponse 根据工具消息数量选择下一次工具调用或最终输出。
func (m *phase3ScriptedModel) investigatorResponse(
	input []*schema.Message,
) (*schema.Message, error) {
	var orderID string
	if len(input) > 1 {
		var request struct {
			OrderID string `json:"order_id"`
		}
		_ = json.Unmarshal([]byte(input[1].Content), &request)
		orderID = request.OrderID
	}
	toolMessages := make([]*schema.Message, 0)
	for _, message := range input {
		if message.Role == schema.Tool {
			toolMessages = append(toolMessages, message)
		}
	}
	toolNames := []string{
		"get_order_snapshot", "get_payment_status",
		"get_inventory_status", "get_event_record",
	}
	if len(toolMessages) < len(toolNames) {
		arguments := map[string]any{"order_id": orderID}
		if toolNames[len(toolMessages)] == "get_event_record" {
			arguments["event_type"] = "payment.succeeded"
		}
		encoded, _ := json.Marshal(arguments)
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: fmt.Sprintf("script-call-%d", len(toolMessages)+1),
			Function: schema.FunctionCall{
				Name: toolNames[len(toolMessages)], Arguments: string(encoded),
			},
		}}), nil
	}
	facts := make([]agentruntime.Fact, 0, len(toolMessages))
	for _, message := range toolMessages {
		var envelope mcp.Envelope
		if err := json.Unmarshal([]byte(message.Content), &envelope); err != nil {
			return nil, err
		}
		facts = append(facts, agentruntime.Fact{
			EvidenceID: envelope.EvidenceID,
			Fact:       "structured evidence collected from " + envelope.Source,
		})
	}
	result := agentruntime.InvestigationResult{
		Summary: "order delivery evidence collected", Facts: facts,
		RemainingQuestions: []string{},
	}
	encoded, _ := json.Marshal(result)
	return schema.AssistantMessage(string(encoded), nil), nil
}

// errorsNew 避免测试模型依赖外部错误类型。
func errorsNew(message string) error {
	return fmt.Errorf("%s", message)
}

// TestPhase3EinoReActCollectsAndPersistsEvidence 验证 Eino ReAct 的模型无关完整链路。
func TestPhase3EinoReActCollectsAndPersistsEvidence(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("set INTEGRATION=1 to run phase 3 integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, env(
		"DATABASE_URL",
		"postgres://orderguard:orderguard@localhost:55432/orderguard?sslmode=disable",
	))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	orderID := createPhase3Order(t, ctx, pool)

	registry := agentruntime.NewMCPRegistry(
		"http://localhost:8091/mcp", "http://localhost:8092/mcp",
		&http.Client{Timeout: 5 * time.Second},
	)
	if err := registry.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	repository := agentruntime.NewRepository(pool)
	chatModel := &phase3ScriptedModel{}
	planner := agentruntime.NewPlanner(chatModel, "phase3-scripted-model", registry)
	investigator := agentruntime.NewInvestigator(
		chatModel, "phase3-scripted-model", registry, repository,
	)
	orchestrator := agentruntime.NewOrchestrator(
		repository, planner, investigator, nilLogger(),
	)
	created, err := orchestrator.CreateRun(
		ctx, "调查订单支付和库存链路", orderID, "trace-"+orderID,
	)
	if err != nil {
		t.Fatal(err)
	}
	run, found, err := repository.ClaimCreated(ctx)
	if err != nil || !found || run.ID != created.ID {
		t.Fatalf("claim run=%+v found=%v err=%v", run, found, err)
	}
	if err := orchestrator.Process(ctx, run); err != nil {
		t.Fatal(err)
	}
	assertPhase3Persistence(t, ctx, pool, repository, created.ID)
}

// createPhase3Order 创建阶段 3 调查所需的真实业务数据。
func createPhase3Order(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	suffix := time.Now().UnixNano()
	orderID := fmt.Sprintf("IT-P3-O-%d", suffix)
	paymentID := fmt.Sprintf("IT-P3-P-%d", suffix)
	skuID := fmt.Sprintf("IT-P3-SKU-%d", suffix)
	if _, err := pool.Exec(ctx, `
		INSERT INTO inventory.stocks (sku_id, available, reserved, version)
		VALUES ($1, 20, 0, 1)`, skuID,
	); err != nil {
		t.Fatal(err)
	}
	postJSON(t, "http://localhost:8081/demo/orders", map[string]any{
		"id": orderID, "user_id": "phase3-user",
		"items": []map[string]any{{"sku_id": skuID, "quantity": 1, "unit_price": 100}},
	}, http.StatusCreated)
	postJSON(t, "http://localhost:8082/demo/orders/"+orderID+"/pay", map[string]any{
		"payment_id": paymentID, "amount": 100,
	}, http.StatusOK)
	waitFor(t, 10*time.Second, func() bool {
		var count int
		return pool.QueryRow(ctx, `
			SELECT count(*) FROM inventory.deductions WHERE order_id = $1`, orderID,
		).Scan(&count) == nil && count == 1
	})
	return orderID
}

// assertPhase3Persistence 核对状态、步骤、证据、MCP 审计和可靠事件。
func assertPhase3Persistence(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	repository *agentruntime.Repository,
	runID string,
) {
	t.Helper()
	run, err := repository.GetRun(ctx, runID)
	if err != nil || run.Status != agentruntime.StatusEvidenceCollected {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	steps, err := repository.ListSteps(ctx, runID)
	if err != nil || len(steps) != 2 || steps[0].Status != "SUCCEEDED" || steps[1].Status != "SUCCEEDED" {
		t.Fatalf("steps=%+v err=%v", steps, err)
	}
	evidence, err := repository.ListEvidence(ctx, runID)
	if err != nil || len(evidence) != 4 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	for _, item := range evidence {
		if len(item.ContentHash) != 64 || item.ToolCallID == "" {
			t.Fatalf("invalid evidence: %+v", item)
		}
	}
	var audited int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM mcp.tool_calls
		WHERE run_id = $1 AND agent_step_id = $2 AND caller = 'investigator-agent'`,
		runID, steps[1].ID,
	).Scan(&audited); err != nil || audited != 4 {
		t.Fatalf("audited=%d err=%v", audited, err)
	}
	events, err := repository.ListEvents(ctx, runID, 0, 100)
	if err != nil || len(events) < 15 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	if events[0].Type != "run.created" || events[len(events)-1].Type != "run.completed" {
		t.Fatalf("unexpected event range: first=%s last=%s", events[0].Type, events[len(events)-1].Type)
	}
}

// nilLogger 返回丢弃输出的测试日志器。
func nilLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
