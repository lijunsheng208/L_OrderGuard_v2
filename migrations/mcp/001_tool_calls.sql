CREATE SCHEMA IF NOT EXISTS mcp;

CREATE TABLE IF NOT EXISTS mcp.tool_calls (
    id BIGSERIAL PRIMARY KEY,
    request_id TEXT NOT NULL,
    server_name TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    arguments JSONB NOT NULL,
    success BOOLEAN NOT NULL,
    evidence_id TEXT,
    source TEXT NOT NULL,
    result JSONB NOT NULL,
    error_code TEXT,
    duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
    called_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS tool_calls_tool_time_idx
    ON mcp.tool_calls (tool_name, called_at DESC);

CREATE INDEX IF NOT EXISTS tool_calls_evidence_idx
    ON mcp.tool_calls (evidence_id)
    WHERE evidence_id IS NOT NULL;

