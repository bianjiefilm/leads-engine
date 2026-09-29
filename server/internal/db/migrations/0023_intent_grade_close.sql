-- HUI-1684 收口：意向模型尝试与规则快照分开。
-- 这台机器没有模型凭证。billing_verdict 只能是 not_completed。
-- PASS、recorded、任务号、费用和结论文本都写不进去。表上没有级别列。

CREATE TABLE IF NOT EXISTS intent_model_attempts (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    subject_kind    TEXT NOT NULL CHECK (subject_kind IN ('lead','session')),
    subject_id      TEXT NOT NULL,
    attempt_state   TEXT NOT NULL CHECK (attempt_state IN ('missing_credentials','unknown','recovered')),
    billing_verdict TEXT NOT NULL CHECK (billing_verdict = 'not_completed'),
    task_id         TEXT NOT NULL DEFAULT '' CHECK (task_id = ''),
    cost_cents      INTEGER NOT NULL DEFAULT 0 CHECK (cost_cents = 0),
    conclusion      TEXT NOT NULL DEFAULT '' CHECK (conclusion = ''),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (tenant_id, subject_kind, subject_id)
);
