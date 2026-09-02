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
