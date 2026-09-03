DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'outbox_events_publish_status_check'
          AND conrelid = 'payments.outbox_events'::regclass
    ) THEN
        ALTER TABLE payments.outbox_events
            DROP CONSTRAINT outbox_events_publish_status_check;
    END IF;
    ALTER TABLE payments.outbox_events
        ADD CONSTRAINT outbox_events_publish_status_check
        CHECK (publish_status IN ('PENDING', 'PUBLISHED', 'FAILED'));
END $$;
