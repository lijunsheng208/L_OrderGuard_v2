package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// AgentOutput 保存一次 Eino 模型执行结果和用量。
type AgentOutput[T any] struct {
	Value        T
	Raw          string
	InputTokens  int64
	OutputTokens int64
}

// Planner 使用一次 Eino ChatModel 调用生成结构化计划。
type Planner struct {
	model     model.BaseChatModel
	modelName string
	registry  *MCPRegistry
}

// NewPlanner 创建结构化 Planner。
func NewPlanner(
	chatModel model.BaseChatModel,
	modelName string,
	registry *MCPRegistry,
) *Planner {
	return &Planner{model: chatModel, modelName: modelName, registry: registry}
}

// ModelName 返回 Planner 使用的模型名。
func (p *Planner) ModelName() string {
	return p.modelName
}

// Run 生成并确定性校验调查计划。
func (p *Planner) Run(ctx context.Context, run Run) (AgentOutput[Plan], error) {
	if p.model == nil {
		return AgentOutput[Plan]{}, errors.New("planner model is not configured")
	}
	input, err := json.Marshal(map[string]any{
		"message": run.UserMessage, "order_id": run.OrderID,
		"available_tools": p.registry.InvestigationDefinitions(),
	})
	if err != nil {
		return AgentOutput[Plan]{}, fmt.Errorf("encode planner input: %w", err)
	}
	response, err := p.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(PlannerPrompt), schema.UserMessage(string(input)),
	})
	if err != nil {
		return AgentOutput[Plan]{}, fmt.Errorf("generate investigation plan: %w", err)
	}
	var plan Plan
	if err := decodeModelJSON(response.Content, &plan); err != nil {
		return AgentOutput[Plan]{Raw: response.Content}, fmt.Errorf("decode investigation plan: %w", err)
	}
	if err := p.validatePlan(run, plan); err != nil {
		return AgentOutput[Plan]{Value: plan, Raw: response.Content}, err
	}
	inputTokens, outputTokens := messageUsage(response)
	return AgentOutput[Plan]{
		Value: plan, Raw: response.Content,
		InputTokens: inputTokens, OutputTokens: outputTokens,
	}, nil
}

// validatePlan 校验计划规模、工具权限、参数 Schema 和订单边界。
func (p *Planner) validatePlan(run Run, plan Plan) error {
	if strings.TrimSpace(plan.Goal) == "" || len(plan.Steps) < 4 || len(plan.Steps) > 8 {
		return errors.New("planner must return a goal and 4 to 8 steps")
	}
	stepIDs := make(map[string]struct{}, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.ID == "" || step.Purpose == "" || step.Tool == "" {
			return errors.New("planner step fields must not be empty")
		}
		if _, exists := stepIDs[step.ID]; exists {
			return fmt.Errorf("duplicate planner step id: %s", step.ID)
		}
		stepIDs[step.ID] = struct{}{}
		registered, ok := p.registry.Get(step.Tool)
		if !ok {
			return fmt.Errorf("planner selected unavailable tool: %s", step.Tool)
		}
		if !isInvestigationTool(step.Tool) {
			return fmt.Errorf("planner selected tool outside investigation stage: %s", step.Tool)
		}
		if err := mcpValidate(registered, step.Args); err != nil {
			return fmt.Errorf("planner arguments for %s: %w", step.Tool, err)
		}
		if orderID, ok := step.Args["order_id"].(string); ok && orderID != run.OrderID {
			return fmt.Errorf("planner step %s uses a different order_id", step.ID)
		}
		if step.Tool == "get_event_record" {
			eventType, _ := step.Args["event_type"].(string)
			if eventType != "payment.succeeded" {
				return fmt.Errorf("planner step %s must use event_type payment.succeeded", step.ID)
			}
		}
	}
	return nil
}

// Investigator 使用 Eino ReAct Agent 动态收集证据。
type Investigator struct {
	model      model.ToolCallingChatModel
	modelName  string
	registry   *MCPRegistry
	repository *Repository
	maxCalls   int
}

// NewInvestigator 创建阶段 3 ReAct Investigator。
func NewInvestigator(
	chatModel model.ToolCallingChatModel,
	modelName string,
	registry *MCPRegistry,
	repository *Repository,
) *Investigator {
	return &Investigator{
		model: chatModel, modelName: modelName, registry: registry,
		repository: repository, maxCalls: 12,
	}
}

// ModelName 返回 Investigator 使用的模型名。
func (i *Investigator) ModelName() string {
	return i.modelName
}

// Run 执行 Eino ReAct 循环并校验最终事实引用。
func (i *Investigator) Run(
	ctx context.Context,
	run Run,
	step AgentStep,
	plan Plan,
) (AgentOutput[InvestigationResult], error) {
	if i.model == nil {
		return AgentOutput[InvestigationResult]{}, errors.New("investigator model is not configured")
	}
	guard := NewToolGuard(run.OrderID, i.maxCalls, 2)
	tools := NewEinoMCPTools(i.registry, run, step, guard, i.repository)
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: i.model,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		MaxStep:          26,
	})
	if err != nil {
		return AgentOutput[InvestigationResult]{}, fmt.Errorf("create investigator react agent: %w", err)
	}
	input, err := json.Marshal(map[string]any{
		"message": run.UserMessage, "order_id": run.OrderID, "plan": plan,
	})
	if err != nil {
		return AgentOutput[InvestigationResult]{}, fmt.Errorf("encode investigator input: %w", err)
	}
	response, err := agent.Generate(ctx, []*schema.Message{
		schema.SystemMessage(InvestigatorPrompt), schema.UserMessage(string(input)),
	})
	if err != nil {
		return AgentOutput[InvestigationResult]{}, fmt.Errorf("run investigator react agent: %w", err)
	}
	var result InvestigationResult
	if err := decodeModelJSON(response.Content, &result); err != nil {
		return AgentOutput[InvestigationResult]{Raw: response.Content}, fmt.Errorf("decode investigator result: %w", err)
	}
	if guard.Total() < 4 {
		return AgentOutput[InvestigationResult]{Value: result, Raw: response.Content},
			errors.New("investigator collected fewer than four tool observations")
	}
	if err := i.validateResult(ctx, run.ID, result); err != nil {
		return AgentOutput[InvestigationResult]{Value: result, Raw: response.Content}, err
	}
	inputTokens, outputTokens := messageUsage(response)
	return AgentOutput[InvestigationResult]{
		Value: result, Raw: response.Content,
		InputTokens: inputTokens, OutputTokens: outputTokens,
	}, nil
}

// validateResult 保证每条调查事实引用当前任务的真实证据。
func (i *Investigator) validateResult(
	ctx context.Context,
	runID string,
	result InvestigationResult,
) error {
	if strings.TrimSpace(result.Summary) == "" || len(result.Facts) == 0 {
		return errors.New("investigator summary and facts must not be empty")
	}
	evidence, err := i.repository.ListEvidence(ctx, runID)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		known[item.EvidenceID] = struct{}{}
	}
	for _, fact := range result.Facts {
		if fact.Fact == "" {
			return errors.New("investigator fact must not be empty")
		}
		if _, ok := known[fact.EvidenceID]; !ok {
			return fmt.Errorf("investigator referenced unknown evidence: %s", fact.EvidenceID)
		}
	}
	return nil
}

// decodeModelJSON 解码纯 JSON 或 Markdown JSON 代码块。
func decodeModelJSON(content string, target any) error {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		firstLine := strings.IndexByte(trimmed, '\n')
		lastFence := strings.LastIndex(trimmed, "```")
		if firstLine < 0 || lastFence <= firstLine {
			return errors.New("invalid JSON code block")
		}
		trimmed = strings.TrimSpace(trimmed[firstLine+1 : lastFence])
	}
	// 兼容模型在 JSON 前后添加说明文字，但仍只解析唯一的 JSON 对象。
	if !strings.HasPrefix(trimmed, "{") {
		start, end := strings.IndexByte(trimmed, '{'), strings.LastIndexByte(trimmed, '}')
		if start < 0 || end <= start {
			return errors.New("model response does not contain a JSON object")
		}
		trimmed = trimmed[start : end+1]
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("model returned trailing content")
	}
	return nil
}

// messageUsage 返回模型响应携带的 Token 用量。
func messageUsage(message *schema.Message) (int64, int64) {
	if message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return 0, 0
	}
	return int64(message.ResponseMeta.Usage.PromptTokens),
		int64(message.ResponseMeta.Usage.CompletionTokens)
}
