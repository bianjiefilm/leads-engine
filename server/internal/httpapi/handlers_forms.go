// HUI-1679 / FEAT-0180 版本化留资表单:管理面(owner 专属)与 schema 契约导出。
//
//   - POST   /forms                     建草稿(version 在 (tenant, store, form_key) 族内递增)
//   - GET    /forms                     列表(本租户)
//   - GET    /forms/{id}                详情
//   - PATCH  /forms/{id}                仅草稿可改;发布后不可变(改配置 = 新版本行)
//   - POST   /forms/{id}/publish        草稿→发布(校验 notice_version/purpose 非空)
//   - POST   /forms/{id}/disable        draft|published→disabled(停用)
//   - GET    /forms/{id}/schema         固定渲染/校验契约 JSON —— 碰一碰(T1)复用的唯一接口
//
// 权限:authz.ActionManageForms,owner 专属;sales/agent 一律 403。
// 首版收敛:固定字段枚举(name/phone/wechat),无自由字段、无拖拽搭建器。
package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// handleFormCreate creates a draft as the next version of its form family.
func (s *Server) handleFormCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var req struct {
		FormKey         string  `json:"form_key"`
		StoreID         string  `json:"store_id"`
		NoticeVersion   string  `json:"notice_version"`
		Purpose         string  `json:"purpose"`
		MarketingPrompt string  `json:"marketing_prompt"`
		Fields          *string `json:"fields"`
		ExpiresAt       *string `json:"expires_at"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	draftIn := store.FormDraftInput{
		FormKey:         optStr(req.FormKey),
		StoreID:         optStr(req.StoreID),
		NoticeVersion:   optStr(req.NoticeVersion),
		Purpose:         optStr(req.Purpose),
		MarketingPrompt: optStr(req.MarketingPrompt),
		Fields:          req.Fields,
		ExpiresAt:       req.ExpiresAt,
	}
	if msg := store.DraftInputProblem(draftIn); msg != "" {
		fail(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	f, err := s.St.CreateFormDraft(c.Member.TenantID, c.Member.ID, draftIn)
	switch {
	case err == nil:
	case isFormBadRequest(err):
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	default:
		fail(w, http.StatusInternalServerError, "internal", "form create failed")
		return
	}
	s.Log.Printf("form draft created id=%s tenant=%s key=%s version=%d by=%s",
		f.ID, c.Member.TenantID, f.FormKey, f.Version, c.Member.ID)
	writeJSON(w, http.StatusCreated, f)
}

// handleFormList answers the tenant's form versions.
func (s *Server) handleFormList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	items, err := s.St.ListForms(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "form list failed")
		return
	}
	if items == nil {
		items = []store.Form{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleFormGet answers one form's detail.
func (s *Server) handleFormGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	f, err := s.St.GetForm(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrFormNotFound) || errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "form lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleFormPatch edits a draft. Published rows are immutable by construction.
func (s *Server) handleFormPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		NoticeVersion   *string `json:"notice_version"`
		Purpose         *string `json:"purpose"`
		MarketingPrompt *string `json:"marketing_prompt"`
		Fields          *string `json:"fields"`
		ExpiresAt       *string `json:"expires_at"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	patch := store.FormDraftInput{
		NoticeVersion:   in.NoticeVersion,
		Purpose:         in.Purpose,
		MarketingPrompt: in.MarketingPrompt,
		Fields:          in.Fields,
		ExpiresAt:       in.ExpiresAt,
	}
	if msg := store.DraftInputProblem(patch); msg != "" {
		fail(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	f, err := s.St.UpdateFormDraft(r.PathValue("id"), c.Member.TenantID, patch)
	switch {
	case errors.Is(err, store.ErrFormNotFound):
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	case errors.Is(err, store.ErrFormNotDraft):
		fail(w, http.StatusConflict, "form_not_draft",
			"published forms are immutable; create a new version instead")
		return
	case isFormBadRequest(err):
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "form patch failed")
		return
	}
	s.Log.Printf("form draft patched id=%s tenant=%s by=%s", f.ID, c.Member.TenantID, c.Member.ID)
	writeJSON(w, http.StatusOK, f)
}

// handleFormPublish moves draft -> published and freezes the version.
func (s *Server) handleFormPublish(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	f, err := s.St.PublishForm(r.PathValue("id"), c.Member.TenantID)
	switch {
	case errors.Is(err, store.ErrFormNotFound):
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	case errors.Is(err, store.ErrFormNotPublishable):
		fail(w, http.StatusConflict, "form_not_draft", "only drafts can be published")
		return
	case errors.Is(err, store.ErrFormIncomplete):
		fail(w, http.StatusBadRequest, "form_incomplete",
			"notice_version and purpose are required before publishing")
		return
	case isFormBadRequest(err):
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "form publish failed")
		return
	}
	s.Log.Printf("form published id=%s tenant=%s version=%d by=%s", f.ID, c.Member.TenantID, f.Version, c.Member.ID)
	writeJSON(w, http.StatusOK, f)
}

// handleFormDisable stops a draft or published form.
func (s *Server) handleFormDisable(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	f, err := s.St.DisableForm(r.PathValue("id"), c.Member.TenantID)
	switch {
	case errors.Is(err, store.ErrFormNotFound):
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	case isFormBadRequest(err):
		fail(w, http.StatusConflict, "form_state", err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "form disable failed")
		return
	}
	s.Log.Printf("form disabled id=%s tenant=%s by=%s", f.ID, c.Member.TenantID, c.Member.ID)
	writeJSON(w, http.StatusOK, f)
}

// handleFormSchema exports the fixed render/validation contract — the ONLY
// integration surface offered to T1 (HUI-1747 碰一碰复用)。 owner-only.
func (s *Server) handleFormSchema(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageForms, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	f, err := s.St.GetForm(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrFormNotFound) || errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "form not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "form lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, formSchemaContract(f, s.formSubmitLimit(), s.formWindowSeconds()))
}

// ---- shared helpers -------------------------------------------------------------

// optStr maps a possibly-empty request string to a nil pointer so an absent
// field and an empty override stay distinguishable in drafts.
func optStr(v string) *string { return &v }

// isFormBadRequest recognizes the store layer's own "form: …" validation
// errors; anything else is internal and must not leak its text.
func isFormBadRequest(err error) bool {
	if err == nil {
		return false
	}
	const prefix = "form: "
	msg := err.Error()
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		// lifecycle errors have their own mapping; the rest are 400s
		switch {
		case errors.Is(err, store.ErrFormNotFound), errors.Is(err, store.ErrFormNotDraft),
			errors.Is(err, store.ErrFormNotPublishable), errors.Is(err, store.ErrFormIncomplete):
			return false
		}
		return true
	}
	return false
}
