package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	jsonschema "github.com/eino-contrib/jsonschema"
	"github.com/lijunsheng/orderguard/internal/mcp"
)

// EinoMCPTool 将一个只读 MCP 工具适配为 Eino InvokableTool。
type EinoMCPTool struct {
	registered RegisteredTool
	run        Run
	step       AgentStep
	guard      *ToolGuard
	repository *Repository
}

// NewEinoMCPTools 为当前 Investigator 步骤创建隔离的 Eino 工具集合。
func NewEinoMCPTools(
	registry *MCPRegistry,
	run Run,
	step AgentStep,
	guard *ToolGuard,
	repository *Repository,
) []tool.BaseTool {
	definitions := registry.InvestigationDefinitions()
	result := make([]tool.BaseTool, 0, len(definitions))
	for _, definition := range definitions {
		registered, _ := registry.Get(definition.Name)
		result = append(result, &EinoMCPTool{
			registered: registered, run: run, step: step,
			guard: guard, repository: repository,
		})
	}
	return result
}

// Info 返回从 MCP tools/list 转换出的 Eino 工具描述。
func (t *EinoMCPTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	encoded, err := json.Marshal(t.registered.Definition.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode mcp tool schema: %w", err)
	}
	parameterSchema := &jsonschema.Schema{}
	if err := json.Unmarshal(encoded, parameterSchema); err != nil {
		return nil, fmt.Errorf("convert mcp tool schema: %w", err)
	}
	return &schema.ToolInfo{
		Name:        t.registered.Definition.Name,
		Desc:        t.registered.Definition.Description,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parameterSchema),
	}, nil
}

// InvokableRun 经 Tool Guard 调用 MCP，并保存成功结果为不可变证据。
func (t *EinoMCPTool) InvokableRun(
	ctx context.Context,
	argumentsJSON string,
	_ ...tool.Option,
) (string, error) {
	var arguments map[string]any
	if err := json.Unmarshal([]byte(argumentsJSON), &arguments); err != nil {
		return "", fmt.Errorf("decode tool arguments: %w", err)
	}
	if err := t.guard.Authorize(t.registered.Definition, arguments); err != nil {
		return "", err
	}
	toolCallID := newID("call")
	_, _ = t.repository.AppendEvent(ctx, t.run.ID, "tool.call_started", map[string]any{
		"step_id": t.step.ID, "tool_call_id": toolCallID,
		"tool_name": t.registered.Definition.Name, "arguments": arguments,
	})
	result, err := t.registered.Client.CallToolWithMetadata(
		ctx, t.registered.Definition.Name, arguments, mcp.RequestMetadata{
			RunID: t.run.ID, AgentStepID: t.step.ID, TraceID: t.run.TraceID,
			ToolCallID: toolCallID, Caller: "investigator-agent",
		},
	)
	if err != nil {
		_, _ = t.repository.AppendEvent(ctx, t.run.ID, "tool.call_completed", map[string]any{
			"step_id": t.step.ID, "tool_call_id": toolCallID,
			"tool_name": t.registered.Definition.Name, "success": false,
		})
		return "", fmt.Errorf("call mcp tool %s: %w", t.registered.Definition.Name, err)
	}
	envelope := result.StructuredContent
	if envelope.Success {
		if err := t.saveEvidence(ctx, toolCallID, arguments, envelope); err != nil {
			return "", err
		}
	}
	_, _ = t.repository.AppendEvent(ctx, t.run.ID, "tool.call_completed", map[string]any{
		"step_id": t.step.ID, "tool_call_id": toolCallID,
		"tool_name": t.registered.Definition.Name, "success": envelope.Success,
		"evidence_id": envelope.EvidenceID,
	})
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode mcp tool result: %w", err)
	}
	return string(encoded), nil
}

// saveEvidence 规范化 MCP Envelope 并插入不可变证据快照。
func (t *EinoMCPTool) saveEvidence(
	ctx context.Context,
	toolCallID string,
	arguments map[string]any,
	envelope mcp.Envelope,
) error {
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("encode evidence arguments: %w", err)
	}
	dataJSON, err := json.Marshal(envelope.Data)
	if err != nil {
		return fmt.Errorf("encode evidence data: %w", err)
	}
	collectedAt, err := time.Parse(time.RFC3339Nano, envelope.CollectedAt)
	if err != nil {
		return fmt.Errorf("parse evidence collected_at: %w", err)
	}
	hashInput, err := json.Marshal(map[string]any{
		"tool_name": t.registered.Definition.Name,
		"arguments": arguments, "source": envelope.Source,
		"collected_at": envelope.CollectedAt, "data": envelope.Data,
	})
	if err != nil {
		return fmt.Errorf("encode evidence hash input: %w", err)
	}
	digest := sha256.Sum256(hashInput)
	return t.repository.SaveEvidence(ctx, Evidence{
		EvidenceID: envelope.EvidenceID, RunID: t.run.ID, AgentStepID: t.step.ID,
		ToolCallID: toolCallID, ToolName: t.registered.Definition.Name,
		Arguments: argumentsJSON, Source: envelope.Source, CollectedAt: collectedAt,
		Data: dataJSON, ContentHash: hex.EncodeToString(digest[:]),
	})
}
