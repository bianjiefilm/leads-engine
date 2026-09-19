-- HUI-1691 / FEAT-0192 客户档案域扩展(全部 additive,不改既有列):
--   contacts 扩展 备注/标签/软删除占位;
--   contact_consents:按 (contact, 来源提交 source_submission_ref, 渠道 source_channel)
--     维度的授权记录。撤销是持久标记:revoked_at 一旦写入,同键事件重放只能幂等返回,
--     绝不清除 —— 重新授权必须换新的来源提交引用(新键新行)。
--     source_submission_ref 字段名是投递端(HUI-1747/T1)的对齐锚点,撞名即可。
--   contact_followups:只追加的跟进时间线;note 全文永不进日志。
-- 删除/停止营销/最小审计三者分别定义:
--   删除 = deleted_at + 字段脱敏占位(行保留,维持 leads/opportunities/consents 引用);
--   停止营销 = marketing_revoked(批量置 revoked_at);
--   最小审计 = consents/followups 行在删除后仍然保留。

ALTER TABLE contacts ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN tags  TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN deleted_at TEXT;

CREATE TABLE IF NOT EXISTS contact_consents (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    contact_id            TEXT NOT NULL REFERENCES contacts(id),
    -- 写入时的租户快照(审计锚);租户真源仍是 tenant_id。
    tenant_scope          TEXT NOT NULL DEFAULT '',
    source_submission_ref TEXT NOT NULL DEFAULT '',
    source_channel        TEXT NOT NULL DEFAULT '',
    notice_version        TEXT NOT NULL DEFAULT '',
    purpose               TEXT NOT NULL DEFAULT '',
    marketing_allowed     INTEGER NOT NULL DEFAULT 0 CHECK (marketing_allowed IN (0,1)),
    -- 持久撤销标记:非 NULL 即已撤销,任何重放/刷新都不得清除或改写。
    revoked_at            TEXT,
    revoked_reason        TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    UNIQUE (contact_id, source_submission_ref, source_channel)
);

CREATE TABLE IF NOT EXISTS contact_followups (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    contact_id TEXT NOT NULL REFERENCES contacts(id),
    member_id  TEXT NOT NULL REFERENCES members(id),
    note       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_contacts_tenant_alive ON contacts(tenant_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_contact_consents_contact ON contact_consents(contact_id);
CREATE INDEX IF NOT EXISTS idx_contact_followups_contact ON contact_followups(contact_id);
