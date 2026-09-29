package tenantmap

import (
	"errors"
	"strings"
	"testing"
)

func TestParseKeepsExplicitBindings(t *testing.T) {
	got, err := Parse(" touch-engine/notify/ext_s=tnt_a@2 , partner-app/notify/ext_s=tnt_b@1 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Version != 2 || got[0].TargetTenant != "tnt_a" || got[0].Basis != BasisOperatorBinding {
		t.Fatalf("bindings=%+v", got)
	}
	if got[1].SourceApp != "partner-app" || got[1].TargetTenant != "tnt_b" || got[1].Version != 1 {
		t.Fatalf("second=%+v", got[1])
	}
}

func TestParseRejectsTheWholeSet(t *testing.T) {
	cases := []string{
		"ext_s=tnt_a,ext_s=tnt_b",
		"ext_s=tnt_a,not-a-pair",
		"ext_s=tnt_a,touch-engine/notify/ext_s=tnt_a",
		"ext_s=ext_s",
		"touch-engine/notify/ext_s=tnt_a@0",
		"ext_s=tnt_a,",
	}
	for _, raw := range cases {
		got, err := Parse(raw)
		var me *Error
		if len(got) != 0 || !errors.As(err, &me) || me.Kind != KindConfig || !strings.Contains(me.Hint, "LEADS_TENANT_BINDINGS") {
			t.Fatalf("raw %q got=%v err=%v", raw, got, err)
		}
	}
	if got, err := Parse("  "); err != nil || got != nil {
		t.Fatalf("empty=%v %v", got, err)
	}
}

func TestResolveUsesBindingUntilLocalRowAppearsWithoutHistory(t *testing.T) {
	bindings, err := Parse("ext_s=tnt_a@2")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) (bool, error) {
		return id == "tnt_a", nil
	}
	d, err := Resolve("touch-engine", NamespaceNotify, "ext_s", bindings, Sticky{}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if d.TargetTenant != "tnt_a" || d.Version != 2 || d.Basis != BasisOperatorBinding || d.SourceTenant != "ext_s" {
		t.Fatalf("decision=%+v", d)
	}
}

func TestResolveLookupErrorDoesNotFallThrough(t *testing.T) {
	bindings, err := Parse("ext_s=tnt_a")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	lookup := func(id string) (bool, error) {
		calls = append(calls, id)
		if id == "ext_s" {
			return false, errors.New("disk")
		}
		return true, nil
	}
	_, err = Resolve("touch-engine", NamespaceNotify, "ext_s", bindings, Sticky{}, lookup)
	var me *Error
	if !errors.As(err, &me) || me.Kind != KindLookup {
		t.Fatalf("err=%v", err)
	}
	if len(calls) != 1 || calls[0] != "ext_s" {
		t.Fatalf("lookups=%v", calls)
	}
}

func TestResolveLocalTenantPlusNewBindingConflicts(t *testing.T) {
	bindings, err := Parse("ext_s=tnt_a")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) (bool, error) { return id == "ext_s" || id == "tnt_a", nil }
	_, err = Resolve("touch-engine", NamespaceNotify, "ext_s", bindings, Sticky{}, lookup)
	var me *Error
	if !errors.As(err, &me) || me.Kind != KindConflict || !strings.Contains(me.Hint, "does not rewrite") {
		t.Fatalf("err=%v", err)
	}
}

func TestResolveStickyBindingIgnoresLaterLocalRow(t *testing.T) {
	bindings, err := Parse("ext_s=tnt_a@1")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) (bool, error) { return true, nil }
	d, err := Resolve("touch-engine", NamespaceNotify, "ext_s", bindings, Sticky{
		Found: true, Target: "tnt_a", Basis: BasisOperatorBinding, Version: 4,
	}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if d.TargetTenant != "tnt_a" || d.Version != 4 || d.Basis != BasisOperatorBinding {
		t.Fatalf("decision=%+v", d)
	}
}

func TestResolveDriftAndMissingTarget(t *testing.T) {
	bindings, err := Parse("ext_s=tnt_b@3")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve("touch-engine", NamespaceNotify, "ext_s", bindings, Sticky{
		Found: true, Target: "tnt_a", Basis: BasisOperatorBinding, Version: 1,
	}, func(id string) (bool, error) { return true, nil })
	var me *Error
	if !errors.As(err, &me) || me.Kind != KindConflict || !strings.Contains(me.Hint, "tnt_a") {
		t.Fatalf("drift=%v", err)
	}
	_, err = Resolve("touch-engine", NamespaceNotify, "ext_s", bindings, Sticky{}, func(id string) (bool, error) {
		return false, nil
	})
	if !errors.As(err, &me) || me.Kind != KindTargetMissing || !strings.Contains(me.Hint, "Do not create a local tenant") {
		t.Fatalf("missing=%v", err)
	}
}

func TestResolvePrimaryKeyOnlyWithoutBinding(t *testing.T) {
	d, err := Resolve("touch-engine", NamespaceNotify, "tnt_a", nil, Sticky{}, func(id string) (bool, error) {
		return id == "tnt_a", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.TargetTenant != "tnt_a" || d.Basis != BasisLeadsPrimaryKey || d.Version != 1 {
		t.Fatalf("decision=%+v", d)
	}
	_, err = Resolve("touch-engine", NamespaceNotify, "missing", nil, Sticky{}, func(string) (bool, error) {
		return false, nil
	})
	var me *Error
	if !errors.As(err, &me) || me.Kind != KindUnknown {
		t.Fatalf("unknown=%v", err)
	}
}

func TestFollowUpRefusesTwoTargets(t *testing.T) {
	bindings, err := Parse("touch-engine/notify/ext_s=tnt_a,partner-app/notify/ext_s=tnt_b")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveFollowUp("", "ext_s", bindings, Sticky{}, func(string) (bool, error) { return true, nil })
	var me *Error
	if !errors.As(err, &me) || me.Kind != KindConflict || !strings.Contains(me.Hint, "source_app") {
		t.Fatalf("unscoped=%v", err)
	}
	d, err := ResolveFollowUp("partner-app", "ext_s", bindings, Sticky{}, func(id string) (bool, error) {
		return id == "tnt_b", nil
	})
	if err != nil || d.TargetTenant != "tnt_b" {
		t.Fatalf("scoped=%+v %v", d, err)
	}
}
