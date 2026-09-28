package httpapi

import (
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
	writeJSON(w, http.StatusOK, workbench.Build(deskScope(c), time.Now().UTC(), facts.Leads, facts.Opps, facts.Desk))
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
	writeJSON(w, http.StatusOK, map[string]any{
		"lead_id":    lead.ID,
		"events":     workbench.AssembleTimeline(facts.Events[lead.ID], true),
		"source":     workbench.SourceLine{Form: lead.SourceForm, Activity: lead.SourceActivity, Channel: lead.SourceChannel, At: lead.SourceAt},
		"statuses":   workbench.ProjectStatus(lead, oppsForContact(facts.Opps, lead.ContactID), false),
		"next":       workbench.ApplyAIScore(workbench.ResolveNext(lead, time.Now().UTC()), 0),
		"billing":    workbench.Billing{OrdinaryCRMChargeCents: workbench.OrdinaryCRMChargeCents("view")},
		"automation": workbench.Automation{},
	})
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
	}
	if !decodeBody(w, r, &in) {
		return
	}
	_ = in.AIScore
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
	if in.Complete {
		if err := s.St.CompleteOpenFollowUps(c.Member.TenantID, rec.ContactID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "follow-up complete failed")
			return
		}
	}
	created, err := s.St.CreateFollowUp(c.Member.TenantID, rec.ContactID, rec.ID, note, nextAt, c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up create failed")
		return
	}
	if nextAt == "" {
		if _, _, _, err := s.St.CompleteFollowUp(created.ID, c.Member.TenantID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "follow-up complete failed")
			return
		}
	}
	facts, err = s.St.LoadDeskFacts(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "workbench lookup failed")
		return
	}
	lead, _ = findDeskLead(facts, rec.ID)
	if channel == "in_channel" && lead.ManualNextKind == "" {
		lead.ManualNextKind = "channel_follow_up"
	}
	next := workbench.ApplyAIScore(workbench.ResolveNext(lead, time.Now().UTC()), in.AIScore)
	writeJSON(w, http.StatusCreated, map[string]any{
		"follow_up_id": created.ID,
		"next":         next,
		"billing":      workbench.Billing{OrdinaryCRMChargeCents: workbench.OrdinaryCRMChargeCents("manual_follow_up")},
		"automation":   workbench.Automation{},
	})
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
