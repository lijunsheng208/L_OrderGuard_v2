package agentruntime

import (
	"encoding/json"
	"time"
)

// Status 表示阶段 3 调查任务状态。
type Status string

const (
	StatusCreated             Status = "CREATED"
	StatusPlanning            Status = "PLANNING"
	StatusInvestigating       Status = "INVESTIGATING"
	StatusKnowledgeLookup     Status = "KNOWLEDGE_LOOKUP"
	StatusDiagnosing          Status = "DIAGNOSING"
	StatusCriticReview        Status = "CRITIC_REVIEW"
	StatusEvidenceCollected   Status = "EVIDENCE_COLLECTED"
	StatusNoAnomaly           Status = "NO_ANOMALY"
	StatusPolicyCheck         Status = "POLICY_CHECK"
	StatusAwaitingApproval    Status = "AWAITING_APPROVAL"
	StatusExecuting           Status = "EXECUTING"
	StatusVerifying           Status = "VERIFYING"
	StatusRepaired            Status = "REPAIRED"
	StatusRejected            Status = "REJECTED"
	StatusExecutionFailed     Status = "EXECUTION_FAILED"
	StatusVerificationFailed  Status = "VERIFICATION_FAILED"
	StatusPlanningFailed      Status = "PLANNING_FAILED"
	StatusInvestigationFailed Status = "INVESTIGATION_FAILED"
	StatusInconclusive        Status = "INCONCLUSIVE"
	StatusCancelled           Status = "CANCELLED"
)

// Run 表示一次自然语言调查任务。
type Run struct {
	ID           string          `json:"run_id"`
	UserMessage  string          `json:"message"`
	OrderID      string          `json:"order_id"`
	Status       Status          `json:"status"`
	TraceID      string          `json:"trace_id"`
	Version      int64           `json:"version"`
	Plan         json.RawMessage `json:"plan,omitempty"`
	FinalSummary json.RawMessage `json:"final_summary,omitempty"`
	ErrorCode    *string         `json:"error_code,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
}

// AgentStep 表示一次 Planner 或 Investigator 执行。
type AgentStep struct {
	ID           string          `json:"step_id"`
	RunID        string          `json:"run_id"`
	AgentType    string          `json:"agent_type"`
	Sequence     int             `json:"sequence"`
	Status       string          `json:"status"`
	ModelName    string          `json:"model_name"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output,omitempty"`
	ErrorCode    *string         `json:"error_code,omitempty"`
	InputTokens  int64           `json:"input_tokens"`
	OutputTokens int64           `json:"output_tokens"`
	StartedAt    time.Time       `json:"started_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
}

// Evidence 表示一次成功 MCP 调用的不可变证据快照。
type Evidence struct {
	EvidenceID  string          `json:"evidence_id"`
	RunID       string          `json:"run_id"`
	AgentStepID string          `json:"agent_step_id"`
	ToolCallID  string          `json:"tool_call_id"`
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	Source      string          `json:"source"`
	CollectedAt time.Time       `json:"collected_at"`
	Data        json.RawMessage `json:"data"`
	ContentHash string          `json:"content_hash"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ApprovalRecord 表示用户对诊断结论或修复动作的一次审批记录。
type ApprovalRecord struct {
	ID         string    `json:"approval_id"`
	RunID      string    `json:"run_id"`
	Type       string    `json:"type"`
	Decision   string    `json:"decision"`
	Conclusion string    `json:"conclusion"`
	NextStatus Status    `json:"next_status"`
	CreatedAt  time.Time `json:"created_at"`
}

// Event 表示可重放的调查进度事件。
type Event struct {
	ID        int64           `json:"id"`
	RunID     string          `json:"run_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// Plan 是 Planner 必须输出的结构化调查计划。
type Plan struct {
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

// PlanStep 表示调查计划中的一个候选工具步骤。
type PlanStep struct {
	ID      string         `json:"id"`
	Purpose string         `json:"purpose"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
}

// InvestigationResult 是 Investigator 的阶段 3 最终输出。
type InvestigationResult struct {
	Summary            string     `json:"summary"`
	Facts              []Fact     `json:"facts"`
	RemainingQuestions []string   `json:"remaining_questions"`
	Diagnosis          *Diagnosis `json:"diagnosis,omitempty"`
}

// SupplementalInvestigationRequest 告诉 Investigator 上一次诊断为何未通过证据校验。
// Investigator 只能据此补充证据，不能把校验反馈当作新的事实。
type SupplementalInvestigationRequest struct {
	Attempt           int                 `json:"attempt"`
	Previous          InvestigationResult `json:"previous_investigation"`
	RejectedDiagnosis Diagnosis           `json:"rejected_diagnosis"`
	ValidationIssues  []string            `json:"validation_issues"`
}

// RootCauseValidation 是 Go 对 Diagnosis 候选根因的支持性校验结果。
// 它只说明候选结论是否成立，不推导或替换为另一个根因。
type RootCauseValidation struct {
	Valid  bool     `json:"valid"`
	Issues []string `json:"issues,omitempty"`
}

// Diagnosis 是阶段 4 的带证据根因结论。
type Diagnosis struct {
	RootCause         string             `json:"root_cause"`
	Confidence        float64            `json:"confidence"`
	EvidenceIDs       []string           `json:"evidence_ids"`
	Alternatives      []AlternativeCause `json:"alternatives,omitempty"`
	RecommendedAction string             `json:"recommended_action,omitempty"`
}

// AlternativeCause 是未确认的替代原因。
type AlternativeCause struct {
	Cause      string  `json:"cause"`
	Confidence float64 `json:"confidence"`
}

// Fact 表示引用不可变证据的调查事实。
type Fact struct {
	EvidenceID string `json:"evidence_id"`
	Fact       string `json:"fact"`
}

// KnowledgeIntent 是 Knowledge Intent Agent 提取出的检索意图。
type KnowledgeIntent struct {
	Type     string   `json:"type"`
	Keywords []string `json:"keywords,omitempty"`
	Service  string   `json:"service,omitempty"`
	Domain   string   `json:"domain,omitempty"`
	Action   string   `json:"action,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// KnowledgeIntentResult 是 Knowledge Intent Agent 的结构化输出。
type KnowledgeIntentResult struct {
	Intents []KnowledgeIntent `json:"intents"`
}

// KnowledgeCall 是经过 Go Planner 校验后的知识工具调用。
type KnowledgeCall struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Reason    string         `json:"reason,omitempty"`
}

// KnowledgeExecutionPlan 是 Knowledge Executor 唯一允许执行的调用集合。
type KnowledgeExecutionPlan struct {
	Calls []KnowledgeCall `json:"calls"`
}

// KnowledgeResult 是知识检索阶段返回给后续诊断的摘要。
type KnowledgeResult struct {
	Intents KnowledgeIntentResult  `json:"intents"`
	Plan    KnowledgeExecutionPlan `json:"plan"`
	Gaps    []string               `json:"gaps,omitempty"`
}
