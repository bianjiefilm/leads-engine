-- HUI-1681 授权渠道互动。评论/私信先落互动，线索候选单独成行。
-- 去重键是租户 + 渠道 + 账号 + 事件 ID。正文只留在本业务表，不进公共 Notify。
-- 验证状态不落成“已支持”：没有真实渠道凭证时由读模型返回 unverified。

CREATE TABLE IF NOT EXISTS channel_grants (
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    provider     TEXT NOT NULL,
    account_id   TEXT NOT NULL,
    app_id       TEXT NOT NULL,
    subject_ns   TEXT NOT NULL,
    message_ns   TEXT NOT NULL,
    post_ns      TEXT NOT NULL,
    capabilities TEXT NOT NULL,
    revoked_at   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (tenant_id, provider, account_id)
);

CREATE TABLE IF NOT EXISTS channel_interactions (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    provider          TEXT NOT NULL,
    account_id        TEXT NOT NULL,
    app_id            TEXT NOT NULL,
    event_id          TEXT NOT NULL,
    kind              TEXT NOT NULL,
    subject_id        TEXT NOT NULL,
    nickname          TEXT NOT NULL DEFAULT '',
    phone             TEXT NOT NULL DEFAULT '',
    body              TEXT NOT NULL,
    text_sha256       TEXT NOT NULL,
    explicit_intent   INTEGER NOT NULL DEFAULT 0 CHECK (explicit_intent IN (0,1)),
    purpose           TEXT NOT NULL DEFAULT '',
    retracted         INTEGER NOT NULL DEFAULT 0 CHECK (retracted IN (0,1)),
    score             INTEGER NOT NULL DEFAULT 0,
    channel_contact   INTEGER NOT NULL DEFAULT 0 CHECK (channel_contact IN (0,1)),
    phone_marketing   INTEGER NOT NULL DEFAULT 0 CHECK (phone_marketing IN (0,1)),
    sms_marketing     INTEGER NOT NULL DEFAULT 0 CHECK (sms_marketing IN (0,1)),
    auto_reach        INTEGER NOT NULL DEFAULT 0 CHECK (auto_reach IN (0,1)),
    receipt_platform  INTEGER NOT NULL DEFAULT 0 CHECK (receipt_platform IN (0,1)),
    receipt_persisted INTEGER NOT NULL DEFAULT 0 CHECK (receipt_persisted IN (0,1)),
    receipt_human     INTEGER NOT NULL DEFAULT 0 CHECK (receipt_human IN (0,1)),
    candidate_id      TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (tenant_id, provider, account_id, event_id)
);

CREATE TABLE IF NOT EXISTS channel_lead_candidates (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    interaction_id  TEXT NOT NULL,
    provider        TEXT NOT NULL,
    account_id      TEXT NOT NULL,
    event_id        TEXT NOT NULL,
    subject_id      TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('open','confirmed','withdrawn')),
    lead_id         TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (tenant_id, provider, account_id, event_id)
);

CREATE TABLE IF NOT EXISTS channel_reply_floor (
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    provider   TEXT NOT NULL,
    account_id TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    holder     TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (tenant_id, provider, account_id, subject_id)
);
