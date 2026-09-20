-- HUI-1685 / FEAT-0186 线索自动分配(全部 additive 新表,不重建任何既有表):
--   lead_assign_pool:租户级销售分配池配置。每租户每成员至多一行(member_id
--   全局唯一,members 本就按租户归属)。列语义:
--     - weight         平滑加权轮询权重(默认 1;CHECK 域 1..1000;全部同权重
--       时退化为纯轮询 —— nginx smooth WRR)。
--     - current_weight 轮询内部游标(smooth WRR 状态,非配置:由分配事务推进,
--       更新它不碰 updated_at,updated_at 只属于配置编辑)。
--     - region/industry 可选标签:新线索首投时按线索携带的地域/行业做「精确
--       匹配优先」(线索提供的每个非空维度都必须与条目标签完全相等),无匹配
--       则回退全池平滑加权轮询。
--   有效池在分配时刻 JOIN members 现算:enabled=1 且 role='sales'(在册在职,
--   复用既有角色模型,不发明新角色);成员被停用/改角色后动态退出轮询,配置行
--   保留。池空不阻塞建档(线索照建,仅不自动分配)。
--
-- 与 0007 重建舞步的关系(0007 注释:「后续新增 REFERENCES leads 的表时,
-- 本舞步必须同步覆盖」):本表 REFERENCES tenants/members,不 REFERENCES
-- leads/contacts,不属于 0007 暂存/回插舞步的覆盖对象(与 0008 follow_ups
-- 同款判定);纯 additive 建表,对既有表零改动。

CREATE TABLE IF NOT EXISTS lead_assign_pool (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    member_id      TEXT NOT NULL REFERENCES members(id),
    weight         INTEGER NOT NULL DEFAULT 1 CHECK (weight >= 1 AND weight <= 1000),
    current_weight INTEGER NOT NULL DEFAULT 0,
    region         TEXT NOT NULL DEFAULT '',
    industry       TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL REFERENCES members(id),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (member_id)
);

CREATE INDEX IF NOT EXISTS idx_lead_assign_pool_tenant ON lead_assign_pool(tenant_id);
