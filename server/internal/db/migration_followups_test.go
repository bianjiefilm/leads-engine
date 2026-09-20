// HUI-1692 / FEAT-0193 迁移测试:follow_ups additive 新表。
//   - 全部字段域:contact 必填外键、lead 可空外键、next_follow_up_at / completed_at
//     可空、created_by 外键、时间戳;
//   - 外键红线:坏 contact / lead / member 引用必须被拒(fail-closed);
//   - 到期查询索引在位;
//   - 纯 additive:绝不重建既有父表(0007 重建舞步的「后续新增 REFERENCES 的表」
//     注释在此对齐——follow_ups 属新增子表,若未来重建 leads/contacts 须一并覆盖)。
package db

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration0008FollowUpsDomain(t *testing.T) {
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
			VALUES('mem_1','tnt_1','usr_1','sales',1,'S','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,created_by,created_at,updated_at)
			VALUES('con_1','tnt_1','甲','13900000001','','merchant_customer','form','pending','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO leads(id,tenant_id,contact_id,status,created_by,created_at,updated_at)
			VALUES('lead_1','tnt_1','con_1','new','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("full row with lead reference and both timestamps", func(t *testing.T) {
		base(t)
		if _, err := d.Exec(`INSERT INTO follow_ups(id,tenant_id,contact_id,lead_id,note,next_follow_up_at,completed_at,created_by,created_at,updated_at)
			VALUES('fup_1','tnt_1','con_1','lead_1','推进','2026-09-21T10:00:00Z','2026-09-20T09:00:00Z','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("full insert: %v", err)
		}
	})

	t.Run("next_follow_up_at and completed_at are nullable", func(t *testing.T) {
		if _, err := d.Exec(`INSERT INTO follow_ups(id,tenant_id,contact_id,note,created_by,created_at,updated_at)
			VALUES('fup_2','tnt_1','con_1','纯记录','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("nullable insert: %v", err)
		}
	})

	t.Run("foreign keys are enforced (fail-closed)", func(t *testing.T) {
		if _, err := d.Exec(`INSERT INTO follow_ups(id,tenant_id,contact_id,note,created_by,created_at,updated_at)
			VALUES('fup_x','tnt_1','con_ghost','坏引用','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
			t.Fatal("bad contact_id must be rejected by FK")
		}
		if _, err := d.Exec(`INSERT INTO follow_ups(id,tenant_id,contact_id,lead_id,note,created_by,created_at,updated_at)
			VALUES('fup_x','tnt_1','con_1','lead_ghost','坏引用','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
			t.Fatal("bad lead_id must be rejected by FK")
		}
		if _, err := d.Exec(`INSERT INTO follow_ups(id,tenant_id,contact_id,note,created_by,created_at,updated_at)
			VALUES('fup_x','tnt_1','con_1','坏引用','mem_ghost','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
			t.Fatal("bad created_by must be rejected by FK")
		}
	})

	t.Run("due-surface index present; migration body is purely additive", func(t *testing.T) {
		var idx int
		if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='index' AND name='idx_follow_ups_due'`).Scan(&idx); err != nil || idx != 1 {
			t.Fatalf("idx_follow_ups_due present = %d err=%v, want 1", idx, err)
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/0008_follow_ups.sql")
		if err != nil {
			t.Fatalf("read migration body: %v", err)
		}
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "drop table") || strings.Contains(lower, "alter table") {
			t.Fatal("0008 must be purely additive (no rebuild of existing parents)")
		}
		// 0007 重建舞步的关系必须显式说明(后续新增 REFERENCES 的表须同步覆盖)。
		if !strings.Contains(string(body), "0007") {
			t.Fatal("0008 must document its relationship to the 0007 rebuild dance")
		}
	})
}
