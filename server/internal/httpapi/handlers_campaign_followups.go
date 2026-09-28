package httpapi

import (
	"net/http"
	"strings"
)

// GET /internal/v1/campaign-follow-ups?campaign_ref=
//
// Restricted summary for the touch workbench. It returns lead ids that still
// have no follow-up. Names, phones and notes stay in the leads domain.
func (s *Server) handleCampaignFollowUps(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.TrimSpace(r.Header.Get(tenantHeader))
	if tenantID == "" {
		fail(w, http.StatusBadRequest, "tenant_required", "header "+tenantHeader+" selects the workspace tenant")
		return
	}
	storeTenant, ok := s.storeTenantForEvent(tenantID)
	if !ok {
		fail(w, http.StatusNotFound, "unknown_tenant", "target tenant is not provisioned")
		return
	}
	tenantID = storeTenant
	refs := r.URL.Query()["campaign_ref"]
	if len(refs) == 0 || len(refs) > 50 {
		fail(w, http.StatusBadRequest, "bad_request", "campaign_ref is required and limited to 50")
		return
	}
	clean := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] || len(ref) > 128 {
			continue
		}
		seen[ref] = true
		clean = append(clean, ref)
	}
	if len(clean) == 0 {
		fail(w, http.StatusBadRequest, "bad_request", "campaign_ref is required")
		return
	}
	grouped, err := s.St.PendingLeadIDsByCampaign(tenantID, clean)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up summary failed")
		return
	}
	campaigns := make([]map[string]any, 0, len(clean))
	for _, ref := range clean {
		ids := grouped[ref]
		if ids == nil {
			ids = []string{}
		}
		campaigns = append(campaigns, map[string]any{
			"campaign_ref":      ref,
			"pending_follow_up": len(ids),
			"lead_ids":          ids,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"known": true, "campaigns": campaigns})
}

// storeTenantForEvent returns the tenant that already exists for this event.
// An operator binding can point an app-local source tenant at that existing
// row. It does not create a tenant and does not rewrite a tenant that is
// already provisioned here.
func (s *Server) storeTenantForEvent(eventTenant string) (string, bool) {
	eventTenant = strings.TrimSpace(eventTenant)
	if eventTenant == "" {
		return "", false
	}
	if _, err := s.St.GetTenant(eventTenant); err == nil {
		return eventTenant, true
	}
	target := strings.TrimSpace(s.Cfg.TenantBindings[eventTenant])
	if target == "" || target == eventTenant {
		return "", false
	}
	if _, err := s.St.GetTenant(target); err != nil {
		return "", false
	}
	return target, true
}
