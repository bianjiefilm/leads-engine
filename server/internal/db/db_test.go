package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsCreateDomainRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	tables, err := TableNames(d)
	if err != nil {
		t.Fatalf("TableNames: %v", err)
	}
	want := []string{"agent_grants", "contacts", "leads", "members", "opportunities", "schema_migrations", "source_refs", "tenants"}
	have := map[string]bool{}
	for _, tb := range tables {
		have[tb] = true
	}
	for _, tb := range want {
		if !have[tb] {
			t.Errorf("missing table %s (have %v)", tb, tables)
		}
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Re-open: migrations must apply cleanly a second time.
	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	d.Close()
	d2.Close()
}

func TestMemberRoleCheckConstraint(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	_, err = d.Exec(
		`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		 VALUES('mem_1','tnt_1','usr_x','superadmin',1,'','cli','2026-01-01','2026-01-01')`)
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("role CHECK constraint must reject unknown roles, got %v", err)
	}
}
