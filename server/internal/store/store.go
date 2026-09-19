// Package store is the SQL layer. Every business query is tenant-scoped and
// fully parameterized; ids are random; timestamps are RFC3339 UTC.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
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

// Contact is the customer profile row (HUI-1691 extended: notes/tags, soft
// delete). Phone/email are stored server-side and must never appear in logs,
// URLs or shared context unmasked.
type Contact struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	Name             string `json:"name"`
	Phone            string `json:"phone"`
	Email            string `json:"email"`
	BusinessCategory string `json:"business_category"`
	SourceType       string `json:"source_type"`
	ConsentStatus    string `json:"consent_status"`
	Notes            string `json:"notes"`
	Tags             string `json:"tags"`
	// DeletedAt is non-empty for soft-deleted (masked tombstone) rows; such
	// rows never come back from the store and only their consents/followups
	// rows are retained as minimal audit.
	DeletedAt        string `json:"deleted_at,omitempty"`
	SourceRefID      string `json:"source_ref_id,omitempty"`
	AssignedMemberID string `json:"assigned_member_id,omitempty"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

const contactCols = `id,tenant_id,name,phone,email,business_category,source_type,consent_status,notes,tags,deleted_at,source_ref_id,assigned_member_id,created_by,created_at,updated_at`

func scanContact(sc interface{ Scan(...any) error }) (Contact, error) {
	var c Contact
	var src, asn, deleted sql.NullString
	err := sc.Scan(&c.ID, &c.TenantID, &c.Name, &c.Phone, &c.Email, &c.BusinessCategory, &c.SourceType,
		&c.ConsentStatus, &c.Notes, &c.Tags, &deleted, &src, &asn, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Contact{}, err
	}
	c.DeletedAt = deleted.String
	c.SourceRefID, c.AssignedMemberID = src.String, asn.String
	return c, nil
}

func (s *Store) CreateContact(c Contact, createdBy, assignTo string) (Contact, error) {
	c.ID = newID("con_")
	c.CreatedBy = createdBy
	c.AssignedMemberID = assignTo
	c.CreatedAt, c.UpdatedAt = now(), now()
	_, err := s.DB.Exec(
		`INSERT INTO contacts(`+contactCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.TenantID, c.Name, c.Phone, c.Email, c.BusinessCategory, c.SourceType, c.ConsentStatus,
		c.Notes, c.Tags, nil, nullable(c.SourceRefID), nullable(c.AssignedMemberID), c.CreatedBy, c.CreatedAt, c.UpdatedAt)
	return c, err
}

// GetContact fetches a live (non-deleted) contact within the tenant.
// Soft-deleted tombstones answer sql.ErrNoRows so the HTTP layer 404s them
// for every role, while their consents/followups rows stay for minimal audit.
func (s *Store) GetContact(id, tenantID string) (Contact, error) {
	row := s.DB.QueryRow(`SELECT `+contactCols+` FROM contacts WHERE id=? AND tenant_id=? AND deleted_at IS NULL`, id, tenantID)
	return scanContact(row)
}

// escapeLike escapes LIKE wildcards in user input; callers must use ESCAPE '\'.
func escapeLike(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(v)
}

// ContactFilter narrows list queries. Name is a substring match on the profile
// name, Tag an exact single-tag match. Phone search is deliberately absent:
// URL 查询参数不承载手机号(HUI-1691 红线),HTTP 层另行拒绝。
type ContactFilter struct {
	NameSubstring string
	Tag           string
}

func (s *Store) ListContacts(tenantID, assigneeFilter string, f ContactFilter) ([]Contact, error) {
	q := `SELECT ` + contactCols + ` FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`
	args := []any{tenantID}
	if assigneeFilter != "" {
		q += ` AND assigned_member_id=?`
		args = append(args, assigneeFilter)
	}
	if f.NameSubstring != "" {
		q += ` AND name LIKE '%'||?||'%' ESCAPE '\'`
		args = append(args, escapeLike(f.NameSubstring))
	}
	if f.Tag != "" {
		q += ` AND (','||tags||',') LIKE '%,'||?||',%' ESCAPE '\'`
		args = append(args, escapeLike(f.Tag))
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
	Notes         *string
	// Tags is the already-normalized comma-joined list (normalization happens
	// at the HTTP layer so the store stays a dumb, parameterized pipe).
	Tags *string
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
	if p.Notes != nil {
		cur.Notes = *p.Notes
	}
	if p.Tags != nil {
		cur.Tags = *p.Tags
	}
	if p.AssignedTo != nil {
		cur.AssignedMemberID = *p.AssignedTo
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE contacts SET name=?,phone=?,email=?,consent_status=?,notes=?,tags=?,assigned_member_id=?,updated_at=? WHERE id=? AND tenant_id=? AND deleted_at IS NULL`,
		cur.Name, cur.Phone, cur.Email, cur.ConsentStatus, cur.Notes, cur.Tags, nullable(cur.AssignedMemberID), cur.UpdatedAt, id, tenantID)
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

// Opportunity is 获客原生域的商机记录(HUI-1693 / FEAT-0194)。
// 语义红线:
//   - AmountCents 为 nil 表示金额未知(NULL),统计进「未知」桶,绝不当 0;
//   - AmountSource 记录金额来源(manual=人工录入,unknown=无金额);
//   - Stage=won 仅表示「人工标记成交」,绝不表示已支付/已收款;
//   - 商机阶段/金额/销售判断不从公共 Task 或接单状态派生。
type Opportunity struct {
	ID               string  `json:"id"`
	TenantID         string  `json:"tenant_id"`
	ContactID        string  `json:"contact_id"`
	Title            string  `json:"title"`
	Stage            string  `json:"stage"`
	BusinessCategory string  `json:"business_category"`
	AmountCents      *int64  `json:"amount_cents"`
	AmountSource     string  `json:"amount_source"`
	Probability      int     `json:"probability"`
	ExpectedCloseAt  *string `json:"expected_close_at"`
	AssignedMemberID string  `json:"assigned_member_id,omitempty"`
	CreatedBy        string  `json:"created_by"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

const oppCols = `id,tenant_id,contact_id,title,stage,business_category,amount_cents,amount_source,probability,expected_close_at,assigned_member_id,created_by,created_at,updated_at`

func scanOpp(sc interface{ Scan(...any) error }) (Opportunity, error) {
	var o Opportunity
	var asn, closeAt sql.NullString
	var amount sql.NullInt64
	err := sc.Scan(&o.ID, &o.TenantID, &o.ContactID, &o.Title, &o.Stage, &o.BusinessCategory,
		&amount, &o.AmountSource, &o.Probability, &closeAt, &asn,
		&o.CreatedBy, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return Opportunity{}, err
	}
	o.AssignedMemberID = asn.String
	if amount.Valid {
		v := amount.Int64
		o.AmountCents = &v
	}
	if closeAt.Valid {
		v := closeAt.String
		o.ExpectedCloseAt = &v
	}
	return o, nil
}

func (s *Store) CreateOpportunity(o Opportunity, createdBy, assignTo string) (Opportunity, error) {
	o.ID = newID("opp_")
	o.CreatedBy = createdBy
	o.AssignedMemberID = assignTo
	// 金额来源由服务端派生:有人工金额=manual,否则 unknown。客户端不可自报。
	if o.AmountCents != nil {
		o.AmountSource = "manual"
	} else {
		o.AmountSource = "unknown"
	}
	if o.Probability < 0 || o.Probability > 100 {
		return Opportunity{}, fmt.Errorf("probability must be 0..100")
	}
	o.CreatedAt, o.UpdatedAt = now(), now()
	tx, err := s.DB.Begin()
	if err != nil {
		return Opportunity{}, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		`INSERT INTO opportunities(`+oppCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.TenantID, o.ContactID, o.Title, o.Stage, o.BusinessCategory,
		nullableInt64(o.AmountCents), o.AmountSource, o.Probability, nullableStr(o.ExpectedCloseAt),
		nullable(o.AssignedMemberID), o.CreatedBy, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return Opportunity{}, err
	}
	// 审计链起始行:from_stage='' 表示创建,时间线完整可回查。
	if _, err := tx.Exec(
		`INSERT INTO opportunity_stage_history(id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note)
		 VALUES(?,?,?,?,?,?,?,?)`,
		newID("ohs_"), o.TenantID, o.ID, "", o.Stage, createdBy, o.CreatedAt, "created"); err != nil {
		return Opportunity{}, err
	}
	if err := tx.Commit(); err != nil {
		return Opportunity{}, err
	}
	return o, nil
}

func (s *Store) GetOpportunity(id, tenantID string) (Opportunity, error) {
	row := s.DB.QueryRow(`SELECT `+oppCols+` FROM opportunities WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanOpp(row)
}

// ListOpportunities lists one business_category within the tenant. Category is
// mandatory at the HTTP layer: 商家经营销售与创意服务从列表层就隔离,无跨类别视图。
func (s *Store) ListOpportunities(tenantID, category, assigneeFilter string) ([]Opportunity, error) {
	q := `SELECT ` + oppCols + ` FROM opportunities WHERE tenant_id=? AND business_category=?`
	args := []any{tenantID, category}
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
	AssignedTo *string
	// AmountCents + AmountSet: Set=false 不变;Set=true 且 nil = 清空(unknown);
	// Set=true 且非 nil = 写入(manual)。来源永远由服务端派生。
	AmountCents *int64
	AmountSet   bool
	// Probability nil 不变。
	Probability *int
	// ExpectedCloseAt + ExpectedCloseSet 同金额语义(nil 值 = 清空)。
	ExpectedCloseAt  *string
	ExpectedCloseSet bool
}

func (s *Store) UpdateOpportunity(id, tenantID string, p OpportunityPatch) (Opportunity, error) {
	cur, err := s.GetOpportunity(id, tenantID)
	if err != nil {
		return Opportunity{}, err
	}
	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.Probability != nil {
		if *p.Probability < 0 || *p.Probability > 100 {
			return Opportunity{}, fmt.Errorf("probability must be 0..100")
		}
		cur.Probability = *p.Probability
	}
	if p.AmountSet {
		cur.AmountCents = p.AmountCents
		if p.AmountCents != nil {
			cur.AmountSource = "manual"
		} else {
			cur.AmountSource = "unknown"
		}
	}
	if p.ExpectedCloseSet {
		if p.ExpectedCloseAt != nil && *p.ExpectedCloseAt == "" {
			cur.ExpectedCloseAt = nil
		} else {
			cur.ExpectedCloseAt = p.ExpectedCloseAt
		}
	}
	if p.AssignedTo != nil {
		cur.AssignedMemberID = *p.AssignedTo
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE opportunities SET title=?,amount_cents=?,amount_source=?,probability=?,expected_close_at=?,assigned_member_id=?,updated_at=?
		 WHERE id=? AND tenant_id=?`,
		cur.Title, nullableInt64(cur.AmountCents), cur.AmountSource, cur.Probability, nullableStr(cur.ExpectedCloseAt),
		nullable(cur.AssignedMemberID), cur.UpdatedAt, id, tenantID)
	return cur, err
}

// OpportunityStageEvent is one audited stage transition. from_stage='' marks
// the creation row. 关闭(won/closed_lost)与重开是同一种普通转换。
type OpportunityStageEvent struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenant_id"`
	OpportunityID string `json:"opportunity_id"`
	FromStage     string `json:"from_stage"`
	ToStage       string `json:"to_stage"`
	ChangedBy     string `json:"changed_by"`
	ChangedAt     string `json:"changed_at"`
	Note          string `json:"note"`
}

// TransitionOpportunityStage moves the stage and appends an audit row inside
// one transaction. Repeating the current stage is idempotent: the record is
// returned unchanged with changed=false and NO new history row.
func (s *Store) TransitionOpportunityStage(id, tenantID, toStage, changedBy, note string) (Opportunity, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return Opportunity{}, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRow(`SELECT `+oppCols+` FROM opportunities WHERE id=? AND tenant_id=?`, id, tenantID)
	cur, err := scanOpp(row)
	if err != nil {
		return Opportunity{}, false, err
	}
	if cur.Stage == toStage {
		return cur, false, nil
	}
	ts := now()
	if _, err := tx.Exec(`UPDATE opportunities SET stage=?,updated_at=? WHERE id=? AND tenant_id=?`,
		toStage, ts, id, tenantID); err != nil {
		return Opportunity{}, false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO opportunity_stage_history(id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note)
		 VALUES(?,?,?,?,?,?,?,?)`,
		newID("ohs_"), tenantID, id, cur.Stage, toStage, changedBy, ts, note); err != nil {
		return Opportunity{}, false, err
	}
	cur.Stage = toStage
	cur.UpdatedAt = ts
	if err := tx.Commit(); err != nil {
		return Opportunity{}, false, err
	}
	return cur, true, nil
}

func (s *Store) ListOpportunityStageEvents(opportunityID, tenantID string) ([]OpportunityStageEvent, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note
		 FROM opportunity_stage_history WHERE opportunity_id=? AND tenant_id=? ORDER BY changed_at, id`,
		opportunityID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpportunityStageEvent
	for rows.Next() {
		var e OpportunityStageEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.OpportunityID, &e.FromStage, &e.ToStage,
			&e.ChangedBy, &e.ChangedAt, &e.Note); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OpportunityStats is the per-category funnel. 无跨类别合计:统计必须且只能
// 针对一个 business_category;NULL 金额计入 UnknownCount(「未知」桶),绝不当 0。
type OpportunityStats struct {
	BusinessCategory string         `json:"business_category"`
	Total            int            `json:"total"`
	Funnel           map[string]int `json:"funnel"`
	Amounts          struct {
		KnownTotalCents int64 `json:"known_total_cents"`
		KnownCount      int   `json:"known_count"`
		UnknownCount    int   `json:"unknown_count"`
	} `json:"amounts"`
}

// OpportunityStages is the canonical minimal stage set.
var OpportunityStages = []string{"open", "qualified", "proposal", "negotiation", "won", "closed_lost"}

func (s *Store) OpportunityStats(tenantID, category, assigneeFilter string) (*OpportunityStats, error) {
	q := `SELECT stage, COUNT(1), COALESCE(SUM(amount_cents),0), COUNT(amount_cents)
	      FROM opportunities WHERE tenant_id=? AND business_category=?`
	args := []any{tenantID, category}
	if assigneeFilter != "" {
		q += ` AND assigned_member_id=?`
		args = append(args, assigneeFilter)
	}
	q += ` GROUP BY stage`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st := &OpportunityStats{BusinessCategory: category, Funnel: map[string]int{}}
	for _, stg := range OpportunityStages {
		st.Funnel[stg] = 0
	}
	for rows.Next() {
		var stage string
		var n int
		var knownTotal int64
		var knownN int
		if err := rows.Scan(&stage, &n, &knownTotal, &knownN); err != nil {
			return nil, err
		}
		st.Funnel[stage] = n
		st.Total += n
		st.Amounts.KnownTotalCents += knownTotal
		st.Amounts.KnownCount += knownN
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	st.Amounts.UnknownCount = st.Total - st.Amounts.KnownCount
	return st, nil
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

// nullableStr maps an optional string (present-or-nil) to a SQL value.
func nullableStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// nullableInt64 maps an optional amount to a SQL value; nil stays NULL (= 未知).
func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
