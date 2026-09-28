package store

import (
	"database/sql"
	"errors"
)

// OutboundPolicy is the tenant call gate. Production auto-dial cannot be stored on.
type OutboundPolicy struct {
	TenantID       string
	ProductionAuto bool
	GlobalStop     bool
	WindowStart    int
	WindowEnd      int
	UpdatedBy      string
	UpdatedAt      string
}

// OutboundStop is a contact-scoped block. Rejection is not tied to a campaign.
type OutboundStop struct {
	ID        string
	TenantID  string
	ContactID string
	Kind      string
	CreatedBy string
	CreatedAt string
}

// OutboundTask is one dial attempt. The unique key is tenant + task_key.
type OutboundTask struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	ContactID   string `json:"contact_id"`
	TaskKey     string `json:"task_key"`
	CampaignID  string `json:"campaign_id"`
	ConsentID   string `json:"consent_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Mode        string `json:"mode"`
	Simulation  bool   `json:"simulation"`
	State       string `json:"state"`
	OperatorID  string `json:"operator_id"`
	BudgetCents *int   `json:"budget_cents"`
	CreatedAt   string `json:"created_at"`
}

// OutboundReceipt is one call fact. Real connect and dial success stay 0.
type OutboundReceipt struct {
	ID                  string `json:"id"`
	TenantID            string `json:"tenant_id"`
	TaskID              string `json:"task_id"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	Simulation          bool   `json:"simulation"`
	RealConnected       bool   `json:"real_connected"`
	DialSucceeded       bool   `json:"dial_succeeded"`
	EmptyNumberDetected bool   `json:"empty_number_detected"`
	ProviderHTTP        int    `json:"provider_http"`
	Refusal             string `json:"refusal,omitempty"`
	CreatedAt           string `json:"created_at"`
}

// OutboundEvent is the public wording. It must not carry a recording or a phone.
type OutboundEvent struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	TaskID    string `json:"task_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) GetOutboundPolicy(tenantID string) (OutboundPolicy, error) {
	var p OutboundPolicy
	var auto, stop int
	err := s.DB.QueryRow(`
		SELECT tenant_id, production_auto, global_stop, window_start, window_end, updated_by, updated_at
		FROM outbound_policy WHERE tenant_id=?`, tenantID).Scan(
		&p.TenantID, &auto, &stop, &p.WindowStart, &p.WindowEnd, &p.UpdatedBy, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return OutboundPolicy{TenantID: tenantID, WindowStart: 9, WindowEnd: 20}, nil
	}
	if err != nil {
		return OutboundPolicy{}, err
	}
	p.ProductionAuto = false
	p.GlobalStop = stop == 1
	return p, nil
}

func (s *Store) SaveOutboundPolicy(p OutboundPolicy) error {
	p.ProductionAuto = false
	_, err := s.DB.Exec(`
		INSERT INTO outbound_policy(tenant_id, production_auto, global_stop, window_start, window_end, updated_by, updated_at)
		VALUES(?,0,?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			production_auto=0,
			window_start=excluded.window_start,
			window_end=excluded.window_end,
			updated_by=excluded.updated_by,
			updated_at=excluded.updated_at`,
		p.TenantID, boolInt(p.GlobalStop), p.WindowStart, p.WindowEnd, p.UpdatedBy, now())
	return err
}

func (s *Store) SetOutboundGlobalStop(tenantID, memberID string) error {
	current, err := s.GetOutboundPolicy(tenantID)
	if err != nil {
		return err
	}
	if current.UpdatedBy == "" {
		current.WindowStart, current.WindowEnd = 9, 20
	}
	_, err = s.DB.Exec(`
		INSERT INTO outbound_policy(tenant_id, production_auto, global_stop, window_start, window_end, updated_by, updated_at)
		VALUES(?,0,1,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET global_stop=1, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		tenantID, current.WindowStart, current.WindowEnd, memberID, now())
	return err
}

func (s *Store) AddOutboundStop(stop OutboundStop) (OutboundStop, error) {
	stop.ID = newID("obs_")
	stop.CreatedAt = now()
	_, err := s.DB.Exec(`
		INSERT INTO outbound_stops(id, tenant_id, contact_id, kind, active, created_by, created_at)
		VALUES(?,?,?,?,1,?,?)`,
		stop.ID, stop.TenantID, stop.ContactID, stop.Kind, stop.CreatedBy, stop.CreatedAt)
	return stop, err
}

func (s *Store) ListOutboundStops(tenantID, contactID string) ([]OutboundStop, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, contact_id, kind, created_by, created_at
		FROM outbound_stops WHERE tenant_id=? AND active=1 AND (contact_id='' OR contact_id=?)`, tenantID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundStop
	for rows.Next() {
		var stop OutboundStop
		if err := rows.Scan(&stop.ID, &stop.TenantID, &stop.ContactID, &stop.Kind, &stop.CreatedBy, &stop.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, stop)
	}
	return out, rows.Err()
}

func (s *Store) GetOutboundTaskByKey(tenantID, taskKey string) (OutboundTask, error) {
	row := s.DB.QueryRow(`SELECT `+outboundTaskCols+` FROM outbound_tasks WHERE tenant_id=? AND task_key=?`, tenantID, taskKey)
	return scanOutboundTask(row)
}

func (s *Store) GetOutboundTask(tenantID, id string) (OutboundTask, error) {
	row := s.DB.QueryRow(`SELECT `+outboundTaskCols+` FROM outbound_tasks WHERE tenant_id=? AND id=?`, tenantID, id)
	return scanOutboundTask(row)
}

func (s *Store) ListOutboundReceipts(tenantID, taskID string) ([]OutboundReceipt, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, task_id, kind, status, simulation, real_connected, dial_succeeded,
		       empty_number_detected, provider_http, refusal, created_at
		FROM outbound_receipts WHERE tenant_id=? AND task_id=? ORDER BY created_at`, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundReceipt
	for rows.Next() {
		rec, err := scanOutboundReceipt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) ListOutboundEvents(tenantID, taskID string) ([]OutboundEvent, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, task_id, body, created_at
		FROM outbound_public_events WHERE tenant_id=? AND task_id=? ORDER BY created_at`, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundEvent
	for rows.Next() {
		var ev OutboundEvent
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.TaskID, &ev.Body, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// InsertOutboundAttempt stores a new task plus its first facts.
// A duplicate task key returns the existing task and replay=true without new receipts.
func (s *Store) InsertOutboundAttempt(task OutboundTask, receipts []OutboundReceipt, eventBody, recording string) (OutboundTask, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return OutboundTask{}, false, err
	}
	defer tx.Rollback()
	existing, err := scanOutboundTask(tx.QueryRow(`SELECT `+outboundTaskCols+` FROM outbound_tasks WHERE tenant_id=? AND task_key=?`, task.TenantID, task.TaskKey))
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return OutboundTask{}, false, err
	}
	task.ID = newID("obk_")
	task.CreatedAt = now()
	var budget any
	if task.BudgetCents != nil {
		budget = *task.BudgetCents
	}
	_, err = tx.Exec(`
		INSERT INTO outbound_tasks(id, tenant_id, contact_id, task_key, campaign_id, consent_id, session_id, mode, simulation, state, operator_id, budget_cents, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		task.ID, task.TenantID, task.ContactID, task.TaskKey, task.CampaignID, task.ConsentID, task.SessionID,
		task.Mode, boolInt(task.Simulation), task.State, task.OperatorID, budget, task.CreatedAt)
	if err != nil {
		return OutboundTask{}, false, err
	}
	if err := insertOutboundFactsTx(tx, task.TenantID, task.ID, receipts, eventBody, recording); err != nil {
		return OutboundTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OutboundTask{}, false, err
	}
	return task, false, nil
}

// AppendOutboundFacts adds receipts to an existing task and optionally moves its state.
func (s *Store) AppendOutboundFacts(tenantID, taskID, state string, receipts []OutboundReceipt, eventBody, recording string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if state != "" {
		if _, err := tx.Exec(`UPDATE outbound_tasks SET state=? WHERE tenant_id=? AND id=?`, state, tenantID, taskID); err != nil {
			return err
		}
	}
	if err := insertOutboundFactsTx(tx, tenantID, taskID, receipts, eventBody, recording); err != nil {
		return err
	}
	return tx.Commit()
}

func insertOutboundFactsTx(tx *sql.Tx, tenantID, taskID string, receipts []OutboundReceipt, eventBody, recording string) error {
	for i := range receipts {
		receipts[i].ID = newID("obr_")
		receipts[i].TenantID = tenantID
		receipts[i].TaskID = taskID
		receipts[i].CreatedAt = now()
		receipts[i].RealConnected = false
		receipts[i].DialSucceeded = false
		receipts[i].EmptyNumberDetected = false
		if _, err := tx.Exec(`
			INSERT INTO outbound_receipts(id, tenant_id, task_id, kind, status, simulation, real_connected, dial_succeeded, empty_number_detected, provider_http, refusal, created_at)
			VALUES(?,?,?,?,?,?,0,0,0,?,?,?)`,
			receipts[i].ID, tenantID, taskID, receipts[i].Kind, receipts[i].Status, boolInt(receipts[i].Simulation),
			receipts[i].ProviderHTTP, receipts[i].Refusal, receipts[i].CreatedAt); err != nil {
			return err
		}
	}
	if eventBody != "" {
		if _, err := tx.Exec(`INSERT INTO outbound_public_events(id, tenant_id, task_id, body, created_at) VALUES(?,?,?,?,?)`,
			newID("obe_"), tenantID, taskID, eventBody, now()); err != nil {
			return err
		}
	}
	if recording != "" {
		if _, err := tx.Exec(`INSERT INTO outbound_recordings(id, tenant_id, task_id, content, created_at) VALUES(?,?,?,?,?)`,
			newID("obrec_"), tenantID, taskID, recording, now()); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO outbound_usage(id, tenant_id, task_id, cost_known, cost_cents, live_charge, created_at) VALUES(?,?,?,0,NULL,0,?)`,
		newID("obu_"), tenantID, taskID, now())
	return err
}

const outboundTaskCols = `id, tenant_id, contact_id, task_key, campaign_id, consent_id, session_id, mode, simulation, state, operator_id, budget_cents, created_at`

func scanOutboundTask(sc interface{ Scan(...any) error }) (OutboundTask, error) {
	var task OutboundTask
	var simulation int
	var budget sql.NullInt64
	err := sc.Scan(&task.ID, &task.TenantID, &task.ContactID, &task.TaskKey, &task.CampaignID, &task.ConsentID, &task.SessionID,
		&task.Mode, &simulation, &task.State, &task.OperatorID, &budget, &task.CreatedAt)
	if err != nil {
		return OutboundTask{}, err
	}
	task.Simulation = simulation == 1
	if budget.Valid {
		v := int(budget.Int64)
		task.BudgetCents = &v
	}
	return task, nil
}

func scanOutboundReceipt(sc interface{ Scan(...any) error }) (OutboundReceipt, error) {
	var rec OutboundReceipt
	var simulation, real, dial, empty int
	err := sc.Scan(&rec.ID, &rec.TenantID, &rec.TaskID, &rec.Kind, &rec.Status, &simulation, &real, &dial, &empty, &rec.ProviderHTTP, &rec.Refusal, &rec.CreatedAt)
	if err != nil {
		return OutboundReceipt{}, err
	}
	rec.Simulation = simulation == 1
	rec.RealConnected = false
	rec.DialSucceeded = false
	rec.EmptyNumberDetected = false
	return rec, nil
}
