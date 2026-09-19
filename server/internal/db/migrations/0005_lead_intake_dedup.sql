-- HUI-1683 / FEAT-0184 线索自动查重去重(全部 additive,不改既有列):
--   lead_intake_events:事件投递流水与幂等账本。幂等键 =
--     (接收租户 tenant_id, 来源 app source_app, 来源命名空间 source_ns, event_id)。
--     同键同内容(content_sha256)重复投递幂等返回原引用;同键异内容必须显式冲突,
--     绝不静默覆盖。class 是首投时的去重判定结果:
--       new             零命中,新建 contact+lead(非去重结果的记账值)
--       exact_duplicate 重复事件(账本里只记首投判定;重放零写入,响应层标 duplicate)
--       repeat_consult  同一联系人再次咨询:新 lead 关联既有 contact,咨询次数不丢
--       ambiguous       共享手机号多命中:新建独立 contact+lead + 候选池行,绝不静默合并
--     phone_fpr = HMAC-SHA256(pepper, 归一化手机号) hex:查重/审计索引不落明文手机号,
--     日志只允许出现指纹前缀。原始事件内容本体不入库(只留 sha256)。
--   merge_candidates:疑似同人候选池。共享号码等信号只产生"候选",合并必须 owner
--     显式执行;contact_a < contact_b 字典序规范对避免重复行。
--   contact_merges:合并审计主体。字段级 provenance + 两侧合并前快照 + 重指明细 id,
--     使 undo 忠实可回放、全程可审计纠错。consent 行随 contact 重指、不归并语义
--     (1691 红线:撤销不可逆在合并后依然成立,最严格原则只作用于 coarse 状态)。

CREATE TABLE IF NOT EXISTS lead_intake_events (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    source_app     TEXT NOT NULL,
    source_ns      TEXT NOT NULL DEFAULT '',
    event_id       TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    phone_fpr      TEXT NOT NULL DEFAULT '',
    class          TEXT NOT NULL CHECK (class IN ('new','exact_duplicate','repeat_consult','ambiguous')),
    lead_id        TEXT NOT NULL REFERENCES leads(id),
    contact_id     TEXT NOT NULL REFERENCES contacts(id),
    created_at     TEXT NOT NULL,
    UNIQUE (tenant_id, source_app, source_ns, event_id)
);

CREATE TABLE IF NOT EXISTS merge_candidates (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    contact_a   TEXT NOT NULL REFERENCES contacts(id),
    contact_b   TEXT NOT NULL REFERENCES contacts(id),
    reason      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','merged','dismissed')),
    created_at  TEXT NOT NULL,
    resolved_by TEXT NOT NULL DEFAULT '',
    resolved_at TEXT,
    UNIQUE (tenant_id, contact_a, contact_b, reason),
    CHECK (contact_a < contact_b)
);

CREATE TABLE IF NOT EXISTS contact_merges (
    id                     TEXT PRIMARY KEY,
    tenant_id              TEXT NOT NULL REFERENCES tenants(id),
    target_contact         TEXT NOT NULL REFERENCES contacts(id),
    source_contact         TEXT NOT NULL REFERENCES contacts(id),
    field_provenance       TEXT NOT NULL DEFAULT '{}',
    target_before          TEXT NOT NULL DEFAULT '{}',
    source_before          TEXT NOT NULL DEFAULT '{}',
    lead_repoint_count     INTEGER NOT NULL DEFAULT 0,
    opp_repoint_count      INTEGER NOT NULL DEFAULT 0,
    consent_repoint_count  INTEGER NOT NULL DEFAULT 0,
    followup_repoint_count INTEGER NOT NULL DEFAULT 0,
    repointed_lead_ids     TEXT NOT NULL DEFAULT '[]',
    repointed_consent_ids  TEXT NOT NULL DEFAULT '[]',
    repointed_opp_ids      TEXT NOT NULL DEFAULT '[]',
    repointed_followup_ids TEXT NOT NULL DEFAULT '[]',
    -- consent 重指时撞 (submission_ref, channel) 唯一键的源侧行:整行 JSON 存档
    -- (绝不删除授权明细而不可回溯),undo 时原样回插。
    collapsed_consent_rows TEXT NOT NULL DEFAULT '[]',
    merged_by              TEXT NOT NULL,
    merged_at              TEXT NOT NULL,
    undone_at              TEXT,
    undone_by              TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_lead_intake_events_tenant ON lead_intake_events(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_lead_intake_events_phone ON lead_intake_events(tenant_id, phone_fpr);
CREATE INDEX IF NOT EXISTS idx_merge_candidates_tenant ON merge_candidates(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_contact_merges_tenant ON contact_merges(tenant_id, merged_at);
