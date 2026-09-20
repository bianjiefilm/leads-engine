// HUI-1686 / FEAT-0187 迁移测试:
//   - 0007 重建 leads(status 域新增 'filtered' + 新增 filter_reason 列),
//     存量行与既有 status 域行为保持不变;
//   - 重建在「存量数据 + lead_intake_events 外键子行在场」时必须安全
//     (defer_foreign_keys:外键检查推迟到提交,重建后父子行原样保留),
//     这里以真实迁移文件体重放验证生产路径。
package db

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration0007LeadFilterDomain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,created_by,created_at,updated_at)
		VALUES('con_1','tnt_1','甲','13900000001','','merchant_customer','form','pending','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// 新域:filtered + 机器原因码可写。
	if _, err := d.Exec(`INSERT INTO leads(id,tenant_id,contact_id,status,filter_reason,created_by,created_at,updated_at)
		VALUES('lead_f','tnt_1','con_1','filtered','invalid_phone','intake:touch','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("filtered status insert: %v", err)
	}
	// 既有域:四个老值一个不少仍可写。
	for _, st := range []string{"new", "in_progress", "converted", "closed"} {
		if _, err := d.Exec(`INSERT INTO leads(id,tenant_id,contact_id,status,created_by,created_at,updated_at)
			VALUES(?, 'tnt_1','con_1',?,'cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, "lead_"+st, st); err != nil {
			t.Fatalf("legacy status %s insert: %v", st, err)
		}
	}
	// 越界值仍被 CHECK 拒绝(域只增不改)。
	if _, err := d.Exec(`INSERT INTO leads(id,tenant_id,contact_id,status,created_by,created_at,updated_at)
		VALUES('lead_x','tnt_1','con_1','bogus','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("bogus status must still be rejected by the CHECK domain")
	}
}

// 用生产迁移文件体重放 0007,证明「存量行 + 外键子行在场」的重建安全且数据原样。
func TestMigration0007RebuildPreservesRowsWithChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	// 存量:tenant/contact/lead + 子行(lead_intake_events 引用 leads)。
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,created_by,created_at,updated_at)
		VALUES('con_1','tnt_1','甲','13900000001','','merchant_customer','form','pending','intake:touch','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO leads(id,tenant_id,contact_id,status,created_by,created_at,updated_at)
		VALUES('lead_1','tnt_1','con_1','new','intake:touch','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO lead_intake_events(id,tenant_id,source_app,source_ns,event_id,content_sha256,phone_fpr,class,lead_id,contact_id,created_at)
		VALUES('iev_1','tnt_1','touch','wx','e1','deadbeef','fpr','new','lead_1','con_1','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// 第二张子表:form_submissions 也 REFERENCES leads(0006),必须同样幸存。
	if _, err := d.Exec(`INSERT INTO forms(id,tenant_id,version,status,created_at,updated_at)
		VALUES('form_1','tnt_1',1,'published','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO form_submissions(id,form_id,form_version,tenant_id,contact_id,lead_id,marketing_allowed,source,source_ref,payload_sha256,created_at)
		VALUES('fsub_1','form_1',1,'tnt_1','con_1','lead_1',1,'wx_post','camp-1','sha','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	body, err := fs.ReadFile(migrationsFS, "migrations/0007_lead_filter.sql")
	if err != nil {
		t.Fatalf("read migration body: %v", err)
	}
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(string(body)); err != nil {
		tx.Rollback()
		t.Fatalf("rebuild with populated parent/child rows must succeed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit rebuild: %v (外键约束必须在提交时成立)", err)
	}

	// 数据原样:lead 与子行都在,列值未漂移。
	var status string
	if err := d.QueryRow(`SELECT status FROM leads WHERE id='lead_1'`).Scan(&status); err != nil {
		t.Fatalf("lead survived: %v", err)
	}
	if status != "new" {
		t.Fatalf("legacy status drifted to %q", status)
	}
	var events int
	if err := d.QueryRow(`SELECT COUNT(1) FROM lead_intake_events WHERE lead_id='lead_1' AND class='new'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("intake event child rows = %d err=%v, want 1", events, err)
	}
	var submissions int
	if err := d.QueryRow(`SELECT COUNT(1) FROM form_submissions WHERE lead_id='lead_1' AND id='fsub_1'`).Scan(&submissions); err != nil || submissions != 1 {
		t.Fatalf("form submission child rows = %d err=%v, want 1", submissions, err)
	}
	// 暂存备份表不留残骸。
	var backups int
	if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name LIKE '%hui1686_backup%'`).Scan(&backups); err != nil || backups != 0 {
		t.Fatalf("leftover backup tables = %d err=%v, want 0", backups, err)
	}
	// 新域可用。
	if _, err := d.Exec(`INSERT INTO leads(id,tenant_id,contact_id,status,filter_reason,created_by,created_at,updated_at)
		VALUES('lead_2','tnt_1','con_1','filtered','invalid_email','intake:touch','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("filtered insert after rebuild: %v", err)
	}
	// 老索引仍在。
	var idx int
	if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='index' AND name='idx_leads_tenant'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("idx_leads_tenant present = %d err=%v, want 1", idx, err)
	}
	// 旧表不应残留。
	var leftover int
	if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name LIKE 'leads_%'`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("leftover rebuild tables = %d err=%v, want 0", leftover, err)
	}
	if strings.Contains(strings.ToLower(string(body)), "update leads set") {
		t.Fatal("migration must not rewrite existing rows in place (additive rebuild only)")
	}
}
