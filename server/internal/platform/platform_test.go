package platform

import (
	"errors"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
)

func TestNotifyGate(t *testing.T) {
	t.Run("off by default even with config present", func(t *testing.T) {
		c := NotifyClient{BaseURL: "http://127.0.0.1:18105", Token: "x", Enabled: false}
		if err := c.Publish("lead.created", "k1", nil); !errors.Is(err, ErrFeatureDisabled) {
			t.Fatalf("want ErrFeatureDisabled, got %v", err)
		}
	})
	t.Run("on without token fails explicitly", func(t *testing.T) {
		c := NotifyClient{BaseURL: "http://127.0.0.1:18105", Token: "", Enabled: true}
		if err := c.Publish("lead.created", "k1", nil); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("want ErrNotConfigured, got %v", err)
		}
	})
	t.Run("on with config still refuses to fake success in L0", func(t *testing.T) {
		c := NotifyClient{BaseURL: "http://127.0.0.1:18105", Token: "x", Enabled: true}
		if err := c.Publish("lead.created", "k1", nil); !errors.Is(err, ErrNotImplemented) {
			t.Fatalf("want ErrNotImplemented, got %v", err)
		}
	})
}

func TestUploadGate(t *testing.T) {
	c := UploadClient{Enabled: false}
	if err := c.CreateSession("a.png", 10); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("want ErrFeatureDisabled, got %v", err)
	}
}

func TestConfigGateCouplesFlagsToTokens(t *testing.T) {
	get := func(k string) string {
		switch k {
		case "LEADS_INTERNAL_TOKEN":
			return "internal"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://127.0.0.1:18101"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-dedicated"
		case "FEATURE_NOTIFY":
			return "1"
		}
		return ""
	}
	cfg := config.Load(get)
	problems := cfg.Gate()
	if len(problems) != 2 {
		t.Fatalf("want exactly 2 problems (notify base url + token), got %v", problems)
	}
	if !cfg.FeatureNotify {
		t.Fatal("FEATURE_NOTIFY=1 must parse as enabled")
	}
}
