-- HUI-1688 统一接待核心。全部 additive，租户作用域。
-- 匿名访客只有 visitor_key 的 HMAC，不建 principal，不进营销池。
-- reception_usage.live_charge 恒为 0：本票不向真实客户扣费。

CREATE TABLE IF NOT EXISTS reception_widgets (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    channel         TEXT NOT NULL CHECK (channel IN ('h5')),
    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    default_mode    TEXT NOT NULL CHECK (default_mode IN ('ai','assist','human')),
    persona_wording TEXT NOT NULL DEFAULT '',
    language        TEXT NOT NULL DEFAULT 'zh',
    created_by      TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS knowledge_sources (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    kind         TEXT NOT NULL CHECK (kind IN ('faq')),
    question     TEXT NOT NULL,
    answer       TEXT NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    withdrawn_at TEXT,
    created_by   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_tenant ON knowledge_sources(tenant_id);

CREATE TABLE IF NOT EXISTS reception_sessions (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    widget_id        TEXT NOT NULL REFERENCES reception_widgets(id),
    visitor_key_hash TEXT NOT NULL,
    mode             TEXT NOT NULL CHECK (mode IN ('ai','assist','human')),
    owner_member_id  TEXT REFERENCES members(id),
    epoch            INTEGER NOT NULL DEFAULT 1,
    version          INTEGER NOT NULL DEFAULT 1,
    status           TEXT NOT NULL CHECK (status IN ('open','closed')),
    pending_reason   TEXT NOT NULL DEFAULT '',
    language         TEXT NOT NULL DEFAULT 'zh',
    contact_id       TEXT REFERENCES contacts(id),
    lead_id          TEXT REFERENCES leads(id),
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_reception_open_visitor
    ON reception_sessions(widget_id, visitor_key_hash) WHERE status = 'open';

CREATE TABLE IF NOT EXISTS reception_messages (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    session_id    TEXT NOT NULL REFERENCES reception_sessions(id),
    client_msg_id TEXT NOT NULL,
    body          TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (session_id, client_msg_id)
);

CREATE TABLE IF NOT EXISTS reception_replies (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    session_id      TEXT NOT NULL REFERENCES reception_sessions(id),
    epoch           INTEGER NOT NULL,
    session_version INTEGER NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('ai','draft','human')),
    body            TEXT NOT NULL,
    citations_json  TEXT NOT NULL DEFAULT '[]',
    gaps_json       TEXT NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL CHECK (status IN ('generated','approved','sent','superseded','blocked')),
    generated_at    TEXT,
    approved_at     TEXT,
    sent_at         TEXT,
    client_msg_id   TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    UNIQUE (session_id, client_msg_id)
);

CREATE TABLE IF NOT EXISTS reception_usage (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    reply_id         TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    units            INTEGER NOT NULL,
    live_charge      INTEGER NOT NULL DEFAULT 0 CHECK (live_charge = 0),
    created_at       TEXT NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS reception_events (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    session_id TEXT NOT NULL REFERENCES reception_sessions(id),
    type       TEXT NOT NULL CHECK (type IN ('takeover','release','interruption')),
    epoch      INTEGER NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS reception_receipts (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    reply_id   TEXT NOT NULL REFERENCES reception_replies(id),
    receipt_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (reply_id, receipt_id)
);
