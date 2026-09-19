-- HUI-1693 / FEAT-0194 商机域扩展:
--   阶段细化(open/qualified/proposal/negotiation/won/closed_lost)、
--   金额 amount_cents(NULL = 未知,统计进「未知」桶,绝不当 0)、
--   amount_source(manual|unknown)、probability(0..100)、expected_close_at(可空),
--   以及阶段审计链 opportunity_stage_history(关闭/重开也是普通阶段转换,同一审计)。
-- 语义红线:won 仅表示「人工标记成交」,绝不表示已支付/已收款;
--   商机与接单分域:此处字段由获客原生域管理,不从公共 Task/接单状态派生。

-- SQLite 修改 CHECK 需重建表:建新表 -> 拷贝(存量 lost 映射 closed_lost)-> 改名。
CREATE TABLE opportunities_new (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    contact_id         TEXT NOT NULL REFERENCES contacts(id),
    title              TEXT NOT NULL,
    stage              TEXT NOT NULL CHECK (stage IN ('open','qualified','proposal','negotiation','won','closed_lost')),
    business_category  TEXT NOT NULL CHECK (business_category IN ('merchant_customer','creative_service')),
    amount_cents       INTEGER,
    amount_source      TEXT NOT NULL DEFAULT 'unknown' CHECK (amount_source IN ('manual','unknown')),
    probability        INTEGER NOT NULL DEFAULT 0 CHECK (probability BETWEEN 0 AND 100),
    expected_close_at  TEXT,
    assigned_member_id TEXT REFERENCES members(id),
    created_by         TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

INSERT INTO opportunities_new (
    id, tenant_id, contact_id, title, stage, business_category,
    amount_cents, amount_source, probability, expected_close_at,
    assigned_member_id, created_by, created_at, updated_at
)
SELECT
    id, tenant_id, contact_id, title,
    CASE stage WHEN 'lost' THEN 'closed_lost' ELSE stage END,
    business_category,
    NULL, 'unknown', 0, NULL,
    assigned_member_id, created_by, created_at, updated_at
FROM opportunities;

DROP TABLE opportunities;
ALTER TABLE opportunities_new RENAME TO opportunities;

-- 阶段审计链:每行租户作用域;创建商机写 from_stage='' 的起始行。
CREATE TABLE IF NOT EXISTS opportunity_stage_history (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    opportunity_id TEXT NOT NULL REFERENCES opportunities(id),
    from_stage     TEXT NOT NULL,
    to_stage       TEXT NOT NULL,
    changed_by     TEXT NOT NULL REFERENCES members(id),
    changed_at     TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_opportunities_tenant ON opportunities(tenant_id);
CREATE INDEX IF NOT EXISTS idx_opportunity_stage_history_opp ON opportunity_stage_history(opportunity_id);
