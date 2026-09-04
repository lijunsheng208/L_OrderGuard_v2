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
		investigation := investigatorOutput.Value
		const maxSupplementalInvestigations = 1
		for attempt := 0; ; attempt++ {
			if err := o.repository.Transition(ctx, &run, StatusKnowledgeLookup, "run.status_changed", nil); err != nil {
				return err
			}
			knowledgeResult, err := o.CollectKnowledge(ctx, &run, investigation)
			if err != nil {
				_ = o.repository.Fail(ctx, &run, StatusInconclusive, "KNOWLEDGE_FAILED", err.Error())
				return err
			}
			if err := o.repository.Transition(ctx, &run, StatusDiagnosing, "run.status_changed", nil); err != nil {
				return err
			}
			diagnosisStep, err := o.repository.StartStep(ctx, run.ID, "DIAGNOSIS", o.phase4ModelName, map[string]any{"order_id": run.OrderID, "attempt": attempt + 1})
			if err != nil {
				return err
			}
			diagnosis, err := o.RunDiagnosisAgent(ctx, run, investigation, knowledgeResult)
			if err != nil {
				_ = o.repository.FinishStep(ctx, &diagnosisStep, map[string]any{"error": err.Error()}, 0, 0, "DIAGNOSIS_INCONCLUSIVE")
				_ = o.repository.Fail(ctx, &run, StatusInconclusive, "DIAGNOSIS_INCONCLUSIVE", err.Error())
				return err
			}
			evidence, err := o.repository.ListEvidence(ctx, run.ID)
			if err != nil {
				return err
			}
			// Conflicting or dirty evidence is a hard safety stop. Do not let a
			// model select only the favorable evidence and turn the run into an
			// executable diagnosis.
			if evidenceRequiresInconclusive(evidence) {
				err := errors.New("evidence is conflicting or contains dirty data")
				_ = o.repository.FinishStep(ctx, &diagnosisStep, map[string]any{
					"diagnosis": diagnosis,
					"error":     err.Error(),
				}, 0, 0, "EVIDENCE_CONFLICT")
				_ = o.repository.Fail(ctx, &run, StatusInconclusive, "EVIDENCE_CONFLICT", err.Error())
				return err
			}
			validation := ValidateDiagnosisRootCause(diagnosis, evidence)
			if validation.Valid {
				if err := o.repository.FinishStep(ctx, &diagnosisStep, map[string]any{"diagnosis": diagnosis, "validation": validation}, 0, 0, ""); err != nil {
					return err
				}
				_, _ = o.repository.AppendEvent(ctx, run.ID, "diagnosis.validated", map[string]any{"root_cause": diagnosis.RootCause})
				investigation.Diagnosis = &diagnosis
				return o.repository.Complete(ctx, &run, investigation)
			}

			if err := o.repository.FinishStep(ctx, &diagnosisStep, map[string]any{"diagnosis": diagnosis, "validation": validation}, 0, 0, "DIAGNOSIS_UNSUPPORTED"); err != nil {
				return err
			}
			_, _ = o.repository.AppendEvent(ctx, run.ID, "diagnosis.validation_failed", map[string]any{"root_cause": diagnosis.RootCause, "issues": validation.Issues, "attempt": attempt + 1})
			if attempt >= maxSupplementalInvestigations {
				err := fmt.Errorf("diagnosis root cause is not supported after supplemental investigation: %v", validation.Issues)
				_ = o.repository.Fail(ctx, &run, StatusInconclusive, "DIAGNOSIS_UNSUPPORTED", err.Error())
				return err
			}

			if err := o.repository.Transition(ctx, &run, StatusInvestigating, "investigation.supplement_requested", map[string]any{"root_cause": diagnosis.RootCause, "issues": validation.Issues, "attempt": attempt + 1}); err != nil {
				return err
			}
			supplement := SupplementalInvestigationRequest{
				Attempt: attempt + 1, Previous: investigation,
				RejectedDiagnosis: diagnosis, ValidationIssues: validation.Issues,
			}
			supplementStep, err := o.repository.StartStep(ctx, run.ID, "INVESTIGATOR", o.investigator.ModelName(), map[string]any{
				"message": run.UserMessage, "order_id": run.OrderID,
				"plan": plannerOutput.Value, "supplemental_request": supplement,
			})
			if err != nil {
				return err
			}
			supplementOutput, err := o.investigator.Run(ctx, run, supplementStep, plannerOutput.Value, supplement)
			if err != nil {
				code := "SUPPLEMENTAL_INVESTIGATION_FAILED"
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrToolDenied) {
					code = "SUPPLEMENTAL_INVESTIGATION_INCONCLUSIVE"
				}
				_ = o.repository.FinishStep(ctx, &supplementStep, map[string]any{"raw": supplementOutput.Raw, "error": err.Error()}, supplementOutput.InputTokens, supplementOutput.OutputTokens, code)
				_ = o.repository.Fail(ctx, &run, StatusInconclusive, code, err.Error())
				return err
			}
			if err := o.repository.FinishStep(ctx, &supplementStep, supplementOutput.Value, supplementOutput.InputTokens, supplementOutput.OutputTokens, ""); err != nil {
				return err
			}
			investigation = mergeInvestigationResults(investigation, supplementOutput.Value)
		}
	}
	return o.repository.Complete(ctx, &run, investigatorOutput.Value)
}

func mergeInvestigationResults(previous, supplemental InvestigationResult) InvestigationResult {
	result := supplemental
	if previous.Summary != "" && supplemental.Summary != "" {
		result.Summary = previous.Summary + " 补充调查：" + supplemental.Summary
	}
	seen := make(map[string]bool, len(previous.Facts)+len(supplemental.Facts))
	result.Facts = nil
	for _, fact := range append(previous.Facts, supplemental.Facts...) {
		key := fact.EvidenceID + "\x00" + fact.Fact
		if !seen[key] {
			seen[key] = true
			result.Facts = append(result.Facts, fact)
		}
	}
	result.Diagnosis = nil
	return result
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
