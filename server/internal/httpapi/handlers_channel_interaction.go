// HUI-1681 授权评论/私信接入。
//
// 只接收已经绑定到本租户的连接器事件。发布授权不推导私信权限。
// 没有真实渠道凭证，capability 保持 unverified，不宣称已支持任何平台。
// 客户全文不进日志。高分不自动触达，回复权也不等于已经发出。
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/channelix"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const channelMaxBody = 64 << 10

func (s *Server) handleChannelCapability(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	cap := channelix.Capability()
	writeJSON(w, http.StatusOK, map[string]any{
		"verification":                 cap.Verification,
		"live_providers":               []string{},
		"publish_implies_message_read": cap.PublishImpliesMessageRead,
		"note":                         "没有真实渠道凭证。本接口只接收已登记的授权连接器事件，不代表已支持任何平台。",
	})
}

func (s *Server) handleChannelGrantSave(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in store.ChannelGrantIn
	if !readChannelJSON(w, r, &in) {
		return
	}
	if err := s.St.SaveChannelGrant(c.Member.TenantID, in); err != nil {
		fail(w, http.StatusBadRequest, "bad_grant", "channel grant was rejected")
		return
	}
	s.Log.Printf("channel grant %s", channelix.AuditLine(c.Member.TenantID, in.Provider, in.AccountID, "", ""))
	writeJSON(w, http.StatusCreated, map[string]any{
		"provider": in.Provider, "account_id": in.AccountID, "verification": "unverified",
	})
}

func (s *Server) handleChannelGrantRevoke(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Provider  string `json:"provider"`
		AccountID string `json:"account_id"`
	}
	if !readChannelJSON(w, r, &in) {
		return
	}
	if err := s.St.RevokeChannelGrant(c.Member.TenantID, in.Provider, in.AccountID); err != nil {
		fail(w, http.StatusNotFound, "unknown_grant", "channel grant was not found")
		return
	}
	s.Log.Printf("channel revoke %s", channelix.AuditLine(c.Member.TenantID, in.Provider, in.AccountID, "", ""))
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func (s *Server) handleChannelFloor(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Provider  string `json:"provider"`
		AccountID string `json:"account_id"`
		SubjectID string `json:"subject_id"`
		Holder    string `json:"holder"`
	}
	if !readChannelJSON(w, r, &in) {
		return
	}
	if err := s.St.SetChannelFloor(c.Member.TenantID, in.Provider, in.AccountID, in.SubjectID, in.Holder); err != nil {
		fail(w, http.StatusBadRequest, "bad_floor", "reply floor was rejected")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"holder": in.Holder})
}

func (s *Server) handleChannelIngest(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in store.ChannelEventIn
	if !readChannelJSON(w, r, &in) {
		return
	}
	result, err := s.St.IngestChannelEvent(c.Member.TenantID, in)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel ingest failed")
		return
	}
	s.Log.Printf("channel ingest %s refusal=%s", channelix.AuditLine(c.Member.TenantID, in.Provider, in.AccountID, in.EventID, in.Text), result.Refusal)
	if result.Refusal != "" {
		fail(w, http.StatusForbidden, result.Refusal, "channel event was rejected")
		return
	}
	code := http.StatusCreated
	if result.Idempotent {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]any{
		"interaction_id":   result.InteractionID,
		"candidate_id":     result.CandidateID,
		"candidate_status": result.CandidateStatus,
		"idempotent":       result.Idempotent,
		"auto_reach":       false,
		"phone_marketing":  result.PhoneMarketing,
		"sms_marketing":    result.SMSMarketing,
		"reply_allowed":    result.ReplyAllowed,
		"reply_refusal":    result.ReplyRefusal,
		"delivered":        false,
		"verification":     result.Verification,
	})
}

func (s *Server) handleChannelList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	items, err := s.St.ListChannelInteractions(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel list failed")
		return
	}
	if items == nil {
		items = []store.ChannelInteractionRow{}
	}
	cap := channelix.Capability()
	writeJSON(w, http.StatusOK, map[string]any{
		"verification":   cap.Verification,
		"live_providers": []string{},
		"items":          items,
	})
}

func (s *Server) handleChannelConfirm(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	leadID, err := s.St.ConfirmChannelCandidate(c.Member.TenantID, r.PathValue("id"), c.Member.ID)
	if err != nil {
		fail(w, http.StatusConflict, "candidate_not_open", "candidate was not confirmed")
		return
	}
	lead, err := s.St.GetLead(leadID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
		return
	}
	s.Log.Printf("channel confirm %s lead=%s", channelix.AuditLine(c.Member.TenantID, "", "", r.PathValue("id"), ""), leadID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"lead_id": lead.ID, "contact_id": lead.ContactID, "verification": "unverified",
	})
}

func readChannelJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, channelMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable")
		return false
	}
	if err := json.Unmarshal(body, dest); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}
