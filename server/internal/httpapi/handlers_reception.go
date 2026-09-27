// HUI-1688 统一接待 HTTP 面。FEATURE_RECEPTION 默认 off 时路由不注册。
// 公共入口的租户只来自 widget 归属。用量 live_charge 恒为 0，不调用外部模型或扣费。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/reception"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const (
	receptionMaxBody     = 1 << 14
	receptionGreeting    = "你好，这里是本店的自有站点接待。价格和订单以实时核对为准。"
	receptionQuestionMax = 200
	receptionAnswerMax   = 2000
	receptionPersonaMax  = 200
	receptionMessageMax  = 2000
)

func (s *Server) mountReception(mux *http.ServeMux) {
	if !s.Cfg.FeatureReception {
		return
	}
	mux.Handle("POST /api/v1/reception/widgets", s.requireSession(s.handleReceptionWidgetCreate))
	mux.Handle("POST /api/v1/reception/knowledge", s.requireSession(s.handleKnowledgeCreate))
	mux.Handle("GET /api/v1/reception/knowledge", s.requireSession(s.handleKnowledgeList))
	mux.Handle("PATCH /api/v1/reception/knowledge/{id}", s.requireSession(s.handleKnowledgePatch))
	mux.Handle("POST /api/v1/reception/knowledge/{id}/withdraw", s.requireSession(s.handleKnowledgeWithdraw))
	mux.Handle("POST /api/v1/reception/knowledge/{id}/enable", s.requireSession(s.handleKnowledgeEnable))
	mux.Handle("GET /api/v1/reception/desk", s.requireSession(s.handleReceptionDesk))
	mux.Handle("GET /api/v1/reception/sessions/{id}", s.requireSession(s.handleReceptionSessionGet))
	mux.Handle("POST /api/v1/reception/sessions/{id}/takeover", s.requireSession(s.handleReceptionTakeover))
	mux.Handle("POST /api/v1/reception/sessions/{id}/release", s.requireSession(s.handleReceptionRelease))
	mux.Handle("POST /api/v1/reception/sessions/{id}/close", s.requireSession(s.handleReceptionClose))
	mux.Handle("POST /api/v1/reception/sessions/{id}/lead", s.requireSession(s.handleReceptionLead))
	mux.Handle("POST /api/v1/reception/sessions/{id}/follow-up", s.requireSession(s.handleReceptionFollowUp))
	mux.Handle("GET /api/v1/reception/sessions/{id}/presentation", s.requireSession(s.handleReceptionPresentation))
	mux.Handle("POST /api/v1/reception/sessions/{id}/replies", s.requireSession(s.handleReceptionHumanReply))
	mux.Handle("POST /api/v1/reception/sessions/{id}/replies/{replyId}/approve", s.requireSession(s.handleReceptionApprove))
	mux.Handle("POST /api/v1/reception/sessions/{id}/replies/{replyId}/send", s.requireSession(s.handleReceptionSend))

	mux.Handle("GET /api/v1/public/reception/widgets/{id}", s.requireInternal(s.handlePublicWidget))
	mux.Handle("POST /api/v1/public/reception/widgets/{id}/sessions", s.requireInternal(s.handlePublicSessionOpen))
	mux.Handle("GET /api/v1/public/reception/sessions/{id}", s.requireInternal(s.handlePublicSessionGet))
	mux.Handle("POST /api/v1/public/reception/sessions/{id}/messages", s.requireInternal(s.handlePublicMessage))
	mux.Handle("GET /api/v1/public/reception/sessions/{id}/presentation", s.requireInternal(s.handlePublicPresentation))
}

func (s *Server) receptionPepper(w http.ResponseWriter) bool {
	if strings.TrimSpace(s.Cfg.VisitorPepper) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config_gate_reception",
			"message": "RECEPTION_VISITOR_PEPPER is required when FEATURE_RECEPTION=on (fail-closed)",
		})
		return false
	}
	return true
}

func (s *Server) ownerReception(w http.ResponseWriter, r *http.Request) (*caller, bool) {
	if !s.receptionPepper(w) {
		return nil, false
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageReception, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return nil, false
	}
	return c, true
}

func readReceptionBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, receptionMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable")
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	if err := json.Unmarshal(body, dst); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

func receptionToken(v string, max int) bool {
	if v == "" || len(v) > max {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == ':' || r == '@':
		default:
			return false
		}
	}
	return true
}

func validReceptionMode(mode string) bool {
	return mode == reception.ModeAI || mode == reception.ModeAssist || mode == reception.ModeHuman
}

func validKnowledgeText(w http.ResponseWriter, question, answer string) bool {
	question, answer = strings.TrimSpace(question), strings.TrimSpace(answer)
	if question == "" || answer == "" {
		fail(w, http.StatusBadRequest, "bad_request", "question and answer are required")
		return false
	}
	if utf8.RuneCountInString(question) > receptionQuestionMax || utf8.RuneCountInString(answer) > receptionAnswerMax {
		fail(w, http.StatusBadRequest, "bad_request", "question or answer is too long")
		return false
	}
	return true
}

func (s *Server) handleReceptionWidgetCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownerReception(w, r)
	if !ok {
		return
	}
	var in struct {
		DefaultMode    string `json:"default_mode"`
		PersonaWording string `json:"persona_wording"`
		Language       string `json:"language"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	if in.DefaultMode == "" {
		in.DefaultMode = reception.ModeAI
	}
	if !validReceptionMode(in.DefaultMode) {
		fail(w, http.StatusBadRequest, "bad_request", "default_mode must be ai, assist, or human")
		return
	}
	if utf8.RuneCountInString(in.PersonaWording) > receptionPersonaMax {
		fail(w, http.StatusBadRequest, "bad_request", "persona_wording is too long")
		return
	}
	wdt, err := s.St.CreateReceptionWidget(c.Member.TenantID, in.DefaultMode, in.PersonaWording, in.Language, c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "widget create failed")
		return
	}
	writeJSON(w, http.StatusCreated, wdt)
}

func (s *Server) handleKnowledgeCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownerReception(w, r)
	if !ok {
		return
	}
	var in struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}
	if !readReceptionBody(w, r, &in) || !validKnowledgeText(w, in.Question, in.Answer) {
		return
	}
	k, err := s.St.CreateKnowledge(c.Member.TenantID, strings.TrimSpace(in.Question), strings.TrimSpace(in.Answer), c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "knowledge create failed")
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) handleKnowledgeList(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownerReception(w, r)
	if !ok {
		return
	}
	items, err := s.St.ListKnowledge(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "knowledge list failed")
		return
	}
	if items == nil {
		items = []store.KnowledgeSource{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleKnowledgePatch(w http.ResponseWriter, r *http.Request) {
	c, ok := s.ownerReception(w, r)
	if !ok {
		return
	}
	var in struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}
	if !readReceptionBody(w, r, &in) || !validKnowledgeText(w, in.Question, in.Answer) {
		return
	}
	k, err := s.St.PatchKnowledge(c.Member.TenantID, r.PathValue("id"), strings.TrimSpace(in.Question), strings.TrimSpace(in.Answer))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "knowledge update failed")
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) handleKnowledgeWithdraw(w http.ResponseWriter, r *http.Request) {
	s.setKnowledge(w, r, false)
}

func (s *Server) handleKnowledgeEnable(w http.ResponseWriter, r *http.Request) {
	s.setKnowledge(w, r, true)
}

func (s *Server) setKnowledge(w http.ResponseWriter, r *http.Request, enabled bool) {
	c, ok := s.ownerReception(w, r)
	if !ok {
		return
	}
	k, err := s.St.SetKnowledgeEnabled(c.Member.TenantID, r.PathValue("id"), enabled)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "knowledge update failed")
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) handleReceptionDesk(w http.ResponseWriter, r *http.Request) {
	if !s.receptionPepper(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	items, err := s.St.ListReceptionDesk(c.Member.TenantID, c.Member.ID, c.Member.Role)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "desk failed")
		return
	}
	if items == nil {
		items = []store.DeskItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) staffSession(w http.ResponseWriter, r *http.Request) (*caller, store.ReceptionSession, bool) {
	if !s.receptionPepper(w) {
		return nil, store.ReceptionSession{}, false
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, AssigneeMemberID: c.Member.ID}, w) {
		return nil, store.ReceptionSession{}, false
	}
	sess, err := s.St.GetReceptionSession(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !store.SessionVisible(c.Member.Role, c.Member.ID, sess)) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return nil, store.ReceptionSession{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "session lookup failed")
		return nil, store.ReceptionSession{}, false
	}
	return c, sess, true
}

func (s *Server) handleReceptionSessionGet(w http.ResponseWriter, r *http.Request) {
	_, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	msgs, err := s.St.ListReceptionMessages(sess.TenantID, sess.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "messages failed")
		return
	}
	replies, err := s.St.ListReceptionReplies(sess.TenantID, sess.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "replies failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":  sess,
		"messages": messageViews(msgs),
		"replies":  replyViews(replies),
	})
}

func (s *Server) handleReceptionTakeover(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	var in struct {
		Epoch int `json:"epoch"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	if in.Epoch == 0 {
		in.Epoch = sess.Epoch
	}
	next, err := s.St.TakeoverSession(c.Member.TenantID, sess.ID, c.Member.ID, in.Epoch)
	if errors.Is(err, store.ErrTakeoverLost) {
		fail(w, http.StatusConflict, "takeover_lost", "another member already holds this session")
		return
	}
	if errors.Is(err, store.ErrSessionClosed) {
		fail(w, http.StatusConflict, "session_closed", "session is closed")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "takeover failed")
		return
	}
	writeJSON(w, http.StatusOK, next)
}

func (s *Server) handleReceptionRelease(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	var in struct {
		Epoch int `json:"epoch"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	if in.Epoch == 0 {
		in.Epoch = sess.Epoch
	}
	next, err := s.St.ReleaseSessionToAI(c.Member.TenantID, sess.ID, c.Member.ID, c.Member.Role, in.Epoch)
	if errors.Is(err, store.ErrTakeoverLost) {
		fail(w, http.StatusConflict, "release_rejected", "only the current owner can return the session to AI")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "release failed")
		return
	}
	writeJSON(w, http.StatusOK, next)
}

func (s *Server) handleReceptionClose(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	next, err := s.St.CloseReceptionSession(c.Member.TenantID, sess.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "close failed")
		return
	}
	writeJSON(w, http.StatusOK, next)
}

func (s *Server) handleReceptionLead(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(s.Cfg.DedupPepper) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "config_gate_dedup", "message": "LEADS_DEDUP_PEPPER is required for lead intake (fail-closed)",
		})
		return
	}
	var in struct {
		Purpose          string `json:"purpose"`
		AllowContact     bool   `json:"allow_contact"`
		NoticeVersion    string `json:"notice_version"`
		ContactName      string `json:"contact_name"`
		Phone            string `json:"phone"`
		Email            string `json:"email"`
		MarketingAllowed bool   `json:"marketing_allowed"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	if in.Purpose != "sales_followup" || !in.AllowContact || strings.TrimSpace(in.ContactName) == "" || strings.TrimSpace(in.Phone) == "" || strings.TrimSpace(in.NoticeVersion) == "" {
		fail(w, http.StatusBadRequest, "lead_not_authorized", "a lead requires purpose sales_followup, allow_contact, a name, a phone, and a notice version")
		return
	}
	if utf8.RuneCountInString(in.NoticeVersion) > 64 {
		fail(w, http.StatusBadRequest, "bad_request", "notice_version is too long")
		return
	}
	content, _ := json.Marshal(map[string]string{
		"session_id": sess.ID, "name": strings.TrimSpace(in.ContactName), "phone": strings.TrimSpace(in.Phone), "purpose": in.Purpose,
	})
	res, err := s.St.AttachReceptionLead(sess.ID, sess.OwnerMemberID, store.IntakeInput{
		TenantID: sess.TenantID, SourceApp: "leads-engine", SourceNS: "reception", EventID: sess.ID,
		Content: content, ContactName: strings.TrimSpace(in.ContactName), Phone: strings.TrimSpace(in.Phone),
		Email: strings.TrimSpace(in.Email), BusinessCategory: "merchant_customer", SourceType: "form",
		Pepper: s.Cfg.DedupPepper, FilterEnabled: s.Cfg.FeatureLeadsFilter, AssignEnabled: false,
		Consent: &store.ConsentUpsert{
			SourceSubmissionRef: sess.ID, SourceChannel: "reception_h5",
			NoticeVersion: strings.TrimSpace(in.NoticeVersion), Purpose: "sales_followup",
			MarketingAllowed: in.MarketingAllowed,
		},
	})
	if errors.Is(err, store.ErrSessionClosed) {
		fail(w, http.StatusConflict, "session_closed", "session is closed")
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "lead was not created")
		return
	}
	_ = c
	writeJSON(w, http.StatusOK, map[string]any{
		"lead_id": res.LeadID, "contact_id": res.ContactID, "duplicate": res.Duplicate, "class": res.Class,
	})
}

func (s *Server) handleReceptionFollowUp(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	if sess.ContactID == "" || sess.LeadID == "" {
		fail(w, http.StatusBadRequest, "lead_required", "an authorized lead is required before a follow-up")
		return
	}
	var in struct {
		Note string `json:"note"`
		Next string `json:"next_follow_up_at"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" || utf8.RuneCountInString(note) > followUpNoteMaxRunes {
		fail(w, http.StatusBadRequest, "bad_request", "note is required")
		return
	}
	next := ""
	if strings.TrimSpace(in.Next) != "" {
		n, good := normalizeFollowUpWhen(in.Next)
		if !good {
			fail(w, http.StatusBadRequest, "bad_request", "next_follow_up_at must be RFC3339")
			return
		}
		next = n
	}
	f, err := s.St.CreateFollowUp(sess.TenantID, sess.ContactID, sess.LeadID, note, next, c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up failed")
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleReceptionHumanReply(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	if sess.Mode != reception.ModeHuman || (sess.OwnerMemberID != c.Member.ID && c.Member.Role != "owner") {
		fail(w, http.StatusConflict, "not_owner", "human replies require the current takeover")
		return
	}
	var in struct {
		ClientReplyID string `json:"client_reply_id"`
		Body          string `json:"body"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	if !receptionToken(in.ClientReplyID, 128) || strings.TrimSpace(in.Body) == "" || utf8.RuneCountInString(in.Body) > receptionMessageMax {
		fail(w, http.StatusBadRequest, "bad_request", "client_reply_id and body are required")
		return
	}
	if existing, err := s.St.GetReceptionReplyByClient(sess.ID, in.ClientReplyID); err == nil {
		if existing.Body != in.Body {
			fail(w, http.StatusConflict, "reply_conflict", "this client_reply_id was already used")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reply": replyView(existing), "duplicate": true})
		return
	}
	rp, err := s.St.InsertReceptionReply(store.ReceptionReply{
		TenantID: sess.TenantID, SessionID: sess.ID, Epoch: sess.Epoch, SessionVersion: sess.Version,
		Kind: "human", Body: strings.TrimSpace(in.Body), Status: "approved", ClientMsgID: in.ClientReplyID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reply failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"reply": replyView(rp), "duplicate": false})
}

func (s *Server) handleReceptionApprove(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	rp, err := s.St.ApproveReceptionReply(c.Member.TenantID, sess.ID, r.PathValue("replyId"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if errors.Is(err, store.ErrNotDeliverable) {
		fail(w, http.StatusConflict, "send_blocked", "this draft can no longer be approved")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "approve failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reply": replyView(rp)})
}

func (s *Server) handleReceptionSend(w http.ResponseWriter, r *http.Request) {
	c, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	var in struct {
		ReceiptID string `json:"receipt_id"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	if in.ReceiptID != "" && !receptionToken(in.ReceiptID, 128) {
		fail(w, http.StatusBadRequest, "bad_request", "receipt_id is not a token")
		return
	}
	rp, already, err := s.St.SendReceptionReply(c.Member.TenantID, sess.ID, r.PathValue("replyId"), in.ReceiptID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if errors.Is(err, store.ErrNotDeliverable) {
		fail(w, http.StatusConflict, "send_blocked", "this reply can no longer be sent on text or audio")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "send failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reply": replyView(rp), "duplicate": already})
}

func (s *Server) handleReceptionPresentation(w http.ResponseWriter, r *http.Request) {
	_, sess, ok := s.staffSession(w, r)
	if !ok {
		return
	}
	s.writePresentation(w, sess)
}

func (s *Server) handlePublicWidget(w http.ResponseWriter, r *http.Request) {
	if !s.receptionPepper(w) {
		return
	}
	wdt, err := s.St.GetReceptionWidget(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !wdt.Enabled) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "widget lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": wdt.ID, "channel": wdt.Channel, "language": wdt.Language, "greeting": receptionGreeting,
	})
}

func (s *Server) handlePublicSessionOpen(w http.ResponseWriter, r *http.Request) {
	if !s.receptionPepper(w) {
		return
	}
	wdt, err := s.St.GetReceptionWidget(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !wdt.Enabled) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "widget lookup failed")
		return
	}
	var in struct {
		VisitorKey string `json:"visitor_key"`
		Language   string `json:"language"`
		TenantID   string `json:"tenant_id"`
	}
	if !readReceptionBody(w, r, &in) {
		return
	}
	_ = in.TenantID
	hash, err := reception.HashVisitor(s.Cfg.VisitorPepper, in.VisitorKey)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "visitor_key is required")
		return
	}
	lang := wdt.Language
	if in.Language != "" && utf8.RuneCountInString(in.Language) <= 16 {
		lang = in.Language
	}
	sess, resumed, err := s.St.OpenReceptionSession(wdt.TenantID, wdt.ID, hash, wdt.DefaultMode, lang)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "session open failed")
		return
	}
	status := http.StatusCreated
	if resumed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"session": publicSession(sess), "resumed": resumed})
}

func (s *Server) publicVisitorSession(w http.ResponseWriter, r *http.Request) (store.ReceptionSession, bool) {
	if !s.receptionPepper(w) {
		return store.ReceptionSession{}, false
	}
	key := r.URL.Query().Get("visitor_key")
	if key == "" {
		var in struct {
			VisitorKey string `json:"visitor_key"`
		}
		if !readReceptionBody(w, r, &in) {
			return store.ReceptionSession{}, false
		}
		key = in.VisitorKey
		r.Body = io.NopCloser(strings.NewReader(""))
	}
	sess, err := s.St.GetReceptionSessionAny(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !reception.SameVisitor(s.Cfg.VisitorPepper, key, sess.VisitorKeyHash)) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.ReceptionSession{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "session lookup failed")
		return store.ReceptionSession{}, false
	}
	return sess, true
}

func (s *Server) handlePublicSessionGet(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.publicVisitorSession(w, r)
	if !ok {
		return
	}
	s.writeTranscript(w, sess, false)
}

func (s *Server) handlePublicPresentation(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.publicVisitorSession(w, r)
	if !ok {
		return
	}
	s.writePresentation(w, sess)
}

func (s *Server) handlePublicMessage(w http.ResponseWriter, r *http.Request) {
	if !s.receptionPepper(w) {
		return
	}
	var in struct {
		VisitorKey  string `json:"visitor_key"`
		ClientMsgID string `json:"client_msg_id"`
		Text        string `json:"text"`
		TenantID    string `json:"tenant_id"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, receptionMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable")
		return
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	_ = in.TenantID
	sess, err := s.St.GetReceptionSessionAny(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !reception.SameVisitor(s.Cfg.VisitorPepper, in.VisitorKey, sess.VisitorKeyHash)) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "session lookup failed")
		return
	}
	if sess.Status != "open" {
		fail(w, http.StatusConflict, "session_closed", "session is closed")
		return
	}
	if !receptionToken(in.ClientMsgID, 128) || strings.TrimSpace(in.Text) == "" || utf8.RuneCountInString(in.Text) > receptionMessageMax {
		fail(w, http.StatusBadRequest, "bad_request", "client_msg_id and text are required")
		return
	}
	if _, err := s.St.GetReceptionMessageByClient(sess.ID, in.ClientMsgID); err == nil {
		s.writeDuplicateMessage(w, sess, in.ClientMsgID)
		return
	}
	if _, err := s.St.InsertReceptionMessage(sess.TenantID, sess.ID, in.ClientMsgID, strings.TrimSpace(in.Text)); err != nil {
		if _, err2 := s.St.GetReceptionMessageByClient(sess.ID, in.ClientMsgID); err2 == nil {
			s.writeDuplicateMessage(w, sess, in.ClientMsgID)
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "message failed")
		return
	}
	if sess.Mode == reception.ModeHuman {
		_ = s.St.AddReceptionEvent(sess.TenantID, sess.ID, "interruption", sess.Epoch)
		next, _ := s.St.TouchReceptionSession(sess.TenantID, sess.ID, reception.PendingHuman)
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": false, "session": publicSession(next), "reply": nil})
		return
	}
	faqs := s.faqView(sess.TenantID)
	now := time.Now().UTC()
	fact, factCode := s.lookupFact(sess.TenantID, in.Text, now)
	wdt, _ := s.St.GetReceptionWidget(sess.WidgetID)
	out := reception.Compose(reception.ComposeInput{
		Mode: sess.Mode, VisitorText: in.Text, Persona: wdt.PersonaWording, FAQs: faqs, Now: now,
		Fact: fact, FactCode: factCode, ModelDown: s.ReceptionModelUp != nil && !s.ReceptionModelUp(),
	})
	if out.SkipModel {
		next, _ := s.St.TouchReceptionSession(sess.TenantID, sess.ID, out.PendingReason)
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": false, "session": publicSession(next), "reply": nil})
		return
	}
	cj, _ := json.Marshal(out.Citations)
	gj, _ := json.Marshal(out.Gaps)
	if out.Citations == nil {
		cj = []byte("[]")
	}
	if out.Gaps == nil {
		gj = []byte("[]")
	}
	status := "generated"
	if out.ShouldSend {
		status = "approved"
	}
	rp, err := s.St.InsertReceptionReply(store.ReceptionReply{
		TenantID: sess.TenantID, SessionID: sess.ID, Epoch: sess.Epoch, SessionVersion: sess.Version,
		Kind: out.Kind, Body: out.Body, CitationsJSON: string(cj), GapsJSON: string(gj),
		Status: status, ClientMsgID: in.ClientMsgID,
	})
	if err != nil {
		if existing, err2 := s.St.GetReceptionReplyByClient(sess.ID, in.ClientMsgID); err2 == nil {
			s.writeDuplicateMessage(w, sess, in.ClientMsgID)
			_ = existing
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "reply failed")
		return
	}
	_, _ = s.St.InsertReceptionUsage(sess.TenantID, rp.ID, sess.ID+":"+in.ClientMsgID, 1)
	if out.ShouldSend {
		sent, _, sendErr := s.St.SendReceptionReply(sess.TenantID, sess.ID, rp.ID, "auto:"+in.ClientMsgID)
		if sendErr == nil {
			rp = sent
		}
	}
	next, _ := s.St.TouchReceptionSession(sess.TenantID, sess.ID, out.PendingReason)
	rows, units, live, _ := s.St.CountReceptionUsage(sess.TenantID, sess.ID+":"+in.ClientMsgID)
	writeJSON(w, http.StatusOK, map[string]any{
		"duplicate": false,
		"session":   publicSession(next),
		"reply":     publicReply(rp, next.Epoch),
		"usage":     map[string]any{"rows": rows, "units": units, "live_charge": live},
	})
}

func (s *Server) writeDuplicateMessage(w http.ResponseWriter, sess store.ReceptionSession, clientMsgID string) {
	fresh, _ := s.St.GetReceptionSession(sess.TenantID, sess.ID)
	epoch := sess.Epoch
	if fresh.ID != "" {
		epoch = fresh.Epoch
	}
	var reply any
	if rp, err := s.St.GetReceptionReplyByClient(sess.ID, clientMsgID); err == nil {
		reply = publicReply(rp, epoch)
	}
	rows, units, live, _ := s.St.CountReceptionUsage(sess.TenantID, sess.ID+":"+clientMsgID)
	if fresh.ID == "" {
		fresh = sess
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"duplicate": true,
		"session":   publicSession(fresh),
		"reply":     reply,
		"usage":     map[string]any{"rows": rows, "units": units, "live_charge": live},
	})
}

func (s *Server) faqView(tenantID string) []reception.FAQ {
	rows, err := s.St.ListKnowledge(tenantID)
	if err != nil {
		return nil
	}
	out := make([]reception.FAQ, 0, len(rows))
	for _, k := range rows {
		out = append(out, reception.FAQ{
			ID: k.ID, Question: k.Question, Answer: k.Answer, Version: k.Version, UpdatedAt: k.UpdatedAt,
			Enabled: k.Enabled && k.WithdrawnAt == "", Withdrawn: k.WithdrawnAt != "" || !k.Enabled,
		})
	}
	return out
}

func (s *Server) lookupFact(tenantID, text string, now time.Time) (*reception.Fact, string) {
	kind := reception.Intent(text)
	if kind != "price" && kind != "inventory" && kind != "order_status" {
		return nil, ""
	}
	if s.ReceptionFacts == nil {
		return nil, "missing"
	}
	value, exp, code := s.ReceptionFacts.Lookup(tenantID, kind, now)
	if code != "" {
		return nil, code
	}
	return &reception.Fact{Kind: kind, Value: value, ExpiresAt: exp}, ""
}

func (s *Server) writeTranscript(w http.ResponseWriter, sess store.ReceptionSession, staff bool) {
	msgs, _ := s.St.ListReceptionMessages(sess.TenantID, sess.ID)
	replies, _ := s.St.ListReceptionReplies(sess.TenantID, sess.ID)
	session := any(publicSession(sess))
	if staff {
		session = sess
	}
	views := replyViews(replies)
	if !staff {
		views = visitorReplyViews(replies, sess.Epoch)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session": session, "messages": messageViews(msgs), "replies": views,
	})
}

func (s *Server) writePresentation(w http.ResponseWriter, sess store.ReceptionSession) {
	replies, _ := s.St.ListReceptionReplies(sess.TenantID, sess.ID)
	events, _ := s.St.ListReceptionEvents(sess.TenantID, sess.ID)
	type channel struct {
		Text  bool `json:"text"`
		Audio bool `json:"audio"`
	}
	type reply struct {
		ReplyID     string  `json:"reply_id"`
		Version     int     `json:"version"`
		Text        string  `json:"text"`
		Language    string  `json:"language"`
		Deliverable channel `json:"deliverable"`
	}
	type event struct {
		Type  string `json:"type"`
		Epoch int    `json:"epoch"`
	}
	outReplies := []reply{}
	for _, rp := range replies {
		textOK, audioOK := reception.MayDeliver(rp.Epoch, sess.Epoch, rp.Status)
		if !textOK && !audioOK {
			continue
		}
		outReplies = append(outReplies, reply{
			ReplyID: rp.ID, Version: rp.SessionVersion, Text: rp.Body, Language: sess.Language,
			Deliverable: channel{Text: textOK, Audio: audioOK},
		})
	}
	outEvents := []event{}
	for _, ev := range events {
		outEvents = append(outEvents, event{Type: ev.Type, Epoch: ev.Epoch})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_ref": sess.ID,
		"language":    sess.Language,
		"version":     sess.Version,
		"events":      outEvents,
		"replies":     outReplies,
	})
}

func publicSession(sess store.ReceptionSession) map[string]any {
	return map[string]any{
		"id": sess.ID, "mode": sess.Mode, "epoch": sess.Epoch, "version": sess.Version,
		"status": sess.Status, "pending_reason": sess.PendingReason, "language": sess.Language,
	}
}

func messageViews(msgs []store.ReceptionMessage) []map[string]any {
	if msgs == nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{"id": m.ID, "client_msg_id": m.ClientMsgID, "body": m.Body, "created_at": m.CreatedAt})
	}
	return out
}

func replyViews(replies []store.ReceptionReply) []map[string]any {
	if replies == nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(replies))
	for _, rp := range replies {
		out = append(out, replyView(rp))
	}
	return out
}

func replyView(rp store.ReceptionReply) map[string]any {
	return map[string]any{
		"id": rp.ID, "epoch": rp.Epoch, "session_version": rp.SessionVersion, "kind": rp.Kind,
		"body": rp.Body, "status": rp.Status, "citations": store.CitationsOf(rp.CitationsJSON),
		"gaps": store.GapsOf(rp.GapsJSON), "generated_at": rp.GeneratedAt, "approved_at": rp.ApprovedAt,
		"sent_at": rp.SentAt, "created_at": rp.CreatedAt,
	}
}

// visitorMaySeeReply is the visitor transcript gate.
// Sent history stays. A current generated answer (clarify, model-down, refusal) stays.
// Approved-but-unsent, assist drafts, superseded, and blocked text stay off this channel.
func visitorMaySeeReply(rp store.ReceptionReply, epoch int) bool {
	if rp.Status == "sent" {
		return true
	}
	return rp.Status == "generated" && rp.Epoch == epoch && rp.Kind != "draft" && rp.Kind != "human"
}

func visitorReplyViews(replies []store.ReceptionReply, epoch int) []map[string]any {
	out := []map[string]any{}
	for _, rp := range replies {
		if visitorMaySeeReply(rp, epoch) {
			out = append(out, replyView(rp))
		}
	}
	return out
}

func publicReply(rp store.ReceptionReply, epoch int) any {
	if !visitorMaySeeReply(rp, epoch) {
		return nil
	}
	return replyView(rp)
}
