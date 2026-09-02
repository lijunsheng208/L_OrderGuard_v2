CREATE SCHEMA IF NOT EXISTS observability;

CREATE TABLE IF NOT EXISTS observability.signals (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL,
    order_id TEXT,
    service_name TEXT NOT NULL,
    signal_type TEXT NOT NULL CHECK (signal_type IN ('LOG', 'TRACE')),
    operation TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS signals_trace_idx
    ON observability.signals (trace_id, started_at);

CREATE INDEX IF NOT EXISTS signals_order_service_idx
    ON observability.signals (order_id, service_name, started_at DESC);
