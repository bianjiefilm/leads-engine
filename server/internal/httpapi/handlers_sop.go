// HUI-1689 跟进提醒、回复草稿和人工确认。FEATURE_SOP_REACH 默认 off。
// 无人值守不因余额或 AI 分数打开。没有真实渠道回执时不写已送达，live_charge 恒为 0。
// 不调用短信、邮件或企微，也不新建会话和知识库。
package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/sopreach"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) mountSOP(mux *http.ServeMux) {
	if !s.Cfg.FeatureSOPReach {
		return
	}
	mux.Handle("GET /api/v1/sop/capability", s.requireSession(s.handleSOPCapability))
	mux.Handle("GET /api/v1/sop/actions", s.requireSession(s.handleSOPList))
	mux.Handle("POST /api/v1/sop/policy", s.requireSession(s.handleSOPPolicy))
	mux.Handle("POST /api/v1/sop/stops", s.requireSession(s.handleSOPStop))
	mux.Handle("POST /api/v1/sop/reminders", s.requireSession(s.handleSOPRemind))
	mux.Handle("POST /api/v1/sop/drafts", s.requireSession(s.handleSOPDraft))
	mux.Handle("POST /api/v1/sop/drafts/{id}/confirm", s.requireSession(s.handleSOPConfirm))
	mux.Handle("POST /api/v1/sop/drafts/{id}/retry", s.requireSession(s.handleSOPRetry))
	mux.Handle("POST /api/v1/sop/replies", s.requireSession(s.handleSOPReply))
	mux.Handle("POST /api/v1/sop/auto", s.requireSession(s.handleSOPAuto))
}

type sopBody struct {
	ContactID          string `json:"contact_id"`
	SessionID          string `json:"session_id"`
	Channel            string `json:"channel"`
	Recipient          string `json:"recipient"`
	Purpose            string `json:"purpose"`
	ConsentID          string `json:"consent_id"`
	ContentVersion     int    `json:"content_version"`
	BudgetCents        *int   `json:"budget_cents"`
	ParentID           string `json:"parent_id"`
	Body               string `json:"body"`
	BalanceCents       int    `json:"balance_cents"`
	AIScore            int    `json:"ai_score"`
	TimedOut           bool   `json:"timed_out"`
	OriginalReconciled bool   `json:"original_reconciled"`
}

func (s *Server) handleSOPCapability(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	policy, err := s.St.GetSOPPolicy(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"level":         policy.Level,
		"unattended":    policy.Explicit && policy.Level == sopreach.LevelUnattended,
		"verification":  "unverified",
		"live_channels": []string{},
		"live_charge":   0,
		"global_stop":   policy.GlobalStop,
		"note":          "没有真实短信、邮件或企微渠道。人工确认后停在待发送或未送达，不会标成已送达。",
	})
}

func (s *Server) handleSOPPolicy(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageReception, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Level       string `json:"level"`
		Explicit    bool   `json:"explicit_unattended"`
		WindowStart int    `json:"window_start"`
		WindowEnd   int    `json:"window_end"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !validSOPWindow(in.WindowStart, in.WindowEnd) {
		fail(w, http.StatusBadRequest, "bad_request", "window_start and window_end must describe a Shanghai hour range")
		return
	}
	policy := store.SOPPolicy{
		TenantID: c.Member.TenantID, Level: in.Level, Explicit: in.Explicit,
		WindowStart: in.WindowStart, WindowEnd: in.WindowEnd, UpdatedBy: c.Member.ID,
	}
	if err := s.St.SaveSOPPolicy(policy); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy save failed")
		return
	}
	saved, err := s.St.GetSOPPolicy(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"level": saved.Level, "explicit_unattended": saved.Explicit, "unattended": saved.Explicit && saved.Level == sopreach.LevelUnattended,
		"global_stop": saved.GlobalStop, "window_start": saved.WindowStart, "window_end": saved.WindowEnd,
	})
}

func validSOPWindow(start, end int) bool {
	if start == 0 && end == 24 {
		return true
	}
	return start >= 0 && start < end && end <= 24
}

func (s *Server) handleSOPStop(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageReception, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Kind      string `json:"kind"`
		ContactID string `json:"contact_id"`
		Channel   string `json:"channel"`
		Purpose   string `json:"purpose"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	switch in.Kind {
	case "unsubscribe", "customer_stop", "reject", "global_stop":
	default:
		fail(w, http.StatusBadRequest, "bad_request", "unknown stop kind")
		return
	}
	if in.Kind != "global_stop" {
		if _, ok := s.sopContact(w, r, c, in.ContactID); !ok {
			return
		}
	}
	if in.Kind == "global_stop" {
		if err := s.St.SetSOPGlobalStop(c.Member.TenantID, c.Member.ID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "global stop failed")
			return
		}
	}
	stop, err := s.St.AddSOPStop(store.SOPStop{
		TenantID: c.Member.TenantID, ContactID: in.ContactID, Channel: in.Channel,
		Purpose: in.Purpose, Kind: in.Kind, CreatedBy: c.Member.ID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stop save failed")
		return
	}
	s.Log.Printf("sop stop tenant=%s kind=%s contact=%s", c.Member.TenantID, stop.Kind, stop.ContactID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": stop.ID, "kind": stop.Kind})
}

func (s *Server) handleSOPList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	assignee := ""
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		assignee = c.Member.ID
	}
	items, err := s.St.ListSOPActions(c.Member.TenantID, r.URL.Query().Get("contact_id"), assignee)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "action list failed")
		return
	}
	if items == nil {
		items = []store.SOPAction{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleSOPRemind(w http.ResponseWriter, r *http.Request) {
	s.createSOP(w, r, sopreach.OpRemind)
}

func (s *Server) handleSOPDraft(w http.ResponseWriter, r *http.Request) {
	s.createSOP(w, r, sopreach.OpDraft)
}

func (s *Server) handleSOPReply(w http.ResponseWriter, r *http.Request) {
	s.createSOP(w, r, sopreach.OpUserReply)
}

func (s *Server) handleSOPAuto(w http.ResponseWriter, r *http.Request) {
	s.createSOP(w, r, sopreach.OpAuto)
}

func (s *Server) handleSOPConfirm(w http.ResponseWriter, r *http.Request) {
	s.actOnDraft(w, r, sopreach.OpConfirm)
}

func (s *Server) handleSOPRetry(w http.ResponseWriter, r *http.Request) {
	s.actOnDraft(w, r, sopreach.OpRetry)
}

func (s *Server) createSOP(w http.ResponseWriter, r *http.Request, op string) {
	c := callerFrom(r)
	var in sopBody
	if !decodeBody(w, r, &in) {
		return
	}
	contact, ok := s.sopContact(w, r, c, in.ContactID)
	if !ok {
		return
	}
	if in.ParentID != "" {
		parent, err := s.St.GetSOPAction(c.Member.TenantID, in.ParentID)
		if errors.Is(err, sql.ErrNoRows) || parent.ContactID != contact.ID {
			fail(w, http.StatusBadRequest, "bad_parent", "parent action is not in this tenant")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "parent lookup failed")
			return
		}
	}
	s.decideSOP(w, c, contact, in, op, http.StatusCreated)
}

func (s *Server) actOnDraft(w http.ResponseWriter, r *http.Request, op string) {
	c := callerFrom(r)
	var extra sopBody
	if !decodeBody(w, r, &extra) {
		return
	}
	draft, err := s.St.GetSOPAction(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "draft not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	if draft.Kind != sopreach.KindDraft {
		fail(w, http.StatusNotFound, "not_found", "draft not found")
		return
	}
	contact, ok := s.sopContact(w, r, c, draft.ContactID)
	if !ok {
		return
	}
	budget := draft.BudgetCents
	in := sopBody{
		ContactID: draft.ContactID, SessionID: draft.SessionID, Channel: draft.Channel,
		Recipient: draft.Recipient, Purpose: draft.Purpose, ConsentID: draft.ConsentID,
		ContentVersion: draft.ContentVersion, BudgetCents: &budget, ParentID: draft.ID,
		Body: draft.Body, TimedOut: extra.TimedOut, OriginalReconciled: extra.OriginalReconciled,
	}
	s.decideSOP(w, c, contact, in, op, http.StatusOK)
}

func (s *Server) sopContact(w http.ResponseWriter, r *http.Request, c *caller, id string) (store.Contact, bool) {
	contact, err := s.St.GetContact(id, c.Member.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.Contact{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact lookup failed")
		return store.Contact{}, false
	}
	if !s.requireAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: contact.TenantID, AssigneeMemberID: contact.AssignedMemberID}, w) {
		return store.Contact{}, false
	}
	return contact, true
}

func (s *Server) decideSOP(w http.ResponseWriter, c *caller, contact store.Contact, in sopBody, op string, okStatus int) {
	if op != sopreach.OpConfirm && op != sopreach.OpRetry && op != sopreach.OpAuto {
		if strings.TrimSpace(in.Body) == "" || runeLen(in.Body) > 2000 || runeLen(in.Recipient) > 200 {
			fail(w, http.StatusBadRequest, "bad_request", "body and recipient are required")
			return
		}
	}
	input, code, status, ok := s.sopSnapshot(c, contact, in, op)
	if !ok {
		fail(w, status, code, "sop action is not bound")
		return
	}
	decision := sopreach.Decide(input)
	rows := make([]store.SOPAction, 0, len(decision.Records))
	for _, rec := range decision.Records {
		rows = append(rows, store.SOPAction{
			TenantID: contact.TenantID, ContactID: contact.ID, SessionID: in.SessionID,
			Channel: in.Channel, Recipient: in.Recipient, Purpose: in.Purpose, ConsentID: in.ConsentID,
			ContentVersion: in.ContentVersion, OperatorID: c.Member.ID, BudgetCents: budgetOrZero(in.BudgetCents),
			Kind: rec.Kind, Status: rec.Status, Refusal: decision.Refusal, ParentID: in.ParentID,
			Attempt: input.Attempt, Body: in.Body,
		})
	}
	var stored []store.SOPAction
	if len(rows) > 0 {
		var err error
		stored, err = s.St.InsertSOPActions(rows)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "sop action save failed")
			return
		}
	}
	for _, row := range stored {
		s.Log.Printf("sop op=%s tenant=%s contact=%s kind=%s status=%s refusal=%s", op, row.TenantID, row.ContactID, row.Kind, row.Status, row.Refusal)
	}
	if !decision.OK {
		writeJSON(w, sopRefusalStatus(decision.Refusal), map[string]any{
			"error": decision.Refusal, "delivered": false, "unattended": decision.Unattended, "live_charge": 0,
			"records": stored,
		})
		return
	}
	if len(stored) == 1 && (op == sopreach.OpRemind || op == sopreach.OpDraft || op == sopreach.OpUserReply) {
		row := stored[0]
		writeJSON(w, okStatus, map[string]any{
			"id": row.ID, "kind": row.Kind, "status": row.Status, "delivered": false, "live_charge": 0,
		})
		return
	}
	writeJSON(w, okStatus, map[string]any{
		"delivered": false, "live_charge": 0, "unattended": decision.Unattended, "records": stored,
	})
}

func sopRefusalStatus(refusal string) int {
	switch refusal {
	case sopreach.RefusalBinding, sopreach.RefusalTenant, sopreach.RefusalCapability:
		return http.StatusBadRequest
	default:
		return http.StatusConflict
	}
}

func budgetOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func (s *Server) sopSnapshot(c *caller, contact store.Contact, in sopBody, op string) (sopreach.Input, string, int, bool) {
	policy, err := s.St.GetSOPPolicy(c.Member.TenantID)
	if err != nil {
		return sopreach.Input{}, "internal", http.StatusInternalServerError, false
	}
	input := sopreach.Input{
		Op: op, Level: policy.Level, ExplicitUnattended: policy.Explicit,
		BalanceCents: in.BalanceCents, AIScore: in.AIScore,
		Channel: in.Channel, Capability: knownSOPChannel(in.Channel), LiveChannel: false,
		Recipient: in.Recipient, Purpose: in.Purpose,
		TenantID: contact.TenantID, ActionTenantID: c.Member.TenantID,
		ContentVersion: in.ContentVersion, OperatorID: c.Member.ID,
		BudgetBound: in.BudgetCents != nil, GlobalStop: policy.GlobalStop,
		InsideWindow: store.InsideSOPWindow(time.Now(), policy.WindowStart, policy.WindowEnd),
		TimedOut:     in.TimedOut, OriginalReconciled: in.OriginalReconciled, MaxAttempts: 1,
	}
	if in.ConsentID != "" {
		consent, err := s.St.GetConsentByID(c.Member.TenantID, in.ConsentID)
		if errors.Is(err, sql.ErrNoRows) || consent.ContactID != contact.ID {
			return sopreach.Input{}, "bad_consent", http.StatusBadRequest, false
		}
		if err != nil {
			return sopreach.Input{}, "internal", http.StatusInternalServerError, false
		}
		input.ConsentPurpose = consent.Purpose
		input.MarketingAllowed = consent.MarketingAllowed
		input.ConsultationOnly = !consent.MarketingAllowed
		input.Revoked = consent.RevokedAt != ""
		if knownSOPChannel(consent.SourceChannel) {
			input.ConsentChannels = []string{consent.SourceChannel}
		}
	}
	if in.SessionID != "" {
		sess, err := s.St.GetReceptionSession(c.Member.TenantID, in.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return sopreach.Input{}, "bad_session", http.StatusBadRequest, false
		}
		if err != nil {
			return sopreach.Input{}, "internal", http.StatusInternalServerError, false
		}
		input.HumanTakeover = sess.Mode == "human"
	}
	stops, err := s.St.ListSOPStops(c.Member.TenantID, contact.ID)
	if err != nil {
		return sopreach.Input{}, "internal", http.StatusInternalServerError, false
	}
	for _, stop := range stops {
		if stop.Kind == "global_stop" {
			input.GlobalStop = true
		}
		if stop.Channel != "" && stop.Channel != in.Channel {
			continue
		}
		if stop.Purpose != "" && stop.Purpose != in.Purpose {
			continue
		}
		switch stop.Kind {
		case "unsubscribe":
			input.Unsubscribed = true
		case "customer_stop":
			input.CustomerStop = true
		case "reject":
			input.Rejected = true
		}
	}
	if op == sopreach.OpConfirm || op == sopreach.OpRetry || op == sopreach.OpAuto {
		since := store.ShanghaiDayStart(time.Now())
		n, err := s.St.CountSOPSubmissions(contact.TenantID, contact.ID, in.Channel, in.Purpose, in.ParentID, since)
		if err != nil {
			return sopreach.Input{}, "internal", http.StatusInternalServerError, false
		}
		input.RateLimited = n > 0
		attempts, err := s.St.CountSOPAttempts(in.ParentID)
		if err != nil {
			return sopreach.Input{}, "internal", http.StatusInternalServerError, false
		}
		input.Attempt = attempts
	}
	return input, "", 0, true
}

func knownSOPChannel(channel string) bool {
	switch channel {
	case sopreach.ChannelSMS, sopreach.ChannelEmail, sopreach.ChannelWecom:
		return true
	default:
		return false
	}
}
