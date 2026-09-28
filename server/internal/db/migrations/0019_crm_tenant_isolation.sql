-- HUI-2054: brand is a source/display row, not a second CRM database.
-- Customer rows stay on the existing tenant-scoped tables.

CREATE TABLE IF NOT EXISTS brands (
    id           TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contact_origins (
    contact_id TEXT PRIMARY KEY REFERENCES contacts(id),
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    brand_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_operators (
    principal_ref TEXT PRIMARY KEY,
    channel_id    TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS crm_delegations (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('support','operation','offboarding')),
    revoked_at    TEXT,
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tenant_lifecycle (
    tenant_id  TEXT PRIMARY KEY REFERENCES tenants(id),
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    updated_at TEXT NOT NULL
);

-- Channel purchase price and the internal token stay in this table.
-- CRM export must not read them.
CREATE TABLE IF NOT EXISTS channel_procurement (
    tenant_id            TEXT PRIMARY KEY REFERENCES tenants(id),
    purchase_price_cents INTEGER NOT NULL DEFAULT 0,
    ai_quota             INTEGER NOT NULL DEFAULT 0,
    internal_token       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS crm_export_jobs (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    brand_id            TEXT NOT NULL DEFAULT '',
    requester_principal TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_contact_origins_tenant ON contact_origins(tenant_id);
CREATE INDEX IF NOT EXISTS idx_crm_delegations_tenant ON crm_delegations(tenant_id, principal_ref);

-- Extend the HUI-1993 plan status set. Downgrade is a new observation,
-- not a deletion of the cache or of CRM history.
CREATE TABLE crm_entitlement_cache_hui2054 (
    tenant_id                    TEXT PRIMARY KEY REFERENCES tenants(id),
    plan                         TEXT NOT NULL,
    status                       TEXT NOT NULL CHECK (status IN ('active','expired','missing','downgraded')),
    wallet_balance_cents         INTEGER NOT NULL,
    org_billing_account_id       TEXT NOT NULL,
    authorized_payer_account_id  TEXT NOT NULL DEFAULT '',
    crm_subscription_cents       INTEGER NOT NULL,
    merchant_deal_cents          INTEGER NOT NULL,
    updated_at                   TEXT NOT NULL
);
INSERT INTO crm_entitlement_cache_hui2054(
    tenant_id, plan, status, wallet_balance_cents, org_billing_account_id,
    authorized_payer_account_id, crm_subscription_cents, merchant_deal_cents, updated_at)
SELECT tenant_id, plan, status, wallet_balance_cents, org_billing_account_id,
    authorized_payer_account_id, crm_subscription_cents, merchant_deal_cents, updated_at
FROM crm_entitlement_cache;
DROP TABLE crm_entitlement_cache;
ALTER TABLE crm_entitlement_cache_hui2054 RENAME TO crm_entitlement_cache;
