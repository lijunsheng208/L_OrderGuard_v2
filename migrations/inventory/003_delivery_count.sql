ALTER TABLE inventory.consumed_events
    ADD COLUMN IF NOT EXISTS delivery_count INTEGER NOT NULL DEFAULT 1;

