// Package provision holds bootstrap/ops provisioning used by the CLI
// subcommands and by tests. It writes membership rows directly; principal refs
// must still be platform principals (usr_*), never emails or phones.
package provision

import (
	"fmt"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/db"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func checkPrincipal(principal string) error {
	if !strings.HasPrefix(principal, "usr_") || len(principal) <= len("usr_") {
		return fmt.Errorf("principal must be a platform principal ref (usr_*); never an email or phone")
	}
	return nil
}

// Tenant creates a tenant and returns its id.
func Tenant(dbPath, name string) (string, error) {
	d, err := db.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer d.Close()
	t, err := store.New(d).CreateTenant(name)
	if err != nil {
		return "", err
	}
	return t.ID, nil
}

// Member adds a member to a tenant and returns the member id.
func Member(dbPath, tenantID, principal, role, displayName string, enabled bool) (string, error) {
	if err := checkPrincipal(principal); err != nil {
		return "", err
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer d.Close()
	m, err := store.New(d).CreateMember(tenantID, principal, role, displayName, "cli-bootstrap", enabled)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}

// Grant adds a per-tenant agent grant and returns the grant id.
func Grant(dbPath, tenantID, principal string) (string, error) {
	if err := checkPrincipal(principal); err != nil {
		return "", err
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer d.Close()
	g, err := store.New(d).CreateAgentGrant(tenantID, principal, "cli-bootstrap")
	if err != nil {
		return "", err
	}
	return g.ID, nil
}
