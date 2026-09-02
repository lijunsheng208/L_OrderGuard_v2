CREATE TABLE IF NOT EXISTS orders.state_history (
    id BIGSERIAL PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders.orders(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    from_status TEXT,
    to_status TEXT NOT NULL,
    reason TEXT NOT NULL,
    trace_id TEXT,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id, version)
);

INSERT INTO orders.state_history (
    order_id, version, from_status, to_status, reason, changed_at
)
SELECT id, version, NULL, status, 'MIGRATION_BACKFILL', created_at
FROM orders.orders
ON CONFLICT (order_id, version) DO NOTHING;

