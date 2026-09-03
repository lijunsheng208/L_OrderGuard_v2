CREATE TABLE IF NOT EXISTS agent.demo_faults (
    order_id TEXT PRIMARY KEY,
    fault_type TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    cleared_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS demo_faults_enabled_idx ON agent.demo_faults (enabled, fault_type);
