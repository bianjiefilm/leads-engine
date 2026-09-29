-- HUI-2054 r3: source tag and campaign id stay on the origin row.
-- They are not a tenant and must not be written into contacts.tenant_id.
ALTER TABLE contact_origins ADD COLUMN source_tag TEXT NOT NULL DEFAULT '';
ALTER TABLE contact_origins ADD COLUMN campaign_id TEXT NOT NULL DEFAULT '';
