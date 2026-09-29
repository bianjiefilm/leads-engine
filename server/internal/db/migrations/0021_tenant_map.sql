-- HUI-2316: record the source tenant and the mapping decision beside notify rows.
-- Additive. tenant_id stays the CRM target and is not rewritten.
-- Older rows only kept that target, so source_tenant_id is backfilled to it
-- and marked legacy_stored_tenant. This does not move leads, contacts, or consents.

ALTER TABLE notify_inbox ADD COLUMN source_ns TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_inbox ADD COLUMN source_tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_inbox ADD COLUMN map_target_tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_inbox ADD COLUMN map_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_inbox ADD COLUMN map_basis TEXT NOT NULL DEFAULT '';

UPDATE notify_inbox SET
    source_ns = CASE WHEN source_ns = '' THEN 'notify' ELSE source_ns END,
    source_tenant_id = CASE WHEN source_tenant_id = '' THEN tenant_id ELSE source_tenant_id END,
    map_target_tenant_id = CASE WHEN map_target_tenant_id = '' THEN tenant_id ELSE map_target_tenant_id END,
    map_basis = CASE WHEN map_basis = '' THEN 'legacy_stored_tenant' ELSE map_basis END;

ALTER TABLE notify_revocations ADD COLUMN source_ns TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_revocations ADD COLUMN source_tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_revocations ADD COLUMN map_target_tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_revocations ADD COLUMN map_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_revocations ADD COLUMN map_basis TEXT NOT NULL DEFAULT '';

UPDATE notify_revocations SET
    source_ns = CASE WHEN source_ns = '' THEN 'notify' ELSE source_ns END,
    source_tenant_id = CASE WHEN source_tenant_id = '' THEN tenant_id ELSE source_tenant_id END,
    map_target_tenant_id = CASE WHEN map_target_tenant_id = '' THEN tenant_id ELSE map_target_tenant_id END,
    map_basis = CASE WHEN map_basis = '' THEN 'legacy_stored_tenant' ELSE map_basis END;

CREATE UNIQUE INDEX IF NOT EXISTS idx_notify_inbox_source_event
    ON notify_inbox(source_app, source_ns, source_tenant_id, profile_event_id);

CREATE INDEX IF NOT EXISTS idx_notify_inbox_source_tenant
    ON notify_inbox(source_tenant_id, source_app, source_ns);

CREATE INDEX IF NOT EXISTS idx_notify_revocations_source
    ON notify_revocations(source_app, source_ns, source_tenant_id, source_ref);
