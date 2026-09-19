package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/redact"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// ---- shared validation -------------------------------------------------------

var (
	validCategories = map[string]bool{"merchant_customer": true, "creative_service": true}
	validSourceTypes = map[string]bool{"manual": true, "form": true, "touch_campaign": true}
	validConsent    = map[string]bool{"pending": true, "granted": true, "denied": true}
	validLeadStatus = map[string]bool{"new": true, "in_progress": true, "converted": true, "closed": true}
	// HUI-1693 最小阶段集:open/qualified/proposal/negotiation/won/closed_lost。
	// won 仅表示人工标记成交,绝不表示已支付/已收款。
	validOppStage = map[string]bool{
		"open": true, "qualified": true, "proposal": true,
		"negotiation": true, "won": true, "closed_lost": true,
	}
)

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func validRFC3339(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

type sourceInput struct {
	SourceApp         string `json:"source_app"`
	SourceRef         string `json:"source_ref"`
	AuthScopeSnapshot string `json:"auth_scope_snapshot"`
}

// resolveSource creates the provenance row when a source is declared. When
// required is true (non-manual source types), missing provenance is a 400 —
// 来源留痕是硬约束,不是可选装饰。
func (s *Server) resolveSource(w http.ResponseWriter, r *http.Request, c *caller, required bool, in *sourceInput) (string, bool) {
	hasSource := in != nil && (in.SourceApp != "" || in.SourceRef != "" || in.AuthScopeSnapshot != "")
	if required && !hasSource {
		fail(w, http.StatusBadRequest, "source_required",
			"this record type requires source_app/source_ref/auth_scope_snapshot provenance")
		return "", false
	}
	if !hasSource {
		return "", true
	}
	if in.SourceApp == "" || in.SourceRef == "" {
		fail(w, http.StatusBadRequest, "bad_source", "source_app and source_ref are both required")
		return "", false
	}
	sr, err := s.St.CreateSourceRef(c.Member.TenantID, in.SourceApp, in.SourceRef, in.AuthScopeSnapshot, c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "source ref create failed")
		return "", false
	}
	return sr.ID, true
}

// resolveAssignee applies the assignment policy: owner may assign anyone in the
// tenant; sales/agent may only assign themselves (default self).
func (s *Server) resolveAssignee(w http.ResponseWriter, c *caller, requested string) (string, bool) {
	if requested == "" || requested == c.Member.ID {
		return c.Member.ID, true
	}
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		fail(w, http.StatusForbidden, authz.ReasonForbidden, "only the owner can assign records to others")
		return "", false
	}
	m, err := s.St.GetMember(requested)
	if err != nil || m.TenantID != c.Member.TenantID {
		fail(w, http.StatusBadRequest, "bad_assignee", "assignee must be a member of the same tenant")
		return "", false
	}
	return m.ID, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, in any) bool {
	if err := json.NewDecoder(r.Body).Decode(in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

// ---- contacts -----------------------------------------------------------------

func (s *Server) handleContactCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Name             string      `json:"name"`
		Phone            string      `json:"phone"`
		Email            string      `json:"email"`
		BusinessCategory string      `json:"business_category"`
		SourceType       string      `json:"source_type"`
		ConsentStatus    string      `json:"consent_status"`
		AssignedMemberID string      `json:"assigned_member_id"`
		Source           *sourceInput `json:"source"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Name == "" {
		fail(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	if in.SourceType == "" {
		in.SourceType = "manual"
	}
	if in.ConsentStatus == "" {
		in.ConsentStatus = "pending"
	}
	if !validCategories[in.BusinessCategory] {
		fail(w, http.StatusBadRequest, "bad_request", "business_category must be merchant_customer or creative_service")
		return
	}
	if !validSourceTypes[in.SourceType] {
		fail(w, http.StatusBadRequest, "bad_request", "source_type must be manual, form or touch_campaign")
		return
	}
	if !validConsent[in.ConsentStatus] {
		fail(w, http.StatusBadRequest, "bad_request", "consent_status must be pending, granted or denied")
		return
	}
	sourceRefID, ok := s.resolveSource(w, r, c, in.SourceType != "manual", in.Source)
	if !ok {
		return
	}
	assignee, ok := s.resolveAssignee(w, c, in.AssignedMemberID)
	if !ok {
		return
	}
	created, err := s.St.CreateContact(store.Contact{
		TenantID:         c.Member.TenantID,
		Name:             in.Name,
		Phone:            in.Phone,
		Email:            in.Email,
		BusinessCategory: in.BusinessCategory,
		SourceType:       in.SourceType,
		ConsentStatus:    in.ConsentStatus,
		SourceRefID:      sourceRefID,
	}, c.Member.ID, assignee)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact create failed")
		return
	}
	s.Log.Printf("contact created id=%s %s", created.ID, redact.Person(created.Name, created.Phone, created.Email))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleContactList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	filter := ""
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		filter = c.Member.ID // sales/agent only ever see their own records
	}
	items, err := s.St.ListContacts(c.Member.TenantID, filter)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact list failed")
		return
	}
	if items == nil {
		items = []store.Contact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// contactRecord fetches within the caller's tenant and authorizes the action;
// missing or forbidden-by-assignment both answer 404 (assignment topology never
// leaks to non-owners).
func (s *Server) contactRecord(w http.ResponseWriter, r *http.Request, c *caller, action authz.Action) (store.Contact, bool) {
	id := r.PathValue("id")
	rec, err := s.St.GetContact(id, c.Member.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.Contact{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact lookup failed")
		return store.Contact{}, false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: rec.TenantID, AssigneeMemberID: rec.AssignedMemberID}, w) {
		return store.Contact{}, false
	}
	return rec, true
}

func (s *Server) handleContactGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleContactPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Name             *string `json:"name"`
		Phone            *string `json:"phone"`
		Email            *string `json:"email"`
		ConsentStatus    *string `json:"consent_status"`
		AssignedMemberID *string `json:"assigned_member_id"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ConsentStatus != nil && !validConsent[*in.ConsentStatus] {
		fail(w, http.StatusBadRequest, "bad_request", "consent_status must be pending, granted or denied")
		return
	}
	var patch store.ContactPatch
	if in.Name != nil {
		patch.Name = in.Name
	}
	if in.Phone != nil {
		patch.Phone = in.Phone
	}
	if in.Email != nil {
		patch.Email = in.Email
	}
	if in.ConsentStatus != nil {
		patch.ConsentStatus = in.ConsentStatus
	}
	if in.AssignedMemberID != nil {
		// reassignment is an owner-only action even on your own record
		if authz.Role(c.Member.Role) != authz.RoleOwner {
			fail(w, http.StatusForbidden, authz.ReasonForbidden, "only the owner can reassign records")
			return
		}
		assignee, ok := s.resolveAssignee(w, c, *in.AssignedMemberID)
		if !ok {
			return
		}
		patch.AssignedTo = &assignee
	}
	updated, err := s.St.UpdateContact(rec.ID, c.Member.TenantID, patch)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact update failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ---- leads ----------------------------------------------------------------

func (s *Server) handleLeadCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		ContactID        string       `json:"contact_id"`
		Status           string       `json:"status"`
		AssignedMemberID string       `json:"assigned_member_id"`
		Source           *sourceInput `json:"source"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ContactID == "" {
		fail(w, http.StatusBadRequest, "bad_request", "contact_id is required")
		return
	}
	if in.Status == "" {
		in.Status = "new"
	}
	if !validLeadStatus[in.Status] {
		fail(w, http.StatusBadRequest, "bad_request", "status must be new, in_progress, converted or closed")
		return
	}
	// contact must exist in the caller's own tenant (cross-tenant links are
	// impossible by construction: the lookup is tenant-scoped)
	if _, err := s.St.GetContact(in.ContactID, c.Member.TenantID); err != nil {
		fail(w, http.StatusBadRequest, "bad_contact", "contact_id does not exist in this tenant")
		return
	}
	sourceRefID := ""
	if in.Source != nil {
		var ok bool
		if sourceRefID, ok = s.resolveSource(w, r, c, true, in.Source); !ok {
			return
		}
	}
	assignee, ok := s.resolveAssignee(w, c, in.AssignedMemberID)
	if !ok {
		return
	}
	created, err := s.St.CreateLead(store.Lead{
		TenantID: c.Member.TenantID, ContactID: in.ContactID, Status: in.Status, SourceRefID: sourceRefID,
	}, c.Member.ID, assignee)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead create failed")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleLeadList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	filter := ""
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		filter = c.Member.ID
	}
	items, err := s.St.ListLeads(c.Member.TenantID, filter)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead list failed")
		return
	}
	if items == nil {
		items = []store.Lead{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) leadRecord(w http.ResponseWriter, r *http.Request, c *caller, action authz.Action) (store.Lead, bool) {
	id := r.PathValue("id")
	rec, err := s.St.GetLead(id, c.Member.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.Lead{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
		return store.Lead{}, false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: rec.TenantID, AssigneeMemberID: rec.AssignedMemberID}, w) {
		return store.Lead{}, false
	}
	return rec, true
}

func (s *Server) handleLeadGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.leadRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleLeadPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.leadRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Status           *string `json:"status"`
		AssignedMemberID *string `json:"assigned_member_id"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Status != nil && !validLeadStatus[*in.Status] {
		fail(w, http.StatusBadRequest, "bad_request", "status must be new, in_progress, converted or closed")
		return
	}
	var patch store.LeadPatch
	patch.Status = in.Status
	if in.AssignedMemberID != nil {
		if authz.Role(c.Member.Role) != authz.RoleOwner {
			fail(w, http.StatusForbidden, authz.ReasonForbidden, "only the owner can reassign records")
			return
		}
		assignee, ok := s.resolveAssignee(w, c, *in.AssignedMemberID)
		if !ok {
			return
		}
		patch.AssignedTo = &assignee
	}
	updated, err := s.St.UpdateLead(rec.ID, c.Member.TenantID, patch)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead update failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ---- opportunities ----------------------------------------------------------

// handleOppCreate creates an opportunity. 阶段/金额/概率/预计时间由获客原生域管理;
// won 及其他阶段的语义红线见 store.Opportunity。
func (s *Server) handleOppCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		ContactID        string  `json:"contact_id"`
		Title            string  `json:"title"`
		Stage            string  `json:"stage"`
		BusinessCategory string  `json:"business_category"`
		AmountCents      *int64  `json:"amount_cents"`
		Probability      *int    `json:"probability"`
		ExpectedCloseAt  *string `json:"expected_close_at"`
		AssignedMemberID string  `json:"assigned_member_id"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ContactID == "" || in.Title == "" {
		fail(w, http.StatusBadRequest, "bad_request", "contact_id and title are required")
		return
	}
	if in.Stage == "" {
		in.Stage = "open"
	}
	if !validOppStage[in.Stage] {
		fail(w, http.StatusBadRequest, "bad_request",
			"stage must be open, qualified, proposal, negotiation, won or closed_lost")
		return
	}
	if !validCategories[in.BusinessCategory] {
		fail(w, http.StatusBadRequest, "bad_request", "business_category must be merchant_customer or creative_service")
		return
	}
	if in.Probability != nil && (*in.Probability < 0 || *in.Probability > 100) {
		fail(w, http.StatusBadRequest, "bad_request", "probability must be between 0 and 100")
		return
	}
	if in.ExpectedCloseAt != nil && !validRFC3339(*in.ExpectedCloseAt) {
		fail(w, http.StatusBadRequest, "bad_request", "expected_close_at must be an RFC3339 timestamp")
		return
	}
	if _, err := s.St.GetContact(in.ContactID, c.Member.TenantID); err != nil {
		fail(w, http.StatusBadRequest, "bad_contact", "contact_id does not exist in this tenant")
		return
	}
	assignee, ok := s.resolveAssignee(w, c, in.AssignedMemberID)
	if !ok {
		return
	}
	created, err := s.St.CreateOpportunity(store.Opportunity{
		TenantID: c.Member.TenantID, ContactID: in.ContactID, Title: in.Title,
		Stage: in.Stage, BusinessCategory: in.BusinessCategory,
		AmountCents: in.AmountCents, Probability: derefInt(in.Probability),
		ExpectedCloseAt: in.ExpectedCloseAt,
	}, c.Member.ID, assignee)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "opportunity create failed")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleOppList requires business_category: 商家经营销售与创意服务从列表层隔离,
// 不提供跨类别列表/合计(coordinator 拍板,FEAT-0194 分域补充第 2 条)。
func (s *Server) handleOppList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	category := r.URL.Query().Get("category")
	if !validCategories[category] {
		fail(w, http.StatusBadRequest, "bad_request",
			"category query parameter is required and must be merchant_customer or creative_service (no cross-category list)")
		return
	}
	filter := ""
	if authz.Role(c.Member.Role) != authz.RoleOwner {
		filter = c.Member.ID // sales/agent only ever see their own records
	}
	items, err := s.St.ListOpportunities(c.Member.TenantID, category, filter)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "opportunity list failed")
		return
	}
	if items == nil {
		items = []store.Opportunity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) oppRecord(w http.ResponseWriter, r *http.Request, c *caller, action authz.Action) (store.Opportunity, bool) {
	id := r.PathValue("id")
	rec, err := s.St.GetOpportunity(id, c.Member.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.Opportunity{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "opportunity lookup failed")
		return store.Opportunity{}, false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: rec.TenantID, AssigneeMemberID: rec.AssignedMemberID}, w) {
		return store.Opportunity{}, false
	}
	return rec, true
}

func (s *Server) handleOppGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.oppRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// jsonOptInt distinguishes an absent field (no change) from an explicit JSON
// null (clear the value -> 金额回到 unknown,绝不静默当 0)。
type jsonOptInt struct {
	Set   bool
	Value *int64
}

func (o *jsonOptInt) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

type jsonOptString struct {
	Set   bool
	Value *string
}

func (o *jsonOptString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// handleOppPatch edits title/amount/probability/expected_close_at/assignee.
// Stage is NOT patchable: 阶段转换必须走 POST /stage(带权限与审计),显式 400 拒绝绕行。
func (s *Server) handleOppPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.oppRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Title            *string        `json:"title"`
		Stage            *string        `json:"stage"`
		AmountCents      jsonOptInt     `json:"amount_cents"`
		Probability      *int           `json:"probability"`
		ExpectedCloseAt  jsonOptString  `json:"expected_close_at"`
		AssignedMemberID *string        `json:"assigned_member_id"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Stage != nil {
		fail(w, http.StatusBadRequest, "bad_request",
			"stage cannot be patched; use POST /api/v1/opportunities/{id}/stage (audited transition)")
		return
	}
	if in.Title != nil && *in.Title == "" {
		fail(w, http.StatusBadRequest, "bad_request", "title must be a non-empty string")
		return
	}
	if in.Probability != nil && (*in.Probability < 0 || *in.Probability > 100) {
		fail(w, http.StatusBadRequest, "bad_request", "probability must be between 0 and 100")
		return
	}
	if in.ExpectedCloseAt.Value != nil && *in.ExpectedCloseAt.Value != "" && !validRFC3339(*in.ExpectedCloseAt.Value) {
		fail(w, http.StatusBadRequest, "bad_request", "expected_close_at must be an RFC3339 timestamp")
		return
	}
	var patch store.OpportunityPatch
	patch.Title = in.Title
	if in.AmountCents.Set {
		patch.AmountSet = true
		patch.AmountCents = in.AmountCents.Value
	}
	patch.Probability = in.Probability
	if in.ExpectedCloseAt.Set {
		patch.ExpectedCloseSet = true
		patch.ExpectedCloseAt = in.ExpectedCloseAt.Value
	}
	if in.AssignedMemberID != nil {
		if authz.Role(c.Member.Role) != authz.RoleOwner {
			fail(w, http.StatusForbidden, authz.ReasonForbidden, "only the owner can reassign records")
			return
		}
		assignee, ok := s.resolveAssignee(w, c, *in.AssignedMemberID)
		if !ok {
			return
		}
		patch.AssignedTo = &assignee
	}
	updated, err := s.St.UpdateOpportunity(rec.ID, c.Member.TenantID, patch)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "opportunity update failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
