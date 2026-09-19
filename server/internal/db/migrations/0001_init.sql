-- HUI-1748 L0 domain root. Minimal fields only; extensions belong to later FEAT tickets
-- (HUI-1691 contacts enrichment/dedup, HUI-1683/1680 lead intake, HUI-1692 follow-ups,
-- FEAT-0194 opportunity details). Every business row is tenant-scoped and audited.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- Platform members: a reference to a platform-identity principal plus the tenant role.
-- 身份纪律:principal_ref 只能来自 identity 会话解析,不可变;绝不从手机号/邮箱派生;
-- 本表不代表平台账户的存在或创建。
CREATE TABLE IF NOT EXISTS members (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('owner','sales','agent')),
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    display_name  TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (tenant_id, principal_ref)
);

-- Agent cross-merchant access: one explicit authorization row per (tenant, principal).
-- 无全局共享客户池:没有这一行,agent 对该租户一律 403。
CREATE TABLE IF NOT EXISTS agent_grants (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    granted_by    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (tenant_id, principal_ref)
);

-- Source provenance: which app produced the record, its opaque reference, and a
-- snapshot of the authorization scope under which it was captured.
CREATE TABLE IF NOT EXISTS source_refs (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    source_app          TEXT NOT NULL,
    source_ref          TEXT NOT NULL,
    auth_scope_snapshot TEXT NOT NULL,
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contacts (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    name              TEXT NOT NULL,
    phone             TEXT NOT NULL DEFAULT '',
    email             TEXT NOT NULL DEFAULT '',
    business_category TEXT NOT NULL CHECK (business_category IN ('merchant_customer','creative_service')),
    source_type       TEXT NOT NULL CHECK (source_type IN ('manual','form','touch_campaign')),
    consent_status    TEXT NOT NULL CHECK (consent_status IN ('pending','granted','denied')),
    source_ref_id     TEXT REFERENCES source_refs(id),
    assigned_member_id TEXT REFERENCES members(id),
    created_by        TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS leads (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    contact_id         TEXT NOT NULL REFERENCES contacts(id),
    source_ref_id      TEXT REFERENCES source_refs(id),
    status             TEXT NOT NULL CHECK (status IN ('new','in_progress','converted','closed')),
    assigned_member_id TEXT REFERENCES members(id),
    created_by         TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS opportunities (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    contact_id         TEXT NOT NULL REFERENCES contacts(id),
    title              TEXT NOT NULL,
    stage              TEXT NOT NULL CHECK (stage IN ('open','won','lost')),
    business_category  TEXT NOT NULL CHECK (business_category IN ('merchant_customer','creative_service')),
    assigned_member_id TEXT REFERENCES members(id),
    created_by         TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_contacts_tenant ON contacts(tenant_id);
CREATE INDEX IF NOT EXISTS idx_leads_tenant ON leads(tenant_id);
CREATE INDEX IF NOT EXISTS idx_opportunities_tenant ON opportunities(tenant_id);
CREATE INDEX IF NOT EXISTS idx_members_tenant ON members(tenant_id);
