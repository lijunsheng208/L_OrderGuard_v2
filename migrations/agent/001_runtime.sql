CREATE SCHEMA IF NOT EXISTS agent;

CREATE TABLE IF NOT EXISTS agent.investigation_runs (
    id TEXT PRIMARY KEY,
    user_message TEXT NOT NULL,
    order_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN (
        'CREATED', 'PLANNING', 'INVESTIGATING', 'KNOWLEDGE_LOOKUP', 'DIAGNOSING', 'CRITIC_REVIEW', 'EVIDENCE_COLLECTED', 'NO_ANOMALY', 'POLICY_CHECK', 'AWAITING_APPROVAL', 'EXECUTING', 'VERIFYING', 'REPAIRED', 'REJECTED', 'EXECUTION_FAILED', 'VERIFICATION_FAILED',
        'PLANNING_FAILED', 'INVESTIGATION_FAILED', 'INCONCLUSIVE', 'CANCELLED'
    )),
    trace_id TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    plan JSONB,
    final_summary JSONB,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS investigation_runs_queue_idx
    ON agent.investigation_runs (created_at)
    WHERE status = 'CREATED';

CREATE TABLE IF NOT EXISTS agent.agent_steps (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    agent_type TEXT NOT NULL CHECK (agent_type IN ('PLANNER', 'INVESTIGATOR')),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    status TEXT NOT NULL CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED')),
    model_name TEXT NOT NULL,
    input JSONB NOT NULL,
    output JSONB,
    error_code TEXT,
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (run_id, sequence)
);

CREATE TABLE IF NOT EXISTS agent.evidence_snapshots (
    evidence_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    agent_step_id TEXT NOT NULL REFERENCES agent.agent_steps(id) ON DELETE CASCADE,
    tool_call_id TEXT NOT NULL UNIQUE,
    tool_name TEXT NOT NULL,
    arguments JSONB NOT NULL,
    source TEXT NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    data JSONB NOT NULL,
    content_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS evidence_snapshots_run_idx
    ON agent.evidence_snapshots (run_id, created_at);

CREATE TABLE IF NOT EXISTS agent.repair_plans (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    evidence_version BIGINT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent.repair_executions (
    id TEXT PRIMARY KEY,
    repair_plan_id TEXT NOT NULL REFERENCES agent.repair_plans(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('STARTED', 'SUCCEEDED', 'FAILED')),
    request JSONB NOT NULL,
    result JSONB,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS agent.verification_results (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    repair_execution_id TEXT REFERENCES agent.repair_executions(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('PASSED', 'FAILED')),
    assertions JSONB NOT NULL,
    evidence_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS repair_plans_run_idx ON agent.repair_plans (run_id, created_at);
CREATE INDEX IF NOT EXISTS repair_executions_run_idx ON agent.repair_executions (run_id, started_at);
CREATE INDEX IF NOT EXISTS verification_results_run_idx ON agent.verification_results (run_id, created_at);

CREATE TABLE IF NOT EXISTS agent.approval_records (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    approval_type TEXT NOT NULL CHECK (approval_type IN ('NO_ISSUE_CONFIRMATION', 'REPAIR_APPROVAL')),
    decision TEXT NOT NULL CHECK (decision IN ('APPROVED', 'REJECTED')),
    conclusion TEXT NOT NULL,
    next_status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS approval_records_run_idx ON agent.approval_records (run_id, created_at);

CREATE TABLE IF NOT EXISTS agent.investigation_events (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent.investigation_runs(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS investigation_events_run_idx
    ON agent.investigation_events (run_id, id);

ALTER TABLE mcp.tool_calls ADD COLUMN IF NOT EXISTS run_id TEXT;
ALTER TABLE mcp.tool_calls ADD COLUMN IF NOT EXISTS agent_step_id TEXT;
ALTER TABLE mcp.tool_calls ADD COLUMN IF NOT EXISTS trace_id TEXT;
ALTER TABLE mcp.tool_calls ADD COLUMN IF NOT EXISTS tool_call_id TEXT;
ALTER TABLE mcp.tool_calls ADD COLUMN IF NOT EXISTS caller TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS tool_calls_tool_call_id_idx
    ON mcp.tool_calls (tool_call_id)
    WHERE tool_call_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS tool_calls_run_idx
    ON mcp.tool_calls (run_id, called_at)
    WHERE run_id IS NOT NULL;

ALTER TABLE agent.agent_steps DROP CONSTRAINT IF EXISTS agent_steps_agent_type_check;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_steps_agent_type_check') THEN
        ALTER TABLE agent.agent_steps ADD CONSTRAINT agent_steps_agent_type_check CHECK (agent_type IN ('PLANNER', 'INVESTIGATOR', 'KNOWLEDGE', 'DIAGNOSIS', 'CRITIC', 'VERIFY'));
    END IF;
END $$;

ALTER TABLE agent.investigation_runs DROP CONSTRAINT IF EXISTS investigation_runs_status_check;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'investigation_runs_status_check') THEN
        ALTER TABLE agent.investigation_runs ADD CONSTRAINT investigation_runs_status_check CHECK (status IN (
            'CREATED', 'PLANNING', 'INVESTIGATING', 'KNOWLEDGE_LOOKUP', 'DIAGNOSING', 'CRITIC_REVIEW',
            'EVIDENCE_COLLECTED', 'NO_ANOMALY', 'POLICY_CHECK', 'AWAITING_APPROVAL', 'EXECUTING', 'VERIFYING', 'REPAIRED', 'REJECTED', 'EXECUTION_FAILED', 'VERIFICATION_FAILED', 'PLANNING_FAILED', 'INVESTIGATION_FAILED', 'INCONCLUSIVE', 'CANCELLED'
        ));
    END IF;
END $$;
