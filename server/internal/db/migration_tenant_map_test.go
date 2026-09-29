package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration0021TenantMap(t *testing.T) {
	body, err := os.ReadFile("migrations/0021_tenant_map.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := strings.ToLower(string(body))
	if !strings.Contains(sqlText, "legacy_stored_tenant") {
		t.Fatal("backfill must mark older rows legacy_stored_tenant")
	}
	if strings.Contains(sqlText, "update leads") || strings.Contains(sqlText, "drop table") {
		t.Fatal("migration must not rewrite or drop leads")
	}

	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	cols := map[string]bool{}
	rows, err := d.Query(`PRAGMA table_info(notify_inbox)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, name := range []string{"source_ns", "source_tenant_id", "map_target_tenant_id", "map_version", "map_basis"} {
		if !cols[name] {
			t.Fatalf("notify_inbox missing %s", name)
		}
	}
	var indexSQL string
	if err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE name='idx_notify_inbox_source_event'`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(indexSQL), "UNIQUE") {
		t.Fatalf("source event index = %s", indexSQL)
	}

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_old','Old','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO notify_inbox(
		id,tenant_id,source_app,event_type,source_ref,source_version,profile_event_id,body_sha256,receipt_json,created_at,updated_at)
		VALUES('nin_old','tnt_old','touch-engine','lead.authorized_submitted','sub_old',1,'evt_old','abc','{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE notify_inbox SET
		source_ns = CASE WHEN source_ns = '' THEN 'notify' ELSE source_ns END,
		source_tenant_id = CASE WHEN source_tenant_id = '' THEN tenant_id ELSE source_tenant_id END,
		map_target_tenant_id = CASE WHEN map_target_tenant_id = '' THEN tenant_id ELSE map_target_tenant_id END,
		map_basis = CASE WHEN map_basis = '' THEN 'legacy_stored_tenant' ELSE map_basis END`); err != nil {
		t.Fatal(err)
	}
	var source, target, basis string
	var version int
	if err := d.QueryRow(`SELECT source_tenant_id, map_target_tenant_id, map_version, map_basis FROM notify_inbox WHERE id='nin_old'`).Scan(&source, &target, &version, &basis); err != nil {
		t.Fatal(err)
	}
	if source != "tnt_old" || target != "tnt_old" || version != 0 || basis != "legacy_stored_tenant" {
		t.Fatalf("backfill source=%s target=%s version=%d basis=%s", source, target, version, basis)
	}
	if _, err := d.Exec(`INSERT INTO notify_inbox(
		id,tenant_id,source_app,event_type,source_ref,source_version,profile_event_id,body_sha256,receipt_json,created_at,updated_at,
		source_ns,source_tenant_id,map_target_tenant_id,map_version,map_basis)
		VALUES('nin_dup','tnt_old','touch-engine','lead.authorized_submitted','sub_other',1,'evt_old','abc','{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',
		'notify','tnt_old','tnt_old',0,'legacy_stored_tenant')`); err == nil {
		t.Fatal("duplicate source event must be rejected")
	}
}
