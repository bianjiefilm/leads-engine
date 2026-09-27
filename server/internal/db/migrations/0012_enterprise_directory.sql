-- HUI-1678 / FEAT-0179 企业资料筛选。
-- 行只属于本租户。没有跨商家共享企业库。
-- 自然人联系方式与企业筛选字段分开存放。
-- 拒绝/删除写独立抑制表：同一企业再次导入不得清除抑制，也不得因此获得营销许可。

CREATE TABLE IF NOT EXISTS enterprise_imports (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    source_key         TEXT NOT NULL,
    source_name        TEXT NOT NULL,
    collected_at       TEXT NOT NULL,
    update_cycle_days  INTEGER NOT NULL CHECK (update_cycle_days > 0),
    license            TEXT NOT NULL,
    correction         TEXT NOT NULL,
    created_by         TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    UNIQUE (tenant_id, source_key)
);

CREATE TABLE IF NOT EXISTS enterprise_records (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    import_id       TEXT NOT NULL REFERENCES enterprise_imports(id),
    source_key      TEXT NOT NULL,
    enterprise_id   TEXT NOT NULL,
    enterprise_name TEXT NOT NULL,
    industry        TEXT NOT NULL DEFAULT '',
    region          TEXT NOT NULL DEFAULT '',
    scale           TEXT NOT NULL DEFAULT '',
    person_name     TEXT NOT NULL DEFAULT '',
    person_phone    TEXT NOT NULL DEFAULT '',
    person_email    TEXT NOT NULL DEFAULT '',
    collected_at    TEXT NOT NULL,
    update_cycle_days INTEGER NOT NULL CHECK (update_cycle_days > 0),
    content_sha     TEXT NOT NULL,
    confirmed_sha   TEXT NOT NULL DEFAULT '',
    conflict        INTEGER NOT NULL DEFAULT 0 CHECK (conflict IN (0,1)),
    status          TEXT NOT NULL CHECK (status IN ('draft','confirmed','refused','deleted')),
    lead_id         TEXT,
    contact_id      TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (tenant_id, source_key, enterprise_id)
);

CREATE TABLE IF NOT EXISTS enterprise_suppressions (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    source_key    TEXT NOT NULL,
    enterprise_id TEXT NOT NULL,
    reason        TEXT NOT NULL CHECK (reason IN ('refused','deleted')),
    created_by    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (tenant_id, source_key, enterprise_id)
);

CREATE INDEX IF NOT EXISTS idx_enterprise_records_screen
    ON enterprise_records(tenant_id, industry, region, scale);
