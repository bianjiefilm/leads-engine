package store

import (
	"database/sql"
	"time"
	_ "time/tzdata"
)

// SOPPolicy is the tenant automation level. Unattended stays off unless
// explicit_unattended was saved on purpose. Balance and score are not columns.
type SOPPolicy struct {
	TenantID    string
	Level       string
	Explicit    bool
	GlobalStop  bool
	WindowStart int
	WindowEnd   int
	UpdatedBy   string
	UpdatedAt   string
}

// SOPStop is one unsubscribe, rejection, customer stop, or global stop.
type SOPStop struct {
	ID        string
	TenantID  string
	ContactID string
	Channel   string
	Purpose   string
	Kind      string
	CreatedBy string
	CreatedAt string
}

// SOPAction is one record kind. Delivered and LiveCharge are stored as 0.
type SOPAction struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	ContactID      string `json:"contact_id"`
	SessionID      string `json:"session_id,omitempty"`
	Channel        string `json:"channel"`
	Recipient      string `json:"-"`
	Purpose        string `json:"purpose"`
	ConsentID      string `json:"consent_id,omitempty"`
	ContentVersion int    `json:"content_version"`
	OperatorID     string `json:"operator_id"`
	BudgetCents    int    `json:"budget_cents"`
	LiveCharge     int    `json:"live_charge"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Refusal        string `json:"refusal,omitempty"`
	ParentID       string `json:"parent_id,omitempty"`
	Attempt        int    `json:"attempt"`
	Body           string `json:"body,omitempty"`
	Delivered      bool   `json:"delivered"`
	CreatedAt      string `json:"created_at"`
}

func (s *Store) GetConsentByID(tenantID, id string) (ContactConsent, error) {
	row := s.DB.QueryRow(`SELECT `+consentCols+` FROM contact_consents WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanConsent(row)
}

func (s *Store) GetSOPPolicy(tenantID string) (SOPPolicy, error) {
	var p SOPPolicy
	var explicit, stop int
	err := s.DB.QueryRow(`
		SELECT tenant_id, level, explicit_unattended, global_stop, window_start, window_end, updated_by, updated_at
		FROM sop_policy WHERE tenant_id=?`, tenantID).Scan(
		&p.TenantID, &p.Level, &explicit, &stop, &p.WindowStart, &p.WindowEnd, &p.UpdatedBy, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return SOPPolicy{TenantID: tenantID, Level: "remind_draft_confirm", WindowStart: 9, WindowEnd: 20}, nil
	}
	if err != nil {
		return SOPPolicy{}, err
	}
	p.Explicit = explicit == 1
	p.GlobalStop = stop == 1
	return p, nil
}

func (s *Store) SaveSOPPolicy(p SOPPolicy) error {
	current, err := s.GetSOPPolicy(p.TenantID)
	if err != nil {
		return err
	}
	p.GlobalStop = current.GlobalStop
	if p.Level != "unattended" || !p.Explicit {
		p.Level = "remind_draft_confirm"
		p.Explicit = false
	}
	explicit, stop := boolInt(p.Explicit), boolInt(p.GlobalStop)
	_, err = s.DB.Exec(`
		INSERT INTO sop_policy(tenant_id, level, explicit_unattended, global_stop, window_start, window_end, updated_by, updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			level=excluded.level,
			explicit_unattended=excluded.explicit_unattended,
			global_stop=sop_policy.global_stop,
			window_start=excluded.window_start,
			window_end=excluded.window_end,
			updated_by=excluded.updated_by,
			updated_at=excluded.updated_at`,
		p.TenantID, p.Level, explicit, stop, p.WindowStart, p.WindowEnd, p.UpdatedBy, now())
	return err
}

func (s *Store) SetSOPGlobalStop(tenantID, memberID string) error {
	current, err := s.GetSOPPolicy(tenantID)
	if err != nil {
		return err
	}
	if current.UpdatedBy == "" {
		current.WindowStart, current.WindowEnd = 9, 20
		current.Level = "remind_draft_confirm"
	}
	_, err = s.DB.Exec(`
		INSERT INTO sop_policy(tenant_id, level, explicit_unattended, global_stop, window_start, window_end, updated_by, updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET global_stop=1, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		tenantID, current.Level, boolInt(current.Explicit), 1, current.WindowStart, current.WindowEnd, memberID, now())
	return err
}

func (s *Store) AddSOPStop(stop SOPStop) (SOPStop, error) {
	stop.ID = newID("sops_")
	stop.CreatedAt = now()
	_, err := s.DB.Exec(`
		INSERT INTO sop_stops(id, tenant_id, contact_id, channel, purpose, kind, active, created_by, created_at)
		VALUES(?,?,?,?,?,?,1,?,?)`,
		stop.ID, stop.TenantID, stop.ContactID, stop.Channel, stop.Purpose, stop.Kind, stop.CreatedBy, stop.CreatedAt)
	return stop, err
}

func (s *Store) ListSOPStops(tenantID, contactID string) ([]SOPStop, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, contact_id, channel, purpose, kind, created_by, created_at
		FROM sop_stops WHERE tenant_id=? AND active=1 AND (contact_id='' OR contact_id=?)`, tenantID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SOPStop
	for rows.Next() {
		var stop SOPStop
		if err := rows.Scan(&stop.ID, &stop.TenantID, &stop.ContactID, &stop.Channel, &stop.Purpose, &stop.Kind, &stop.CreatedBy, &stop.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, stop)
	}
	return out, rows.Err()
}

func (s *Store) InsertSOPActions(rows []SOPAction) ([]SOPAction, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i := range rows {
		rows[i].ID = newID("sopa_")
		rows[i].CreatedAt = now()
		rows[i].LiveCharge = 0
		rows[i].Delivered = false
		if _, err := tx.Exec(`
			INSERT INTO sop_actions(
				id, tenant_id, contact_id, session_id, channel, recipient, purpose, consent_id,
				content_version, operator_id, budget_cents, live_charge, kind, status, refusal,
				parent_id, attempt, body, delivered, created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,0,?,?,?,?,?,?,0,?)`,
			rows[i].ID, rows[i].TenantID, rows[i].ContactID, rows[i].SessionID, rows[i].Channel, rows[i].Recipient,
			rows[i].Purpose, rows[i].ConsentID, rows[i].ContentVersion, rows[i].OperatorID, rows[i].BudgetCents,
			rows[i].Kind, rows[i].Status, rows[i].Refusal, rows[i].ParentID, rows[i].Attempt, rows[i].Body, rows[i].CreatedAt); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) CountSOPSubmissions(tenantID, contactID, channel, purpose, exceptParent, since string) (int, error) {
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*) FROM sop_actions
		WHERE tenant_id=? AND contact_id=? AND channel=? AND purpose=?
		  AND kind='channel_submission' AND status='pending_send'
		  AND parent_id<>? AND created_at>=?`,
		tenantID, contactID, channel, purpose, exceptParent, since).Scan(&n)
	return n, err
}

func (s *Store) CountSOPAttempts(parentID string) (int, error) {
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*) FROM sop_actions
		WHERE parent_id=? AND kind='channel_submission' AND status='pending_send'`, parentID).Scan(&n)
	return n, err
}

func (s *Store) ListSOPActions(tenantID, contactID, assigneeID string) ([]SOPAction, error) {
	rows, err := s.DB.Query(`
		SELECT a.id, a.tenant_id, a.contact_id, a.session_id, a.channel, a.purpose, a.consent_id,
		       a.content_version, a.operator_id, a.budget_cents, a.live_charge, a.kind, a.status,
		       a.refusal, a.parent_id, a.attempt, a.body, a.delivered, a.created_at
		FROM sop_actions a
		JOIN contacts c ON c.id=a.contact_id
		WHERE a.tenant_id=? AND c.deleted_at IS NULL
		  AND (?='' OR a.contact_id=?)
		  AND (?='' OR c.assigned_member_id=?)
		ORDER BY a.created_at`, tenantID, contactID, contactID, assigneeID, assigneeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SOPAction
	for rows.Next() {
		var a SOPAction
		var delivered int
		if err := rows.Scan(&a.ID, &a.TenantID, &a.ContactID, &a.SessionID, &a.Channel, &a.Purpose, &a.ConsentID,
			&a.ContentVersion, &a.OperatorID, &a.BudgetCents, &a.LiveCharge, &a.Kind, &a.Status,
			&a.Refusal, &a.ParentID, &a.Attempt, &a.Body, &delivered, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Delivered = false
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetSOPAction(tenantID, id string) (SOPAction, error) {
	var a SOPAction
	var delivered int
	err := s.DB.QueryRow(`
		SELECT id, tenant_id, contact_id, session_id, channel, recipient, purpose, consent_id,
		       content_version, operator_id, budget_cents, live_charge, kind, status, refusal,
		       parent_id, attempt, body, delivered, created_at
		FROM sop_actions WHERE id=? AND tenant_id=?`, id, tenantID).Scan(
		&a.ID, &a.TenantID, &a.ContactID, &a.SessionID, &a.Channel, &a.Recipient, &a.Purpose, &a.ConsentID,
		&a.ContentVersion, &a.OperatorID, &a.BudgetCents, &a.LiveCharge, &a.Kind, &a.Status, &a.Refusal,
		&a.ParentID, &a.Attempt, &a.Body, &delivered, &a.CreatedAt)
	a.Delivered = false
	return a, err
}

// ShanghaiDayStart is the UTC timestamp of local midnight, for rate checks.
func ShanghaiDayStart(now time.Time) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start.UTC().Format(time.RFC3339Nano)
}

// InsideSOPWindow reports whether now falls in [start, end) Asia/Shanghai hours.
// 0 and 24 is the whole day. Any other empty range is closed.
func InsideSOPWindow(now time.Time, start, end int) bool {
	if start == 0 && end == 24 {
		return true
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	hour := now.In(loc).Hour()
	return start < end && hour >= start && hour < end
}
