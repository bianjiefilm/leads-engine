// Package tenantmap decides which leads tenant owns one external event.
// It is not an identity service and it does not create tenants.
// A local row with the same id is never a silent fallback when a binding exists.
package tenantmap

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	BasisOperatorBinding = "operator_binding"
	BasisLeadsPrimaryKey = "leads_primary_key"
	BasisLegacyStored    = "legacy_stored_tenant"
	NamespaceNotify      = "notify"
	Wildcard             = "*"
)

const (
	KindLookup        = "lookup"
	KindConflict      = "conflict"
	KindTargetMissing = "target_missing"
	KindUnknown       = "unknown"
	KindConfig        = "config"
)

// Binding is one explicit operator map entry.
type Binding struct {
	SourceApp      string
	SourceNS       string
	ExternalTenant string
	TargetTenant   string
	Version        int
	Basis          string
}

// Sticky is the target already stored for a source key.
// Ambiguous means those rows name more than one target.
type Sticky struct {
	Found     bool
	Ambiguous bool
	Target    string
	Basis     string
	Version   int
}

// Decision is the ownership written onto a new event.
type Decision struct {
	SourceApp    string
	SourceNS     string
	SourceTenant string
	TargetTenant string
	Version      int
	Basis        string
}

// Error is a refused map. Hint is the operator action. Detail is config text.
type Error struct {
	Kind   string
	Hint   string
	Detail string
}

func (e *Error) Error() string {
	if e == nil {
		return "tenant map"
	}
	if e.Detail != "" {
		return e.Kind + ": " + e.Detail
	}
	if e.Hint != "" {
		return e.Kind + ": " + e.Hint
	}
	return e.Kind
}

// Lookup reports whether a leads tenant id exists.
// err is a database failure, never "no row". exists is false only when the row is absent.
type Lookup func(id string) (exists bool, err error)

const hintConfig = "Fix LEADS_TENANT_BINDINGS so each source_app/source_ns/external appears once, either as external=target[@version] or source_app/source_ns/external=target[@version]. One invalid or duplicate entry drops the whole set; earlier pairs are not kept. Restart after the correction. Existing leads are not moved."

// Parse reads the operator list. Any invalid or overlapping entry rejects the whole list.
func Parse(raw string) ([]Binding, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []Binding
	seen := map[string]struct{}{}
	var problems []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			problems = append(problems, "empty binding entry")
			continue
		}
		b, err := parseOne(part)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		key := b.SourceApp + "\x00" + b.SourceNS + "\x00" + b.ExternalTenant
		if _, ok := seen[key]; ok {
			problems = append(problems, "duplicate binding for "+b.SourceApp+"/"+b.SourceNS+"/"+b.ExternalTenant)
			continue
		}
		seen[key] = struct{}{}
		out = append(out, b)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if overlaps(out[i], out[j]) {
				problems = append(problems, "overlapping bindings for external tenant "+out[i].ExternalTenant)
			}
		}
	}
	if len(problems) > 0 {
		return nil, &Error{Kind: KindConfig, Detail: strings.Join(problems, "; "), Hint: hintConfig}
	}
	return out, nil
}

func parseOne(part string) (Binding, error) {
	left, right, ok := strings.Cut(part, "=")
	if !ok {
		return Binding{}, fmt.Errorf("missing '=' in %q", part)
	}
	target, version, err := splitVersion(strings.TrimSpace(right))
	if err != nil {
		return Binding{}, fmt.Errorf("%s in %q", err.Error(), part)
	}
	app, ns, external, err := splitSource(strings.TrimSpace(left))
	if err != nil {
		return Binding{}, fmt.Errorf("%s in %q", err.Error(), part)
	}
	if !componentOK(app) || !componentOK(ns) || !idOK(external) || !idOK(target) {
		return Binding{}, fmt.Errorf("invalid id in %q", part)
	}
	if external == target {
		return Binding{}, fmt.Errorf("source and target are the same in %q", part)
	}
	return Binding{
		SourceApp: app, SourceNS: ns, ExternalTenant: external, TargetTenant: target,
		Version: version, Basis: BasisOperatorBinding,
	}, nil
}

func splitSource(left string) (app, ns, external string, err error) {
	if left == "" {
		return "", "", "", fmt.Errorf("empty source")
	}
	parts := strings.Split(left, "/")
	switch len(parts) {
	case 1:
		return Wildcard, Wildcard, parts[0], nil
	case 3:
		if parts[0] == Wildcard || parts[1] == Wildcard || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return "", "", "", fmt.Errorf("explicit binding cannot use a wildcard or an empty segment")
		}
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("source must be external or source_app/source_ns/external")
	}
}

func splitVersion(right string) (string, int, error) {
	target, ver, ok := strings.Cut(right, "@")
	if !ok {
		if strings.TrimSpace(target) == "" {
			return "", 0, fmt.Errorf("empty target")
		}
		return strings.TrimSpace(target), 1, nil
	}
	if strings.Contains(ver, "@") || ver == "" {
		return "", 0, fmt.Errorf("invalid version")
	}
	n, err := strconv.Atoi(ver)
	if err != nil || n < 1 {
		return "", 0, fmt.Errorf("version must be an integer >= 1")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 0, fmt.Errorf("empty target")
	}
	return target, n, nil
}

func componentOK(s string) bool {
	return s == Wildcard || idOK(s)
}

func idOK(s string) bool {
	if s == "" || s == Wildcard || len(s) > 256 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}

func overlaps(a, b Binding) bool {
	if a.ExternalTenant != b.ExternalTenant {
		return false
	}
	if a.SourceApp == b.SourceApp && a.SourceNS == b.SourceNS {
		return true
	}
	return wildcard(a) || wildcard(b)
}

func wildcard(b Binding) bool {
	return b.SourceApp == Wildcard && b.SourceNS == Wildcard
}

// Resolve chooses the target for a new source ref.
// lookup errors are returned as KindLookup and never treated as a missing row.
func Resolve(app, ns, external string, bindings []Binding, sticky Sticky, lookup Lookup) (Decision, error) {
	external = strings.TrimSpace(external)
	app = strings.TrimSpace(app)
	ns = strings.TrimSpace(ns)
	if external == "" || app == "" || ns == "" {
		return Decision{}, &Error{Kind: KindUnknown, Hint: "event tenant, source app, and source namespace are required"}
	}
	if lookup == nil {
		return Decision{}, &Error{Kind: KindLookup, Hint: "tenant lookup is not configured"}
	}
	if sticky.Ambiguous {
		return Decision{}, &Error{Kind: KindConflict, Hint: "Stored maps for " + app + "/" + ns + "/" + external + " name more than one target. New source refs are refused. Replay of an already stored event stays on its own row. This server does not pick a target or move leads."}
	}
	binding, found, err := selectBinding(app, ns, external, bindings)
	if err != nil {
		return Decision{}, err
	}
	sourceExists, err := lookup(external)
	if err != nil {
		return Decision{}, &Error{Kind: KindLookup, Hint: "database read failed while checking the source tenant; it was not treated as missing and the event was not routed elsewhere"}
	}
	targetExists := false
	if found {
		targetExists, err = lookup(binding.TargetTenant)
		if err != nil {
			return Decision{}, &Error{Kind: KindLookup, Hint: "database read failed while checking the binding target; it was not treated as missing"}
		}
	}
	base := Decision{SourceApp: app, SourceNS: ns, SourceTenant: external}
	if sticky.Found {
		if found && binding.TargetTenant != sticky.Target {
			return Decision{}, &Error{Kind: KindConflict, Hint: driftHint(app, ns, external, sticky, binding.TargetTenant)}
		}
		if found && !targetExists {
			return Decision{}, &Error{Kind: KindTargetMissing, Hint: missingHint(binding)}
		}
		if !found && sticky.Target != external {
			return Decision{}, &Error{Kind: KindConflict, Hint: "Events from " + external + " are stored on " + sticky.Target + " and that binding is no longer configured. New source refs are refused instead of attaching them to a local tenant. Restore the binding, or plan a migration that rewrites tenant_id on leads, contacts, consents, source_refs, notify_inbox, and notify_revocations together. Replay and revocation of a stored source ref stay on " + sticky.Target + "."}
		}
		out := base
		out.TargetTenant = sticky.Target
		out.Basis = sticky.Basis
		out.Version = sticky.Version
		if found {
			out.Basis = BasisOperatorBinding
			if binding.Version > out.Version {
				out.Version = binding.Version
			}
		}
		if out.Version < 1 {
			out.Version = 1
		}
		if out.Basis == "" {
			out.Basis = BasisLegacyStored
		}
		return out, nil
	}
	if found {
		if !targetExists {
			return Decision{}, &Error{Kind: KindTargetMissing, Hint: missingHint(binding)}
		}
		if sourceExists && binding.TargetTenant != external {
			return Decision{}, &Error{Kind: KindConflict, Hint: "Local tenant " + external + " already exists, and LEADS_TENANT_BINDINGS would send it to " + binding.TargetTenant + ". New events are refused. To keep " + external + ", remove that binding and restart. To send new events to " + binding.TargetTenant + ", delete local tenant " + external + " only when it has no CRM rows, then restart. This server does not rewrite lead tenant_id."}
		}
		base.TargetTenant = binding.TargetTenant
		base.Version = binding.Version
		base.Basis = BasisOperatorBinding
		return base, nil
	}
	if sourceExists {
		base.TargetTenant = external
		base.Version = 1
		base.Basis = BasisLeadsPrimaryKey
		return base, nil
	}
	return Decision{}, &Error{Kind: KindUnknown, Hint: "no explicit binding and the tenant is not provisioned in this database"}
}

func selectBinding(app, ns, external string, bindings []Binding) (Binding, bool, error) {
	var exact, wild []Binding
	for _, b := range bindings {
		if b.ExternalTenant != external {
			continue
		}
		if wildcard(b) {
			wild = append(wild, b)
			continue
		}
		if b.SourceApp == app && b.SourceNS == ns {
			exact = append(exact, b)
		}
	}
	if len(exact) > 1 || len(wild) > 1 || (len(exact) == 1 && len(wild) == 1) {
		return Binding{}, false, &Error{Kind: KindConfig, Hint: hintConfig, Detail: "more than one binding matches " + app + "/" + ns + "/" + external}
	}
	if len(exact) == 1 {
		return exact[0], true, nil
	}
	if len(wild) == 1 {
		return wild[0], true, nil
	}
	return Binding{}, false, nil
}

// ResolveFollowUp resolves a summary caller.
// An empty sourceApp is allowed only when every configured or stored target agrees.
func ResolveFollowUp(sourceApp, external string, bindings []Binding, sticky Sticky, lookup Lookup) (Decision, error) {
	if sticky.Ambiguous {
		return Decision{}, &Error{Kind: KindConflict, Hint: "This external tenant is stored against more than one target. Pass source_app to read one source. Lead ids are not merged, and this server does not move them."}
	}
	if sticky.Found {
		app := sourceApp
		if app == "" {
			app = Wildcard
		}
		return Decision{
			SourceApp: app, SourceNS: NamespaceNotify, SourceTenant: external,
			TargetTenant: sticky.Target, Version: sticky.Version, Basis: sticky.Basis,
		}, nil
	}
	if strings.TrimSpace(sourceApp) != "" {
		return Resolve(sourceApp, NamespaceNotify, external, bindings, Sticky{}, lookup)
	}
	var matched []Binding
	for _, b := range bindings {
		if b.ExternalTenant != strings.TrimSpace(external) {
			continue
		}
		if b.SourceNS == NamespaceNotify || b.SourceNS == Wildcard {
			matched = append(matched, b)
		}
	}
	targets := map[string]struct{}{}
	for _, b := range matched {
		targets[b.TargetTenant] = struct{}{}
	}
	switch len(targets) {
	case 0:
		return Resolve(Wildcard, NamespaceNotify, external, nil, Sticky{}, lookup)
	case 1:
		return Resolve(matched[0].SourceApp, matched[0].SourceNS, external, matched, Sticky{}, lookup)
	default:
		return Decision{}, &Error{Kind: KindConflict, Hint: "This external tenant is mapped to more than one target. Pass source_app to read one registered source. Lead ids from the other source are not returned."}
	}
}

func driftHint(app, ns, external string, sticky Sticky, configured string) string {
	return fmt.Sprintf("Stored map %s/%s/%s is target %s version %d (%s). Current LEADS_TENANT_BINDINGS points at %s. New source refs are refused. Replay, a higher source_version, and revocation of a source ref already stored stay on %s. Moving history requires a separate migration of tenant_id on leads, contacts, consents, source_refs, notify_inbox, and notify_revocations together. This server will not do that.",
		app, ns, external, sticky.Target, sticky.Version, sticky.Basis, configured, sticky.Target)
}

func missingHint(b Binding) string {
	return fmt.Sprintf("Binding %s/%s/%s@%d targets %s, which is not provisioned. Provision %s or correct LEADS_TENANT_BINDINGS, then restart. Do not create a local tenant %s to force a route. Existing stored events stay on their recorded target.",
		b.SourceApp, b.SourceNS, b.ExternalTenant, b.Version, b.TargetTenant, b.TargetTenant, b.ExternalTenant)
}
