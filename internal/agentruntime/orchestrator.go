package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/lijunsheng/orderguard/internal/config"
	"log/slog"
	"time"
)

// Orchestrator 按阶段 3 状态机编排 Planner 和 Investigator。
type Orchestrator struct {
	repository      *Repository
	planner         *Planner
	investigator    *Investigator
	knowledge       *MCPRegistry
	phase4Model     model.BaseChatModel
	phase4ModelName string
	logger          *slog.Logger
	runTimeout      time.Duration
}

// NewOrchestratorWithPhase4 创建包含本地知识检索和诊断审查的编排器。
func NewOrchestratorWithPhase4(repository *Repository, planner *Planner, investigator *Investigator, knowledge *MCPRegistry, logger *slog.Logger, models ...model.BaseChatModel) *Orchestrator {
	o := NewOrchestrator(repository, planner, investigator, logger)
	o.knowledge = knowledge
	if len(models) > 0 {
		o.phase4Model = models[0]
	}
	if investigator != nil {
		o.phase4ModelName = investigator.ModelName()
	}
	return o
}

// NewOrchestrator 创建阶段 3 Agent 编排器。
func NewOrchestrator(
	repository *Repository,
	planner *Planner,
	investigator *Investigator,
	logger *slog.Logger,
) *Orchestrator {
	return &Orchestrator{
		repository: repository, planner: planner, investigator: investigator,
		logger: logger, runTimeout: config.AgentRunTimeout(),
	}
}

// Ready 返回 Planner 和 Investigator 是否已经配置。
func (o *Orchestrator) Ready() bool {
	return o != nil && o.planner != nil && o.investigator != nil
}

// Process 执行一个已处于 PLANNING 状态的调查任务。
func (o *Orchestrator) Process(parent context.Context, run Run) error {
	if !o.Ready() {
		return errors.New("agent model is not configured")
	}
	ctx, cancel := context.WithTimeout(parent, o.runTimeout)
	defer cancel()

	plannerStep, err := o.repository.StartStep(
		ctx, run.ID, "PLANNER", o.planner.ModelName(), map[string]any{
			"message": run.UserMessage, "order_id": run.OrderID,
		},
	)
	if err != nil {
		return err
	}
	plannerOutput, err := o.planner.Run(ctx, run)
	if err != nil {
		_ = o.repository.FinishStep(ctx, &plannerStep, map[string]any{
			"raw": plannerOutput.Raw, "error": err.Error(),
		}, plannerOutput.InputTokens, plannerOutput.OutputTokens, "PLANNER_FAILED")
		_ = o.repository.Fail(ctx, &run, StatusPlanningFailed, "PLANNER_FAILED", err.Error())
		return err
	}
	if err := o.repository.FinishStep(
		ctx, &plannerStep, plannerOutput.Value,
		plannerOutput.InputTokens, plannerOutput.OutputTokens, "",
	); err != nil {
		return err
	}
	if err := o.repository.SavePlan(ctx, run.ID, plannerOutput.Value); err != nil {
		return err
	}
	if err := o.repository.Transition(
		ctx, &run, StatusInvestigating, "run.status_changed", nil,
	); err != nil {
		return err
	}

	investigatorStep, err := o.repository.StartStep(
		ctx, run.ID, "INVESTIGATOR", o.investigator.ModelName(), map[string]any{
			"message": run.UserMessage, "order_id": run.OrderID,
			"plan": plannerOutput.Value,
		},
	)
	if err != nil {
		return err
	}
	investigatorOutput, err := o.investigator.Run(
		ctx, run, investigatorStep, plannerOutput.Value,
	)
	if err != nil {
		code := "INVESTIGATOR_FAILED"
		status := StatusInvestigationFailed
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrToolDenied) {
			code = "INVESTIGATION_INCONCLUSIVE"
			status = StatusInconclusive
		}
		_ = o.repository.FinishStep(ctx, &investigatorStep, map[string]any{
			"raw": investigatorOutput.Raw, "error": err.Error(),
		}, investigatorOutput.InputTokens, investigatorOutput.OutputTokens, code)
		_ = o.repository.Fail(ctx, &run, status, code, err.Error())
		return err
	}
	if err := o.repository.FinishStep(
		ctx, &investigatorStep, investigatorOutput.Value,
		investigatorOutput.InputTokens, investigatorOutput.OutputTokens, "",
	); err != nil {
		return err
	}
	if o.knowledge != nil {
		if err := o.repository.Transition(ctx, &run, StatusKnowledgeLookup, "run.status_changed", nil); err != nil {
			return err
		}
		knowledgeResult, err := o.CollectKnowledge(ctx, &run, investigatorOutput.Value)
		if err != nil {
			_ = o.repository.Fail(ctx, &run, StatusInconclusive, "KNOWLEDGE_FAILED", err.Error())
			return err
		}
		if err := o.repository.Transition(ctx, &run, StatusDiagnosing, "run.status_changed", nil); err != nil {
			return err
		}
		diagnosisStep, err := o.repository.StartStep(ctx, run.ID, "DIAGNOSIS", "deterministic-phase4", map[string]any{"order_id": run.OrderID})
		if err != nil {
			return err
		}
		diagnosis, err := o.RunDiagnosisAgent(ctx, run, investigatorOutput.Value, knowledgeResult)
		if err != nil {
			_ = o.repository.FinishStep(ctx, &diagnosisStep, map[string]any{"error": err.Error()}, 0, 0, "DIAGNOSIS_INCONCLUSIVE")
			_ = o.repository.Fail(ctx, &run, StatusInconclusive, "DIAGNOSIS_INCONCLUSIVE", err.Error())
			return err
		}
		if err := o.repository.FinishStep(ctx, &diagnosisStep, diagnosis, 0, 0, ""); err != nil {
			return err
		}
		if err := o.repository.Transition(ctx, &run, StatusCriticReview, "run.status_changed", map[string]any{"root_cause": diagnosis.RootCause}); err != nil {
			return err
		}
		criticStep, err := o.repository.StartStep(ctx, run.ID, "CRITIC", "deterministic-phase4", diagnosis)
		if err != nil {
			return err
		}
		if err := o.RunCriticAgent(ctx, run, diagnosis); err != nil {
			_ = o.repository.FinishStep(ctx, &criticStep, map[string]any{"error": err.Error()}, 0, 0, "CRITIC_REJECTED")
			_ = o.repository.Fail(ctx, &run, StatusInconclusive, "CRITIC_REJECTED", err.Error())
			return err
		}
		if err := o.repository.FinishStep(ctx, &criticStep, map[string]any{"approved": true}, 0, 0, ""); err != nil {
			return err
		}
		investigatorOutput.Value.Diagnosis = &diagnosis
		return o.repository.Complete(ctx, &run, investigatorOutput.Value)
	}
	return o.repository.Complete(ctx, &run, investigatorOutput.Value)
}

// RunWorker 持续领取 CREATED 任务并执行调查。
func (o *Orchestrator) RunWorker(ctx context.Context) error {
	if !o.Ready() {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, found, err := o.repository.ClaimCreated(ctx)
		if err != nil {
			return err
		}
		if found {
			if err := o.Process(ctx, run); err != nil {
				o.logger.Error("process investigation", "run_id", run.ID, "error", err)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// CreateRun 验证输入并创建异步调查任务。
func (o *Orchestrator) CreateRun(
	ctx context.Context,
	message string,
	orderID string,
	traceID string,
) (Run, error) {
	if !o.Ready() {
		return Run{}, errors.New("agent model is not configured")
	}
	if message == "" || orderID == "" {
		return Run{}, fmt.Errorf("message and order_id are required")
	}
	return o.repository.CreateRun(ctx, message, orderID, traceID)
}
