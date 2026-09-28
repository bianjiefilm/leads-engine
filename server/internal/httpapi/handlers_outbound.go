// HUI-1687 外呼安全门。FEATURE_OUTBOUND_CALL 默认 off。
// 本进程没有真实线路，生产自动外呼不能打开。隔离演练只记模拟。
// 不拨号、不扣费，也不另建一套客户档案。
package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/outbound"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) mountOutbound(mux *http.ServeMux) {
	if !s.Cfg.FeatureOutbound {
		return
	}
	mux.Handle("GET /api/v1/outbound/capability", s.requireSession(s.handleOutboundCapability))
	mux.Handle("POST /api/v1/outbound/policy", s.requireSession(s.handleOutboundPolicy))
	mux.Handle("POST /api/v1/outbound/stops", s.requireSession(s.handleOutboundStop))
	mux.Handle("POST /api/v1/outbound/tasks", s.requireSession(s.handleOutboundTask))
	mux.Handle("GET /api/v1/outbound/tasks/{id}", s.requireSession(s.handleOutboundGet))
	mux.Handle("GET /api/v1/outbound/tasks/{id}/events", s.requireSession(s.handleOutboundEvents))
	mux.Handle("POST /api/v1/outbound/tasks/{id}/cancel", s.requireSession(s.handleOutboundCancel))
	mux.Handle("POST /api/v1/outbound/tasks/{id}/transfer", s.requireSession(s.handleOutboundTransfer))
	mux.Handle("POST /api/v1/outbound/tasks/{id}/retry", s.requireSession(s.handleOutboundRetry))
	mux.Handle("POST /api/v1/outbound/tasks/{id}/receipts", s.requireSession(s.handleOutboundReceipt))
}

type outboundBody struct {
	TaskKey      string `json:"task_key"`
	ContactID    string `json:"contact_id"`
	CampaignID   string `json:"campaign_id"`
	ConsentID    string `json:"consent_id"`
	SessionID    string `json:"session_id"`
	Mode         string `json:"mode"`
	BudgetCents  *int   `json:"budget_cents"`
	Op           string `json:"op"`
	AIScore      int    `json:"ai_score"`
	PublicSource bool   `json:"public_source"`
	Phone        string `json:"phone"`
	Kind         string `json:"kind"`
	ProviderHTTP int    `json:"provider_http"`
	ClaimsDial   bool   `json:"claims_dial_success"`
	ClaimsEmpty  bool   `json:"claims_empty_number"`
	ClaimsConn   bool   `json:"claims_connected"`
	Recording    string `json:"recording"`
	Transcript   string `json:"transcript"`
}

func (s *Server) handleOutboundCapability(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	policy, err := s.St.GetOutboundPolicy(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"production_auto": false,
		"real_line":       false,
		"verification":    "unverified",
		"cost":            "unknown",
		"live_charge":     0,
		"global_stop":     policy.GlobalStop,
		"window_start":    policy.WindowStart,
		"window_end":      policy.WindowEnd,
		"note":            "没有真实线路。生产自动外呼关闭。隔离演练只记录模拟，不代表拨打成功或真实接通。",
	})
}

func (s *Server) handleOutboundPolicy(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageReception, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		WindowStart int `json:"window_start"`
		WindowEnd   int `json:"window_end"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !validSOPWindow(in.WindowStart, in.WindowEnd) {
		fail(w, http.StatusBadRequest, "bad_request", "window_start and window_end must describe a Shanghai hour range")
		return
	}
	current, err := s.St.GetOutboundPolicy(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy lookup failed")
		return
	}
	policy := store.OutboundPolicy{
		TenantID: c.Member.TenantID, GlobalStop: current.GlobalStop,
		WindowStart: in.WindowStart, WindowEnd: in.WindowEnd, UpdatedBy: c.Member.ID,
	}
	if err := s.St.SaveOutboundPolicy(policy); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy save failed")
		return
	}
	saved, err := s.St.GetOutboundPolicy(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "policy lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"production_auto": false, "global_stop": saved.GlobalStop,
		"window_start": saved.WindowStart, "window_end": saved.WindowEnd,
	})
}

func (s *Server) handleOutboundStop(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageReception, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Kind      string `json:"kind"`
		ContactID string `json:"contact_id"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	switch in.Kind {
	case "unsubscribe", "suppression", "reject", "global_stop":
	default:
		fail(w, http.StatusBadRequest, "bad_request", "unknown stop kind")
		return
	}
	if in.Kind != "global_stop" {
		if _, ok := s.outboundContact(w, c, in.ContactID); !ok {
			return
		}
	}
	if in.Kind == "global_stop" {
		if err := s.St.SetOutboundGlobalStop(c.Member.TenantID, c.Member.ID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "global stop failed")
			return
		}
	}
	stop, err := s.St.AddOutboundStop(store.OutboundStop{
		TenantID: c.Member.TenantID, ContactID: in.ContactID, Kind: in.Kind, CreatedBy: c.Member.ID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stop save failed")
		return
	}
	s.Log.Printf("outbound stop tenant=%s kind=%s contact=%s", c.Member.TenantID, stop.Kind, stop.ContactID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": stop.ID, "kind": stop.Kind})
}

func (s *Server) handleOutboundTask(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	var in outboundBody
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Op == "" {
		in.Op = outbound.OpDial
	}
	if in.Op != outbound.OpDial && in.Op != outbound.OpAuto {
		fail(w, http.StatusBadRequest, "bad_request", "op must be dial or auto")
		return
	}
	contact, ok := s.outboundContact(w, c, in.ContactID)
	if !ok {
		return
	}
	if strings.TrimSpace(in.TaskKey) == "" || (in.Mode != outbound.ModeIsolation && in.Mode != outbound.ModeProduction) {
		fail(w, http.StatusBadRequest, outbound.RefusalBinding, "task key and mode are required")
		return
	}
	if existing, err := s.St.GetOutboundTaskByKey(contact.TenantID, in.TaskKey); err == nil {
		s.writeOutbound(w, http.StatusOK, existing, nil, true)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "task lookup failed")
		return
	}
	input, code, status, ok := s.outboundSnapshot(c, contact, in, outbound.Input{Op: in.Op, TaskKey: in.TaskKey, CampaignID: in.CampaignID})
	if !ok {
		fail(w, status, code, "outbound task is not bound")
		return
	}
	decision := outbound.Decide(input)
	task := store.OutboundTask{
		TenantID: contact.TenantID, ContactID: contact.ID, TaskKey: in.TaskKey, CampaignID: in.CampaignID,
		ConsentID: in.ConsentID, SessionID: in.SessionID, Mode: in.Mode,
		Simulation: in.Mode == outbound.ModeIsolation, State: outboundState(decision, in.Op),
		OperatorID: c.Member.ID, BudgetCents: in.BudgetCents,
	}
	stored, replay, err := s.St.InsertOutboundAttempt(task, outboundReceipts(decision, in.ProviderHTTP), decision.PublicEvent, "")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "task save failed")
		return
	}
	s.logOutbound(in.Op, stored, decision)
	codeStatus := http.StatusCreated
	if replay {
		codeStatus = http.StatusOK
	}
	if !decision.OK && !replay {
		codeStatus = outboundRefusalStatus(decision.Refusal)
	}
	s.writeOutbound(w, codeStatus, stored, &decision, replay)
}

func (s *Server) handleOutboundGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	task, ok := s.outboundTask(w, c, r.PathValue("id"))
	if !ok {
		return
	}
	s.writeOutbound(w, http.StatusOK, task, nil, false)
}

func (s *Server) handleOutboundEvents(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	task, ok := s.outboundTask(w, c, r.PathValue("id"))
	if !ok {
		return
	}
	items, err := s.St.ListOutboundEvents(task.TenantID, task.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "event list failed")
		return
	}
	if items == nil {
		items = []store.OutboundEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleOutboundCancel(w http.ResponseWriter, r *http.Request) {
	s.actOutbound(w, r, outbound.OpCancel)
}

func (s *Server) handleOutboundTransfer(w http.ResponseWriter, r *http.Request) {
	s.actOutbound(w, r, outbound.OpTransfer)
}

func (s *Server) handleOutboundRetry(w http.ResponseWriter, r *http.Request) {
	s.actOutbound(w, r, outbound.OpRetry)
}

func (s *Server) handleOutboundReceipt(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	var in outboundBody
	if !decodeBody(w, r, &in) {
		return
	}
	task, ok := s.outboundTask(w, c, r.PathValue("id"))
	if !ok {
		return
	}
	contact, ok := s.outboundContact(w, c, task.ContactID)
	if !ok {
		return
	}
	base := outbound.Input{
		Op: outbound.OpReceipt, TaskKey: task.TaskKey, CampaignID: task.CampaignID,
		ReceiptKind: in.Kind, ProviderHTTP: in.ProviderHTTP,
		ClaimsDialSuccess: in.ClaimsDial, ClaimsEmptyNumber: in.ClaimsEmpty, ClaimsConnected: in.ClaimsConn,
		Phone: in.Phone, Recording: in.Recording, Transcript: in.Transcript,
	}
	input, code, status, ok := s.outboundSnapshot(c, contact, outboundBody{
		ConsentID: task.ConsentID, SessionID: task.SessionID, Mode: task.Mode, BudgetCents: task.BudgetCents,
		Phone: in.Phone, Recording: in.Recording, Transcript: in.Transcript,
	}, base)
	if !ok {
		fail(w, status, code, "outbound task is not bound")
		return
	}
	s.finishOutbound(w, c, contact, task, in, input, http.StatusOK)
}

func (s *Server) actOutbound(w http.ResponseWriter, r *http.Request, op string) {
	c := callerFrom(r)
	if op == outbound.OpRetry && !decodeBody(w, r, &struct{}{}) {
		return
	}
	task, ok := s.outboundTask(w, c, r.PathValue("id"))
	if !ok {
		return
	}
	contact, ok := s.outboundContact(w, c, task.ContactID)
	if !ok {
		return
	}
	base := outbound.Input{Op: op, TaskKey: task.TaskKey, CampaignID: task.CampaignID}
	input, code, status, ok := s.outboundSnapshot(c, contact, outboundBody{
		ConsentID: task.ConsentID, SessionID: task.SessionID, Mode: task.Mode, BudgetCents: task.BudgetCents,
	}, base)
	if !ok {
		fail(w, status, code, "outbound task is not bound")
		return
	}
	s.finishOutbound(w, c, contact, task, outboundBody{}, input, http.StatusOK)
}

func (s *Server) finishOutbound(w http.ResponseWriter, c *caller, contact store.Contact, task store.OutboundTask, in outboundBody, input outbound.Input, okStatus int) {
	decision := outbound.Decide(input)
	state := ""
	if decision.OK && input.Op == outbound.OpCancel {
		state = "cancelled"
	}
	if decision.OK && input.Op == outbound.OpTransfer {
		state = "transferred"
	}
	recording := ""
	if decision.OK && input.Op == outbound.OpReceipt {
		recording = strings.TrimSpace(in.Recording)
	}
	if err := s.St.AppendOutboundFacts(contact.TenantID, task.ID, state, outboundReceipts(decision, in.ProviderHTTP), decision.PublicEvent, recording); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "outbound fact save failed")
		return
	}
	if state != "" {
		task.State = state
	}
	s.logOutbound(input.Op, task, decision)
	status := okStatus
	if !decision.OK {
		status = outboundRefusalStatus(decision.Refusal)
	}
	s.writeOutbound(w, status, task, &decision, false)
}

func (s *Server) writeOutbound(w http.ResponseWriter, status int, task store.OutboundTask, decision *outbound.Result, replay bool) {
	receipts, err := s.St.ListOutboundReceipts(task.TenantID, task.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "receipt list failed")
		return
	}
	if receipts == nil {
		receipts = []store.OutboundReceipt{}
	}
	body := map[string]any{
		"id": task.ID, "task_key": task.TaskKey, "state": task.State, "mode": task.Mode,
		"simulation": task.Mode == outbound.ModeIsolation || task.Simulation,
		"replay":     replay, "dial_succeeded": false, "real_connected": false,
		"empty_number_detected": false, "cost_known": false, "cost": "unknown", "live_charge": 0,
		"receipts": receipts,
	}
	if decision != nil && decision.Refusal != "" {
		body["error"] = decision.Refusal
	}
	writeJSON(w, status, body)
}

func (s *Server) logOutbound(op string, task store.OutboundTask, decision outbound.Result) {
	kind := ""
	if len(decision.Receipts) == 1 {
		kind = decision.Receipts[0].Kind
	}
	s.Log.Printf("outbound op=%s tenant=%s task=%s kind=%s refusal=%s", op, task.TenantID, task.ID, kind, decision.Refusal)
}

func outboundState(decision outbound.Result, op string) string {
	if !decision.OK {
		return "blocked"
	}
	switch op {
	case outbound.OpCancel:
		return "cancelled"
	case outbound.OpTransfer:
		return "transferred"
	default:
		return "open"
	}
}

func outboundReceipts(decision outbound.Result, providerHTTP int) []store.OutboundReceipt {
	out := make([]store.OutboundReceipt, 0, len(decision.Receipts))
	for _, rec := range decision.Receipts {
		out = append(out, store.OutboundReceipt{
			Kind: rec.Kind, Status: rec.Status, Simulation: rec.Simulation,
			ProviderHTTP: providerHTTP, Refusal: decision.Refusal,
		})
	}
	return out
}

func outboundRefusalStatus(refusal string) int {
	switch refusal {
	case outbound.RefusalBinding:
		return http.StatusBadRequest
	default:
		return http.StatusConflict
	}
}

func (s *Server) outboundContact(w http.ResponseWriter, c *caller, id string) (store.Contact, bool) {
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

func (s *Server) outboundTask(w http.ResponseWriter, c *caller, id string) (store.OutboundTask, bool) {
	task, err := s.St.GetOutboundTask(c.Member.TenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "task not found")
		return store.OutboundTask{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "task lookup failed")
		return store.OutboundTask{}, false
	}
	if _, ok := s.outboundContact(w, c, task.ContactID); !ok {
		return store.OutboundTask{}, false
	}
	return task, true
}

func (s *Server) outboundSnapshot(c *caller, contact store.Contact, in outboundBody, base outbound.Input) (outbound.Input, string, int, bool) {
	policy, err := s.St.GetOutboundPolicy(c.Member.TenantID)
	if err != nil {
		return outbound.Input{}, "internal", http.StatusInternalServerError, false
	}
	input := base
	input.Mode = in.Mode
	if input.Mode == "" {
		input.Mode = outbound.ModeIsolation
	}
	input.ProductionAuto = false
	input.RealLine = false
	input.Simulation = input.Mode == outbound.ModeIsolation
	input.AIScore = in.AIScore
	input.PublicSource = in.PublicSource
	input.PhoneValid = phoneLooksDialable(in.Phone)
	input.Phone = in.Phone
	input.Recording = in.Recording
	input.Transcript = in.Transcript
	input.BudgetBound = in.BudgetCents != nil
	input.InsideWindow = store.InsideSOPWindow(time.Now(), policy.WindowStart, policy.WindowEnd)
	input.GlobalStop = policy.GlobalStop
	input.OriginalLookedUp = true
	if in.ConsentID != "" {
		consent, err := s.St.GetConsentByID(c.Member.TenantID, in.ConsentID)
		if errors.Is(err, sql.ErrNoRows) || consent.ContactID != contact.ID {
			return outbound.Input{}, "bad_consent", http.StatusBadRequest, false
		}
		if err != nil {
			return outbound.Input{}, "internal", http.StatusInternalServerError, false
		}
		input.Revoked = consent.RevokedAt != ""
		input.MarketingAllowed = consent.MarketingAllowed && consent.Purpose == "marketing"
		input.ExplicitConsent = input.MarketingAllowed && !input.Revoked && consent.SourceChannel == "voice"
	}
	if in.SessionID != "" {
		sess, err := s.St.GetReceptionSession(c.Member.TenantID, in.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return outbound.Input{}, "bad_session", http.StatusBadRequest, false
		}
		if err != nil {
			return outbound.Input{}, "internal", http.StatusInternalServerError, false
		}
		if sess.Mode == "human" {
			input.HumanTransfer = true
		}
	}
	if task, err := s.St.GetOutboundTaskByKey(contact.TenantID, input.TaskKey); err == nil {
		if task.State == "cancelled" {
			input.Cancelled = true
		}
		if task.State == "transferred" {
			input.HumanTransfer = true
		}
		receipts, err := s.St.ListOutboundReceipts(task.TenantID, task.ID)
		if err != nil {
			return outbound.Input{}, "internal", http.StatusInternalServerError, false
		}
		input.OriginalReconciled = outboundReconciled(receipts)
		input.ResultUnknown = !input.OriginalReconciled
	} else if !errors.Is(err, sql.ErrNoRows) {
		return outbound.Input{}, "internal", http.StatusInternalServerError, false
	}
	stops, err := s.St.ListOutboundStops(c.Member.TenantID, contact.ID)
	if err != nil {
		return outbound.Input{}, "internal", http.StatusInternalServerError, false
	}
	for _, stop := range stops {
		applyOutboundStop(&input, stop.Kind)
	}
	sopStops, err := s.St.ListSOPStops(c.Member.TenantID, contact.ID)
	if err != nil {
		return outbound.Input{}, "internal", http.StatusInternalServerError, false
	}
	for _, stop := range sopStops {
		if stop.Channel != "" && stop.Channel != "voice" {
			continue
		}
		if stop.Purpose != "" && stop.Purpose != "marketing" {
			continue
		}
		applyOutboundStop(&input, stop.Kind)
	}
	return input, "", 0, true
}

func applyOutboundStop(in *outbound.Input, kind string) {
	switch kind {
	case "unsubscribe":
		in.Unsubscribed = true
	case "suppression", "customer_stop":
		in.Suppressed = true
	case "reject":
		in.Rejected = true
	case "global_stop":
		in.GlobalStop = true
	}
}

func outboundReconciled(receipts []store.OutboundReceipt) bool {
	for _, rec := range receipts {
		if rec.Status == outbound.StatusBlocked {
			continue
		}
		switch rec.Kind {
		case outbound.KindRinging, outbound.KindConnected, outbound.KindCompleted, outbound.KindIntent:
			return true
		}
	}
	return false
}

func phoneLooksDialable(phone string) bool {
	n := 0
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n >= 11
}
