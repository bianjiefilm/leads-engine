// Package crmtenant decides which tenant owns a CRM row, and what a brand,
// a channel operator, an export, or a pause is allowed to change.
// Brand is a source and a display name. It is not a second CRM database
// and it is not a tenant.
package crmtenant

import "errors"

var (
	ErrTenantRequired = errors.New("tenant required")
	ErrCrossTenant    = errors.New("cross tenant")
)

// AuthorityTenant returns the membership tenant. A brand id in the body is
// never used as the tenant. A body tenant that disagrees with the member is
// refused. An empty member tenant cannot be filled in from a brand.
func AuthorityTenant(memberTenant, bodyTenant, brandID string) (string, error) {
	if memberTenant == "" {
		return "", ErrTenantRequired
	}
	// A brand id placed in the tenant field is display context, not a move.
	if bodyTenant != "" && bodyTenant != memberTenant && bodyTenant != brandID {
		return "", ErrCrossTenant
	}
	return memberTenant, nil
}

// PhonePlace is one contact occurrence of a phone number.
type PhonePlace struct {
	TenantID string
	BrandID  string
	Phone    string
}

// AllowContactMerge is true only inside one tenant and one brand. Two tenants
// or two brands never merge, even when the phone matches.
func AllowContactMerge(a, b PhonePlace) bool {
	if a.TenantID == "" || a.TenantID != b.TenantID {
		return false
	}
	if a.BrandID != "" && b.BrandID != "" && a.BrandID != b.BrandID {
		return false
	}
	return true
}

// BindPlatformPrincipal is always false. A phone number is not a principal.
func BindPlatformPrincipal(string) bool { return false }

// ContactAnchor is the history a brand rename must leave alone.
type ContactAnchor struct {
	TenantID       string
	BrandID        string
	BrandDisplay   string
	ConsentSource  string
	ConsentChannel string
	OpportunityID  string
	Stage          string
}

// AfterBrandRename changes only the display name.
func AfterBrandRename(before ContactAnchor, display string) ContactAnchor {
	before.BrandDisplay = display
	return before
}

// ChannelAdminPlaintext is true only when support or operation is active.
func ChannelAdminPlaintext(supportActive, operationActive bool) bool {
	return supportActive || operationActive
}

// ExportBundle is a tenant export before it is shown to the caller.
type ExportBundle struct {
	TenantID string
	Contacts []map[string]string
	Extra    map[string]string
}

// SanitizeExport keeps this tenant's rows and safe source refs. Channel
// purchase prices and internal tokens are removed.
func SanitizeExport(in ExportBundle) ExportBundle {
	out := ExportBundle{TenantID: in.TenantID, Extra: map[string]string{}}
	for _, row := range in.Contacts {
		if row["tenant_id"] == in.TenantID {
			out.Contacts = append(out.Contacts, row)
		}
	}
	for k, v := range in.Extra {
		switch k {
		case "purchase_price_cents", "procurement_price", "internal_token", "session_token", "identity_token":
			continue
		default:
			out.Extra[k] = v
		}
	}
	return out
}

// ExportJob is the binding checked again at download time.
type ExportJob struct {
	TenantID  string
	Requester string
	Expired   bool
}

// DownloadAllowed requires the confirm step, the same tenant, the same
// requester, and a job that has not expired.
func DownloadAllowed(job ExportJob, callerTenant, callerPrincipal string, confirmed bool) bool {
	if !confirmed || job.Expired {
		return false
	}
	return job.TenantID == callerTenant && job.Requester == callerPrincipal
}

// NewPaidAIAllowed stops a new paid AI action after a downgrade or when the
// channel quota row exists and is empty. Absence of a quota row is not a stop.
func NewPaidAIAllowed(planStatus string, quotaKnown bool, quota int) bool {
	if planStatus == "downgraded" {
		return false
	}
	if quotaKnown && quota <= 0 {
		return false
	}
	return true
}

// History is whether an existing CRM row stays readable.
type History struct {
	Readable bool
	Deleted  bool
	Hidden   bool
}

// HistoryKept keeps customers, leads, opportunities, and follow-ups visible.
func HistoryKept(kind string) History {
	switch kind {
	case "contact", "lead", "opportunity", "follow_up":
		return History{Readable: true}
	default:
		return History{}
	}
}

// IndependentPause reports the two flags separately.
func IndependentPause(tenantStatus, brandStatus string) (tenantSuspended, brandSuspended bool) {
	return tenantStatus == "suspended", brandStatus == "suspended"
}

// ResumeRestores reports whether resume turns a revoked membership, consent,
// or delegation back on. It never does.
func ResumeRestores(memberRevoked, consentRevoked, delegationRevoked bool) (bool, bool, bool) {
	if memberRevoked || consentRevoked || delegationRevoked {
		return false, false, false
	}
	return false, false, false
}

// ReplayMarketing is false once consent has been revoked. A replay does not
// grant marketing permission by itself.
func ReplayMarketing(revoked bool) bool {
	if revoked {
		return false
	}
	return false
}
