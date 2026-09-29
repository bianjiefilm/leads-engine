-- HUI-1688 收口：协助事件，以及可选的平台任务回执。
-- billing_verdict 只有 not_completed 和 recorded。PASS 不能写入。
-- not_completed 不能带正成本。live_charge 仍不在这张表里。

CREATE TABLE reception_events_v2 (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    session_id TEXT NOT NULL REFERENCES reception_sessions(id),
    type       TEXT NOT NULL CHECK (type IN ('takeover','release','interruption','assist')),
    epoch      INTEGER NOT NULL,
    created_at TEXT NOT NULL
);

INSERT INTO reception_events_v2(id, tenant_id, session_id, type, epoch, created_at)
SELECT id, tenant_id, session_id, type, epoch, created_at FROM reception_events;

DROP TABLE reception_events;

ALTER TABLE reception_events_v2 RENAME TO reception_events;

CREATE TABLE IF NOT EXISTS reception_model_calls (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    reply_id         TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    task_id          TEXT NOT NULL DEFAULT '',
    cost_cents       INTEGER NOT NULL DEFAULT 0 CHECK (cost_cents >= 0),
    billing_verdict  TEXT NOT NULL CHECK (billing_verdict IN ('not_completed','recorded')),
    created_at       TEXT NOT NULL,
    UNIQUE (tenant_id, idempotency_key),
    CHECK (
        (billing_verdict = 'recorded' AND task_id <> '')
        OR (billing_verdict = 'not_completed' AND cost_cents = 0)
    )
);
