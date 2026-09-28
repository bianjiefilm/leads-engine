package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/crmtenant"
	"github.com/bianjiefilm/leads-engine/server/internal/identity"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) serveChannelOperator(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, principal identity.Principal, tenantID string) bool {
	op, err := s.St.GetChannelOperator(principal.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel operator lookup failed")
		return true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/channel/tenant-status" {
		c := &caller{Principal: principal, Operator: &op}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey, c)))
		return true
	}
	if r.Method == http.MethodGet && channelContactRead(r.URL.Path) {
		active, err := s.St.HasActiveSupport(tenantID, principal.ID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "delegation lookup failed")
			return true
		}
		if !active || !crmtenant.ChannelAdminPlaintext(active, false) {
			fail(w, http.StatusForbidden, "delegation_required", "channel operator needs an independent support or operation delegation")
			return true
		}
		s.writeDelegatedContacts(w, r, tenantID)
		return true
	}
	fail(w, http.StatusForbidden, "delegation_required", "channel operator needs an independent support or operation delegation")
	return true
}

func channelContactRead(path string) bool {
	if path == "/api/v1/contacts" {
		return true
	}
	rest, ok := strings.CutPrefix(path, "/api/v1/contacts/")
	if !ok || rest == "" || rest == "export" || strings.Contains(rest, "/") {
		return false
	}
	return true
}

func (s *Server) writeDelegatedContacts(w http.ResponseWriter, r *http.Request, tenantID string) {
	if id := strings.TrimPrefix(r.URL.Path, "/api/v1/contacts/"); id != "" && id != "/api/v1/contacts" && !strings.Contains(id, "/") && r.URL.Path != "/api/v1/contacts" {
		item, err := s.St.GetContact(id, tenantID)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "not_found", "record not found")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "contact lookup failed")
			return
		}
		item.OriginBrandID = s.St.ContactOriginBrand(item.ID, tenantID)
		writeJSON(w, http.StatusOK, item)
		return
	}
	items, err := s.St.ListContacts(tenantID, "", store.ContactFilter{})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact list failed")
		return
	}
	if items == nil {
		items = []store.Contact{}
	}
	s.St.FillContactOrigins(tenantID, items)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) bindWriteTenant(w http.ResponseWriter, c *caller, bodyTenant, brandID string) bool {
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
		return false
	}
	if _, err := crmtenant.AuthorityTenant(c.Member.TenantID, bodyTenant, brandID); err != nil {
		fail(w, http.StatusBadRequest, authz.ReasonCrossTenant, "brand is a source, not a tenant")
		return false
	}
	if s.St.TenantLifecycle(c.Member.TenantID) == "suspended" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "tenant_suspended", "message": "this tenant is paused",
			"history_retained": true, "data_lost": false,
		})
		return false
	}
	return true
}

func (s *Server) handleBrandCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		BrandID     string `json:"brand_id"`
		DisplayName string `json:"display_name"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.BrandID) == "" || strings.TrimSpace(in.DisplayName) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "brand_id and display_name are required")
		return
	}
	if in.BrandID == c.Member.TenantID {
		fail(w, http.StatusBadRequest, "brand_not_tenant", "a brand id cannot be the tenant id")
		return
	}
	row, err := s.St.UpsertBrand(in.BrandID, in.DisplayName)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "brand create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"brand_id": row.ID, "display_name": row.DisplayName, "status": row.Status,
	})
}

func (s *Server) handleBrandRename(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		DisplayName string `json:"display_name"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	row, err := s.St.RenameBrand(r.PathValue("id"), strings.TrimSpace(in.DisplayName))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "brand not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "brand rename failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brand_id": row.ID, "display_name": row.DisplayName, "status": row.Status})
}

func (s *Server) handleBrandLifecycle(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Status != "active" && in.Status != "suspended" {
		fail(w, http.StatusBadRequest, "bad_request", "status must be active or suspended")
		return
	}
	row, err := s.St.SetBrandStatus(r.PathValue("id"), in.Status)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "brand not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "brand lifecycle failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"brand_id": row.ID, "brand_status": row.Status,
		"tenant_status": s.St.TenantLifecycle(c.Member.TenantID),
	})
}

func (s *Server) handleTenantLifecycle(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Status != "active" && in.Status != "suspended" {
		fail(w, http.StatusBadRequest, "bad_request", "status must be active or suspended")
		return
	}
	if err := s.St.SetTenantLifecycle(c.Member.TenantID, in.Status); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lifecycle failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": c.Member.TenantID, "tenant_status": in.Status,
		"brand_status": s.St.BrandStatusForTenant(c.Member.TenantID),
	})
}

func (s *Server) handleTenantLifecycleGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	tenantStatus := s.St.TenantLifecycle(c.Member.TenantID)
	brandStatus := s.St.BrandStatusForTenant(c.Member.TenantID)
	tenantPaused, brandPaused := crmtenant.IndependentPause(tenantStatus, brandStatus)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": c.Member.TenantID, "tenant_status": tenantStatus, "brand_status": brandStatus,
		"tenant_suspended": tenantPaused, "brand_suspended": brandPaused,
	})
}

func (s *Server) handleDelegationCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		PrincipalRef string `json:"principal_ref"`
		Kind         string `json:"kind"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Kind != "support" && in.Kind != "operation" && in.Kind != "offboarding" {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be support, operation or offboarding")
		return
	}
	if !strings.HasPrefix(in.PrincipalRef, "usr_") {
		fail(w, http.StatusBadRequest, "bad_request", "principal_ref must be a platform principal")
		return
	}
	row, err := s.St.CreateCRMDelegation(c.Member.TenantID, in.PrincipalRef, in.Kind)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "delegation create failed")
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) handleDelegationRevoke(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	if err := s.St.RevokeCRMDelegation(c.Member.TenantID, r.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "not_found", "delegation not found")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "delegation revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func (s *Server) handleChannelTenantStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	tenantID := strings.TrimSpace(r.Header.Get(tenantHeader))
	if c == nil || (c.Member == nil && c.Operator == nil) {
		fail(w, http.StatusForbidden, "delegation_required", "channel operator needs an independent support or operation delegation")
		return
	}
	if c.Member != nil {
		tenantID = c.Member.TenantID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": tenantID, "tenant_status": s.St.TenantLifecycle(tenantID),
		"brand_status": s.St.BrandStatusForTenant(tenantID), "history_retained": true,
	})
}

func (s *Server) handleCRMExportCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionExport, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Purpose string `json:"purpose"`
		BrandID string `json:"brand_id"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Purpose) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "purpose is required")
		return
	}
	job, err := s.St.CreateExportJob(c.Member.TenantID, in.BrandID, c.Principal.ID, in.Purpose, 24*time.Hour)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export job failed")
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleCRMExportDownload(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
		return
	}
	if r.Header.Get("X-Export-Confirm") != "1" {
		fail(w, http.StatusUnauthorized, "reauth_required", "download must be confirmed with a fresh authorization")
		return
	}
	if !s.requireAction(c, authz.ActionExport, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	job, err := s.St.GetExportJob(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "export job not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export lookup failed")
		return
	}
	expires, err := time.Parse(time.RFC3339Nano, job.ExpiresAt)
	if err != nil {
		expires, err = time.Parse(time.RFC3339, job.ExpiresAt)
	}
	expired := err != nil || !expires.After(time.Now().UTC())
	if !crmtenant.DownloadAllowed(crmtenant.ExportJob{
		TenantID: job.TenantID, Requester: job.Requester, Expired: expired,
	}, c.Member.TenantID, c.Principal.ID, true) {
		if expired && job.TenantID == c.Member.TenantID && job.Requester == c.Principal.ID {
			fail(w, http.StatusGone, "export_expired", "export job has expired")
			return
		}
		fail(w, http.StatusNotFound, "not_found", "export job not found")
		return
	}
	bundle, err := s.St.TenantExport(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export failed")
		return
	}
	bundle["brand_id"] = job.BrandID
	bundle["purpose"] = job.Purpose
	writeJSON(w, http.StatusOK, bundle)
}
