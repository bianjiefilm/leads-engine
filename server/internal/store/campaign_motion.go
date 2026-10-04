package store

// CampaignMotion is the stored leads-side handoff.
// ExportJSON is the only document that may leave leads.
// LocalConversionJSON is not part of that document.
type CampaignMotion struct {
	ID                    string
	TenantID              string
	CampaignID            string
	ExportJSON            string
	LocalConversionJSON   string
	ModelCalls            int
	CreativeProjectWrites int
	PieceGenerated        bool
	PieceSent             bool
	UpdatedBy             string
	CreatedAt             string
	UpdatedAt             string
}

// LoadCampaignMotionSubject returns the assignee of a touch-campaign lead.
// Phone, email, and the contact name are not selected.
func (s *Store) LoadCampaignMotionSubject(id, tenantID string) (string, error) {
	var assignee string
	err := s.DB.QueryRow(`
		SELECT COALESCE(l.assigned_member_id,'')
		FROM leads l
		JOIN contacts c ON c.id = l.contact_id AND c.tenant_id = l.tenant_id AND c.deleted_at IS NULL
		WHERE l.id=? AND l.tenant_id=? AND c.source_type='touch_campaign'`, id, tenantID).Scan(&assignee)
	return assignee, err
}

func (s *Store) GetCampaignMotion(tenantID, campaignID string) (CampaignMotion, error) {
	row := s.DB.QueryRow(`
		SELECT id, tenant_id, campaign_id, export_json, local_conversion_json,
		       model_calls, creative_project_writes, piece_generated, piece_sent,
		       updated_by, created_at, updated_at
		FROM campaign_motion_handoffs
		WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	return scanCampaignMotion(row)
}

// SaveCampaignMotion upserts one handoff. Generation and creative-project writes cannot be stored.
func (s *Store) SaveCampaignMotion(row CampaignMotion) (CampaignMotion, error) {
	if row.ID == "" {
		row.ID = newID("cmh_")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = now()
	}
	row.UpdatedAt = now()
	_, err := s.DB.Exec(`
		INSERT INTO campaign_motion_handoffs(
			id, tenant_id, campaign_id, export_json, local_conversion_json,
			model_calls, creative_project_writes, piece_generated, piece_sent,
			updated_by, created_at, updated_at)
		VALUES(?,?,?,?,?,0,0,0,0,?,?,?)
		ON CONFLICT(tenant_id, campaign_id) DO UPDATE SET
			export_json=excluded.export_json,
			local_conversion_json=excluded.local_conversion_json,
			updated_by=excluded.updated_by,
			updated_at=excluded.updated_at`,
		row.ID, row.TenantID, row.CampaignID, row.ExportJSON, row.LocalConversionJSON,
		row.UpdatedBy, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return CampaignMotion{}, err
	}
	return s.GetCampaignMotion(row.TenantID, row.CampaignID)
}

func scanCampaignMotion(sc interface{ Scan(...any) error }) (CampaignMotion, error) {
	var row CampaignMotion
	var generated, sent int
	err := sc.Scan(&row.ID, &row.TenantID, &row.CampaignID, &row.ExportJSON, &row.LocalConversionJSON,
		&row.ModelCalls, &row.CreativeProjectWrites, &generated, &sent,
		&row.UpdatedBy, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return CampaignMotion{}, err
	}
	row.PieceGenerated = generated != 0
	row.PieceSent = sent != 0
	return row, nil
}
