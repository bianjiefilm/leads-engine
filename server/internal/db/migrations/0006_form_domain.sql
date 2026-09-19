-- HUI-1679 / FEAT-0180 版本化留资表单域(全部 additive,不改既有列)。
-- 首版收敛:固定字段留资表单 + 版本化 schema;不做自由字段/拖拽搭建平台。
-- 表单领域配置与语义归获客;碰一碰(T1/HUI-1747)只复用 schema 导出端点的
-- 固定渲染/校验契约,不建第二套通用表单系统。
--
-- forms:每行 = 一个版本。form_key 是 (tenant_id, store_id) 内的表单族键,
--   version 在族内递增;发布后行不可变(改配置 = 同族新版本行)。
--   status: draft(草稿,公共不可见) / published(已发布) / disabled(停用) /
--   expired(保留枚举;功能性过期由 expires_at 判定,提交侧按 410 处理)。
--   fields 只接受固定枚举 {name, phone, wechat} 的白名单 JSON(服务端校验),
--   store_id 是可空门店归属占位(''=租户级;L0 无 stores 表,故不设外键)。
--
-- form_submissions:公共提交流水与幂等账本。幂等键 =
--   (接收租户 tenant_id, 表单 form_id, 来源 source, 来源引用 source_ref)。
--   同键重试幂等返回原 submission,零写入。tenant_id 恒等于表单归属租户:
--   接收租户由表单决定,URL/请求参数不携带也不生效。
--   payload 原文不入库,只有 payload_sha256(内容审计),联系方式只落
--   contacts/contact_consents 各自的域表;真实咨询次数语义归 HUI-1683 intake
--   (提交内部走 IntakeLeadInTx 三分类,repeat_consult 次数不丢)。

CREATE TABLE IF NOT EXISTS forms (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    store_id         TEXT NOT NULL DEFAULT '',
    form_key         TEXT NOT NULL DEFAULT 'main',
    version          INTEGER NOT NULL CHECK (version >= 1),
    notice_version   TEXT NOT NULL DEFAULT '',
    purpose          TEXT NOT NULL DEFAULT '',
    marketing_prompt TEXT NOT NULL DEFAULT '',
    fields           TEXT NOT NULL DEFAULT '{}',
    status           TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','disabled','expired')),
    expires_at       TEXT,
    published_at     TEXT,
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE (tenant_id, store_id, form_key, version)
);

CREATE TABLE IF NOT EXISTS form_submissions (
    id                TEXT PRIMARY KEY,
    form_id           TEXT NOT NULL REFERENCES forms(id),
    form_version      INTEGER NOT NULL,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    contact_id        TEXT NOT NULL REFERENCES contacts(id),
    lead_id           TEXT NOT NULL REFERENCES leads(id),
    consent_id        TEXT NOT NULL DEFAULT '',
    marketing_allowed INTEGER NOT NULL DEFAULT 0 CHECK (marketing_allowed IN (0,1)),
    source            TEXT NOT NULL DEFAULT '',
    source_ref        TEXT NOT NULL DEFAULT '',
    payload_sha256    TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    UNIQUE (tenant_id, form_id, source, source_ref)
);

CREATE INDEX IF NOT EXISTS idx_forms_tenant_status ON forms(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_form_submissions_form ON form_submissions(tenant_id, form_id, created_at);
CREATE INDEX IF NOT EXISTS idx_form_submissions_contact ON form_submissions(contact_id);
