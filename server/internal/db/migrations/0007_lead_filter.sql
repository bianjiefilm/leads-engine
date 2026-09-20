-- HUI-1686 / FEAT-0187 无效线索过滤(全部 additive,不改既有列语义):
--   leads 重建:status 域新增 'filtered'(机器判定,仅 intake 写入,人工 API 白名单
--     不放行)+ 新增 filter_reason 列(机器原因码:invalid_phone / invalid_email)。
--   语义:过滤不静默丢弃 —— 无效线索仍写台账(contact/lead/事件行俱全),
--   status=filtered 而非 new,故不进入营销池;事件三分类(去重)语义不变,
--   过滤维度记在 lead 上,两维正交。与 consent 语义正交(无营销许可本就不入池,
--   过滤再加一道)。
--   PII:filter_reason 只存机器码,绝不存联系方式原文。

-- SQLite 修改 CHECK 需重建表(先例:0002 opportunities)。与 0002 不同,本表已有
-- 外键子行(lead_intake_events 与 form_submissions 的 lead_id):迁移事务内
-- PRAGMA foreign_keys 是 no-op,而 defer_foreign_keys 只把 DROP 父表的隐式 DELETE
-- 违规推迟到提交 —— 计数不会因子表更名复位而清零,COMMIT 必然失败。
-- 因此:先把两张子表行原样暂存并清空(删子行不涉违规),重建父表后再回插,
-- 全程在迁移自己的一个事务里,崩溃安全、提交时外键约束自然成立。
-- 注意:后续新增 REFERENCES leads 的表时,本舞步必须同步覆盖。
CREATE TABLE lead_intake_events_hui1686_backup AS SELECT * FROM lead_intake_events;
DELETE FROM lead_intake_events;
CREATE TABLE form_submissions_hui1686_backup AS SELECT * FROM form_submissions;
DELETE FROM form_submissions;

CREATE TABLE leads_new (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    contact_id         TEXT NOT NULL REFERENCES contacts(id),
    source_ref_id      TEXT REFERENCES source_refs(id),
    status             TEXT NOT NULL CHECK (status IN ('new','in_progress','converted','closed','filtered')),
    filter_reason      TEXT NOT NULL DEFAULT '',
    assigned_member_id TEXT REFERENCES members(id),
    created_by         TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

INSERT INTO leads_new (
    id, tenant_id, contact_id, source_ref_id, status, filter_reason,
    assigned_member_id, created_by, created_at, updated_at
)
SELECT
    id, tenant_id, contact_id, source_ref_id, status, '',
    assigned_member_id, created_by, created_at, updated_at
FROM leads;

DROP TABLE leads;
ALTER TABLE leads_new RENAME TO leads;

-- 子行原样回插(列序与暂存一致;回插时父行已全部在场,无延迟违规)。
INSERT INTO lead_intake_events SELECT * FROM lead_intake_events_hui1686_backup;
DROP TABLE lead_intake_events_hui1686_backup;
INSERT INTO form_submissions SELECT * FROM form_submissions_hui1686_backup;
DROP TABLE form_submissions_hui1686_backup;

CREATE INDEX IF NOT EXISTS idx_leads_tenant ON leads(tenant_id);
