-- Touch 留资事实按 trace_id + 整数 source_version 幂等。
-- payload_json 只存引用和展示文案，不存联系方式。
-- brand_display_name 留在 payload_json，不写租户。grant_ref 只是引用。

ALTER TABLE notify_inbox ADD COLUMN trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_inbox ADD COLUMN payload_json TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_notify_inbox_trace_version
    ON notify_inbox(source_app, source_ns, source_tenant_id, trace_id, source_version)
    WHERE trace_id <> '';
