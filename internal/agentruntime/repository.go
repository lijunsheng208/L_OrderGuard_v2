package agentruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRunNotFound = errors.New("investigation run not found")
	ErrConflict    = errors.New("investigation run version conflict")
)

// Repository 持久化阶段 3 调查状态、步骤、证据和事件。
type Repository struct {
	db *pgxpool.Pool
}

// InjectDemoFault mutates only demo-order records to create a reproducible fault.
func (r *Repository) InjectDemoFault(ctx context.Context, orderID, faultType string) error {
	if strings.TrimSpace(orderID) == "" || !strings.HasPrefix(orderID, "O-DEMO-") {
		return errors.New("fault injection is limited to O-DEMO-* orders")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders.orders WHERE id=$1)`, orderID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrRunNotFound
	}
	switch faultType {
	case "NO_ISSUE":
		_, err = tx.Exec(ctx, `DELETE FROM agent.demo_faults WHERE order_id=$1`, orderID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "OUTBOX_NOT_CREATED":
		err = resetDemoInventory(ctx, tx, orderID)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM payments.outbox_events WHERE aggregate_id=$1`, orderID)
		}
	case "OUTBOX_NOT_PUBLISHED", "OUTBOX_PUBLISH_FAILED":
		// Keep the business status PENDING; the demo switch blocks the publisher.
		_, err = tx.Exec(ctx, `UPDATE payments.outbox_events SET publish_status='PENDING', published_at=NULL WHERE aggregate_id=$1`, orderID)
		if err == nil {
			err = resetDemoInventory(ctx, tx, orderID)
		}
	case "INVENTORY_EVENT_NOT_CONSUMED", "INVENTORY_CONSUMER_FAILED", "INVENTORY_DEDUCTION_NOT_PERSISTED", "EVIDENCE_CONFLICT":
		err = resetDemoInventory(ctx, tx, orderID)
	case "TOOL_TIMEOUT_OR_DIRTY_DATA":
		// The underlying business chain remains healthy. The inventory query
		// adapter exposes a deliberately malformed semantic status for this order.
	default:
		return fmt.Errorf("unsupported demo fault type: %s", faultType)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO agent.demo_faults(order_id,fault_type,enabled,cleared_at) VALUES($1,$2,true,NULL) ON CONFLICT(order_id) DO UPDATE SET fault_type=EXCLUDED.fault_type, enabled=true, cleared_at=NULL`, orderID, faultType)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func resetDemoInventory(ctx context.Context, tx pgx.Tx, orderID string) error {
	if err := restoreDemoStock(ctx, tx, orderID); err != nil {
		return err
	}
	// Demo orders are created repeatedly by the UI and evaluation harness. Keep
	// the fixture SKU replenished so replaying a failed consumer is not rejected
	// merely because an earlier demo run exhausted the shared stock row.
	if _, err := tx.Exec(ctx, `
		UPDATE inventory.stocks
		SET available = GREATEST(available, 100), updated_at = now()
		WHERE sku_id IN (SELECT DISTINCT sku_id FROM orders.order_items WHERE order_id=$1)`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM inventory.deductions WHERE order_id=$1`, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM inventory.consumed_events WHERE event_id IN (SELECT event_id FROM payments.outbox_events WHERE aggregate_id=$1)`, orderID)
	return err
}

func restoreDemoStock(ctx context.Context, tx pgx.Tx, orderID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE inventory.stocks s
		SET available = s.available + d.quantity, version = s.version + 1, updated_at = now()
		FROM (SELECT sku_id, SUM(quantity) AS quantity FROM inventory.deductions WHERE order_id=$1 GROUP BY sku_id) d
		WHERE s.sku_id = d.sku_id`, orderID)
	return err
}

// CreateRepairPlan 保存一次待执行的修复方案。
func (r *Repository) CreateRepairPlan(ctx context.Context, runID, action string, evidenceVersion int64, payload any) (string, error) {
	id := newID("repair-plan")
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO agent.repair_plans (id,run_id,action,policy_version,evidence_version,payload) VALUES ($1,$2,$3,$4,$5,$6)`, id, runID, action, "phase5-v1", evidenceVersion, data)
	return id, err
}

// StartRepairExecution 保存修复开始记录。
func (r *Repository) StartRepairExecution(ctx context.Context, planID, runID, key string, request any) (string, error) {
	id := newID("repair-exec")
	data, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO agent.repair_executions (id,repair_plan_id,run_id,idempotency_key,status,request) VALUES ($1,$2,$3,$4,'STARTED',$5)`, id, planID, runID, key, data)
	return id, err
}

// FinishRepairExecution 更新修复执行结果。
func (r *Repository) FinishRepairExecution(ctx context.Context, id, status, errorCode string, result any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `UPDATE agent.repair_executions SET status=$2,result=$3,error_code=NULLIF($4,''),completed_at=now() WHERE id=$1`, id, status, data, errorCode)
	return err
}

// SaveVerificationResult 保存 Verify Agent 的断言结果。
func (r *Repository) SaveVerificationResult(ctx context.Context, runID, executionID, status string, assertions any, evidenceIDs any, summary string) error {
	a, err := json.Marshal(assertions)
	if err != nil {
		return err
	}
	e, err := json.Marshal(evidenceIDs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO agent.verification_results (id,run_id,repair_execution_id,status,assertions,evidence_ids,summary) VALUES ($1,$2,$3,$4,$5,$6,$7)`, newID("verification"), runID, executionID, status, a, e, summary)
	return err
}

// SaveApprovalRecord 保存一次诊断结论或修复动作审批。
func (r *Repository) SaveApprovalRecord(ctx context.Context, runID, approvalType, decision, conclusion string, nextStatus Status) (ApprovalRecord, error) {
	record := ApprovalRecord{
		ID: newID("approval"), RunID: runID, Type: approvalType,
		Decision: decision, Conclusion: conclusion, NextStatus: nextStatus,
		CreatedAt: time.Now().UTC(),
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO agent.approval_records (
			id, run_id, approval_type, decision, conclusion, next_status, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		record.ID, record.RunID, record.Type, record.Decision,
		record.Conclusion, record.NextStatus, record.CreatedAt,
	)
	return record, err
}

// ListApprovalRecords 返回指定调查的全部审批记录。
func (r *Repository) ListApprovalRecords(ctx context.Context, runID string) ([]ApprovalRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, run_id, approval_type, decision, conclusion, next_status, created_at
		FROM agent.approval_records WHERE run_id=$1 ORDER BY created_at DESC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ApprovalRecord, 0)
	for rows.Next() {
		var item ApprovalRecord
		if err := rows.Scan(&item.ID, &item.RunID, &item.Type, &item.Decision, &item.Conclusion, &item.NextStatus, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// NewRepository 创建 Agent Runtime Repository。
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateRun 创建调查任务并原子写入首个进度事件。
func (r *Repository) CreateRun(
	ctx context.Context,
	message string,
	orderID string,
	traceID string,
) (Run, error) {
	now := time.Now().UTC()
	run := Run{
		ID: newID("run"), UserMessage: message, OrderID: orderID,
		Status: StatusCreated, TraceID: traceID, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, fmt.Errorf("begin create investigation: %w", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO agent.investigation_runs (
			id, user_message, order_id, status, trace_id, version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		run.ID, run.UserMessage, run.OrderID, run.Status,
		run.TraceID, run.Version, now,
	)
	if err != nil {
		return Run{}, fmt.Errorf("insert investigation: %w", err)
	}
	if _, err := appendEventTx(ctx, tx, run.ID, "run.created", map[string]any{
		"status": run.Status, "trace_id": traceID,
	}); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit investigation: %w", err)
	}
	return run, nil
}

// ClaimCreated 领取一个待处理任务并将其推进到 PLANNING。
func (r *Repository) ClaimCreated(ctx context.Context) (Run, bool, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, false, fmt.Errorf("begin claim investigation: %w", err)
	}
	defer tx.Rollback(ctx)
	var run Run
	err = tx.QueryRow(ctx, `
		SELECT id, user_message, order_id, status, trace_id, version, created_at, updated_at
		FROM agent.investigation_runs
		WHERE status = 'CREATED'
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED LIMIT 1`,
	).Scan(
		&run.ID, &run.UserMessage, &run.OrderID, &run.Status,
		&run.TraceID, &run.Version, &run.CreatedAt, &run.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("select investigation: %w", err)
	}
	if err := transitionTx(ctx, tx, &run, StatusPlanning, "run.status_changed", nil); err != nil {
		return Run{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, false, fmt.Errorf("commit claim investigation: %w", err)
	}
	return run, true, nil
}

// Transition 使用乐观锁推进任务状态并追加事件。
func (r *Repository) Transition(
	ctx context.Context,
	run *Run,
	to Status,
	eventType string,
	payload map[string]any,
) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transition investigation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := transitionTx(ctx, tx, run, to, eventType, payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit investigation transition: %w", err)
	}
	return nil
}

// SavePlan 保存已通过确定性校验的调查计划。
func (r *Repository) SavePlan(ctx context.Context, runID string, plan Plan) error {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode investigation plan: %w", err)
	}
	command, err := r.db.Exec(ctx, `
		UPDATE agent.investigation_runs SET plan = $2, updated_at = now() WHERE id = $1`,
		runID, encoded,
	)
	if err != nil {
		return fmt.Errorf("save investigation plan: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrRunNotFound
	}
	return nil
}

// Complete 保存 Investigator 输出并将任务推进到证据已收集状态。
func (r *Repository) Complete(ctx context.Context, run *Run, result InvestigationResult) error {
	return r.CompleteStatus(ctx, run, result, StatusEvidenceCollected)
}

// CompleteStatus 保存最终结果并推进到指定终态。
func (r *Repository) CompleteStatus(ctx context.Context, run *Run, result InvestigationResult, target Status) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode investigation result: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin complete investigation: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE agent.investigation_runs
		SET status = $4, version = version + 1, final_summary = $5,
			updated_at = now(), completed_at = now()
		WHERE id = $1 AND status = $2 AND version = $3`,
		run.ID, run.Status, run.Version, target, encoded,
	)
	if err != nil {
		return fmt.Errorf("complete investigation: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := appendEventTx(ctx, tx, run.ID, "run.completed", map[string]any{
		"status": target, "summary": result.Summary,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit complete investigation: %w", err)
	}
	run.Status = target
	run.Version++
	run.FinalSummary = encoded
	return nil
}

// Fail 保存稳定错误码并将任务推进到终止失败状态。
func (r *Repository) Fail(
	ctx context.Context,
	run *Run,
	to Status,
	errorCode string,
	message string,
) error {
	if err := ValidateTransition(run.Status, to); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin fail investigation: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE agent.investigation_runs
		SET status = $4, version = version + 1, error_code = $5,
			updated_at = now(), completed_at = now()
		WHERE id = $1 AND status = $2 AND version = $3`,
		run.ID, run.Status, run.Version, to, errorCode,
	)
	if err != nil {
		return fmt.Errorf("fail investigation: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := appendEventTx(ctx, tx, run.ID, "run.failed", map[string]any{
		"from": run.Status, "to": to, "error_code": errorCode, "message": message,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fail investigation: %w", err)
	}
	run.Status = to
	run.Version++
	run.ErrorCode = &errorCode
	return nil
}

// GetRun 查询单个调查任务。
func (r *Repository) GetRun(ctx context.Context, runID string) (Run, error) {
	var run Run
	var plan, summary []byte
	err := r.db.QueryRow(ctx, `
		SELECT id, user_message, order_id, status, trace_id, version,
			COALESCE(plan, 'null'::jsonb), COALESCE(final_summary, 'null'::jsonb),
			error_code, created_at, updated_at, completed_at
		FROM agent.investigation_runs WHERE id = $1`, runID,
	).Scan(
		&run.ID, &run.UserMessage, &run.OrderID, &run.Status,
		&run.TraceID, &run.Version, &plan, &summary, &run.ErrorCode,
		&run.CreatedAt, &run.UpdatedAt, &run.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("query investigation: %w", err)
	}
	if string(plan) != "null" {
		run.Plan = plan
	}
	if string(summary) != "null" {
		run.FinalSummary = summary
	}
	return run, nil
}

// StartStep 创建一个运行中的 Agent 步骤。
func (r *Repository) StartStep(
	ctx context.Context,
	runID string,
	agentType string,
	modelName string,
	input any,
) (AgentStep, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return AgentStep{}, fmt.Errorf("encode agent step input: %w", err)
	}
	step := AgentStep{
		ID: newID("step"), RunID: runID, AgentType: agentType,
		Status: "RUNNING", ModelName: modelName, Input: encoded,
		StartedAt: time.Now().UTC(),
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AgentStep{}, fmt.Errorf("begin agent step: %w", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, runID)
	if err != nil {
		return AgentStep{}, fmt.Errorf("lock agent step sequence: %w", err)
	}
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1
		FROM agent.agent_steps WHERE run_id = $1`, runID,
	).Scan(&step.Sequence)
	if err != nil {
		return AgentStep{}, fmt.Errorf("allocate agent step sequence: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent.agent_steps (
			id, run_id, agent_type, sequence, status, model_name, input, started_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		step.ID, step.RunID, step.AgentType, step.Sequence,
		step.Status, step.ModelName, step.Input, step.StartedAt,
	)
	if err != nil {
		return AgentStep{}, fmt.Errorf("insert agent step: %w", err)
	}
	if _, err := appendEventTx(ctx, tx, runID, agentEvent(agentType, "started"), map[string]any{
		"step_id": step.ID, "sequence": step.Sequence,
	}); err != nil {
		return AgentStep{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentStep{}, fmt.Errorf("commit agent step: %w", err)
	}
	return step, nil
}

// FinishStep 保存 Agent 步骤输出、Token 用量和结果状态。
func (r *Repository) FinishStep(
	ctx context.Context,
	step *AgentStep,
	output any,
	inputTokens int64,
	outputTokens int64,
	errorCode string,
) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode agent step output: %w", err)
	}
	status := "SUCCEEDED"
	var storedError *string
	if errorCode != "" {
		status = "FAILED"
		storedError = &errorCode
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin finish agent step: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE agent.agent_steps
		SET status = $2, output = $3, error_code = $4,
			input_tokens = $5, output_tokens = $6, completed_at = now()
		WHERE id = $1 AND status = 'RUNNING'`,
		step.ID, status, encoded, storedError, inputTokens, outputTokens,
	)
	if err != nil {
		return fmt.Errorf("finish agent step: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := appendEventTx(ctx, tx, step.RunID, agentEvent(step.AgentType, "completed"), map[string]any{
		"step_id": step.ID, "status": status, "error_code": storedError,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finish agent step: %w", err)
	}
	step.Status = status
	step.Output = encoded
	step.ErrorCode = storedError
	step.InputTokens = inputTokens
	step.OutputTokens = outputTokens
	return nil
}

// SaveEvidence 插入一条不可变证据快照。
func (r *Repository) SaveEvidence(ctx context.Context, evidence Evidence) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin save evidence: %w", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO agent.evidence_snapshots (
			evidence_id, run_id, agent_step_id, tool_call_id, tool_name,
			arguments, source, collected_at, data, content_hash
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		evidence.EvidenceID, evidence.RunID, evidence.AgentStepID,
		evidence.ToolCallID, evidence.ToolName, evidence.Arguments,
		evidence.Source, evidence.CollectedAt, evidence.Data, evidence.ContentHash,
	)
	if err != nil {
		return fmt.Errorf("insert evidence snapshot: %w", err)
	}
	_, err = appendEventTx(ctx, tx, evidence.RunID, "evidence.created", map[string]any{
		"evidence_id": evidence.EvidenceID, "tool_name": evidence.ToolName,
		"source": evidence.Source,
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit save evidence: %w", err)
	}
	return nil
}

// ListEvidence 返回任务的不可变证据快照。
func (r *Repository) ListEvidence(ctx context.Context, runID string) ([]Evidence, error) {
	rows, err := r.db.Query(ctx, `
		SELECT evidence_id, run_id, agent_step_id, tool_call_id, tool_name,
			arguments, source, collected_at, data, content_hash, created_at
		FROM agent.evidence_snapshots WHERE run_id = $1 ORDER BY created_at`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("query evidence snapshots: %w", err)
	}
	defer rows.Close()
	result := make([]Evidence, 0)
	for rows.Next() {
		var item Evidence
		if err := rows.Scan(
			&item.EvidenceID, &item.RunID, &item.AgentStepID, &item.ToolCallID,
			&item.ToolName, &item.Arguments, &item.Source, &item.CollectedAt,
			&item.Data, &item.ContentHash, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan evidence snapshot: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ListSteps 返回任务的 Agent 执行步骤。
func (r *Repository) ListSteps(ctx context.Context, runID string) ([]AgentStep, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, run_id, agent_type, sequence, status, model_name, input,
			COALESCE(output, 'null'::jsonb), error_code, input_tokens,
			output_tokens, started_at, completed_at
		FROM agent.agent_steps WHERE run_id = $1 ORDER BY sequence`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("query agent steps: %w", err)
	}
	defer rows.Close()
	result := make([]AgentStep, 0)
	for rows.Next() {
		var item AgentStep
		var output []byte
		if err := rows.Scan(
			&item.ID, &item.RunID, &item.AgentType, &item.Sequence,
			&item.Status, &item.ModelName, &item.Input, &output, &item.ErrorCode,
			&item.InputTokens, &item.OutputTokens, &item.StartedAt, &item.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent step: %w", err)
		}
		if string(output) != "null" {
			item.Output = output
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// AppendEvent 保存一条可供 SSE 重放的进度事件。
func (r *Repository) AppendEvent(
	ctx context.Context,
	runID string,
	eventType string,
	payload any,
) (Event, error) {
	return appendEventTx(ctx, r.db, runID, eventType, payload)
}

// ListEvents 返回指定事件 ID 之后的任务事件。
func (r *Repository) ListEvents(
	ctx context.Context,
	runID string,
	afterID int64,
	limit int,
) ([]Event, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, run_id, event_type, payload, created_at
		FROM agent.investigation_events
		WHERE run_id = $1 AND id > $2 ORDER BY id LIMIT $3`, runID, afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query investigation events: %w", err)
	}
	defer rows.Close()
	result := make([]Event, 0)
	for rows.Next() {
		var item Event
		if err := rows.Scan(&item.ID, &item.RunID, &item.Type, &item.Payload, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan investigation event: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type eventExecutor interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// transitionTx 在现有事务中执行乐观锁状态迁移并追加事件。
func transitionTx(
	ctx context.Context,
	tx pgx.Tx,
	run *Run,
	to Status,
	eventType string,
	payload map[string]any,
) error {
	if err := ValidateTransition(run.Status, to); err != nil {
		return err
	}
	completed := to == StatusEvidenceCollected || to == StatusPlanningFailed ||
		to == StatusInvestigationFailed || to == StatusInconclusive || to == StatusCancelled
	command, err := tx.Exec(ctx, `
		UPDATE agent.investigation_runs
		SET status = $4, version = version + 1, updated_at = now(),
			completed_at = CASE WHEN $5 THEN now() ELSE completed_at END
		WHERE id = $1 AND status = $2 AND version = $3`,
		run.ID, run.Status, run.Version, to, completed,
	)
	if err != nil {
		return fmt.Errorf("transition investigation: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["from"] = run.Status
	payload["to"] = to
	if _, err := appendEventTx(ctx, tx, run.ID, eventType, payload); err != nil {
		return err
	}
	run.Status = to
	run.Version++
	return nil
}

// appendEventTx 使用事务或连接池写入任务事件。
func appendEventTx(
	ctx context.Context,
	target eventExecutor,
	runID string,
	eventType string,
	payload any,
) (Event, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode investigation event: %w", err)
	}
	var event Event
	err = target.QueryRow(ctx, `
		INSERT INTO agent.investigation_events (run_id, event_type, payload)
		VALUES ($1, $2, $3)
		RETURNING id, run_id, event_type, payload, created_at`,
		runID, eventType, encoded,
	).Scan(&event.ID, &event.RunID, &event.Type, &event.Payload, &event.CreatedAt)
	if err != nil {
		return Event{}, fmt.Errorf("insert investigation event: %w", err)
	}
	return event, nil
}

// agentEvent 将 Agent 类型转换为稳定事件名。
func agentEvent(agentType, suffix string) string {
	prefixes := map[string]string{"PLANNER": "planner", "INVESTIGATOR": "investigator", "KNOWLEDGE": "knowledge", "DIAGNOSIS": "diagnosis", "CRITIC": "critic", "VERIFY": "verify"}
	if prefix, ok := prefixes[agentType]; ok {
		return prefix + "." + suffix
	}
	return "agent." + strings.ToLower(agentType) + "." + suffix
}

// newID 生成本地唯一的运行时标识。
func newID(prefix string) string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(random)
}
