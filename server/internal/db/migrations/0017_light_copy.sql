-- HUI-1682 场景内轻文案。只保存手写回复、邮件和营销简介。
-- 没有真实模型，generated 恒为 0。没有发送渠道，sent/published/
-- marketing_permitted/live_charge 恒为 0。确认只表示用户看过草稿。
-- 交接不在本库创建视频、产品图或数字人工程。

CREATE TABLE IF NOT EXISTS light_copy_drafts (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    subject_kind          TEXT NOT NULL CHECK (subject_kind IN ('opportunity','campaign')),
    subject_id            TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('reply','email','marketing_brief')),
    body                  TEXT NOT NULL,
    origin                TEXT NOT NULL CHECK (origin = 'manual'),
    content_version       INTEGER NOT NULL,
    generated             INTEGER NOT NULL DEFAULT 0 CHECK (generated = 0),
    sent                  INTEGER NOT NULL DEFAULT 0 CHECK (sent = 0),
    published             INTEGER NOT NULL DEFAULT 0 CHECK (published = 0),
    marketing_permitted   INTEGER NOT NULL DEFAULT 0 CHECK (marketing_permitted = 0),
    user_confirmed        INTEGER NOT NULL DEFAULT 0 CHECK (user_confirmed IN (0,1)),
    live_charge           INTEGER NOT NULL DEFAULT 0 CHECK (live_charge = 0),
    updated_by            TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    UNIQUE (tenant_id, subject_kind, subject_id, kind)
);

CREATE TABLE IF NOT EXISTS light_copy_handoffs (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    idempotency_key   TEXT NOT NULL,
    draft_id          TEXT NOT NULL DEFAULT '',
    tool              TEXT NOT NULL,
    source_kind       TEXT NOT NULL,
    source_id         TEXT NOT NULL,
    content_version   INTEGER NOT NULL DEFAULT 0,
    fact_json         TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('failed','recorded','declined','refused')),
    reason            TEXT NOT NULL,
    project_created   INTEGER NOT NULL DEFAULT 0 CHECK (project_created = 0),
    regenerated       INTEGER NOT NULL DEFAULT 0 CHECK (regenerated = 0),
    created_at        TEXT NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_light_copy_drafts_subject ON light_copy_drafts(tenant_id, subject_kind, subject_id);
