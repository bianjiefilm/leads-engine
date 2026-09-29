package store

import (
	"database/sql"
	"errors"
	"time"
)

// ChannelOperator is a white-label channel admin. It is not a CRM membership.
type ChannelOperator struct {
	PrincipalRef string
	ChannelID    string
	CreatedAt    string
}

func (s *Store) RegisterChannelOperator(principal, channelID string) (ChannelOperator, error) {
	row := ChannelOperator{PrincipalRef: principal, ChannelID: channelID, CreatedAt: now()}
	_, err := s.DB.Exec(`INSERT INTO channel_operators(principal_ref,channel_id,created_at) VALUES(?,?,?)
		ON CONFLICT(principal_ref) DO UPDATE SET channel_id=excluded.channel_id`,
		row.PrincipalRef, row.ChannelID, row.CreatedAt)
	return row, err
}

func (s *Store) GetChannelOperator(principal string) (ChannelOperator, error) {
	var row ChannelOperator
	err := s.DB.QueryRow(`SELECT principal_ref,channel_id,created_at FROM channel_operators WHERE principal_ref=?`, principal).
		Scan(&row.PrincipalRef, &row.ChannelID, &row.CreatedAt)
	return row, err
}

// PutChannelProcurement stores the channel's purchase price and token.
// Callers that build a customer export must not read this table.
func (s *Store) PutChannelProcurement(tenantID string, priceCents, quota int, token string) (int, error) {
	_, err := s.DB.Exec(`INSERT INTO channel_procurement(tenant_id,purchase_price_cents,ai_quota,internal_token)
		VALUES(?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET purchase_price_cents=excluded.purchase_price_cents,
			ai_quota=excluded.ai_quota, internal_token=excluded.internal_token`,
		tenantID, priceCents, quota, token)
	return quota, err
}

// ChannelAIQuota reports whether a quota row exists and the remaining count.
// It does not return the purchase price or the internal token.
func (s *Store) ChannelAIQuota(tenantID string) (bool, int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT ai_quota FROM channel_procurement WHERE tenant_id=?`, tenantID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, n, nil
}

type Brand struct {
	ID          string `json:"brand_id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

func (s *Store) UpsertBrand(id, display string) (Brand, error) {
	ts := now()
	_, err := s.DB.Exec(`INSERT INTO brands(id,display_name,status,created_at,updated_at) VALUES(?,?,'active',?,?)
		ON CONFLICT(id) DO NOTHING`, id, display, ts, ts)
	if err != nil {
		return Brand{}, err
	}
	return s.GetBrand(id)
}

func (s *Store) GetBrand(id string) (Brand, error) {
	var b Brand
	err := s.DB.QueryRow(`SELECT id,display_name,status FROM brands WHERE id=?`, id).Scan(&b.ID, &b.DisplayName, &b.Status)
	return b, err
}

// RenameBrand changes the display name only.
func (s *Store) RenameBrand(id, display string) (Brand, error) {
	res, err := s.DB.Exec(`UPDATE brands SET display_name=?, updated_at=? WHERE id=?`, display, now(), id)
	if err != nil {
		return Brand{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Brand{}, sql.ErrNoRows
	}
	return s.GetBrand(id)
}

func (s *Store) SetBrandStatus(id, status string) (Brand, error) {
	res, err := s.DB.Exec(`UPDATE brands SET status=?, updated_at=? WHERE id=?`, status, now(), id)
	if err != nil {
		return Brand{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Brand{}, sql.ErrNoRows
	}
	return s.GetBrand(id)
}

func (s *Store) SetContactOrigin(contactID, tenantID, brandID, sourceTag, campaignID string) error {
	_, err := s.DB.Exec(`INSERT INTO contact_origins(contact_id,tenant_id,brand_id,source_tag,campaign_id,created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(contact_id) DO UPDATE SET brand_id=excluded.brand_id, source_tag=excluded.source_tag, campaign_id=excluded.campaign_id`,
		contactID, tenantID, brandID, sourceTag, campaignID, now())
	return err
}

func (s *Store) ApplyContactOrigin(c *Contact) {
	if c == nil {
		return
	}
	var brand, tag, campaign string
	err := s.DB.QueryRow(`SELECT brand_id, source_tag, campaign_id FROM contact_origins WHERE contact_id=? AND tenant_id=?`, c.ID, c.TenantID).
		Scan(&brand, &tag, &campaign)
	if err != nil {
		return
	}
	c.OriginBrandID = brand
	c.SourceTag = tag
	c.CampaignID = campaign
}

func (s *Store) ContactOriginBrand(contactID, tenantID string) string {
	c := Contact{ID: contactID, TenantID: tenantID}
	s.ApplyContactOrigin(&c)
	return c.OriginBrandID
}

func (s *Store) FillContactOrigins(tenantID string, items []Contact) {
	if len(items) == 0 {
		return
	}
	rows, err := s.DB.Query(`SELECT contact_id, brand_id, source_tag, campaign_id FROM contact_origins WHERE tenant_id=?`, tenantID)
	if err != nil {
		return
	}
	defer rows.Close()
	type origin struct{ brand, tag, campaign string }
	facts := map[string]origin{}
	for rows.Next() {
		var id string
		var fact origin
		if err := rows.Scan(&id, &fact.brand, &fact.tag, &fact.campaign); err == nil {
			facts[id] = fact
		}
	}
	for i := range items {
		fact, ok := facts[items[i].ID]
		if !ok {
			continue
		}
		items[i].OriginBrandID = fact.brand
		items[i].SourceTag = fact.tag
		items[i].CampaignID = fact.campaign
	}
}

func (s *Store) SetTenantLifecycle(tenantID, status string) error {
	_, err := s.DB.Exec(`INSERT INTO tenant_lifecycle(tenant_id,status,updated_at) VALUES(?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET status=excluded.status, updated_at=excluded.updated_at`,
		tenantID, status, now())
	return err
}

func (s *Store) TenantLifecycle(tenantID string) string {
	var status string
	err := s.DB.QueryRow(`SELECT status FROM tenant_lifecycle WHERE tenant_id=?`, tenantID).Scan(&status)
	if err != nil {
		return "active"
	}
	return status
}

// BrandStatusForTenant is the display brand linked from this tenant's
// contacts. It is independent of tenant_lifecycle.
func (s *Store) BrandStatusForTenant(tenantID string) string {
	var status string
	err := s.DB.QueryRow(`SELECT b.status FROM contact_origins o
		JOIN brands b ON b.id=o.brand_id
		WHERE o.tenant_id=? ORDER BY o.created_at LIMIT 1`, tenantID).Scan(&status)
	if err != nil || status == "" {
		return "active"
	}
	return status
}

type CRMDelegation struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PrincipalRef string `json:"principal_ref"`
	Kind         string `json:"kind"`
	RevokedAt    string `json:"revoked_at,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func (s *Store) CreateCRMDelegation(tenantID, principal, kind string) (CRMDelegation, error) {
	row := CRMDelegation{
		ID: newID("dlg_"), TenantID: tenantID, PrincipalRef: principal, Kind: kind, CreatedAt: now(),
	}
	_, err := s.DB.Exec(`INSERT INTO crm_delegations(id,tenant_id,principal_ref,kind,revoked_at,created_at) VALUES(?,?,?,?,NULL,?)`,
		row.ID, row.TenantID, row.PrincipalRef, row.Kind, row.CreatedAt)
	return row, err
}

func (s *Store) RevokeCRMDelegation(tenantID, id string) error {
	res, err := s.DB.Exec(`UPDATE crm_delegations SET revoked_at=COALESCE(revoked_at, ?) WHERE id=? AND tenant_id=?`,
		now(), id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// HasActiveSupport reports an unrevoked support or operation delegation.
func (s *Store) HasActiveSupport(tenantID, principal string) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM crm_delegations
		WHERE tenant_id=? AND principal_ref=? AND kind IN ('support','operation') AND revoked_at IS NULL`,
		tenantID, principal).Scan(&n)
	return n > 0, err
}

type ExportJob struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	BrandID   string `json:"brand_id"`
	Requester string `json:"requester"`
	Purpose   string `json:"purpose"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreateExportJob(tenantID, brandID, requester, purpose string, ttl time.Duration) (ExportJob, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	row := ExportJob{
		ID: newID("exp_"), TenantID: tenantID, BrandID: brandID, Requester: requester,
		Purpose: purpose, ExpiresAt: time.Now().UTC().Add(ttl).Format(time.RFC3339Nano), CreatedAt: now(),
	}
	_, err := s.DB.Exec(`INSERT INTO crm_export_jobs(id,tenant_id,brand_id,requester_principal,purpose,expires_at,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		row.ID, row.TenantID, row.BrandID, row.Requester, row.Purpose, row.ExpiresAt, row.CreatedAt)
	return row, err
}

func (s *Store) GetExportJob(id string) (ExportJob, error) {
	var row ExportJob
	err := s.DB.QueryRow(`SELECT id,tenant_id,brand_id,requester_principal,purpose,expires_at,created_at
		FROM crm_export_jobs WHERE id=?`, id).Scan(
		&row.ID, &row.TenantID, &row.BrandID, &row.Requester, &row.Purpose, &row.ExpiresAt, &row.CreatedAt)
	return row, err
}

// TenantExport is the customer-visible bundle. It never selects channel
// procurement columns.
func (s *Store) TenantExport(tenantID string) (map[string]any, error) {
	contacts, err := s.ListContacts(tenantID, "", ContactFilter{})
	if err != nil {
		return nil, err
	}
	s.FillContactOrigins(tenantID, contacts)
	leads, err := s.listExportRows(`SELECT id,tenant_id,contact_id,status,COALESCE(assigned_member_id,''),created_at FROM leads WHERE tenant_id=?`, tenantID,
		[]string{"id", "tenant_id", "contact_id", "status", "assigned_member_id", "created_at"})
	if err != nil {
		return nil, err
	}
	opps, err := s.listExportRows(`SELECT id,tenant_id,contact_id,title,stage,COALESCE(assigned_member_id,''),created_at FROM opportunities WHERE tenant_id=?`, tenantID,
		[]string{"id", "tenant_id", "contact_id", "title", "stage", "assigned_member_id", "created_at"})
	if err != nil {
		return nil, err
	}
	follows, err := s.listExportRows(`SELECT id,tenant_id,contact_id,COALESCE(lead_id,''),note,COALESCE(created_by,''),created_at FROM follow_ups WHERE tenant_id=?`, tenantID,
		[]string{"id", "tenant_id", "contact_id", "lead_id", "note", "assigned_member_id", "created_at"})
	if err != nil {
		return nil, err
	}
	audit, err := s.listExportRows(`SELECT id,tenant_id,contact_id,note,member_id,created_at FROM contact_followups WHERE tenant_id=?`, tenantID,
		[]string{"id", "tenant_id", "contact_id", "note", "assigned_member_id", "created_at"})
	if err != nil {
		return nil, err
	}
	consents, err := s.listExportRows(`SELECT id,tenant_id,contact_id,source_submission_ref,source_channel,purpose,CASE marketing_allowed WHEN 1 THEN 'granted' ELSE 'denied' END,COALESCE(revoked_at,''),revoked_reason FROM contact_consents WHERE tenant_id=?`, tenantID,
		[]string{"id", "tenant_id", "contact_id", "source_submission_ref", "source_channel", "purpose", "marketing", "revoked_at", "withdrawal"})
	if err != nil {
		return nil, err
	}
	sources, err := s.listExportRows(`SELECT contact_id,tenant_id,brand_id,source_tag,campaign_id,created_at FROM contact_origins WHERE tenant_id=?`, tenantID,
		[]string{"contact_id", "tenant_id", "brand_id", "source_tag", "campaign_id", "created_at"})
	if err != nil {
		return nil, err
	}
	assignments := []map[string]string{}
	for _, c := range contacts {
		if c.AssignedMemberID != "" {
			assignments = append(assignments, map[string]string{
				"kind": "contact", "id": c.ID, "tenant_id": c.TenantID, "assigned_member_id": c.AssignedMemberID,
			})
		}
	}
	return map[string]any{
		"tenant_id":     tenantID,
		"contacts":      contacts,
		"leads":         leads,
		"opportunities": opps,
		"follow_ups":    append(follows, audit...),
		"assignments":   assignments,
		"sources":       sources,
		"consents":      consents,
	}, nil
}

func (s *Store) listExportRows(query, tenantID string, cols []string) ([]map[string]string, error) {
	rows, err := s.DB.Query(query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		vals := make([]string, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]string{}
		for i, name := range cols {
			row[name] = vals[i]
		}
		if row["tenant_id"] != "" && row["tenant_id"] != tenantID {
			continue
		}
		out = append(out, row)
	}
	if out == nil {
		out = []map[string]string{}
	}
	return out, rows.Err()
}
