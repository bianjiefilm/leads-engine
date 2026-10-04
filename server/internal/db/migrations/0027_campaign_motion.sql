-- HUI-2747 获客侧活动动效交出。
-- export_json 只有允许带出的活动事实和 Motion 引用。
-- 转化在 local_conversion_json，不写进 export_json，也不写创作工程。
-- 本表不表示成片已生成或已送出。model_calls 与 creative_project_writes 恒为 0。

CREATE TABLE IF NOT EXISTS campaign_motion_handoffs (
    id                        TEXT PRIMARY KEY,
    tenant_id                 TEXT NOT NULL REFERENCES tenants(id),
    campaign_id               TEXT NOT NULL,
    export_json               TEXT NOT NULL,
    local_conversion_json     TEXT NOT NULL,
    model_calls               INTEGER NOT NULL DEFAULT 0 CHECK (model_calls = 0),
    creative_project_writes   INTEGER NOT NULL DEFAULT 0 CHECK (creative_project_writes = 0),
    piece_generated           INTEGER NOT NULL DEFAULT 0 CHECK (piece_generated = 0),
    piece_sent                INTEGER NOT NULL DEFAULT 0 CHECK (piece_sent = 0),
    updated_by                TEXT NOT NULL,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id)
);
