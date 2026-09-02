package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

const knowledgeAgentPrompt = `你是 OrderGuard Knowledge Agent。
根据当前订单问题，使用提供的 knowledge-mcp 只读工具检索相关故障手册、历史事故、服务拓扑、状态机和修复策略。
至少调用一次工具；不得调用业务或观测工具，不得执行修复。
完成后只输出 JSON：{"summary":"...","sources":["knowledge/..."] ,"gaps":["..."]}`

// RunDiagnosisAgent 调用 Eino Diagnosis Agent 并校验结构化输出。
func (o *Orchestrator) RunDiagnosisAgent(ctx context.Context, run Run) (Diagnosis, error) {
	if o.phase4Model == nil {
		return o.DiagnoseFromEvidence(ctx, run)
	}
	evidence, err := o.repository.ListEvidence(ctx, run.ID)
	if err != nil {
		return Diagnosis{}, err
	}
	input, _ := json.Marshal(map[string]any{"message": run.UserMessage, "order_id": run.OrderID, "evidence": evidence})
	response, err := o.phase4Model.Generate(ctx, []*schema.Message{schema.SystemMessage(BuildDiagnosisPrompt()), schema.UserMessage(string(input))})
	if err != nil {
		return Diagnosis{}, fmt.Errorf("diagnosis agent: %w", err)
	}
	var result Diagnosis
	if err := decodeModelJSON(response.Content, &result); err != nil {
		return Diagnosis{}, fmt.Errorf("decode diagnosis: %w", err)
	}
	if err := validateDiagnosis(result, evidence); err != nil {
		return Diagnosis{}, err
	}
	return result, nil
}

// RunCriticAgent 调用 Eino Critic Agent，并在模型结论后执行确定性证据校验。
func (o *Orchestrator) RunCriticAgent(ctx context.Context, run Run, diagnosis Diagnosis) error {
	if o.phase4Model == nil {
		return o.CriticReview(ctx, run.ID, diagnosis)
	}
	evidence, err := o.repository.ListEvidence(ctx, run.ID)
	if err != nil {
		return err
	}
	input, _ := json.Marshal(map[string]any{"order_id": run.OrderID, "diagnosis": diagnosis, "evidence": evidence})
	response, err := o.phase4Model.Generate(ctx, []*schema.Message{schema.SystemMessage(CriticPrompt), schema.UserMessage(string(input))})
	if err != nil {
		return fmt.Errorf("critic agent: %w", err)
	}
	var result struct {
		Approved     bool     `json:"approved"`
		Issues       []string `json:"issues"`
		MissingTools []string `json:"missing_tools"`
	}
	if err := decodeModelJSON(response.Content, &result); err != nil {
		return fmt.Errorf("decode critic: %w", err)
	}
	if err := o.CriticReview(ctx, run.ID, diagnosis); err != nil {
		return err
	}
	if !result.Approved {
		return fmt.Errorf("critic rejected diagnosis: %v", result.Issues)
	}
	return nil
}

func validateDiagnosis(result Diagnosis, evidence []Evidence) error {
	allowed := RootCauseCodes()
	if !allowed[result.RootCause] || result.Confidence < 0 || result.Confidence > 1 || len(result.EvidenceIDs) == 0 {
		return errors.New("invalid diagnosis output")
	}
	known := map[string]bool{}
	for _, item := range evidence {
		known[item.EvidenceID] = true
	}
	for _, id := range result.EvidenceIDs {
		if !known[id] {
			return fmt.Errorf("diagnosis references unknown evidence_id %s", id)
		}
	}
	for _, alternative := range result.Alternatives {
		if !allowed[alternative.Cause] || alternative.Confidence < 0 || alternative.Confidence > 1 {
			return fmt.Errorf("invalid alternative root cause %s", alternative.Cause)
		}
	}
	if result.RootCause == "NO_CONFIRMED_ROOT_CAUSE" && result.Confidence > 0.5 {
		return errors.New("inconclusive diagnosis confidence is too high")
	}
	return nil
}

// ProcessPhase4 在 Investigator 后执行知识检索、诊断和 Critic 审查。
func (o *Orchestrator) ProcessPhase4(parent context.Context, run Run) error {
	if !o.Ready() {
		return errors.New("agent model is not configured")
	}
	ctx, cancel := context.WithTimeout(parent, o.runTimeout)
	defer cancel()
	if err := o.Process(ctx, run); err != nil {
		return err
	}
	return nil
}

// CollectKnowledge 调用本地知识 MCP 并把结果保存为证据。
func (o *Orchestrator) CollectKnowledge(ctx context.Context, run *Run) error {
	if o.phase4Model == nil {
		return errors.New("knowledge agent model is not configured")
	}
	chatModel, ok := o.phase4Model.(model.ToolCallingChatModel)
	if !ok {
		return errors.New("knowledge agent requires a tool-calling model")
	}
	if _, ok := o.knowledgeTool("search_runbook"); !ok {
		return errors.New("knowledge-mcp is not configured")
	}
	step, err := o.repository.StartStep(ctx, run.ID, "KNOWLEDGE", "local-markdown", map[string]any{"order_id": run.OrderID})
	if err != nil {
		return err
	}
	guard := NewToolGuard(run.OrderID, 8, 2)
	agent, err := react.NewAgent(ctx, &react.AgentConfig{ToolCallingModel: chatModel, ToolsConfig: compose.ToolsNodeConfig{Tools: o.knowledgeTools(*run, step, guard)}, MaxStep: 12})
	if err != nil {
		return err
	}
	input, _ := json.Marshal(map[string]any{"message": run.UserMessage, "order_id": run.OrderID})
	_, err = agent.Generate(ctx, []*schema.Message{schema.SystemMessage(knowledgeAgentPrompt), schema.UserMessage(string(input))})
	if err != nil {
		_ = o.repository.FinishStep(ctx, &step, map[string]any{"error": err.Error()}, 0, 0, "KNOWLEDGE_FAILED")
		return err
	}
	if guard.Total() == 0 {
		return errors.New("knowledge agent collected no observations")
	}
	_ = o.repository.FinishStep(ctx, &step, map[string]any{"tool_calls": guard.Total()}, 0, 0, "")
	return nil
}

// knowledgeTools 只向 Knowledge Agent 暴露 knowledge-mcp 工具。
func (o *Orchestrator) knowledgeTools(run Run, step AgentStep, guard *ToolGuard) []tool.BaseTool {
	result := make([]tool.BaseTool, 0, 5)
	for _, name := range []string{"search_runbook", "search_historical_incidents", "get_service_topology", "get_state_machine", "get_repair_policy"} {
		if registered, ok := o.knowledge.Get(name); ok {
			result = append(result, &EinoMCPTool{registered: registered, run: run, step: step, guard: guard, repository: o.repository})
		}
	}
	return result
}

// knowledgeTool 查找由 knowledge-mcp 动态发现的工具。
func (o *Orchestrator) knowledgeTool(name string) (RegisteredTool, bool) {
	if o.knowledge == nil {
		return RegisteredTool{}, false
	}
	return o.knowledge.Get(name)
}

// saveEnvelopeEvidence 将 MCP Envelope 规范化为不可变证据。
func saveEnvelopeEvidence(ctx context.Context, repo *Repository, run *Run, step AgentStep, callID, tool string, args map[string]any, envelope mcp.Envelope) error {
	a, _ := json.Marshal(args)
	d, _ := json.Marshal(envelope.Data)
	raw, _ := json.Marshal(map[string]any{"tool_name": tool, "arguments": args, "source": envelope.Source, "collected_at": envelope.CollectedAt, "data": envelope.Data})
	sum := sha256.Sum256(raw)
	collected, err := time.Parse(time.RFC3339Nano, envelope.CollectedAt)
	if err != nil {
		return err
	}
	return repo.SaveEvidence(ctx, Evidence{EvidenceID: envelope.EvidenceID, RunID: run.ID, AgentStepID: step.ID, ToolCallID: callID, ToolName: tool, Arguments: a, Source: envelope.Source, CollectedAt: collected, Data: d, ContentHash: hex.EncodeToString(sum[:])})
}

// DiagnoseFromEvidence 依据业务证据生成受限根因枚举，不凭空创建证据。
func (o *Orchestrator) DiagnoseFromEvidence(ctx context.Context, run Run) (Diagnosis, error) {
	evidence, err := o.repository.ListEvidence(ctx, run.ID)
	if err != nil {
		return Diagnosis{}, err
	}
	ids := make([]string, 0)
	hasInventory := false
	notDeducted := false
	hasPayment := false
	for _, item := range evidence {
		ids = append(ids, item.EvidenceID)
		if item.ToolName == "get_inventory_status" {
			hasInventory = true
			notDeducted = string(item.Data) != "" && (contains(string(item.Data), "NOT_DEDUCTED"))
		}
		if item.ToolName == "get_payment_status" {
			hasPayment = true
		}
	}
	if !hasInventory || !hasPayment || len(ids) == 0 {
		return Diagnosis{}, errors.New("insufficient evidence")
	}
	d := Diagnosis{RootCause: "NO_CONFIRMED_ROOT_CAUSE", Confidence: 0.35, EvidenceIDs: ids}
	if notDeducted {
		d.RootCause = "PAYMENT_EVENT_NOT_PUBLISHED"
		d.Confidence = 0.78
		d.RecommendedAction = "DEDUCT_INVENTORY_ONCE"
	}
	return d, nil
}

// CriticReview 校验诊断证据引用和允许的根因枚举。
func (o *Orchestrator) CriticReview(ctx context.Context, runID string, diagnosis Diagnosis) error {
	evidence, err := o.repository.ListEvidence(ctx, runID)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, e := range evidence {
		known[e.EvidenceID] = true
	}
	for _, id := range diagnosis.EvidenceIDs {
		if !known[id] {
			return fmt.Errorf("critic rejected unknown evidence_id %s", id)
		}
	}
	if !RootCauseCodes()[diagnosis.RootCause] {
		return errors.New("critic rejected unknown root cause")
	}
	return nil
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
