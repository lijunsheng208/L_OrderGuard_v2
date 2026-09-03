CREATE SCHEMA IF NOT EXISTS payments;

CREATE TABLE IF NOT EXISTS payments.payments (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL UNIQUE REFERENCES orders.orders(id),
    amount BIGINT NOT NULL CHECK (amount >= 0),
    status TEXT NOT NULL CHECK (
        status IN ('INITIATED', 'PROCESSING', 'SUCCESS', 'FAILED', 'REFUNDED')
    ),
    paid_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payments.outbox_events (
    event_id TEXT PRIMARY KEY,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    publish_status TEXT NOT NULL DEFAULT 'PENDING' CHECK (
        publish_status IN ('PENDING', 'PUBLISHED', 'FAILED')
    ),
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON payments.outbox_events (created_at)
    WHERE publish_status = 'PENDING';
