CREATE SCHEMA IF NOT EXISTS inventory;

CREATE TABLE IF NOT EXISTS inventory.stocks (
    sku_id TEXT PRIMARY KEY,
    available INTEGER NOT NULL CHECK (available >= 0),
    reserved INTEGER NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS inventory.deductions (
    order_id TEXT NOT NULL,
    sku_id TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('DEDUCTED', 'FAILED', 'RELEASED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (order_id, sku_id)
);

CREATE TABLE IF NOT EXISTS inventory.consumed_events (
    event_id TEXT PRIMARY KEY,
    consumer TEXT NOT NULL,
    result JSONB NOT NULL,
    delivery_count INTEGER NOT NULL DEFAULT 1 CHECK (delivery_count > 0),
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
