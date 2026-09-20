// HUI-1690 / FEAT-0191 迁移测试:contact_tag_defs + contact_tag_links(additive 两新表)。
//   - 标签定义:名称租户内唯一(UNIQUE 兜底)、颜色/描述可空默认、created_by 外键;
//   - 打标关联:幂等唯一键 (tenant, tag, contact)、applied_by/applied_at 留痕必填;
//   - 外键红线:坏 tenant/tag/contact/member 引用必须被拒(fail-closed);
//   - 索引在位;纯 additive:绝不重建既有父表(0007 重建舞步注释对齐)。
package db

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration0010ContactTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	base := func(t *testing.T) {
		t.Helper()
		if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
			VALUES('mem_1','tnt_1','usr_1','owner',1,'O','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,created_by,created_at,updated_at)
			VALUES('con_1','tnt_1','甲','13900000001','','merchant_customer','form','pending','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("tag def with empty color/description defaults", func(t *testing.T) {
		base(t)
		if _, err := d.Exec(`INSERT INTO contact_tag_defs(id,tenant_id,name,created_by,created_at,updated_at)
			VALUES('ctd_1','tnt_1','高意向','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("insert tag def: %v", err)
		}
		var color, desc string
		if err := d.QueryRow(`SELECT color,description FROM contact_tag_defs WHERE id='ctd_1'`).Scan(&color, &desc); err != nil {
			t.Fatal(err)
		}
		if color != "" || desc != "" {
			t.Fatalf("color/description default = %q/%q, want empty", color, desc)
		}
	})

	t.Run("tag name is unique within tenant", func(t *testing.T) {
		if _, err := d.Exec(`INSERT INTO contact_tag_defs(id,tenant_id,name,created_by,created_at,updated_at)
			VALUES('ctd_dup','tnt_1','高意向','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
			t.Fatal("duplicate tag name within tenant must be rejected by UNIQUE")
		}
	})

	t.Run("link idempotent unique key and mandatory audit fields", func(t *testing.T) {
		if _, err := d.Exec(`INSERT INTO contact_tag_links(id,tenant_id,tag_id,contact_id,applied_by,applied_at)
			VALUES('ctl_1','tnt_1','ctd_1','con_1','mem_1','2026-01-02T00:00:00Z')`); err != nil {
			t.Fatalf("insert link: %v", err)
		}
		if _, err := d.Exec(`INSERT INTO contact_tag_links(id,tenant_id,tag_id,contact_id,applied_by,applied_at)
			VALUES('ctl_dup','tnt_1','ctd_1','con_1','mem_1','2026-01-03T00:00:00Z')`); err == nil {
			t.Fatal("duplicate (tenant, tag, contact) link must be rejected by UNIQUE (幂等)")
		}
		if _, err := d.Exec(`INSERT INTO contact_tag_links(id,tenant_id,tag_id,contact_id,applied_by,applied_at)
			VALUES('ctl_x','tnt_1','ctd_1','con_1',NULL,'2026-01-03T00:00:00Z')`); err == nil {
			t.Fatal("applied_by is the tagging audit anchor; NULL must be rejected")
		}
	})

	t.Run("foreign keys are enforced (fail-closed)", func(t *testing.T) {
		if _, err := d.Exec(`INSERT INTO contact_tag_defs(id,tenant_id,name,created_by,created_at,updated_at)
			VALUES('ctd_x','tnt_ghost','坏租户','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
			t.Fatal("bad tenant_id must be rejected by FK")
		}
		if _, err := d.Exec(`INSERT INTO contact_tag_defs(id,tenant_id,name,created_by,created_at,updated_at)
			VALUES('ctd_x','tnt_1','坏成员','mem_ghost','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
			t.Fatal("bad created_by must be rejected by FK")
		}
		if _, err := d.Exec(`INSERT INTO contact_tag_links(id,tenant_id,tag_id,contact_id,applied_by,applied_at)
			VALUES('ctl_x','tnt_1','ctd_ghost','con_1','mem_1','2026-01-03T00:00:00Z')`); err == nil {
			t.Fatal("bad tag_id must be rejected by FK")
		}
		if _, err := d.Exec(`INSERT INTO contact_tag_links(id,tenant_id,tag_id,contact_id,applied_by,applied_at)
			VALUES('ctl_x','tnt_1','ctd_1','con_ghost','mem_1','2026-01-03T00:00:00Z')`); err == nil {
			t.Fatal("bad contact_id must be rejected by FK")
		}
	})

	t.Run("indexes present; migration body is purely additive", func(t *testing.T) {
		for _, idx := range []string{"idx_contact_tag_defs_tenant", "idx_contact_tag_links_tag", "idx_contact_tag_links_contact"} {
			var n int
			if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil || n != 1 {
				t.Fatalf("index %s present = %d err=%v, want 1", idx, n, err)
			}
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/0010_contact_tags.sql")
		if err != nil {
			t.Fatalf("read migration body: %v", err)
		}
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "drop table") || strings.Contains(lower, "alter table") {
			t.Fatal("0010 must be purely additive (no rebuild of existing parents)")
		}
		if !strings.Contains(string(body), "0007") {
			t.Fatal("0010 must document its relationship to the 0007 rebuild dance")
		}
	})
}
