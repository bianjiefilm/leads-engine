// HUI-1684 意向分级 HTTP 面。FEATURE_INTENT_GRADE 默认 off 时路由不注册。
// 只对当前租户有权读取的线索或会话评分。用量按主体记一行，live_charge 恒为 0。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/intentgrade"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) mountIntentGrade(mux *http.ServeMux) {
	if !s.Cfg.FeatureIntentGrade {
		return
	}
	mux.Handle("POST /api/v1/intent-grades", s.requireSession(s.handleIntentGradeScore))
	mux.Handle("GET /api/v1/intent-grades/sample-report", s.requireSession(s.handleIntentGradeSampleReport))
	mux.Handle("GET /api/v1/intent-grades/{id}", s.requireSession(s.handleIntentGradeGet))
	mux.Handle("POST /api/v1/intent-grades/{id}/corrections", s.requireSession(s.handleIntentGradeCorrect))
}

type intentEvidenceIn struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	At        string `json:"at"`
	Retracted bool   `json:"retracted"`
}

func (s *Server) handleIntentGradeSampleReport(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	report := intentgrade.EvaluateLabeled(intentgrade.LabeledSamples())
	outcomes := make([]map[string]any, 0, len(report.Outcomes))
	for _, outcome := range report.Outcomes {
		outcomes = append(outcomes, intentOutcomeJSON(outcome))
	}
	misses := make([]map[string]any, 0, len(report.Misjudgments))
	for _, outcome := range report.Misjudgments {
		misses = append(misses, intentOutcomeJSON(outcome))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rule_version":                 report.RuleVersion,
		"model_version":                report.ModelVersion,
		"disclaimer":                   report.Disclaimer,
		"real_person_close_prediction": report.RealPersonClosePrediction,
		"sample_count":                 report.SampleCount,
		"misjudgment_count":            report.MisjudgmentCount,
		"outcomes":                     outcomes,
		"misjudgments":                 misses,
	})
}

func intentOutcomeJSON(outcome intentgrade.Outcome) map[string]any {
	return map[string]any{
		"id":           outcome.ID,
		"kind":         outcome.Kind,
		"label":        outcome.Label,
		"predicted":    outcome.Predicted,
		"match":        outcome.Match,
		"reason":       outcome.Reason,
		"suggestion":   outcome.Suggestion,
		"rule_version": outcome.RuleVersion,
		"refusal":      outcome.Refusal,
	}
}

func (s *Server) handleIntentGradeGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	row, err := s.St.GetIntentSnapshot(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intent grade lookup failed")
		return
	}
	if !s.intentSubjectAllowed(w, r, c, row.SubjectKind, row.SubjectID, authz.ActionReadRecord) {
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) handleIntentGradeScore(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	var in struct {
		SubjectKind string             `json:"subject_kind"`
		SubjectID   string             `json:"subject_id"`
		Evidence    []intentEvidenceIn `json:"evidence"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !s.intentSubjectAllowed(w, r, c, in.SubjectKind, in.SubjectID, authz.ActionReadRecord) {
		return
	}
	evidence, ok := intentEvidence(w, in.Evidence)
	if !ok {
		return
	}
	latest, err := s.St.LatestIntentSnapshot(c.Member.TenantID, in.SubjectKind, in.SubjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "intent grade lookup failed")
		return
	}
	input := intentgrade.Input{
		TenantID: c.Member.TenantID, SubjectKind: in.SubjectKind, SubjectID: in.SubjectID,
		Now: time.Now().UTC(), Evidence: evidence,
	}
	if err == nil {
		input.Prior = intentPrior(latest)
		if latest.HumanLocked {
			input.Human = humanFromSnapshot(latest)
		}
	}
	s.finishIntentScore(w, c, input, evidence)
}

func (s *Server) handleIntentGradeCorrect(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	current, err := s.St.GetIntentSnapshot(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intent grade lookup failed")
		return
	}
	if !s.intentSubjectAllowed(w, r, c, current.SubjectKind, current.SubjectID, authz.ActionUpdate) {
		return
	}
	var in struct {
		Grade       string `json:"grade"`
		Misjudgment bool   `json:"misjudgment"`
		Disposition string `json:"disposition"`
		Reason      string `json:"reason"`
		Facts       []struct {
			Field string `json:"field"`
			Value string `json:"value"`
		} `json:"facts"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	facts := make([]intentgrade.ConfirmedFact, 0, len(in.Facts))
	for _, fact := range in.Facts {
		facts = append(facts, intentgrade.ConfirmedFact{Field: fact.Field, Value: fact.Value})
	}
	var stored []intentEvidenceIn
	if err := json.Unmarshal([]byte(current.EvidenceJSON), &stored); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stored evidence is unreadable")
		return
	}
	evidence, ok := intentEvidence(w, stored)
	if !ok {
		return
	}
	latest, latestErr := s.St.LatestIntentSnapshot(c.Member.TenantID, current.SubjectKind, current.SubjectID)
	if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "intent grade lookup failed")
		return
	}
	if latestErr == nil && latest.ID != current.ID {
		fail(w, http.StatusConflict, "not_latest", "只能修正当前这条分级")
		return
	}
	input := intentgrade.Input{
		TenantID: c.Member.TenantID, SubjectKind: current.SubjectKind, SubjectID: current.SubjectID,
		Now: time.Now().UTC(), Evidence: evidence,
		Human: &intentgrade.HumanConfirmation{
			Grade: in.Grade, Misjudgment: in.Misjudgment, Disposition: in.Disposition, Reason: in.Reason, Facts: facts,
		},
		Prior: intentPrior(current), Correction: true,
	}
	s.finishIntentScore(w, c, input, evidence)
}

func (s *Server) finishIntentScore(w http.ResponseWriter, c *caller, input intentgrade.Input, evidence []intentgrade.Evidence) {
	res := intentgrade.Score(input)
	if res.Refusal != "" {
		fail(w, http.StatusUnprocessableEntity, res.Refusal, res.Reason)
		return
	}
	if input.Prior != nil && input.Prior.Fingerprint == res.Fingerprint && !res.PriorStale {
		rows, units, live, err := s.St.CountIntentUsage(input.TenantID, input.SubjectKind, input.SubjectID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "intent usage lookup failed")
			return
		}
		current, err := s.St.LatestIntentSnapshot(input.TenantID, input.SubjectKind, input.SubjectID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "intent grade lookup failed")
			return
		}
		writeIntentScore(w, http.StatusOK, current, false, "", rows, units, live)
		return
	}
	rawEvidence, err := json.Marshal(evidenceView(evidence))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "evidence encode failed")
		return
	}
	row := store.IntentSnapshot{
		TenantID: input.TenantID, SubjectKind: input.SubjectKind, SubjectID: input.SubjectID,
		Grade: res.Grade, Reason: res.Reason, Missing: res.Missing,
		RuleVersion: res.RuleVersion, ModelVersion: res.ModelVersion, Calibrated: false,
		AssessedAt: res.AssessedAt.UTC().Format(time.RFC3339Nano),
		FreshUntil: res.FreshUntil.UTC().Format(time.RFC3339Nano),
		Suggestion: store.IntentSuggestion{
			ForTicket: res.Suggestion.ForTicket, Kind: res.Suggestion.Kind, Label: res.Suggestion.Label,
			AutoCall: false, AutoSMS: false, AutoGroup: false, CreateOrder: false, ContactDecidedByScore: false,
		},
		HumanLocked: res.HumanLocked, Disclaimer: res.Disclaimer, Fingerprint: res.Fingerprint,
		EvidenceJSON: string(rawEvidence), CreatedBy: c.Member.ID,
	}
	for _, cite := range res.Citations {
		row.Citations = append(row.Citations, store.IntentCitation{EvidenceID: cite.EvidenceID, Excerpt: cite.Excerpt, At: cite.At})
	}
	if res.Human != nil {
		row.HumanDisposition = res.Human.Disposition
		row.HumanReason = strings.TrimSpace(res.Human.Reason)
		row.Misjudgment = res.Human.Misjudgment
		for _, fact := range res.PreservedFacts {
			row.PreservedFacts = append(row.PreservedFacts, store.IntentFact{Field: fact.Field, Value: fact.Value})
		}
	}
	saved, err := s.St.InsertIntentSnapshot(row)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intent grade save failed")
		return
	}
	if res.PriorStale {
		if err := s.St.SupersedeIntentSnapshots(input.TenantID, input.SubjectKind, input.SubjectID, saved.ID, res.PriorStaleReason); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "intent grade stale mark failed")
			return
		}
	}
	if err := s.St.EnsureIntentUsage(input.TenantID, input.SubjectKind, input.SubjectID); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intent usage save failed")
		return
	}
	rows, units, live, err := s.St.CountIntentUsage(input.TenantID, input.SubjectKind, input.SubjectID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "intent usage lookup failed")
		return
	}
	writeIntentScore(w, http.StatusCreated, saved, res.PriorStale, res.PriorStaleReason, rows, units, live)
}

func writeIntentScore(w http.ResponseWriter, status int, snap store.IntentSnapshot, priorStale bool, priorReason string, rows, units, live int) {
	writeJSON(w, status, map[string]any{
		"snapshot":           snap,
		"prior_stale":        priorStale,
		"prior_stale_reason": priorReason,
		"usage":              map[string]any{"rows": rows, "units": units, "live_charge": live},
	})
}

func (s *Server) intentSubjectAllowed(w http.ResponseWriter, r *http.Request, c *caller, kind, id string, action authz.Action) bool {
	if id == "" || (kind != "lead" && kind != "session") {
		fail(w, http.StatusBadRequest, "bad_request", "subject_kind must be lead or session and subject_id is required")
		return false
	}
	var assignee, tenant string
	switch kind {
	case "lead":
		rec, err := s.St.GetLead(id, c.Member.TenantID)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "not_found", "record not found")
			return false
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
			return false
		}
		assignee, tenant = rec.AssignedMemberID, rec.TenantID
	case "session":
		rec, err := s.St.GetReceptionSession(c.Member.TenantID, id)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "not_found", "record not found")
			return false
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return false
		}
		assignee, tenant = rec.OwnerMemberID, rec.TenantID
	}
	return s.requireAction(c, action, authz.RecordScope{TenantID: tenant, AssigneeMemberID: assignee}, w)
}

func intentEvidence(w http.ResponseWriter, in []intentEvidenceIn) ([]intentgrade.Evidence, bool) {
	out := make([]intentgrade.Evidence, 0, len(in))
	for _, item := range in {
		if strings.TrimSpace(item.ID) == "" {
			fail(w, http.StatusBadRequest, "bad_request", "evidence id is required")
			return nil, false
		}
		var at time.Time
		if item.At != "" {
			parsed, err := time.Parse(time.RFC3339, item.At)
			if err != nil {
				fail(w, http.StatusBadRequest, "bad_request", "evidence at must be RFC3339")
				return nil, false
			}
			at = parsed
		}
		tenant := strings.TrimSpace(item.TenantID)
		kind := item.Kind
		if kind == "" {
			kind = "message"
		}
		out = append(out, intentgrade.Evidence{
			ID: item.ID, TenantID: tenant, Kind: kind, Text: item.Text, At: at, Retracted: item.Retracted,
		})
	}
	return out, true
}

func intentPrior(row store.IntentSnapshot) *intentgrade.SnapshotRef {
	fresh, err := time.Parse(time.RFC3339Nano, row.FreshUntil)
	if err != nil {
		fresh, _ = time.Parse(time.RFC3339, row.FreshUntil)
	}
	return &intentgrade.SnapshotRef{
		ID: row.ID, Fingerprint: row.Fingerprint, FreshUntil: fresh, HumanLocked: row.HumanLocked,
	}
}

func humanFromSnapshot(row store.IntentSnapshot) *intentgrade.HumanConfirmation {
	facts := make([]intentgrade.ConfirmedFact, 0, len(row.PreservedFacts))
	for _, fact := range row.PreservedFacts {
		facts = append(facts, intentgrade.ConfirmedFact{Field: fact.Field, Value: fact.Value})
	}
	return &intentgrade.HumanConfirmation{
		Grade: row.Grade, Misjudgment: row.Misjudgment, Disposition: row.HumanDisposition,
		Reason: row.HumanReason, Facts: facts,
	}
}

func evidenceView(rows []intentgrade.Evidence) []intentEvidenceIn {
	out := make([]intentEvidenceIn, 0, len(rows))
	for _, row := range rows {
		at := ""
		if !row.At.IsZero() {
			at = row.At.UTC().Format(time.RFC3339)
		}
		out = append(out, intentEvidenceIn{
			ID: row.ID, TenantID: row.TenantID, Kind: row.Kind, Text: row.Text, At: at, Retracted: row.Retracted,
		})
	}
	return out
}
