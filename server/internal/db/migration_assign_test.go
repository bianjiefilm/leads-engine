// HUI-1685 / FEAT-0186 迁移测试:0009 新增 lead_assign_pool(纯 additive,
// 不重建任何既有表):
//   - 表存在且列形状符合设计(weight 默认 1、current_weight 游标默认 0、
//     region/industry 默认空串);
//   - weight CHECK 域 1..1000:越界拒绝;
//   - member_id 唯一:同成员重复入池拒绝;
//   - 外键指向 tenants/members(不 REFERENCES leads/contacts,故不进入 0007
//     的暂存/回插舞步 —— 与 0008 follow_ups 同款说明)。
package db

import (
	"path/filepath"
	"testing"
)

func TestMigration0009AssignPoolShape(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		VALUES('mem_1','tnt_1','usr_1','sales',1,'S1','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		VALUES('mem_2','tnt_1','usr_2','sales',1,'S2','cli','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	// 默认值:weight=1、current_weight=0、region/industry 空串。
	if _, err := d.Exec(`INSERT INTO lead_assign_pool(id,tenant_id,member_id,created_by,created_at,updated_at)
		VALUES('pap_1','tnt_1','mem_1','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("default insert: %v", err)
	}
	var weight, current int
	var region, industry string
	if err := d.QueryRow(`SELECT weight,current_weight,region,industry FROM lead_assign_pool WHERE id='pap_1'`).
		Scan(&weight, &current, &region, &industry); err != nil {
		t.Fatal(err)
	}
	if weight != 1 || current != 0 || region != "" || industry != "" {
		t.Fatalf("defaults = w%d c%d r%q i%q, want 1/0/''/''", weight, current, region, industry)
	}

	// weight CHECK 域:0 与 1001 拒绝,1000 可写。
	for _, w := range []int{0, 1001} {
		if _, err := d.Exec(`INSERT INTO lead_assign_pool(id,tenant_id,member_id,weight,created_by,created_at,updated_at)
			VALUES(?, 'tnt_1','mem_2',?,'mem_2','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, "pap_bad_"+string(rune('a'+w)), w); err == nil {
			t.Fatalf("weight %d must be rejected by the CHECK domain", w)
		}
	}
	if _, err := d.Exec(`INSERT INTO lead_assign_pool(id,tenant_id,member_id,weight,created_by,created_at,updated_at)
		VALUES('pap_1000','tnt_1','mem_2',1000,'mem_2','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("weight 1000 insert: %v", err)
	}

	// member_id 唯一:同一成员(即便换租户语境)不可第二行。
	if _, err := d.Exec(`INSERT INTO lead_assign_pool(id,tenant_id,member_id,created_by,created_at,updated_at)
		VALUES('pap_dup','tnt_1','mem_1','mem_1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("duplicate member_id must be rejected by UNIQUE")
	}
}
