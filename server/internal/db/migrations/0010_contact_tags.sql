-- HUI-1690 / FEAT-0191 客户画像标签体系(人工标签层,additive 两新表):
--   contact_tag_defs:租户自定义标签定义(名称租户内唯一 / 颜色可空 / 描述可空)。
--     标签目录是租户级运营配置,语义归 owner(服务端 authz manage_contact_tags
--     单点裁决);名称唯一约束在库层面兜底,服务层先查先判给 409。
--   contact_tag_links:联系人打标关联。幂等唯一键 (tenant_id, tag_id, contact_id):
--     重复打标零新行;首打留痕(applied_by/applied_at)= 谁/何时/哪标签,可回查,
--     重放不改写首打时间(与 consent 撤销首戳、跟进完结首戳同款纪律)。
--     去标物理删行(关联是纯运营标注,不含业务事实;留痕在删前可查)。
-- 派生标签(生命周期/活跃度/来源渠道/跟进状态)是纯只读按需重算,零迁移、
-- 零落库 —— 本迁移只承载人工标签层。
--
-- 与 0007 重建舞步的关系(0007 注释:「后续新增 REFERENCES leads 的表时,
-- 本舞步必须同步覆盖」):两表均 REFERENCES contacts(tag_defs 另引 tenants/
-- members),属新增子表;本迁移纯 additive 建表,不重建任何既有父表;若未来
-- 重建 leads/contacts,contact_tag_links 必须与 follow_ups / lead_intake_events /
-- form_submissions 一并纳入暂存与回插。

CREATE TABLE IF NOT EXISTS contact_tag_defs (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL REFERENCES members(id),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS contact_tag_links (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    tag_id     TEXT NOT NULL REFERENCES contact_tag_defs(id),
    contact_id TEXT NOT NULL REFERENCES contacts(id),
    applied_by TEXT NOT NULL REFERENCES members(id),
    applied_at TEXT NOT NULL,
    UNIQUE (tenant_id, tag_id, contact_id)
);

CREATE INDEX IF NOT EXISTS idx_contact_tag_defs_tenant ON contact_tag_defs(tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_contact_tag_links_tag ON contact_tag_links(tag_id);
CREATE INDEX IF NOT EXISTS idx_contact_tag_links_contact ON contact_tag_links(contact_id);
