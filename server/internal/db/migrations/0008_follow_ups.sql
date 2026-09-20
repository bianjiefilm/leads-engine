-- HUI-1692 / FEAT-0193 销售跟进记录(additive 新表,不重建任何既有表):
--   follow_ups:可编辑、可完结/重开的销售跟进工作记录。与 HUI-1691 只追加的
--   contact_followups 审计时间线正交:那边是「发生过什么」的流水,本表是
--   「接下来要做什么/做完了没有」的工作项。
--   - 记录挂 contact(必填)/ lead(可空;挂 lead 时其 contact_id 必须等于
--     contact_id,由服务端校验,外键是最后一道防线);
--   - 记录级授权作用域 = 挂靠 lead(优先)或 contact 的 assigned_member_id,
--     完全沿用 L0 记录级作用域,不发明新权限模型;
--   - next_follow_up_at 可空(RFC3339,服务端规范化为 UTC 秒精度后入库,
--     使字符串比较即时间比较,跨时区确定性排序);completed_at 可空
--     (完结=首次服务端时间戳且幂等保留,重开=置 NULL;完结记录不进到期面);
--   - note 为业务数据入库,但全文不入日志/URL/Context(沿用 redact 管道,
--     日志只带长度摘要);
--   - idx_follow_ups_due 支撑「我的到期跟进」确定性查询(租户+完结过滤+到期升序)。
--
-- 与 0007 重建舞步的关系(0007 注释:「后续新增 REFERENCES leads 的表时,
-- 本舞步必须同步覆盖」):本表 REFERENCES leads/contacts,正是该注释所指的
-- 新增子表;本迁移为纯 additive 建表,不重建 leads/contacts,故不影响 0007 的
-- 暂存/回插舞步;若未来重建 leads/contacts,follow_ups 必须与
-- lead_intake_events / form_submissions 一并纳入暂存与回插。

CREATE TABLE IF NOT EXISTS follow_ups (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    contact_id        TEXT NOT NULL REFERENCES contacts(id),
    lead_id           TEXT REFERENCES leads(id),
    note              TEXT NOT NULL,
    next_follow_up_at TEXT,
    completed_at      TEXT,
    created_by        TEXT NOT NULL REFERENCES members(id),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_follow_ups_due ON follow_ups(tenant_id, completed_at, next_follow_up_at);
CREATE INDEX IF NOT EXISTS idx_follow_ups_contact ON follow_ups(contact_id);
CREATE INDEX IF NOT EXISTS idx_follow_ups_lead ON follow_ups(lead_id);
