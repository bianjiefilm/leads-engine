-- HUI-1689 多渠道跟进首版。只记录内部提醒、待发送草稿、提交渠道、
-- 渠道送达和用户回复。没有真实渠道回执，status 不能是 delivered，
-- delivered 与 live_charge 恒为 0。不发送短信、邮件或企微，不扣费。

CREATE TABLE IF NOT EXISTS sop_policy (
    tenant_id            TEXT PRIMARY KEY REFERENCES tenants(id),
    level                TEXT NOT NULL CHECK (level IN ('remind_draft_confirm','unattended')),
    explicit_unattended  INTEGER NOT NULL DEFAULT 0 CHECK (explicit_unattended IN (0,1)),
    global_stop          INTEGER NOT NULL DEFAULT 0 CHECK (global_stop IN (0,1)),
    window_start         INTEGER NOT NULL,
    window_end           INTEGER NOT NULL,
    updated_by           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sop_stops (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    contact_id  TEXT NOT NULL DEFAULT '',
    channel     TEXT NOT NULL DEFAULT '',
    purpose     TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL CHECK (kind IN ('unsubscribe','customer_stop','reject','global_stop')),
    active      INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sop_stops_tenant ON sop_stops(tenant_id, contact_id, active);

CREATE TABLE IF NOT EXISTS sop_actions (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    contact_id          TEXT NOT NULL REFERENCES contacts(id),
    session_id          TEXT NOT NULL DEFAULT '',
    channel             TEXT NOT NULL,
    recipient           TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    consent_id          TEXT NOT NULL DEFAULT '',
    content_version     INTEGER NOT NULL,
    operator_id         TEXT NOT NULL REFERENCES members(id),
    budget_cents        INTEGER NOT NULL,
    live_charge         INTEGER NOT NULL DEFAULT 0 CHECK (live_charge = 0),
    kind                TEXT NOT NULL CHECK (kind IN ('internal_reminder','pending_draft','channel_submission','channel_delivery','user_reply')),
    status              TEXT NOT NULL CHECK (status IN ('recorded','pending_send','undelivered','blocked')),
    refusal             TEXT NOT NULL DEFAULT '',
    parent_id           TEXT NOT NULL DEFAULT '',
    attempt             INTEGER NOT NULL DEFAULT 0,
    body                TEXT NOT NULL DEFAULT '',
    delivered           INTEGER NOT NULL DEFAULT 0 CHECK (delivered = 0),
    created_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sop_actions_tenant ON sop_actions(tenant_id, contact_id, created_at);
CREATE INDEX IF NOT EXISTS idx_sop_actions_parent ON sop_actions(parent_id, kind, status);
