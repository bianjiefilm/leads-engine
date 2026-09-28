// HUI-1682 场景内轻文案。FEATURE_LIGHT_COPY 默认 off。
// 没有真实模型时生成失败并说明原因。保存和手改不依赖模型。
// 不发送、不发布、不记营销许可、不扣费，也不在 CRM 里创建专业制作工程。
package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/lightcopy"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) mountLightCopy(mux *http.ServeMux) {
	if !s.Cfg.FeatureLightCopy {
		return
	}
	mux.Handle("GET /api/v1/light-copy/capability", s.requireSession(s.handleLightCapability))
	s.mountLightSubject(mux, "opportunities", lightcopy.KindOpportunity)
	s.mountLightSubject(mux, "campaigns", lightcopy.KindCampaign)
}

func (s *Server) mountLightSubject(mux *http.ServeMux, segment, subjectKind string) {
	base := "/api/v1/" + segment + "/{id}/light-copy"
	mux.Handle("GET "+base, s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightList(w, r, subjectKind)
	}))
	mux.Handle("PUT "+base+"/{kind}", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightSave(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/generate", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightGenerate(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/confirm", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightConfirm(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/facts", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightFacts(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/handoff", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightHandoff(w, r, subjectKind)
	}))
}

func (s *Server) handleLightCapability(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"model_ready": false,
		"live_charge": 0,
		"tools":       []string{lightcopy.ToolGoBoost, lightcopy.ToolProductImage, lightcopy.ToolDigitalHuman, lightcopy.ToolAiCut},
		"note":        "没有接入真实文案模型。生成会失败并说明原因，不会用模板占位。保存和手改不需要模型。生成完成不等于已发送、已发布或已取得营销许可。",
	})
}

func validLightKind(kind string) bool {
	return kind == lightcopy.KindReply || kind == lightcopy.KindEmail || kind == lightcopy.KindBrief
}

func (s *Server) lightSubject(w http.ResponseWriter, r *http.Request, subjectKind string, action authz.Action) (lightcopy.Subject, bool) {
	c := callerFrom(r)
	id := r.PathValue("id")
	var (
		sub      lightcopy.Subject
		assignee string
		err      error
	)
	if subjectKind == lightcopy.KindOpportunity {
		sub, assignee, err = s.St.LoadLightOpportunity(id, c.Member.TenantID)
	} else {
		sub, assignee, err = s.St.LoadLightCampaign(id, c.Member.TenantID)
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrLightNotCampaign) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return lightcopy.Subject{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "scene lookup failed")
		return lightcopy.Subject{}, false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: sub.TenantID, AssigneeMemberID: assignee}, w) {
		return lightcopy.Subject{}, false
	}
	return sub, true
}

func (s *Server) handleLightList(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionReadRecord)
	if !ok {
		return
	}
	items, err := s.St.ListLightDrafts(sub.TenantID, sub.Kind, sub.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft list failed")
		return
	}
	if items == nil {
		items = []store.LightDraft{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleLightSave(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	current, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		current = store.LightDraft{}
	}
	next, reason := lightcopy.SaveManual(draftToRule(current), in.Body)
	if reason != "" {
		fail(w, http.StatusBadRequest, reason, "草稿正文不能为空")
		return
	}
	saved, err := s.St.SaveLightDraft(ruleToDraft(next, current, sub, kind, callerFrom(r).Member.ID))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft save failed")
		return
	}
	s.Log.Printf("light copy save tenant=%s subject=%s kind=%s version=%d", sub.TenantID, sub.ID, kind, saved.ContentVersion)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleLightGenerate(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	current, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	_, reason := lightcopy.Generate(draftToRule(current), lightcopy.ModelOutput{})
	if reason != lightcopy.ReasonModelUnavailable {
		fail(w, http.StatusConflict, reason, "不能使用占位正文")
		return
	}
	s.Log.Printf("light copy generate refused tenant=%s subject=%s kind=%s reason=%s", sub.TenantID, sub.ID, kind, reason)
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":               reason,
		"message":             "没有接入真实文案模型，不能生成正文，也不会用模板占位。请在当前页手写或修改草稿。",
		"generated":           false,
		"sent":                false,
		"published":           false,
		"marketing_permitted": false,
	})
}

func (s *Server) handleLightConfirm(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	current, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "draft not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	next := lightcopy.Confirm(draftToRule(current))
	saved, err := s.St.SaveLightDraft(ruleToDraft(next, current, sub, kind, callerFrom(r).Member.ID))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft confirm failed")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleLightFacts(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionReadRecord)
	if !ok {
		return
	}
	var in struct {
		Selected []string `json:"selected"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	packet, reason := lightcopy.Project(callerFrom(r).Member.TenantID, sub, in.Selected, time.Now().UTC())
	if reason != "" {
		fail(w, http.StatusBadRequest, reason, factMessage(reason))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject_kind": packet.SubjectKind,
		"subject_id":   packet.SubjectID,
		"facts":        packet.Facts,
	})
}

func factMessage(reason string) string {
	switch reason {
	case lightcopy.ReasonTenant:
		return "场景不属于当前租户"
	case lightcopy.ReasonPrice:
		return "没有未过期的价格证明，不能把价格交给后续工具"
	case lightcopy.ReasonUnauthorized:
		return "不能把联系人全文、内部销售备注或未授权字段交给后续工具"
	case lightcopy.ReasonMissing:
		return "所选事实不在当前商机或活动上"
	default:
		return "没有选中可交出的事实"
	}
}

func (s *Server) handleLightHandoff(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	var in struct {
		Tool     string   `json:"tool"`
		Key      string   `json:"idempotency_key"`
		Decline  bool     `json:"decline"`
		Selected []string `json:"selected"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	in.Key = strings.TrimSpace(in.Key)
	if in.Key == "" || len(in.Key) > 128 {
		fail(w, http.StatusBadRequest, "bad_request", "idempotency_key is required")
		return
	}
	c := callerFrom(r)
	var existing *lightcopy.Handoff
	if row, err := s.St.GetLightHandoff(sub.TenantID, in.Key); err == nil {
		existing = handoffToRule(row)
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	draft, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	var facts map[string]any
	if existing == nil && !in.Decline {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusBadRequest, "draft_missing", "先保存草稿，再交接。拒绝专业工具时仍可只保存草稿。")
			return
		}
		packet, reason := lightcopy.Project(c.Member.TenantID, sub, in.Selected, time.Now().UTC())
		if reason != "" {
			fail(w, http.StatusBadRequest, reason, factMessage(reason))
			return
		}
		facts = packet.Facts
	}
	decision := lightcopy.OpenHandoff(lightcopy.HandoffInput{
		Key: in.Key, Tool: in.Tool, Decline: in.Decline, EntryConfigured: false,
		SourceKind: sub.Kind, SourceID: sub.ID, DraftID: draft.ID,
		ContentVersion: draft.ContentVersion, Facts: facts, Existing: existing,
	})
	if decision.Reason == lightcopy.ReasonUnknownTool {
		fail(w, http.StatusBadRequest, decision.Reason, "只能打开已有的专业工具入口，不能在 CRM 里新建视频、产品图或数字人引擎")
		return
	}
	if existing != nil {
		row, _ := s.St.GetLightHandoff(sub.TenantID, in.Key)
		row.Regenerated = false
		row.ProjectCreated = false
		writeJSON(w, http.StatusOK, row)
		return
	}
	saved, err := s.St.InsertLightHandoff(store.LightHandoff{
		TenantID: sub.TenantID, Key: decision.Key, DraftID: decision.DraftID, Tool: decision.Tool,
		SourceKind: decision.SourceKind, SourceID: decision.SourceID, ContentVersion: decision.ContentVersion,
		Facts: decision.Facts, Status: decision.Status, Reason: decision.Reason,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff save failed")
		return
	}
	saved.Regenerated = false
	saved.ProjectCreated = false
	s.Log.Printf("light copy handoff tenant=%s subject=%s tool=%s status=%s", sub.TenantID, sub.ID, saved.Tool, saved.Status)
	writeJSON(w, http.StatusOK, saved)
}

func draftToRule(d store.LightDraft) lightcopy.Draft {
	return lightcopy.Draft{
		Body: d.Body, Origin: d.Origin, ContentVersion: d.ContentVersion,
		Generated: false, Sent: false, Published: false, MarketingPermitted: false,
		UserConfirmed: d.UserConfirmed, LiveCharge: 0,
	}
}

func ruleToDraft(next lightcopy.Draft, current store.LightDraft, sub lightcopy.Subject, kind, memberID string) store.LightDraft {
	id := current.ID
	created := current.CreatedAt
	return store.LightDraft{
		ID: id, TenantID: sub.TenantID, SubjectKind: sub.Kind, SubjectID: sub.ID, Kind: kind,
		Body: next.Body, Origin: lightcopy.OriginManual, ContentVersion: next.ContentVersion,
		UserConfirmed: next.UserConfirmed, UpdatedBy: memberID, CreatedAt: created,
	}
}

func handoffToRule(h store.LightHandoff) *lightcopy.Handoff {
	return &lightcopy.Handoff{
		ID: h.ID, Key: h.Key, Tool: h.Tool, SourceKind: h.SourceKind, SourceID: h.SourceID,
		DraftID: h.DraftID, ContentVersion: h.ContentVersion, Facts: h.Facts,
		Status: h.Status, Reason: h.Reason,
	}
}
