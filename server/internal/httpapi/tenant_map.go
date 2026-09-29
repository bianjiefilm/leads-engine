package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/store"
	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
)

func (s *Server) bindingProblems() *tenantmap.Error {
	if len(s.Cfg.TenantBindingProblems) == 0 {
		return nil
	}
	return &tenantmap.Error{
		Kind:   tenantmap.KindConfig,
		Detail: strings.Join(s.Cfg.TenantBindingProblems, "; "),
		Hint:   "Fix LEADS_TENANT_BINDINGS so each source key appears once. The whole set was ignored, including any earlier pair. Restart after correcting it. Existing leads are not moved.",
	}
}

func (s *Server) lookupTenantExists(id string) (bool, error) {
	if s.mapLookupFn != nil {
		return s.mapLookupFn(id)
	}
	_, err := s.St.GetTenant(id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func writeTenantMapError(w http.ResponseWriter, err error) {
	var me *tenantmap.Error
	if !errors.As(err, &me) {
		if errors.Is(err, store.ErrAmbiguousNotify) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "tenant_map_conflict",
				"message": "stored notifications for this source disagree",
				"hint":    "Replay is refused because more than one target is stored. This server does not pick one or move leads.",
			})
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "tenant map failed")
		return
	}
	switch me.Kind {
	case tenantmap.KindLookup:
		fail(w, http.StatusInternalServerError, "tenant_lookup_failed", "tenant lookup failed")
	case tenantmap.KindConflict:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "tenant_map_conflict",
			"message": "tenant mapping is ambiguous",
			"hint":    me.Hint,
		})
	case tenantmap.KindTargetMissing:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":   "unknown_tenant",
			"message": "target tenant is not provisioned",
			"hint":    me.Hint,
		})
	case tenantmap.KindUnknown:
		fail(w, http.StatusNotFound, "unknown_tenant", "target tenant is not provisioned")
	case tenantmap.KindConfig:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config_gate_tenant_map",
			"message": "tenant bindings are invalid and were not applied",
			"hint":    me.Hint,
			"detail":  me.Detail,
		})
	default:
		fail(w, http.StatusInternalServerError, "internal", "tenant map failed")
	}
}

func (s *Server) followUpStoreTenant(external, sourceApp string) (string, error) {
	if err := s.bindingProblems(); err != nil {
		return "", err
	}
	targets, err := s.St.DistinctNotifyTargets(strings.TrimSpace(external), strings.TrimSpace(sourceApp))
	if err != nil {
		return "", &tenantmap.Error{Kind: tenantmap.KindLookup, Hint: "database read failed while reading the stored tenant map; the follow-up count was not switched to another tenant"}
	}
	if len(targets) > 1 {
		return "", &tenantmap.Error{Kind: tenantmap.KindConflict, Hint: "This external tenant is stored against more than one target. Pass source_app to read one source. Lead ids are not merged, and this server does not move them."}
	}
	if len(targets) == 1 {
		return targets[0], nil
	}
	decision, err := tenantmap.ResolveFollowUp(strings.TrimSpace(sourceApp), external, s.Cfg.TenantBindings, tenantmap.Sticky{}, s.lookupTenantExists)
	if err != nil {
		return "", err
	}
	return decision.TargetTenant, nil
}

func (s *Server) admitNotifyTenant(app, sourceTenant, sourceRef string) (tenantmap.Decision, error) {
	if err := s.bindingProblems(); err != nil {
		return tenantmap.Decision{}, err
	}
	ns := tenantmap.NamespaceNotify
	if owner, err := s.St.NotifySourceRefOwner(app, ns, sourceTenant, sourceRef); err == nil {
		return decisionFromInbox(owner, sourceTenant), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return tenantmap.Decision{}, mapStoreErr(err)
	}
	if rev, err := s.St.NotifyRevocationBySource(app, ns, sourceTenant, sourceRef); err == nil {
		return decisionFromRevocation(rev, sourceTenant), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return tenantmap.Decision{}, mapStoreErr(err)
	}
	if legacy, err := s.St.LegacyNotifySourceRefOwner(app, sourceRef); err == nil {
		return decisionFromInbox(legacy, sourceTenant), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return tenantmap.Decision{}, mapStoreErr(err)
	}
	if legacyRev, err := s.St.LegacyNotifyRevocation(app, sourceRef); err == nil {
		return decisionFromRevocation(legacyRev, sourceTenant), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return tenantmap.Decision{}, mapStoreErr(err)
	}
	stickyRow, err := s.St.StickyNotifyMap(app, ns, sourceTenant)
	if err != nil {
		return tenantmap.Decision{}, mapStoreErr(err)
	}
	sticky := tenantmap.Sticky{
		Found: stickyRow.Found, Ambiguous: stickyRow.Ambiguous,
		Target: stickyRow.Target, Basis: stickyRow.Basis, Version: stickyRow.Version,
	}
	return tenantmap.Resolve(app, ns, sourceTenant, s.Cfg.TenantBindings, sticky, s.lookupTenantExists)
}

func mapStoreErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrAmbiguousNotify) {
		return err
	}
	var me *tenantmap.Error
	if errors.As(err, &me) {
		return err
	}
	return &tenantmap.Error{Kind: tenantmap.KindLookup, Hint: "database read failed; the event was not treated as missing"}
}

func decisionFromInbox(n store.NotifyInbox, sourceTenant string) tenantmap.Decision {
	target := n.MapTargetTenantID
	if target == "" {
		target = n.TenantID
	}
	basis := n.MapBasis
	if basis == "" {
		basis = tenantmap.BasisLegacyStored
	}
	version := n.MapVersion
	if version < 1 {
		version = 1
	}
	src := n.SourceTenantID
	if src == "" {
		src = sourceTenant
	}
	ns := n.SourceNS
	if ns == "" {
		ns = tenantmap.NamespaceNotify
	}
	return tenantmap.Decision{
		SourceApp: n.SourceApp, SourceNS: ns, SourceTenant: src,
		TargetTenant: target, Version: version, Basis: basis,
	}
}

func decisionFromRevocation(rev store.NotifyRevocation, sourceTenant string) tenantmap.Decision {
	target := rev.MapTargetTenantID
	if target == "" {
		target = rev.TenantID
	}
	basis := rev.MapBasis
	if basis == "" {
		basis = tenantmap.BasisLegacyStored
	}
	version := rev.MapVersion
	if version < 1 {
		version = 1
	}
	src := rev.SourceTenantID
	if src == "" {
		src = sourceTenant
	}
	ns := rev.SourceNS
	if ns == "" {
		ns = tenantmap.NamespaceNotify
	}
	return tenantmap.Decision{
		SourceApp: rev.SourceApp, SourceNS: ns, SourceTenant: src,
		TargetTenant: target, Version: version, Basis: basis,
	}
}

func (s *Server) lookupNotifyEvent(app, ns, sourceTenant, eventID string) (store.NotifyInbox, error) {
	row, err := s.St.GetNotifyInboxBySourceEvent(app, ns, sourceTenant, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.St.GetLegacyNotifyInboxByEvent(app, eventID)
	}
	return row, err
}

func (s *Server) lookupNotifyFact(app, ns, sourceTenant, eventType, sourceRef string) (store.NotifyInbox, error) {
	row, err := s.St.GetNotifyInboxBySourceFact(app, ns, sourceTenant, eventType, sourceRef)
	if errors.Is(err, sql.ErrNoRows) {
		return s.St.GetLegacyNotifyInboxByFact(app, eventType, sourceRef)
	}
	return row, err
}
