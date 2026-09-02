CREATE SCHEMA IF NOT EXISTS orders;

CREATE TABLE IF NOT EXISTS orders.orders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    amount BIGINT NOT NULL CHECK (amount >= 0),
    status TEXT NOT NULL CHECK (
        status IN ('PENDING_PAYMENT', 'PAID', 'CANCELLED', 'FULFILLING', 'SHIPPED')
    ),
    shipment_status TEXT NOT NULL DEFAULT 'NOT_SHIPPED',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orders.order_items (
    order_id TEXT NOT NULL REFERENCES orders.orders(id) ON DELETE CASCADE,
    sku_id TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price BIGINT NOT NULL CHECK (unit_price >= 0),
    PRIMARY KEY (order_id, sku_id)
);

