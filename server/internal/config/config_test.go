package config

import "testing"

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
		"LEADS_INTERNAL_TOKEN":        "x",
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
		"LEADS_INTERNAL_TOKEN":        "x",
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
