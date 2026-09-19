// Package store is the SQL layer. Every business query is tenant-scoped and
// fully parameterized; ids are random; timestamps are RFC3339 UTC.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// Store wraps the sqlite handle.
type Store struct{ DB *sql.DB }

func New(db *sql.DB) *Store { return &Store{DB: db} }

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: entropy unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ---- tenants -------------------------------------------------------------

type Tenant struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreateTenant(name string) (Tenant, error) {
	t := Tenant{ID: newID("tnt_"), Name: name, CreatedAt: now()}
	_, err := s.DB.Exec(`INSERT INTO tenants(id,name,created_at) VALUES(?,?,?)`, t.ID, t.Name, t.CreatedAt)
	return t, err
}

func (s *Store) GetTenant(id string) (Tenant, error) {
	var t Tenant
	err := s.DB.QueryRow(`SELECT id,name,created_at FROM tenants WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.CreatedAt)
	return t, err
}

// ---- members --------------------------------------------------------------

type Member struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PrincipalRef string `json:"principal_ref"`
	Role         string `json:"role"`
	Enabled      bool   `json:"enabled"`
	DisplayName  string `json:"display_name"`
	CreatedBy    string `json:"created_by"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// CreateMember inserts a tenant membership. principalRef MUST come from an
// identity-resolved principal (see httpapi admin handlers); callers that pass
// user input directly violate the identity discipline.
func (s *Store) CreateMember(tenantID, principalRef, role, displayName, createdBy string, enabled bool) (Member, error) {
	m := Member{
		ID: newID("mem_"), TenantID: tenantID, PrincipalRef: principalRef,
		Role: role, Enabled: enabled, DisplayName: displayName,
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.PrincipalRef, m.Role, boolInt(m.Enabled), m.DisplayName, m.CreatedBy, m.CreatedAt, m.UpdatedAt)
	return m, err
}

func (s *Store) GetMemberByPrincipal(tenantID, principalRef string) (Member, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at
		 FROM members WHERE tenant_id=? AND principal_ref=?`, tenantID, principalRef)
	return scanMember(row)
}

func (s *Store) GetMember(id string) (Member, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at FROM members WHERE id=?`, id)
	return scanMember(row)
}

func scanMember(row *sql.Row) (Member, error) {
	var m Member
	var enabled int
	err := row.Scan(&m.ID, &m.TenantID, &m.PrincipalRef, &m.Role, &enabled, &m.DisplayName, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return Member{}, err
	}
	m.Enabled = enabled == 1
	return m, nil
}

// UpdateMember changes role/enabled/display_name only. principal_ref is
// immutable by construction: there is no parameter for it here and the HTTP
// layer rejects any attempt to send one (identity discipline test).
func (s *Store) UpdateMember(id string, role *string, enabled *bool, displayName *string) (Member, error) {
	cur, err := s.GetMember(id)
	if err != nil {
		return Member{}, err
	}
	if role != nil {
		cur.Role = *role
	}
	if enabled != nil {
		cur.Enabled = *enabled
	}
	if displayName != nil {
		cur.DisplayName = *displayName
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE members SET role=?,enabled=?,display_name=?,updated_at=? WHERE id=?`,
		cur.Role, boolInt(cur.Enabled), cur.DisplayName, cur.UpdatedAt, cur.ID)
	return cur, err
}

func (s *Store) ListMembers(tenantID string) ([]Member, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at
		 FROM members WHERE tenant_id=? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var enabled int
		if err := rows.Scan(&m.ID, &m.TenantID, &m.PrincipalRef, &m.Role, &enabled, &m.DisplayName, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- agent grants ----------------------------------------------------------

type AgentGrant struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PrincipalRef string `json:"principal_ref"`
	GrantedBy    string `json:"granted_by"`
	CreatedAt    string `json:"created_at"`
}

func (s *Store) CreateAgentGrant(tenantID, principalRef, grantedBy string) (AgentGrant, error) {
	g := AgentGrant{ID: newID("agt_"), TenantID: tenantID, PrincipalRef: principalRef, GrantedBy: grantedBy, CreatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO agent_grants(id,tenant_id,principal_ref,granted_by,created_at) VALUES(?,?,?,?,?)`,
		g.ID, g.TenantID, g.PrincipalRef, g.GrantedBy, g.CreatedAt)
	return g, err
}

func (s *Store) GetAgentGrant(tenantID, principalRef string) (*AgentGrant, error) {
	var g AgentGrant
	err := s.DB.QueryRow(
		`SELECT id,tenant_id,principal_ref,granted_by,created_at FROM agent_grants WHERE tenant_id=? AND principal_ref=?`,
		tenantID, principalRef).Scan(&g.ID, &g.TenantID, &g.PrincipalRef, &g.GrantedBy, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) DeleteAgentGrant(id, tenantID string) error {
	res, err := s.DB.Exec(`DELETE FROM agent_grants WHERE id=? AND tenant_id=?`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListAgentGrants(tenantID string) ([]AgentGrant, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,principal_ref,granted_by,created_at FROM agent_grants WHERE tenant_id=? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentGrant
	for rows.Next() {
		var g AgentGrant
		if err := rows.Scan(&g.ID, &g.TenantID, &g.PrincipalRef, &g.GrantedBy, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---- source refs -----------------------------------------------------------

type SourceRef struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	SourceApp          string `json:"source_app"`
	SourceRef          string `json:"source_ref"`
	AuthScopeSnapshot  string `json:"auth_scope_snapshot"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          string `json:"created_at"`
}

func (s *Store) CreateSourceRef(tenantID, sourceApp, sourceRef, authScope, createdBy string) (SourceRef, error) {
	r := SourceRef{ID: newID("src_"), TenantID: tenantID, SourceApp: sourceApp, SourceRef: sourceRef,
		AuthScopeSnapshot: authScope, CreatedBy: createdBy, CreatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO source_refs(id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at) VALUES(?,?,?,?,?,?,?)`,
		r.ID, r.TenantID, r.SourceApp, r.SourceRef, r.AuthScopeSnapshot, r.CreatedBy, r.CreatedAt)
	return r, err
}

func (s *Store) GetSourceRef(id, tenantID string) (SourceRef, error) {
	var r SourceRef
	err := s.DB.QueryRow(
		`SELECT id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at
		 FROM source_refs WHERE id=? AND tenant_id=?`, id, tenantID).
		Scan(&r.ID, &r.TenantID, &r.SourceApp, &r.SourceRef, &r.AuthScopeSnapshot, &r.CreatedBy, &r.CreatedAt)
	return r, err
}

// ---- contacts / leads / opportunities --------------------------------------

type Contact struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	Name             string `json:"name"`
	Phone            string `json:"phone"`
	Email            string `json:"email"`
	BusinessCategory string `json:"business_category"`
	SourceType       string `json:"source_type"`
	ConsentStatus    string `json:"consent_status"`
	SourceRefID      string `json:"source_ref_id,omitempty"`
	AssignedMemberID string `json:"assigned_member_id,omitempty"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

const contactCols = `id,tenant_id,name,phone,email,business_category,source_type,consent_status,source_ref_id,assigned_member_id,created_by,created_at,updated_at`

func scanContact(sc interface{ Scan(...any) error }) (Contact, error) {
	var c Contact
	var src, asn sql.NullString
	err := sc.Scan(&c.ID, &c.TenantID, &c.Name, &c.Phone, &c.Email, &c.BusinessCategory, &c.SourceType,
		&c.ConsentStatus, &src, &asn, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Contact{}, err
	}
	c.SourceRefID, c.AssignedMemberID = src.String, asn.String
	return c, nil
}

func (s *Store) CreateContact(c Contact, createdBy, assignTo string) (Contact, error) {
	c.ID = newID("con_")
	c.CreatedBy = createdBy
	c.AssignedMemberID = assignTo
	c.CreatedAt, c.UpdatedAt = now(), now()
	_, err := s.DB.Exec(
		`INSERT INTO contacts(`+contactCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.TenantID, c.Name, c.Phone, c.Email, c.BusinessCategory, c.SourceType, c.ConsentStatus,
		nullable(c.SourceRefID), nullable(c.AssignedMemberID), c.CreatedBy, c.CreatedAt, c.UpdatedAt)
	return c, err
}

func (s *Store) GetContact(id, tenantID string) (Contact, error) {
	row := s.DB.QueryRow(`SELECT `+contactCols+` FROM contacts WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanContact(row)
}

func (s *Store) ListContacts(tenantID, assigneeFilter string) ([]Contact, error) {
	q := `SELECT ` + contactCols + ` FROM contacts WHERE tenant_id=?`
	args := []any{tenantID}
	if assigneeFilter != "" {
		q += ` AND assigned_member_id=?`
		args = append(args, assigneeFilter)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContactPatch carries optional field updates; nil leaves the field unchanged.
type ContactPatch struct {
	Name          *string
	Phone         *string
	Email         *string
	ConsentStatus *string
	AssignedTo    *string
}

func (s *Store) UpdateContact(id, tenantID string, p ContactPatch) (Contact, error) {
	cur, err := s.GetContact(id, tenantID)
	if err != nil {
		return Contact{}, err
	}
	if p.Name != nil {
		cur.Name = *p.Name
	}
	if p.Phone != nil {
		cur.Phone = *p.Phone
	}
	if p.Email != nil {
		cur.Email = *p.Email
	}
	if p.ConsentStatus != nil {
		cur.ConsentStatus = *p.ConsentStatus
	}
	if p.AssignedTo != nil {
		cur.AssignedMemberID = *p.AssignedTo
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE contacts SET name=?,phone=?,email=?,consent_status=?,assigned_member_id=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Name, cur.Phone, cur.Email, cur.ConsentStatus, nullable(cur.AssignedMemberID), cur.UpdatedAt, id, tenantID)
	return cur, err
}

type Lead struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	ContactID        string `json:"contact_id"`
	SourceRefID      string `json:"source_ref_id,omitempty"`
	Status           string `json:"status"`
	AssignedMemberID string `json:"assigned_member_id,omitempty"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

const leadCols = `id,tenant_id,contact_id,source_ref_id,status,assigned_member_id,created_by,created_at,updated_at`

func scanLead(sc interface{ Scan(...any) error }) (Lead, error) {
	var l Lead
	var src, asn sql.NullString
	err := sc.Scan(&l.ID, &l.TenantID, &l.ContactID, &src, &l.Status, &asn, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return Lead{}, err
	}
	l.SourceRefID, l.AssignedMemberID = src.String, asn.String
	return l, nil
}

func (s *Store) CreateLead(l Lead, createdBy, assignTo string) (Lead, error) {
	l.ID = newID("lead_")
	l.CreatedBy = createdBy
	l.AssignedMemberID = assignTo
	l.CreatedAt, l.UpdatedAt = now(), now()
	_, err := s.DB.Exec(
		`INSERT INTO leads(`+leadCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		l.ID, l.TenantID, l.ContactID, nullable(l.SourceRefID), l.Status, nullable(l.AssignedMemberID), l.CreatedBy, l.CreatedAt, l.UpdatedAt)
	return l, err
}

func (s *Store) GetLead(id, tenantID string) (Lead, error) {
	row := s.DB.QueryRow(`SELECT `+leadCols+` FROM leads WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanLead(row)
}

func (s *Store) ListLeads(tenantID, assigneeFilter string) ([]Lead, error) {
	q := `SELECT ` + leadCols + ` FROM leads WHERE tenant_id=?`
	args := []any{tenantID}
	if assigneeFilter != "" {
		q += ` AND assigned_member_id=?`
		args = append(args, assigneeFilter)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lead
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

type LeadPatch struct {
	Status    *string
	AssignedTo *string
}

func (s *Store) UpdateLead(id, tenantID string, p LeadPatch) (Lead, error) {
	cur, err := s.GetLead(id, tenantID)
	if err != nil {
		return Lead{}, err
	}
	if p.Status != nil {
		cur.Status = *p.Status
	}
	if p.AssignedTo != nil {
		cur.AssignedMemberID = *p.AssignedTo
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE leads SET status=?,assigned_member_id=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Status, nullable(cur.AssignedMemberID), cur.UpdatedAt, id, tenantID)
	return cur, err
}

type Opportunity struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	ContactID        string `json:"contact_id"`
	Title            string `json:"title"`
	Stage            string `json:"stage"`
	BusinessCategory string `json:"business_category"`
	AssignedMemberID string `json:"assigned_member_id,omitempty"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

const oppCols = `id,tenant_id,contact_id,title,stage,business_category,assigned_member_id,created_by,created_at,updated_at`

func scanOpp(sc interface{ Scan(...any) error }) (Opportunity, error) {
	var o Opportunity
	var asn sql.NullString
	err := sc.Scan(&o.ID, &o.TenantID, &o.ContactID, &o.Title, &o.Stage, &o.BusinessCategory, &asn, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return Opportunity{}, err
	}
	o.AssignedMemberID = asn.String
	return o, nil
}

func (s *Store) CreateOpportunity(o Opportunity, createdBy, assignTo string) (Opportunity, error) {
	o.ID = newID("opp_")
	o.CreatedBy = createdBy
	o.AssignedMemberID = assignTo
	o.CreatedAt, o.UpdatedAt = now(), now()
	_, err := s.DB.Exec(
		`INSERT INTO opportunities(`+oppCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.TenantID, o.ContactID, o.Title, o.Stage, o.BusinessCategory, nullable(o.AssignedMemberID), o.CreatedBy, o.CreatedAt, o.UpdatedAt)
	return o, err
}

func (s *Store) GetOpportunity(id, tenantID string) (Opportunity, error) {
	row := s.DB.QueryRow(`SELECT `+oppCols+` FROM opportunities WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanOpp(row)
}

func (s *Store) ListOpportunities(tenantID, assigneeFilter string) ([]Opportunity, error) {
	q := `SELECT ` + oppCols + ` FROM opportunities WHERE tenant_id=?`
	args := []any{tenantID}
	if assigneeFilter != "" {
		q += ` AND assigned_member_id=?`
		args = append(args, assigneeFilter)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Opportunity
	for rows.Next() {
		o, err := scanOpp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type OpportunityPatch struct {
	Title      *string
	Stage      *string
	AssignedTo *string
}

func (s *Store) UpdateOpportunity(id, tenantID string, p OpportunityPatch) (Opportunity, error) {
	cur, err := s.GetOpportunity(id, tenantID)
	if err != nil {
		return Opportunity{}, err
	}
	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.Stage != nil {
		cur.Stage = *p.Stage
	}
	if p.AssignedTo != nil {
		cur.AssignedMemberID = *p.AssignedTo
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE opportunities SET title=?,stage=?,assigned_member_id=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Title, cur.Stage, nullable(cur.AssignedMemberID), cur.UpdatedAt, id, tenantID)
	return cur, err
}

// ---- helpers ---------------------------------------------------------------

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
