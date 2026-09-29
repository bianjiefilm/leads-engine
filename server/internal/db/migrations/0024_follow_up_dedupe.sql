-- HUI-1893: repeating the same follow-through must not insert a second fact.
-- Empty keys stay out of the unique index so older rows are unchanged.

ALTER TABLE follow_ups ADD COLUMN dedupe_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_follow_ups_dedupe
    ON follow_ups(tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
