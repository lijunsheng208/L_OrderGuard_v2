package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

// RunDiagnosisAgent 调用 Eino Diagnosis Agent 并校验结构化输出。
func (o *Orchestrator) RunDiagnosisAgent(ctx context.Context, run Run, investigation InvestigationResult, knowledge KnowledgeResult) (Diagnosis, error) {
	if o.phase4Model == nil {
		return Diagnosis{}, errors.New("diagnosis agent model is not configured")
	}
	evidence, err := o.repository.ListEvidence(ctx, run.ID)
	if err != nil {
		return Diagnosis{}, err
	}
	inputPayload := map[string]any{
		"message": run.UserMessage, "order_id": run.OrderID,
		"investigation": investigation, "knowledge": knowledge,
		"evidence": evidence,
	}
	input, _ := json.Marshal(inputPayload)
	response, err := o.phase4Model.Generate(ctx, []*schema.Message{schema.SystemMessage(BuildDiagnosisPrompt()), schema.UserMessage(string(input))})
	if err != nil {
		return Diagnosis{}, fmt.Errorf("diagnosis agent: %w", err)
	}
	var result Diagnosis
	if err := decodeModelJSON(response.Content, &result); err != nil {
		return Diagnosis{}, fmt.Errorf("decode diagnosis: %w", err)
	}
	return result, nil
}

func deterministicDiagnosis(evidence []Evidence) Diagnosis {
	signals := diagnosticSignals{}
	ids := make([]string, 0, len(evidence))
	for _, item := range evidence {
		ids = append(ids, item.EvidenceID)
		signals.observe(item)
	}
	if len(ids) == 0 || !signals.hasInventory || !signals.hasPayment {
		return Diagnosis{RootCause: "NO_CONFIRMED_ROOT_CAUSE", Confidence: 0.35, EvidenceIDs: ids}
	}
	return signals.diagnosis(ids)
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

// ValidateDiagnosisRootCause verifies only whether the Diagnosis Agent's
// selected root cause is supported by its cited evidence. It intentionally
// never derives or substitutes a different root cause.
func ValidateDiagnosisRootCause(result Diagnosis, evidence []Evidence) RootCauseValidation {
	validation := RootCauseValidation{}
	known := make(map[string]Evidence, len(evidence))
	for _, item := range evidence {
		known[item.EvidenceID] = item
	}
	if !RootCauseCodes()[result.RootCause] {
		validation.Issues = append(validation.Issues, "根因不属于允许的根因目录")
		return validation
	}
	if len(result.EvidenceIDs) == 0 {
		validation.Issues = append(validation.Issues, "根因没有引用任何证据")
		return validation
	}

	selected := make([]Evidence, 0, len(result.EvidenceIDs))
	seen := map[string]bool{}
	for _, id := range result.EvidenceIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		item, ok := known[id]
		if !ok {
			validation.Issues = append(validation.Issues, "根因引用了不存在的证据 "+id)
			continue
		}
		selected = append(selected, item)
	}
	if len(validation.Issues) > 0 {
		return validation
	}

	signals := diagnosticSignals{}
	for _, item := range selected {
		signals.observe(item)
	}
	require := func(condition bool, issue string) {
		if !condition {
			validation.Issues = append(validation.Issues, issue)
		}
	}
	if signals.dirtyData || signals.consumerSuccess && signals.consumerFailure {
		validation.Issues = append(validation.Issues, "引用证据包含脏数据或互相冲突的消费结果")
	}

	switch result.RootCause {
	case "NO_ISSUE":
		require(signals.paymentSuccess, "缺少支付 SUCCESS 证据")
		require(signals.hasOutbox && signals.outboxStatus == "PUBLISHED", "缺少 Outbox PUBLISHED 证据")
		require(signals.inventoryDeducted, "缺少库存 DEDUCTED 证据")
		require(!signals.consumerFailure, "存在库存消费者失败证据")
	case "PAYMENT_EVENT_NOT_CREATED":
		require(signals.paymentSuccess, "缺少支付 SUCCESS 证据")
		require(signals.hasOutbox && signals.outboxFound != nil && !*signals.outboxFound, "缺少 Outbox found=false 证据")
	case "PAYMENT_EVENT_NOT_PUBLISHED":
		require(signals.paymentSuccess, "缺少支付 SUCCESS 证据")
		require(signals.hasOutbox && (signals.outboxStatus == "PENDING" || signals.outboxStatus == "FAILED"), "缺少 Outbox PENDING 或 FAILED 证据")
	case "EVENT_NOT_AVAILABLE_AFTER_PUBLISH":
		require(signals.hasOutbox && signals.outboxStatus == "PUBLISHED", "缺少 Outbox PUBLISHED 证据")
		require(signals.eventFound != nil && !*signals.eventFound, "缺少事件 found=false 证据")
	case "INVENTORY_EVENT_NOT_CONSUMED":
		require(signals.eventFound != nil && *signals.eventFound, "缺少 payment.succeeded 事件存在的证据")
		require(signals.inventoryNotDeducted, "缺少库存 NOT_DEDUCTED 证据")
		require(!signals.consumerReceived && !signals.consumerSuccess && !signals.consumerFailure, "现有证据表明库存消费者已经处理或尝试处理事件")
	case "INVENTORY_DEDUCTION_FAILED":
		require(signals.inventoryNotDeducted, "缺少库存 NOT_DEDUCTED 证据")
		require(signals.consumerReceived && signals.consumerFailure, "缺少库存消费者接收事件并处理失败的证据")
	case "INVENTORY_DEDUCTION_NOT_PERSISTED":
		require(signals.inventoryNotDeducted, "缺少库存 NOT_DEDUCTED 证据")
		require(signals.consumerSuccess, "缺少库存扣减成功或 OK 的日志/Trace 证据")
		require(!signals.consumerFailure, "存在库存扣减失败或回滚证据")
	case "INVENTORY_CONSUMER_UNAVAILABLE":
		require(signals.hasOutbox && signals.outboxStatus == "PUBLISHED", "缺少 Outbox PUBLISHED 证据")
		require(signals.inventoryNotDeducted, "缺少库存 NOT_DEDUCTED 证据")
		require(signals.consumerFailure, "缺少库存消费者不可用或处理失败的证据")
	case "NO_CONFIRMED_ROOT_CAUSE":
		validation.Issues = append(validation.Issues, "Diagnosis 尚未确认根因，需要补充调查")
	}

	validation.Valid = len(validation.Issues) == 0
	return validation
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
func (o *Orchestrator) CollectKnowledge(ctx context.Context, run *Run, investigation InvestigationResult) (KnowledgeResult, error) {
	if o.phase4Model == nil {
		return KnowledgeResult{}, errors.New("knowledge intent model is not configured")
	}
	if _, ok := o.knowledgeTool("search_runbook"); !ok {
		return KnowledgeResult{}, errors.New("knowledge-mcp is not configured")
	}
	step, err := o.repository.StartStep(ctx, run.ID, "KNOWLEDGE", "local-markdown", map[string]any{"order_id": run.OrderID})
	if err != nil {
		return KnowledgeResult{}, err
	}
	input, _ := json.Marshal(map[string]any{"order_id": run.OrderID, "investigation": investigation})
	response, err := o.phase4Model.Generate(ctx, []*schema.Message{schema.SystemMessage(KnowledgeIntentPrompt), schema.UserMessage(string(input))})
	if err != nil {
		_ = o.repository.FinishStep(ctx, &step, map[string]any{"error": err.Error()}, 0, 0, "KNOWLEDGE_FAILED")
		return KnowledgeResult{}, fmt.Errorf("knowledge intent agent: %w", err)
	}
	var intents KnowledgeIntentResult
	if err := decodeModelJSON(response.Content, &intents); err != nil {
		return KnowledgeResult{}, fmt.Errorf("decode knowledge intents: %w", err)
	}
	plan, err := buildKnowledgePlan(intents)
	if err != nil {
		return KnowledgeResult{}, err
	}
	result := KnowledgeResult{Intents: intents, Plan: plan}
	if err := o.executeKnowledgePlan(ctx, *run, step, &result); err != nil {
		return result, err
	}
	inputTokens, outputTokens := messageUsage(response)
	_ = o.repository.FinishStep(ctx, &step, result, inputTokens, outputTokens, "")
	return result, nil
}

// buildKnowledgePlan 将模型意图映射为受限、去重的 MCP 调用计划。
func buildKnowledgePlan(intents KnowledgeIntentResult) (KnowledgeExecutionPlan, error) {
	if len(intents.Intents) == 0 || len(intents.Intents) > 4 {
		return KnowledgeExecutionPlan{}, errors.New("knowledge intents must contain 1 to 4 items")
	}
	seen := map[string]bool{}
	plan := KnowledgeExecutionPlan{}
	for _, in := range intents.Intents {
		if (in.Type == "historical_incident" || in.Type == "runbook") && (len(in.Keywords) < 1 || len(in.Keywords) > 4) {
			return KnowledgeExecutionPlan{}, errors.New("knowledge search intent must contain 1 to 4 keywords")
		}
		if len(in.Keywords) > 4 {
			return KnowledgeExecutionPlan{}, errors.New("knowledge intent has too many keywords")
		}
		for i := range in.Keywords {
			in.Keywords[i] = strings.TrimSpace(in.Keywords[i])
		}
		var toolName string
		args := map[string]any{}
		switch in.Type {
		case "historical_incident":
			toolName = "search_historical_incidents"
		case "runbook":
			toolName = "search_runbook"
		case "service_topology":
			toolName = "get_service_topology"
			if strings.TrimSpace(in.Service) == "" {
				return KnowledgeExecutionPlan{}, errors.New("service_topology requires service")
			}
			args["service"] = strings.TrimSpace(in.Service)
		case "state_machine":
			toolName = "get_state_machine"
			if strings.TrimSpace(in.Domain) == "" {
				return KnowledgeExecutionPlan{}, errors.New("state_machine requires domain")
			}
			args["domain"] = strings.TrimSpace(in.Domain)
		case "repair_policy":
			toolName = "get_repair_policy"
			if strings.TrimSpace(in.Action) == "" {
				return KnowledgeExecutionPlan{}, errors.New("repair_policy requires action")
			}
			args["action"] = strings.TrimSpace(in.Action)
		default:
			return KnowledgeExecutionPlan{}, fmt.Errorf("unknown knowledge intent type: %s", in.Type)
		}
		if toolName == "search_runbook" || toolName == "search_historical_incidents" {
			query := strings.TrimSpace(strings.Join(in.Keywords, " "))
			if query == "" {
				return KnowledgeExecutionPlan{}, errors.New("knowledge search intent has empty keywords")
			}
			args["query"] = query
		}
		keyBytes, _ := json.Marshal([]any{toolName, args})
		key := string(keyBytes)
		if seen[key] {
			continue
		}
		seen[key] = true
		plan.Calls = append(plan.Calls, KnowledgeCall{Tool: toolName, Arguments: args, Reason: in.Reason})
	}
	if len(plan.Calls) == 0 {
		return KnowledgeExecutionPlan{}, errors.New("knowledge plan contains no executable calls")
	}
	return plan, nil
}

// executeKnowledgePlan 按 Go 计划直接调用知识 MCP，并保存每次结果为证据。
func (o *Orchestrator) executeKnowledgePlan(ctx context.Context, run Run, step AgentStep, result *KnowledgeResult) error {
	for _, call := range result.Plan.Calls {
		registered, ok := o.knowledgeTool(call.Tool)
		if !ok {
			return fmt.Errorf("knowledge tool unavailable: %s", call.Tool)
		}
		if err := mcpValidate(registered, call.Arguments); err != nil {
			return fmt.Errorf("knowledge arguments for %s: %w", call.Tool, err)
		}
		callID := newID("call")
		_, _ = o.repository.AppendEvent(ctx, run.ID, "tool.call_started", map[string]any{"step_id": step.ID, "tool_call_id": callID, "tool_name": call.Tool, "arguments": call.Arguments})
		response, err := registered.Client.CallToolWithMetadata(ctx, call.Tool, call.Arguments, mcp.RequestMetadata{RunID: run.ID, AgentStepID: step.ID, TraceID: run.TraceID, ToolCallID: callID, Caller: "knowledge-agent"})
		if err != nil {
			_, _ = o.repository.AppendEvent(ctx, run.ID, "tool.call_completed", map[string]any{"step_id": step.ID, "tool_call_id": callID, "tool_name": call.Tool, "success": false})
			return err
		}
		if response.StructuredContent.Success {
			if err := saveEnvelopeEvidence(ctx, o.repository, &run, step, callID, call.Tool, call.Arguments, response.StructuredContent); err != nil {
				return err
			}
		}
		_, _ = o.repository.AppendEvent(ctx, run.ID, "tool.call_completed", map[string]any{"step_id": step.ID, "tool_call_id": callID, "tool_name": call.Tool, "success": response.StructuredContent.Success, "evidence_id": response.StructuredContent.EvidenceID})
	}
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
	signals := diagnosticSignals{}
	for _, item := range evidence {
		ids = append(ids, item.EvidenceID)
		signals.observe(item)
	}
	if len(ids) == 0 || !signals.hasInventory || !signals.hasPayment {
		return Diagnosis{}, errors.New("insufficient evidence")
	}
	d := signals.diagnosis(ids)
	return d, nil
}

type diagnosticSignals struct {
	hasInventory, hasPayment, hasOutbox, paymentSuccess, inventoryDeducted, inventoryNotDeducted bool
	dirtyData                                                                                    bool
	outboxStatus                                                                                 string
	outboxFound, eventFound                                                                      *bool
	consumerReceived, consumerSuccess, consumerFailure                                           bool
}

func (s *diagnosticSignals) observe(item Evidence) {
	var data map[string]any
	if json.Unmarshal(item.Data, &data) != nil {
		return
	}
	switch item.ToolName {
	case "get_inventory_status":
		s.hasInventory = true
		v, _ := data["status"].(string)
		if v == "DEDUCTED" {
			s.inventoryDeducted = true
		}
		if v == "NOT_DEDUCTED" {
			s.inventoryNotDeducted = true
		}
		if v != "DEDUCTED" && v != "NOT_DEDUCTED" || stringValue(data, "data_quality") == "DIRTY" {
			s.dirtyData = true
		}
	case "get_payment_status":
		s.hasPayment = true
		s.paymentSuccess = stringValue(data, "status") == "SUCCESS"
	case "get_outbox_status":
		s.hasOutbox = true
		if v, ok := data["found"].(bool); ok {
			s.outboxFound = &v
		}
		s.outboxStatus = stringValue(data, "publish_status")
	case "get_event_record":
		if v, ok := data["found"].(bool); ok {
			s.eventFound = &v
		}
	case "search_service_logs", "get_trace":
		b, _ := json.Marshal(data)
		text := strings.ToLower(string(b))
		inventorySignal := strings.Contains(text, "inventory") && (strings.Contains(text, "consume") || strings.Contains(text, "deduct") || strings.Contains(text, "received") || strings.Contains(text, "库存"))
		if inventorySignal {
			s.consumerReceived = true
			if strings.Contains(text, "success") || strings.Contains(text, "\"status\":\"ok\"") || strings.Contains(text, "\"status\":\"success\"") {
				s.consumerSuccess = true
			}
			if strings.Contains(text, "error") || strings.Contains(text, "fail") || strings.Contains(text, "rollback") {
				s.consumerFailure = true
			}
		}
	}
}

func stringValue(data map[string]any, key string) string {
	v, _ := data[key].(string)
	return strings.ToUpper(strings.TrimSpace(v))
}

func (s diagnosticSignals) diagnosis(ids []string) Diagnosis {
	d := Diagnosis{RootCause: "NO_CONFIRMED_ROOT_CAUSE", Confidence: 0.35, EvidenceIDs: ids}
	if s.dirtyData || s.consumerSuccess && s.consumerFailure {
		d.Confidence = 0.4
		return d
	}
	if s.paymentSuccess && s.inventoryDeducted && s.hasOutbox && s.outboxStatus == "PUBLISHED" {
		d.RootCause, d.Confidence = "NO_ISSUE", 0.95
		return d
	}
	if s.paymentSuccess && s.outboxFound != nil && !*s.outboxFound {
		d.RootCause, d.Confidence = "PAYMENT_EVENT_NOT_CREATED", 0.9
		return d
	}
	if s.paymentSuccess && (s.outboxStatus == "PENDING" || s.outboxStatus == "FAILED") {
		d.RootCause, d.Confidence = "PAYMENT_EVENT_NOT_PUBLISHED", 0.9
		d.RecommendedAction = "RETRY_OUTBOX_PUBLISH"
		return d
	}
	if s.outboxStatus == "PUBLISHED" && s.eventFound != nil && !*s.eventFound {
		d.RootCause, d.Confidence = "EVENT_NOT_AVAILABLE_AFTER_PUBLISH", 0.88
		return d
	}
	if s.inventoryNotDeducted && s.eventFound != nil && *s.eventFound {
		if s.consumerSuccess {
			d.RootCause, d.Confidence = "INVENTORY_DEDUCTION_NOT_PERSISTED", 0.82
		} else if s.consumerFailure {
			d.RootCause, d.Confidence = "INVENTORY_DEDUCTION_FAILED", 0.82
		} else if !s.consumerReceived {
			d.RootCause, d.Confidence = "INVENTORY_EVENT_NOT_CONSUMED", 0.78
		}
	}
	return d
}

func evidenceRequiresInconclusive(evidence []Evidence) bool {
	signals := diagnosticSignals{}
	for _, item := range evidence {
		signals.observe(item)
	}
	return signals.dirtyData || signals.consumerSuccess && signals.consumerFailure
}

// CriticReview 保留为兼容入口，只校验候选根因是否被引用证据支持。
// 它不再计算“预期根因”，也不会覆盖 Diagnosis Agent 的结论。
func (o *Orchestrator) CriticReview(ctx context.Context, runID string, diagnosis Diagnosis) error {
	evidence, err := o.repository.ListEvidence(ctx, runID)
	if err != nil {
		return err
	}
	validation := ValidateDiagnosisRootCause(diagnosis, evidence)
	if !validation.Valid {
		return fmt.Errorf("diagnosis root cause is not supported: %v", validation.Issues)
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

// RunVerifyAgent 使用 Eino Verify Agent 审查修复后的状态断言。
func (o *Orchestrator) RunVerifyAgent(ctx context.Context, run Run, facts map[string]any) error {
	if o.phase4Model == nil {
		return errors.New("verify agent model is not configured")
	}
	input, _ := json.Marshal(map[string]any{"order_id": run.OrderID, "facts": facts, "required_assertions": []string{"inventory.status == DEDUCTED", "successful_deduction_count == 1"}})
	response, err := o.phase4Model.Generate(ctx, []*schema.Message{schema.SystemMessage(VerifyPrompt), schema.UserMessage(string(input))})
	if err != nil {
		return fmt.Errorf("verify agent: %w", err)
	}
	var result struct {
		Approved   bool   `json:"approved"`
		Summary    string `json:"summary,omitempty"`
		Assertions []struct {
			Name   string `json:"name"`
			Passed bool   `json:"passed"`
		} `json:"assertions"`
	}
	if err := decodeModelJSON(response.Content, &result); err != nil {
		return fmt.Errorf("decode verify result: %w", err)
	}
	if !result.Approved || len(result.Assertions) == 0 {
		return errors.New("verify agent rejected repair")
	}
	for _, assertion := range result.Assertions {
		if !assertion.Passed {
			return fmt.Errorf("verification assertion failed: %s", assertion.Name)
		}
	}
	return nil
}
