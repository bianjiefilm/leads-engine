-- HUI-1684 意向分级。规则快照与人工修正，租户作用域。
-- calibrated 与 live_charge 恒为 0：没有校准模型，重算不向真实客户扣费。

CREATE TABLE IF NOT EXISTS intent_grade_snapshots (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    subject_kind       TEXT NOT NULL CHECK (subject_kind IN ('lead','session')),
    subject_id         TEXT NOT NULL,
    grade              TEXT NOT NULL CHECK (grade IN ('high','medium','low','insufficient')),
    reason             TEXT NOT NULL,
    citations_json     TEXT NOT NULL,
    missing_json       TEXT NOT NULL,
    suggestion_json    TEXT NOT NULL,
    rule_version       TEXT NOT NULL,
    model_version      TEXT NOT NULL,
    calibrated         INTEGER NOT NULL DEFAULT 0 CHECK (calibrated = 0),
    assessed_at        TEXT NOT NULL,
    fresh_until        TEXT NOT NULL,
    stale              INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0,1)),
    stale_reason       TEXT NOT NULL DEFAULT '',
    human_locked       INTEGER NOT NULL DEFAULT 0 CHECK (human_locked IN (0,1)),
    human_disposition  TEXT NOT NULL DEFAULT '',
    human_reason       TEXT NOT NULL DEFAULT '',
    misjudgment        INTEGER NOT NULL DEFAULT 0 CHECK (misjudgment IN (0,1)),
    facts_json         TEXT NOT NULL DEFAULT '[]',
    disclaimer         TEXT NOT NULL,
    fingerprint        TEXT NOT NULL,
    evidence_json      TEXT NOT NULL DEFAULT '[]',
    created_by         TEXT NOT NULL,
    created_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_intent_grade_subject
    ON intent_grade_snapshots(tenant_id, subject_kind, subject_id, created_at);

CREATE TABLE IF NOT EXISTS intent_grade_usage (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    subject_kind  TEXT NOT NULL,
    subject_id    TEXT NOT NULL,
    units         INTEGER NOT NULL,
    live_charge   INTEGER NOT NULL DEFAULT 0 CHECK (live_charge = 0),
    created_at    TEXT NOT NULL,
    UNIQUE (tenant_id, subject_kind, subject_id)
);
