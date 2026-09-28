package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/bianjiefilm/leads-engine/server/internal/lightcopy"
)

// ErrLightNotCampaign means the lead is not a touch-campaign scene.
var ErrLightNotCampaign = errors.New("lightcopy: not a campaign")

// LightDraft is one manual reply, email, or marketing brief.
type LightDraft struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	SubjectKind        string `json:"subject_kind"`
	SubjectID          string `json:"subject_id"`
	Kind               string `json:"kind"`
	Body               string `json:"body"`
	Origin             string `json:"origin"`
	ContentVersion     int    `json:"content_version"`
	Generated          bool   `json:"generated"`
	Sent               bool   `json:"sent"`
	Published          bool   `json:"published"`
	MarketingPermitted bool   `json:"marketing_permitted"`
	UserConfirmed      bool   `json:"user_confirmed"`
	LiveCharge         int    `json:"live_charge"`
	UpdatedBy          string `json:"updated_by"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// LightHandoff is one attempt to open an existing professional tool.
type LightHandoff struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	Key            string         `json:"idempotency_key"`
	DraftID        string         `json:"draft_id"`
	Tool           string         `json:"tool"`
	SourceKind     string         `json:"source_kind"`
	SourceID       string         `json:"source_id"`
	ContentVersion int            `json:"content_version"`
	Facts          map[string]any `json:"facts"`
	Status         string         `json:"status"`
	Reason         string         `json:"reason"`
	ProjectCreated bool           `json:"project_created"`
	Regenerated    bool           `json:"regenerated"`
	CreatedAt      string         `json:"created_at"`
}

// LoadLightOpportunity copies only the title. Amount is not a proven price,
// and the contact row is not loaded.
func (s *Store) LoadLightOpportunity(id, tenantID string) (lightcopy.Subject, string, error) {
	opp, err := s.GetOpportunity(id, tenantID)
	if err != nil {
		return lightcopy.Subject{}, "", err
	}
	return lightcopy.Subject{
		Kind: lightcopy.KindOpportunity, ID: opp.ID, TenantID: opp.TenantID, Title: opp.Title,
	}, opp.AssignedMemberID, nil
}

// LoadLightCampaign copies the activity reference from a touch-campaign lead.
// Phone, name, email and the authorization snapshot are not selected.
func (s *Store) LoadLightCampaign(id, tenantID string) (lightcopy.Subject, string, error) {
	var assignee, sourceType, activity string
	err := s.DB.QueryRow(`
		SELECT COALESCE(l.assigned_member_id,''), c.source_type, COALESCE(sr.source_ref,'')
		FROM leads l
		JOIN contacts c ON c.id = l.contact_id AND c.tenant_id = l.tenant_id AND c.deleted_at IS NULL
		LEFT JOIN source_refs sr ON sr.id = l.source_ref_id AND sr.tenant_id = l.tenant_id
		WHERE l.id=? AND l.tenant_id=?`, id, tenantID).Scan(&assignee, &sourceType, &activity)
	if err != nil {
		return lightcopy.Subject{}, "", err
	}
	if sourceType != "touch_campaign" {
		return lightcopy.Subject{}, "", ErrLightNotCampaign
	}
	return lightcopy.Subject{
		Kind: lightcopy.KindCampaign, ID: id, TenantID: tenantID, ActivityRef: activity,
	}, assignee, nil
}

func (s *Store) ListLightDrafts(tenantID, subjectKind, subjectID string) ([]LightDraft, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
		       generated, sent, published, marketing_permitted, user_confirmed, live_charge,
		       updated_by, created_at, updated_at
		FROM light_copy_drafts
		WHERE tenant_id=? AND subject_kind=? AND subject_id=?
		ORDER BY kind`, tenantID, subjectKind, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LightDraft
	for rows.Next() {
		d, err := scanLightDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetLightDraft(tenantID, subjectKind, subjectID, kind string) (LightDraft, error) {
	row := s.DB.QueryRow(`
		SELECT id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
		       generated, sent, published, marketing_permitted, user_confirmed, live_charge,
		       updated_by, created_at, updated_at
		FROM light_copy_drafts
		WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND kind=?`,
		tenantID, subjectKind, subjectID, kind)
	return scanLightDraft(row)
}

func scanLightDraft(sc interface{ Scan(...any) error }) (LightDraft, error) {
	var d LightDraft
	var generated, sent, published, permitted, confirmed, charge int
	err := sc.Scan(&d.ID, &d.TenantID, &d.SubjectKind, &d.SubjectID, &d.Kind, &d.Body, &d.Origin,
		&d.ContentVersion, &generated, &sent, &published, &permitted, &confirmed, &charge,
		&d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return LightDraft{}, err
	}
	d.Generated = generated != 0
	d.Sent = sent != 0
	d.Published = published != 0
	d.MarketingPermitted = permitted != 0
	d.UserConfirmed = confirmed != 0
	d.LiveCharge = charge
	return d, nil
}

// SaveLightDraft upserts the manual text. Flags that would mean sent,
// published, generated, or charged cannot be stored.
func (s *Store) SaveLightDraft(d LightDraft) (LightDraft, error) {
	if d.ID == "" {
		d.ID = newID("lcd_")
	}
	if d.CreatedAt == "" {
		d.CreatedAt = now()
	}
	d.UpdatedAt = now()
	confirmed := 0
	if d.UserConfirmed {
		confirmed = 1
	}
	_, err := s.DB.Exec(`
		INSERT INTO light_copy_drafts(
			id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
			generated, sent, published, marketing_permitted, user_confirmed, live_charge,
			updated_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,0,0,0,0,?,?,?,?,?)
		ON CONFLICT(tenant_id, subject_kind, subject_id, kind) DO UPDATE SET
			body=excluded.body,
			origin=excluded.origin,
			content_version=excluded.content_version,
			user_confirmed=excluded.user_confirmed,
			updated_by=excluded.updated_by,
			updated_at=excluded.updated_at`,
		d.ID, d.TenantID, d.SubjectKind, d.SubjectID, d.Kind, d.Body, d.Origin, d.ContentVersion,
		confirmed, d.LiveCharge, d.UpdatedBy, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return LightDraft{}, err
	}
	return s.GetLightDraft(d.TenantID, d.SubjectKind, d.SubjectID, d.Kind)
}

func (s *Store) GetLightHandoff(tenantID, key string) (LightHandoff, error) {
	var h LightHandoff
	var facts string
	var created, regenerated int
	err := s.DB.QueryRow(`
		SELECT id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
		       content_version, fact_json, status, reason, project_created, regenerated, created_at
		FROM light_copy_handoffs WHERE tenant_id=? AND idempotency_key=?`, tenantID, key).Scan(
		&h.ID, &h.TenantID, &h.Key, &h.DraftID, &h.Tool, &h.SourceKind, &h.SourceID,
		&h.ContentVersion, &facts, &h.Status, &h.Reason, &created, &regenerated, &h.CreatedAt)
	if err != nil {
		return LightHandoff{}, err
	}
	h.ProjectCreated = created != 0
	h.Regenerated = regenerated != 0
	h.Facts = map[string]any{}
	if facts != "" {
		_ = json.Unmarshal([]byte(facts), &h.Facts)
	}
	return h, nil
}

// InsertLightHandoff stores the first attempt for a key. A duplicate key
// returns the original row.
func (s *Store) InsertLightHandoff(h LightHandoff) (LightHandoff, error) {
	if existing, err := s.GetLightHandoff(h.TenantID, h.Key); err == nil {
		existing.Regenerated = false
		existing.ProjectCreated = false
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return LightHandoff{}, err
	}
	if h.ID == "" {
		h.ID = newID("lch_")
	}
	if h.CreatedAt == "" {
		h.CreatedAt = now()
	}
	if h.Facts == nil {
		h.Facts = map[string]any{}
	}
	raw, err := json.Marshal(h.Facts)
	if err != nil {
		return LightHandoff{}, err
	}
	_, err = s.DB.Exec(`
		INSERT INTO light_copy_handoffs(
			id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
			content_version, fact_json, status, reason, project_created, regenerated, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,0,0,?)`,
		h.ID, h.TenantID, h.Key, h.DraftID, h.Tool, h.SourceKind, h.SourceID,
		h.ContentVersion, string(raw), h.Status, h.Reason, h.CreatedAt)
	if err != nil {
		if existing, getErr := s.GetLightHandoff(h.TenantID, h.Key); getErr == nil {
			existing.Regenerated = false
			return existing, nil
		}
		return LightHandoff{}, err
	}
	return s.GetLightHandoff(h.TenantID, h.Key)
}
