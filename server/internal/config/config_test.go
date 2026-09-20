package config

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.HTTPAddr != "127.0.0.1:18230" {
		t.Errorf("HTTPAddr = %s", cfg.HTTPAddr)
	}
	if cfg.AppID != "leads-engine" {
		t.Errorf("AppID = %s", cfg.AppID)
	}
	if cfg.SessionCookie != "leads_session" {
		t.Errorf("SessionCookie = %s", cfg.SessionCookie)
	}
	if cfg.FeatureNotify || cfg.FeatureUpload {
		t.Errorf("feature flags must default to off")
	}
	if cfg.FeatureFunnel {
		t.Errorf("FEATURE_FUNNEL must default to off (routes not registered)")
	}
	if cfg.FeatureChannelAnalytics {
		t.Errorf("FEATURE_CHANNEL_ANALYTICS must default to off (routes not registered)")
	}
}

// HUI-1694 / FEAT-0195: the full-funnel analysis surface is gated by
// FEATURE_FUNNEL; default off (登记制开关,与其他 FEATURE_* 同款语义)。
func TestFeatureFunnelCoupleToConfig(t *testing.T) {
	env := map[string]string{"FEATURE_FUNNEL": "true"}
	cfg := Load(func(k string) string { return env[k] })
	if !cfg.FeatureFunnel {
		t.Fatal("FEATURE_FUNNEL=true must enable the funnel analysis flag")
	}
	off := Load(func(string) string { return "" })
	if off.FeatureFunnel {
		t.Fatal("FEATURE_FUNNEL must default to off")
	}
	// Describe() carries the flag so operators can see the register state.
	if off.Describe() == "" || !strings.Contains(cfg.Describe(), "funnel=on") {
		t.Fatalf("Describe must report funnel=on when enabled: %s", cfg.Describe())
	}
}

// HUI-1695 / FEAT-0196: the channel-effect analysis surface is gated by
// FEATURE_CHANNEL_ANALYTICS; default off (登记制开关,与 FEATURE_FUNNEL 同款
// 语义,两者相互独立)。
func TestFeatureChannelAnalyticsCoupleToConfig(t *testing.T) {
	env := map[string]string{"FEATURE_CHANNEL_ANALYTICS": "true"}
	cfg := Load(func(k string) string { return env[k] })
	if !cfg.FeatureChannelAnalytics {
		t.Fatal("FEATURE_CHANNEL_ANALYTICS=true must enable the channel analytics flag")
	}
	off := Load(func(string) string { return "" })
	if off.FeatureChannelAnalytics {
		t.Fatal("FEATURE_CHANNEL_ANALYTICS must default to off")
	}
	// Describe() carries the flag so operators can see the register state.
	if !strings.Contains(cfg.Describe(), "channel_analytics=on") ||
		!strings.Contains(off.Describe(), "channel_analytics=off") {
		t.Fatalf("Describe must report channel_analytics state: on=%s off=%s",
			cfg.Describe(), off.Describe())
	}
}

func TestGateFailClosed(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	problems := cfg.Gate()
	want := 3 // internal token, identity base url, identity token
	if len(problems) != want {
		t.Fatalf("want %d gate problems, got %v", want, problems)
	}
	for _, p := range problems {
		if p == "" {
			t.Fatal("empty problem string")
		}
	}
}

func TestGateSatisfied(t *testing.T) {
	env := map[string]string{
		"LEADS_INTERNAL_TOKEN":       "x",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "dedicated",
	}
	cfg := Load(func(k string) string { return env[k] })
	if problems := cfg.Gate(); len(problems) != 0 {
		t.Fatalf("gate should be satisfied, got %v", problems)
	}
}

func TestFeatureFlagsCoupleToConfig(t *testing.T) {
	env := map[string]string{
		"LEADS_INTERNAL_TOKEN":       "x",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "dedicated",
		"FEATURE_UPLOAD":             "true",
	}
	cfg := Load(func(k string) string { return env[k] })
	if !cfg.FeatureUpload {
		t.Fatal("FEATURE_UPLOAD=true must enable upload")
	}
	problems := cfg.Gate()
	if len(problems) != 2 {
		t.Fatalf("upload on without base url/token must produce 2 problems, got %v", problems)
	}
}

// HUI-1749: the eco handoff gate is scoped to FEATURE_SERVICE_DRAFT=on and
// fails closed on every missing receiver fact; off never produces problems.
func TestEcoGateScopedAndFailClosed(t *testing.T) {
	off := Load(func(string) string { return "" })
	if problems := off.EcoGate(); len(problems) != 0 {
		t.Fatalf("eco gate must be silent while the feature is off, got %v", problems)
	}

	env := map[string]string{
		"FEATURE_SERVICE_DRAFT": "true",
		"LEADS_INTERNAL_TOKEN":  "x",
	}
	cfg := Load(func(k string) string { return env[k] })
	// target app defaults to the registered receiver id; the other three
	// facts must each be demanded explicitly.
	if cfg.EcoHandoff.TargetAppID != "orders" {
		t.Fatalf("target app default = %q, want orders", cfg.EcoHandoff.TargetAppID)
	}
	if problems := cfg.EcoGate(); len(problems) != 3 {
		t.Fatalf("feature on without url/token/scope must produce 3 problems, got %v", problems)
	}
	for _, k := range []string{"ECO_HANDOFF_INTAKE_URL", "ECO_HANDOFF_TOKEN", "ECO_HANDOFF_TENANT_SCOPE"} {
		env[k] = "set"
	}
	cfg = Load(func(k string) string { return env[k] })
	if problems := cfg.EcoGate(); len(problems) != 0 {
		t.Fatalf("eco gate should be satisfied, got %v", problems)
	}
}
