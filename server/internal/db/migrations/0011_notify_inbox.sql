-- HUI-1680 Notify 接收账本。inbox 与线索写入必须同一事务。
-- 事实键 (tenant, source_app, event_type, source_ref) 上 source_version 单调；
-- profile_event_id 是投递幂等键。body_sha256 是签名正文的内容身份：
-- 同键同内容重放返回原回执，同键异内容拒绝。
-- 联系方式不进本表（只留回执引用与正文哈希）。

CREATE TABLE IF NOT EXISTS notify_inbox (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    source_app        TEXT NOT NULL,
    event_type        TEXT NOT NULL,
    source_ref        TEXT NOT NULL,
    source_version    INTEGER NOT NULL,
    profile_event_id  TEXT NOT NULL,
    notify_event_id   TEXT NOT NULL DEFAULT '',
    delivery_id       TEXT NOT NULL DEFAULT '',
    body_sha256       TEXT NOT NULL,
    lead_id           TEXT,
    contact_id        TEXT,
    receipt_json      TEXT NOT NULL,
    occurred_at       TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (tenant_id, source_app, event_type, source_ref),
    UNIQUE (tenant_id, source_app, profile_event_id)
);

-- 已接受的营销撤销。晚到的更旧提交不得把 marketing_allowed 重新写成 true。
CREATE TABLE IF NOT EXISTS notify_revocations (
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    source_app     TEXT NOT NULL,
    source_ref     TEXT NOT NULL,
    source_version INTEGER NOT NULL,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (tenant_id, source_app, source_ref)
);

CREATE INDEX IF NOT EXISTS idx_notify_inbox_tenant ON notify_inbox(tenant_id, created_at);
