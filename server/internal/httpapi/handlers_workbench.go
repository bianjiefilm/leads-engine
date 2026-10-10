package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
	"github.com/bianjiefilm/leads-engine/server/internal/workbench"
)

func deskScope(c *caller) workbench.Scope {
	return workbench.Scope{Role: c.Member.Role, MemberID: c.Member.ID}
}

func (s *Server) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	facts, err := s.St.LoadDeskFacts(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "workbench lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, workbench.BuildWithDrafts(deskScope(c), time.Now().UTC(), facts.Leads, facts.Opps, facts.Desk, facts.Drafts))
}

func (s *Server) handleLeadTimeline(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.leadRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	facts, err := s.St.LoadDeskFacts(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "timeline lookup failed")
		return
	}
	lead, ok := findDeskLead(facts, rec.ID)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	body := map[string]any{
		"lead_id":            lead.ID,
		"events":             workbench.AssembleTimeline(facts.Events[lead.ID], true),
		"source":             workbench.SourceLine{Form: lead.SourceForm, Activity: lead.SourceActivity, Channel: lead.SourceChannel, At: lead.SourceAt},
		"statuses":           workbench.ProjectStatus(lead, oppsForContact(facts.Opps, lead.ContactID), false),
		"next":               workbench.ApplyAIScore(workbench.ResolveNext(lead, time.Now().UTC()), 0),
		"billing":            workbench.Billing{OrdinaryCRMChargeCents: workbench.OrdinaryCRMChargeCents("view")},
		"automation":         workbench.Automation{},
		"joint_chain":        workbench.ProjectJointChain(),
		"outreach_submitted": workbench.DeskSubmittedOutreach(),
	}
	for k, v := range leadReadout(lead) {
		body[k] = v
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleLeadFollowThrough(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.leadRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Note              string  `json:"note"`
		NextFollowUpAt    *string `json:"next_follow_up_at"`
		Complete          bool    `json:"complete"`
		Channel           string  `json:"channel"`
		AIScore           int     `json:"ai_score"`
		CreateOpportunity bool    `json:"create_opportunity"`
		Disposition       string  `json:"disposition"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	facts, err := s.St.LoadDeskFacts(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "workbench lookup failed")
		return
	}
	lead, ok := findDeskLead(facts, rec.ID)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if in.CreateOpportunity {
		if workbench.RefuseSalesPush(lead.Purpose) {
			fail(w, http.StatusConflict, "refused_sales_push", "售后、咨询或拒绝营销不能被推进成销售商机")
			return
		}
		fail(w, http.StatusBadRequest, "not_automatic", "工作台不创建商机，也不因分数自动触达")
		return
	}
	disposition := strings.TrimSpace(in.Disposition)
	if disposition == "next" {
		disposition = ""
	}
	if disposition != "" && disposition != "waiting_customer" {
		fail(w, http.StatusBadRequest, "bad_request", "disposition must be empty, next, or waiting_customer")
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" || runeLen(note) > followUpNoteMaxRunes {
		fail(w, http.StatusBadRequest, "bad_request", "note is required and must be at most 2000 characters")
		return
	}
	channel := strings.TrimSpace(in.Channel)
	if channel != "" && channel != "in_channel" {
		fail(w, http.StatusBadRequest, "bad_request", "channel must be empty or in_channel")
		return
	}
	if channel == "in_channel" && !channelFollowUpAllowed(lead) {
		fail(w, http.StatusBadRequest, "channel_unavailable", "没有授权的渠道标识，不能安排渠道内跟进")
		return
	}
	nextAt := ""
	if in.NextFollowUpAt != nil && strings.TrimSpace(*in.NextFollowUpAt) != "" {
		normalized, good := normalizeFollowUpWhen(*in.NextFollowUpAt)
		if !good {
			fail(w, http.StatusBadRequest, "bad_request", "next_follow_up_at must be RFC3339")
			return
		}
		nextAt = normalized
	}
	key := workbench.FollowThroughKey(rec.ID, note, nextAt, channel, in.Complete, c.Member.ID, disposition)
	if existing, found, err := s.St.FindFollowUpByDedupe(c.Member.TenantID, key); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up lookup failed")
		return
	} else if found {
		if disposition == "waiting_customer" {
			if err := s.St.SetFollowUpDisposition(c.Member.TenantID, existing.ID, disposition); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "follow-up disposition failed")
				return
			}
		}
		s.finishFollowThrough(w, c, rec, existing.ID, true, nextAt, channel, in.AIScore)
		return
	}
	if in.Complete {
		if err := s.St.CompleteOpenFollowUps(c.Member.TenantID, rec.ContactID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "follow-up complete failed")
			return
		}
	}
	created, replay, err := s.St.CreateFollowUpOnce(c.Member.TenantID, rec.ContactID, rec.ID, note, nextAt, c.Member.ID, key)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up create failed")
		return
	}
	if disposition == "waiting_customer" {
		if err := s.St.SetFollowUpDisposition(c.Member.TenantID, created.ID, disposition); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "follow-up disposition failed")
			return
		}
	}
	s.finishFollowThrough(w, c, rec, created.ID, replay, nextAt, channel, in.AIScore)
}

func (s *Server) finishFollowThrough(w http.ResponseWriter, c *caller, rec store.Lead, followID string, replay bool, nextAt, channel string, aiScore int) {
	if nextAt == "" {
		if _, _, _, err := s.St.CompleteFollowUp(followID, c.Member.TenantID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "follow-up complete failed")
			return
		}
	}
	facts, err := s.St.LoadDeskFacts(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "workbench lookup failed")
		return
	}
	lead, _ := findDeskLead(facts, rec.ID)
	if channel == "in_channel" && lead.ManualNextKind == "" {
		lead.ManualNextKind = "channel_follow_up"
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	now := time.Now().UTC()
	writeJSON(w, status, map[string]any{
		"follow_up_id": followID,
		"replay":       replay,
		"next":         workbench.ApplyAIScore(workbench.ResolveNext(lead, now), aiScore),
		"today_group":  workbench.PlaceToday(lead, now),
		"billing":      workbench.Billing{OrdinaryCRMChargeCents: workbench.OrdinaryCRMChargeCents("manual_follow_up")},
		"automation":   workbench.Automation{},
	})
}

func (s *Server) handleDraftIgnore(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rp, sess, ok := s.draftForCaller(w, r, c)
	if !ok {
		return
	}
	decision := workbench.IgnoreDraft(rp.Status)
	if !decision.Sent && rp.Status == "generated" {
		updated, err := s.St.SupersedeGeneratedReply(c.Member.TenantID, rp.ID)
		if err != nil {
			fail(w, http.StatusConflict, "not_ignorable", "只有未发送的草稿可以忽略")
			return
		}
		rp = updated
		decision = workbench.IgnoreDraft("superseded")
	}
	_ = sess
	writeJSON(w, http.StatusOK, map[string]any{
		"id": rp.ID, "status": decision.Status, "sent": decision.Sent,
		"auto_call": false, "auto_message": false,
	})
}

func (s *Server) handleDraftRevise(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rp, _, ok := s.draftForCaller(w, r, c)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	decision, body, err := workbench.ReviseDraft(rp.Status, in.Body)
	if errors.Is(err, workbench.ErrDraftTooLong) {
		fail(w, http.StatusBadRequest, "bad_request", "body must be at most 2000 characters")
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, "not_editable", "只有未发送的草稿可以修改")
		return
	}
	updated, err := s.St.ReviseGeneratedReply(c.Member.TenantID, rp.ID, body)
	if err != nil {
		fail(w, http.StatusConflict, "not_editable", "只有未发送的草稿可以修改")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": updated.ID, "status": decision.Status, "sent": decision.Sent, "body": updated.Body,
		"auto_call": false, "auto_message": false,
	})
}

// handleSessionReplyDraft stores a reply draft for the open session.
// It never approves or sends. The visitor transcript stays unchanged.
func (s *Server) handleSessionReplyDraft(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	sess, err := s.St.GetReceptionSession(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !store.SessionVisible(c.Member.Role, c.Member.ID, sess)) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "session lookup failed")
		return
	}
	if !workbench.InScope(deskScope(c), sess.OwnerMemberID) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	if sess.Status != "open" {
		fail(w, http.StatusConflict, "session_closed", "session is closed")
		return
	}
	var in struct {
		ClientReplyID string `json:"client_reply_id"`
		Body          string `json:"body"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !receptionToken(in.ClientReplyID, 128) {
		fail(w, http.StatusBadRequest, "bad_request", "client_reply_id is required")
		return
	}
	body, err := workbench.SaveReplyDraft(in.Body)
	if errors.Is(err, workbench.ErrDraftTooLong) {
		fail(w, http.StatusBadRequest, "bad_request", "body must be at most 2000 characters")
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body is required")
		return
	}
	existing, err := s.St.GetReceptionReplyByClient(sess.ID, in.ClientReplyID)
	if err == nil {
		if existing.Body != body {
			fail(w, http.StatusConflict, "reply_conflict", "this client_reply_id was already used")
			return
		}
		writeJSON(w, http.StatusOK, replyDraftSaved(existing, sess.ID, true))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "reply draft lookup failed")
		return
	}
	rp, err := s.St.InsertReceptionReply(store.ReceptionReply{
		TenantID: sess.TenantID, SessionID: sess.ID, Epoch: sess.Epoch, SessionVersion: sess.Version,
		Kind: "draft", Body: body, Status: "generated", ClientMsgID: in.ClientReplyID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reply draft failed")
		return
	}
	writeJSON(w, http.StatusCreated, replyDraftSaved(rp, sess.ID, false))
}

func replyDraftSaved(rp store.ReceptionReply, sessionID string, duplicate bool) map[string]any {
	return map[string]any{
		"id": rp.ID, "status": rp.Status, "sent": rp.SentAt != "", "body": rp.Body, "duplicate": duplicate,
		"auto_call": false, "auto_message": false,
		"continue": map[string]any{"focus_id": rp.ID, "session_id": sessionID, "today_group": workbench.TodayAIDraft},
	}
}

func (s *Server) draftForCaller(w http.ResponseWriter, r *http.Request, c *caller) (store.ReceptionReply, store.ReceptionSession, bool) {
	if !s.requireAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return store.ReceptionReply{}, store.ReceptionSession{}, false
	}
	rp, err := s.St.GetReceptionReply(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.ReceptionReply{}, store.ReceptionSession{}, false
	}
	sess, err := s.St.GetReceptionSession(c.Member.TenantID, rp.SessionID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.ReceptionReply{}, store.ReceptionSession{}, false
	}
	if !workbench.InScope(deskScope(c), sess.OwnerMemberID) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.ReceptionReply{}, store.ReceptionSession{}, false
	}
	if !s.requireAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID, AssigneeMemberID: sess.OwnerMemberID}, w) {
		return store.ReceptionReply{}, store.ReceptionSession{}, false
	}
	return rp, sess, true
}

func leadReadout(lead workbench.LeadView) map[string]any {
	return map[string]any{
		"owner_label":       workbench.DisplayOwner(lead),
		"assignment_reason": workbench.DisplayAssignmentReason(lead),
		"allowed_contacts":  workbench.AllowedContacts(lead),
		"sync":              workbench.ProjectSync(lead.CRMReceiptAt),
		"outreach_notice":   workbench.OutreachNotice(lead.MarketingSMSOrPhone, false),
		"model_advice":      workbench.ModelAdviceMissing,
	}
}

func findDeskLead(facts store.DeskFacts, id string) (workbench.LeadView, bool) {
	for _, lead := range facts.Leads {
		if lead.ID == id {
			return lead, true
		}
	}
	return workbench.LeadView{}, false
}

func oppsForContact(opps []workbench.OpportunityView, contactID string) []workbench.OpportunityView {
	var out []workbench.OpportunityView
	for _, opp := range opps {
		if opp.ContactID == contactID {
			out = append(out, opp)
		}
	}
	return out
}

func channelFollowUpAllowed(lead workbench.LeadView) bool {
	probe := lead
	probe.ManualNextAt = ""
	probe.ManualNextKind = ""
	probe.Status = "new"
	return workbench.ResolveNext(probe, time.Now().UTC()).Kind == "channel_follow_up"
}
