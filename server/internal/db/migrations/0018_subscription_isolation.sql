-- HUI-1993 local read model. This is not a platform wallet and not a charge log.
-- crm_entitlement_cache remembers the last entitlement observation.
-- ai_usage_quotes remember a quote. live_charge and side_effects stay 0:
-- this database cannot debit a wallet, place a call, or send a message.

CREATE TABLE IF NOT EXISTS crm_entitlement_cache (
    tenant_id                    TEXT PRIMARY KEY REFERENCES tenants(id),
    plan                         TEXT NOT NULL,
    status                       TEXT NOT NULL CHECK (status IN ('active','expired','missing')),
    wallet_balance_cents         INTEGER NOT NULL,
    org_billing_account_id       TEXT NOT NULL,
    authorized_payer_account_id  TEXT NOT NULL DEFAULT '',
    crm_subscription_cents       INTEGER NOT NULL,
    merchant_deal_cents          INTEGER NOT NULL,
    updated_at                   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ai_usage_quotes (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    action            TEXT NOT NULL,
    payer_kind        TEXT NOT NULL,
    payer_account_id  TEXT NOT NULL,
    quote_cents       INTEGER NOT NULL,
    revision          TEXT NOT NULL,
    balance_seen      INTEGER NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('quoted','committed')),
    return_to         TEXT NOT NULL DEFAULT '',
    live_charge       INTEGER NOT NULL DEFAULT 0 CHECK (live_charge = 0),
    side_effects      INTEGER NOT NULL DEFAULT 0 CHECK (side_effects = 0),
    created_at        TEXT NOT NULL,
    committed_at      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ai_usage_quotes_tenant ON ai_usage_quotes(tenant_id, created_at);
