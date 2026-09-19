-- HUI-1749 / 平台生态 L1 适用商机转创意服务需求:受限交接发送端台账。
-- 一行 = 一次用户显式确认的服务需求交接快照(机会 × 内容指纹定位):
--   - 幂等:UNIQUE(opportunity_id, fingerprint) 保证同内容同快照同 handoff_id;
--     doc_json 存精确线字节,重试永远原样重发(同键同内容 → 接收端 200 duplicate)。
--   - 版本:source_version 每商机单调递增;新内容 = 新确认 = 新快照新 handoff_id;
--     旧版本只能显式拒绝(superseded),绝不静默重发。
--   - 投影:只存接单返回的受限状态(draft_ref/target_status/dirty/revoked);
--     投递成功 ≠ 成交/已支付,商机阶段与金额永不因交接而变。
--   - 撤销:local_status=revoked 为终态;已接受(接单侧离开 draft)后撤销被
--     显式拒绝并引导走接单侧变更流程。
CREATE TABLE IF NOT EXISTS opportunity_handoffs (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    opportunity_id   TEXT NOT NULL REFERENCES opportunities(id),
    source_version   INTEGER NOT NULL CHECK (source_version >= 1),
    fingerprint      TEXT NOT NULL,
    handoff_id       TEXT NOT NULL UNIQUE,
    doc_json         TEXT NOT NULL,
    doc_sha256       TEXT NOT NULL,
    source_revision  TEXT NOT NULL,
    principal_id     TEXT NOT NULL,
    actor_issuer     TEXT NOT NULL,
    actor_subject    TEXT NOT NULL,
    target_app       TEXT NOT NULL,
    return_target_id TEXT NOT NULL,
    local_status     TEXT NOT NULL CHECK (local_status IN ('confirmed','delivered','delivery_failed','revoked')),
    draft_ref        TEXT NOT NULL DEFAULT '',
    target_status    TEXT NOT NULL DEFAULT '',
    target_dirty     INTEGER NOT NULL DEFAULT 0 CHECK (target_dirty IN (0,1)),
    target_revoked   INTEGER NOT NULL DEFAULT 0 CHECK (target_revoked IN (0,1)),
    last_http_status INTEGER,
    last_error       TEXT NOT NULL DEFAULT '',
    confirmed_by     TEXT NOT NULL REFERENCES members(id),
    confirmed_at     TEXT NOT NULL,
    delivered_at     TEXT,
    revoked_at       TEXT,
    updated_at       TEXT NOT NULL,
    UNIQUE (opportunity_id, fingerprint),
    UNIQUE (opportunity_id, source_version)
);

CREATE INDEX IF NOT EXISTS idx_opportunity_handoffs_opp ON opportunity_handoffs(opportunity_id, source_version);
