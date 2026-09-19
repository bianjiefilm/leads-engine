package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// adminOnly authorizes owner-only actions.
func (s *Server) adminOnly(c *caller, w http.ResponseWriter) bool {
	return s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w)
}

// validPrincipalRef enforces the platform principal shape (identity derive
// produces usr_*). An email or phone number is NEVER a valid principal ref —
// this is the format-level half of the identity discipline; the truth still
// comes from identity session resolution, not from this check.
func validPrincipalRef(ref string) bool {
	return strings.HasPrefix(ref, "usr_") && len(ref) > len("usr_")
}

func (s *Server) handleMemberList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.adminOnly(c, w) {
		return
	}
	items, err := s.St.ListMembers(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "member list failed")
		return
	}
	if items == nil {
		items = []store.Member{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleMemberCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.adminOnly(c, w) {
		return
	}
	var in struct {
		PrincipalRef string `json:"principal_ref"`
		Role         string `json:"role"`
		DisplayName  string `json:"display_name"`
		Enabled      *bool  `json:"enabled"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	// 身份纪律:principal_ref 只接受平台 principal(usr_*);拒绝邮箱/手机号形态,
	// 绝不从 CRM 数据派生平台身份;此端点也绝不触发任何平台侧开户。
	if !validPrincipalRef(in.PrincipalRef) {
		fail(w, http.StatusBadRequest, "bad_principal",
			"principal_ref must be a platform principal id (usr_*); emails or phone numbers are not identities")
		return
	}
	if !authz.Role(in.Role).Valid() {
		fail(w, http.StatusBadRequest, "bad_request", "role must be owner, sales or agent")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	m, err := s.St.CreateMember(c.Member.TenantID, in.PrincipalRef, in.Role, in.DisplayName, c.Member.ID, enabled)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			fail(w, http.StatusConflict, "conflict", "principal is already a member of this tenant")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "member create failed")
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleMemberPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.adminOnly(c, w) {
		return
	}
	// principal_ref is immutable: any attempt to send it is rejected outright.
	var raw map[string]json.RawMessage
	body, err := readAll(r)
	if err != nil || json.Unmarshal(body, &raw) != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if _, present := raw["principal_ref"]; present {
		fail(w, http.StatusBadRequest, "principal_immutable",
			"principal_ref is bound to the platform identity and can never be re-pointed")
		return
	}
	var in struct {
		Role        *string `json:"role"`
		Enabled     *bool   `json:"enabled"`
		DisplayName *string `json:"display_name"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			fail(w, http.StatusBadRequest, "bad_request", "invalid fields")
			return
		}
	}
	if in.Role != nil && !authz.Role(*in.Role).Valid() {
		fail(w, http.StatusBadRequest, "bad_request", "role must be owner, sales or agent")
		return
	}
	id := r.PathValue("id")
	target, err := s.St.GetMember(id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && target.TenantID != c.Member.TenantID) {
		fail(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "member lookup failed")
		return
	}
	updated, err := s.St.UpdateMember(id, in.Role, in.Enabled, in.DisplayName)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "member update failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleGrantCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.adminOnly(c, w) {
		return
	}
	var in struct {
		PrincipalRef string `json:"principal_ref"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !validPrincipalRef(in.PrincipalRef) {
		fail(w, http.StatusBadRequest, "bad_principal", "principal_ref must be a platform principal id (usr_*)")
		return
	}
	// An agent grant only makes sense together with an agent membership in this
	// tenant; grants never create memberships and never share a global pool.
	m, err := s.St.GetMemberByPrincipal(c.Member.TenantID, in.PrincipalRef)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && authz.Role(m.Role) != authz.RoleAgent) {
		fail(w, http.StatusBadRequest, "bad_grant_target",
			"the principal must already be an agent member of this tenant")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "member lookup failed")
		return
	}
	g, err := s.St.CreateAgentGrant(c.Member.TenantID, in.PrincipalRef, c.Member.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			fail(w, http.StatusConflict, "conflict", "grant already exists")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "grant create failed")
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleGrantList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.adminOnly(c, w) {
		return
	}
	items, err := s.St.ListAgentGrants(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "grant list failed")
		return
	}
	if items == nil {
		items = []store.AgentGrant{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGrantDelete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.adminOnly(c, w) {
		return
	}
	if err := s.St.DeleteAgentGrant(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "grant not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleExport is the owner-only tenant data export.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionExport, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	contacts, err := s.St.ListContacts(c.Member.TenantID, "", store.ContactFilter{})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export failed")
		return
	}
	leads, err := s.St.ListLeads(c.Member.TenantID, "")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export failed")
		return
	}
	// Export dumps raw records per business category (two separate arrays);
	// this is a record export, not a cross-category aggregate: 成交额/漏斗
	// 统计仍只在按类别的 stats 端点提供。
	merchantOpps, err := s.St.ListOpportunities(c.Member.TenantID, "merchant_customer", "")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export failed")
		return
	}
	creativeOpps, err := s.St.ListOpportunities(c.Member.TenantID, "creative_service", "")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": c.Member.TenantID,
		"contacts":  contacts,
		"leads":     leads,
		"opportunities": map[string]any{
			"merchant_customer": merchantOpps,
			"creative_service":  creativeOpps,
		},
	})
}

func readAll(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, 1<<20))
}
