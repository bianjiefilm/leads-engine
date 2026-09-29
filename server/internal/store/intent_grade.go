package store

import (
	"encoding/json"
	"errors"
	"strings"
)

// IntentCitation quotes one evidence row on a stored grade.
type IntentCitation struct {
	EvidenceID string `json:"evidence_id"`
	Excerpt    string `json:"excerpt"`
	At         string `json:"at,omitempty"`
}

// IntentSuggestion is the next step handed to the sales desk.
type IntentSuggestion struct {
	ForTicket             string `json:"for_ticket"`
	Kind                  string `json:"kind"`
	Label                 string `json:"label"`
	AutoCall              bool   `json:"auto_call"`
	AutoSMS               bool   `json:"auto_sms"`
	AutoGroup             bool   `json:"auto_group"`
	CreateOrder           bool   `json:"create_order"`
	ContactDecidedByScore bool   `json:"contact_decided_by_score"`
}

// IntentFact is a sales-confirmed field kept on the snapshot.
type IntentFact struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// IntentSnapshot is one scored or corrected grade for a lead or session.
type IntentSnapshot struct {
	ID               string           `json:"id"`
	TenantID         string           `json:"tenant_id"`
	SubjectKind      string           `json:"subject_kind"`
	SubjectID        string           `json:"subject_id"`
	Grade            string           `json:"grade"`
	Reason           string           `json:"reason"`
	Citations        []IntentCitation `json:"citations"`
	Missing          []string         `json:"missing_fields"`
	RuleVersion      string           `json:"rule_version"`
	ModelVersion     string           `json:"model_version"`
	Calibrated       bool             `json:"calibrated"`
	AssessedAt       string           `json:"assessed_at"`
	FreshUntil       string           `json:"fresh_until"`
	Stale            bool             `json:"stale"`
	StaleReason      string           `json:"stale_reason,omitempty"`
	Suggestion       IntentSuggestion `json:"suggestion"`
	HumanLocked      bool             `json:"human_locked"`
	HumanDisposition string           `json:"human_disposition,omitempty"`
	HumanReason      string           `json:"human_reason,omitempty"`
	Misjudgment      bool             `json:"misjudgment"`
	PreservedFacts   []IntentFact     `json:"preserved_facts,omitempty"`
	Disclaimer       string           `json:"disclaimer"`
	Fingerprint      string           `json:"-"`
	EvidenceJSON     string           `json:"-"`
	CreatedBy        string           `json:"created_by"`
	CreatedAt        string           `json:"created_at"`
}

const intentSnapshotCols = `id,tenant_id,subject_kind,subject_id,grade,reason,citations_json,missing_json,suggestion_json,rule_version,model_version,calibrated,assessed_at,fresh_until,stale,stale_reason,human_locked,human_disposition,human_reason,misjudgment,facts_json,disclaimer,fingerprint,evidence_json,created_by,created_at`

// InsertIntentSnapshot stores one grade. calibrated is always written as 0.
func (s *Store) InsertIntentSnapshot(row IntentSnapshot) (IntentSnapshot, error) {
	if row.ID == "" {
		row.ID = newID("igs_")
	}
	row.CreatedAt = now()
	if row.Citations == nil {
		row.Citations = []IntentCitation{}
	}
	if row.Missing == nil {
		row.Missing = []string{}
	}
	if row.PreservedFacts == nil {
		row.PreservedFacts = []IntentFact{}
	}
	citations, err := json.Marshal(row.Citations)
	if err != nil {
		return IntentSnapshot{}, err
	}
	missing, err := json.Marshal(row.Missing)
	if err != nil {
		return IntentSnapshot{}, err
	}
	suggestion, err := json.Marshal(row.Suggestion)
	if err != nil {
		return IntentSnapshot{}, err
	}
	facts, err := json.Marshal(row.PreservedFacts)
	if err != nil {
		return IntentSnapshot{}, err
	}
	if row.EvidenceJSON == "" {
		row.EvidenceJSON = "[]"
	}
	_, err = s.DB.Exec(`INSERT INTO intent_grade_snapshots(`+intentSnapshotCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID, row.TenantID, row.SubjectKind, row.SubjectID, row.Grade, row.Reason, string(citations), string(missing), string(suggestion),
		row.RuleVersion, row.ModelVersion, 0, row.AssessedAt, row.FreshUntil, boolInt(row.Stale), row.StaleReason, boolInt(row.HumanLocked),
		row.HumanDisposition, row.HumanReason, boolInt(row.Misjudgment), string(facts), row.Disclaimer, row.Fingerprint, row.EvidenceJSON,
		row.CreatedBy, row.CreatedAt)
	if err != nil {
		return IntentSnapshot{}, err
	}
	return s.GetIntentSnapshot(row.TenantID, row.ID)
}

// GetIntentSnapshot loads one snapshot in the tenant.
func (s *Store) GetIntentSnapshot(tenantID, id string) (IntentSnapshot, error) {
	row := s.DB.QueryRow(`SELECT `+intentSnapshotCols+` FROM intent_grade_snapshots WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanIntentSnapshot(row)
}

// LatestIntentSnapshot is the newest grade for a subject, stale or not.
func (s *Store) LatestIntentSnapshot(tenantID, kind, subjectID string) (IntentSnapshot, error) {
	row := s.DB.QueryRow(`SELECT `+intentSnapshotCols+` FROM intent_grade_snapshots WHERE tenant_id=? AND subject_kind=? AND subject_id=? ORDER BY created_at DESC LIMIT 1`, tenantID, kind, subjectID)
	return scanIntentSnapshot(row)
}

// SupersedeIntentSnapshots marks every older fresh snapshot for the subject stale.
// The snapshot just written stays current, so two live conclusions cannot coexist.
func (s *Store) SupersedeIntentSnapshots(tenantID, kind, subjectID, keepID, reason string) error {
	_, err := s.DB.Exec(`UPDATE intent_grade_snapshots SET stale=1, stale_reason=? WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND id<>? AND stale=0`,
		reason, tenantID, kind, subjectID, keepID)
	return err
}

// EnsureIntentUsage inserts the single metering row for a subject.
// A recompute hits the unique key and does not add units or live_charge.
func (s *Store) EnsureIntentUsage(tenantID, kind, subjectID string) error {
	_, err := s.DB.Exec(`INSERT INTO intent_grade_usage(id,tenant_id,subject_kind,subject_id,units,live_charge,created_at) VALUES(?,?,?,?,1,0,?)
		ON CONFLICT(tenant_id, subject_kind, subject_id) DO NOTHING`,
		newID("igu_"), tenantID, kind, subjectID, now())
	return err
}

// CountIntentUsage returns rows, unit sum, and max live_charge for one subject.
func (s *Store) CountIntentUsage(tenantID, kind, subjectID string) (rows, units, live int, err error) {
	err = s.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(units),0), COALESCE(MAX(live_charge),0) FROM intent_grade_usage WHERE tenant_id=? AND subject_kind=? AND subject_id=?`,
		tenantID, kind, subjectID).Scan(&rows, &units, &live)
	return rows, units, live, err
}

func scanIntentSnapshot(sc interface{ Scan(...any) error }) (IntentSnapshot, error) {
	var row IntentSnapshot
	var citations, missing, suggestion, facts string
	var calibrated, stale, locked, misjudgment int
	err := sc.Scan(&row.ID, &row.TenantID, &row.SubjectKind, &row.SubjectID, &row.Grade, &row.Reason,
		&citations, &missing, &suggestion, &row.RuleVersion, &row.ModelVersion, &calibrated,
		&row.AssessedAt, &row.FreshUntil, &stale, &row.StaleReason, &locked, &row.HumanDisposition,
		&row.HumanReason, &misjudgment, &facts, &row.Disclaimer, &row.Fingerprint, &row.EvidenceJSON,
		&row.CreatedBy, &row.CreatedAt)
	if err != nil {
		return IntentSnapshot{}, err
	}
	if err := json.Unmarshal([]byte(citations), &row.Citations); err != nil {
		return IntentSnapshot{}, err
	}
	if err := json.Unmarshal([]byte(missing), &row.Missing); err != nil {
		return IntentSnapshot{}, err
	}
	if err := json.Unmarshal([]byte(suggestion), &row.Suggestion); err != nil {
		return IntentSnapshot{}, err
	}
	if facts == "" {
		facts = "[]"
	}
	if err := json.Unmarshal([]byte(facts), &row.PreservedFacts); err != nil {
		return IntentSnapshot{}, err
	}
	row.Calibrated = calibrated != 0
	row.Stale = stale != 0
	row.HumanLocked = locked != 0
	row.Misjudgment = misjudgment != 0
	if row.Citations == nil {
		row.Citations = []IntentCitation{}
	}
	if row.Missing == nil {
		row.Missing = []string{}
	}
	return row, nil
}

// ErrIntentNotFound is reserved for callers that want a stable miss.
var ErrIntentNotFound = errors.New("intent snapshot not found")

// IntentModelAttempt is the local receipt for one subject.
// It never stores a model grade, task id, cost, or conclusion.
type IntentModelAttempt struct {
	ID             string
	TenantID       string
	SubjectKind    string
	SubjectID      string
	AttemptState   string
	BillingVerdict string
	TaskID         string
	CostCents      int
	Conclusion     string
	CreatedAt      string
	UpdatedAt      string
}

// SaveIntentModelAttempt inserts the single receipt for a subject.
// A repeated subject does not add a row. PASS, a task id, a cost, or a conclusion is refused.
func (s *Store) SaveIntentModelAttempt(row IntentModelAttempt) (IntentModelAttempt, error) {
	if row.BillingVerdict != "not_completed" || row.CostCents != 0 || strings.TrimSpace(row.TaskID) != "" || strings.TrimSpace(row.Conclusion) != "" {
		return IntentModelAttempt{}, errors.New("intent model attempt cannot store a model conclusion")
	}
	switch row.AttemptState {
	case "missing_credentials", "unknown", "recovered":
	default:
		return IntentModelAttempt{}, errors.New("intent model attempt state refused")
	}
	if row.SubjectKind != "lead" && row.SubjectKind != "session" {
		return IntentModelAttempt{}, errors.New("intent model attempt subject refused")
	}
	if row.ID == "" {
		row.ID = newID("ima_")
	}
	stamp := now()
	row.CreatedAt = stamp
	row.UpdatedAt = stamp
	_, err := s.DB.Exec(`INSERT INTO intent_model_attempts(id,tenant_id,subject_kind,subject_id,attempt_state,billing_verdict,task_id,cost_cents,conclusion,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id, subject_kind, subject_id) DO NOTHING`,
		row.ID, row.TenantID, row.SubjectKind, row.SubjectID, row.AttemptState, "not_completed", "", 0, "", stamp, stamp)
	if err != nil {
		return IntentModelAttempt{}, err
	}
	return s.GetIntentModelAttempt(row.TenantID, row.SubjectKind, row.SubjectID)
}

// GetIntentModelAttempt loads the one receipt for a subject.
func (s *Store) GetIntentModelAttempt(tenantID, kind, subjectID string) (IntentModelAttempt, error) {
	var row IntentModelAttempt
	err := s.DB.QueryRow(`SELECT id,tenant_id,subject_kind,subject_id,attempt_state,billing_verdict,task_id,cost_cents,conclusion,created_at,updated_at
		FROM intent_model_attempts WHERE tenant_id=? AND subject_kind=? AND subject_id=?`, tenantID, kind, subjectID).Scan(
		&row.ID, &row.TenantID, &row.SubjectKind, &row.SubjectID, &row.AttemptState, &row.BillingVerdict,
		&row.TaskID, &row.CostCents, &row.Conclusion, &row.CreatedAt, &row.UpdatedAt)
	return row, err
}

// MarkIntentModelAttemptRecovered closes an unknown receipt without keeping a model result.
func (s *Store) MarkIntentModelAttemptRecovered(tenantID, kind, subjectID string) (IntentModelAttempt, error) {
	_, err := s.DB.Exec(`UPDATE intent_model_attempts
		SET attempt_state='recovered', billing_verdict='not_completed', task_id='', cost_cents=0, conclusion='', updated_at=?
		WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND attempt_state='unknown'`,
		now(), tenantID, kind, subjectID)
	if err != nil {
		return IntentModelAttempt{}, err
	}
	return s.GetIntentModelAttempt(tenantID, kind, subjectID)
}
