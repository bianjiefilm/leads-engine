-- HUI-1687 外呼安全门。生产自动外呼保持关闭，本进程没有真实线路。
-- 隔离演练必须 simulation=1。拨打成功、真实接通、空号检测和实扣费用都不能落成 1。
-- 公共事件只存任务事实，不存录音和个人信息。

CREATE TABLE IF NOT EXISTS outbound_policy (
    tenant_id        TEXT PRIMARY KEY REFERENCES tenants(id),
    production_auto  INTEGER NOT NULL DEFAULT 0 CHECK (production_auto = 0),
    global_stop      INTEGER NOT NULL DEFAULT 0 CHECK (global_stop IN (0,1)),
    window_start     INTEGER NOT NULL,
    window_end       INTEGER NOT NULL,
    updated_by       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbound_stops (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    contact_id  TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL CHECK (kind IN ('unsubscribe','suppression','reject','global_stop')),
    active      INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_outbound_stops_tenant ON outbound_stops(tenant_id, contact_id, active);

CREATE TABLE IF NOT EXISTS outbound_tasks (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    contact_id    TEXT NOT NULL REFERENCES contacts(id),
    task_key      TEXT NOT NULL,
    campaign_id   TEXT NOT NULL DEFAULT '',
    consent_id    TEXT NOT NULL DEFAULT '',
    session_id    TEXT NOT NULL DEFAULT '',
    mode          TEXT NOT NULL CHECK (mode IN ('production','isolation')),
    simulation    INTEGER NOT NULL CHECK (simulation IN (0,1)),
    state         TEXT NOT NULL CHECK (state IN ('open','cancelled','transferred','blocked')),
    operator_id   TEXT NOT NULL REFERENCES members(id),
    budget_cents  INTEGER,
    created_at    TEXT NOT NULL,
    CHECK (mode != 'isolation' OR simulation = 1),
    UNIQUE (tenant_id, task_key)
);

CREATE INDEX IF NOT EXISTS idx_outbound_tasks_tenant ON outbound_tasks(tenant_id, contact_id, created_at);

CREATE TABLE IF NOT EXISTS outbound_receipts (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id),
    task_id                 TEXT NOT NULL REFERENCES outbound_tasks(id),
    kind                    TEXT NOT NULL CHECK (kind IN ('dial_submission','ringing','connected','call_completed','intent_suggestion','cancel','human_transfer')),
    status                  TEXT NOT NULL CHECK (status IN ('blocked','simulated','provider_ack','recorded')),
    simulation              INTEGER NOT NULL CHECK (simulation IN (0,1)),
    real_connected          INTEGER NOT NULL DEFAULT 0 CHECK (real_connected = 0),
    dial_succeeded          INTEGER NOT NULL DEFAULT 0 CHECK (dial_succeeded = 0),
    empty_number_detected   INTEGER NOT NULL DEFAULT 0 CHECK (empty_number_detected = 0),
    provider_http           INTEGER NOT NULL DEFAULT 0,
    refusal                 TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_outbound_receipts_task ON outbound_receipts(task_id, created_at);

CREATE TABLE IF NOT EXISTS outbound_public_events (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    task_id    TEXT NOT NULL REFERENCES outbound_tasks(id),
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_outbound_events_task ON outbound_public_events(task_id, created_at);

CREATE TABLE IF NOT EXISTS outbound_recordings (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    task_id    TEXT NOT NULL REFERENCES outbound_tasks(id),
    content    TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbound_usage (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    task_id      TEXT NOT NULL REFERENCES outbound_tasks(id),
    cost_known   INTEGER NOT NULL DEFAULT 0 CHECK (cost_known = 0),
    cost_cents   INTEGER,
    live_charge  INTEGER NOT NULL DEFAULT 0 CHECK (live_charge = 0),
    created_at   TEXT NOT NULL
);
