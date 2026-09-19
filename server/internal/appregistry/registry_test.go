// HUI-1749 app registry tests: strict loading (fail on deploy), exact target
// resolution, and the ecosystem facts the handoff sender depends on.
package appregistry

import (
	"strings"
	"testing"
)

func TestEmbeddedManifestLoadsAndCarriesSenderFacts(t *testing.T) {
	r, err := Embedded()
	if err != nil {
		t.Fatalf("embedded manifest must load: %v", err)
	}
	self, ok := r.WithSelf("leads-engine").Self()
	if !ok || !self.Enabled {
		t.Fatalf("self app leads-engine must be registered and enabled: %v", ok)
	}
	if !r.HasCapability("leads-engine", "leads.handoff") {
		t.Fatalf("self app must declare leads.handoff capability")
	}
	// return_target_id fact (O2 expectation: rc-leads-engine-main).
	if u, ok := r.ResolveTarget("leads-engine", "rc-leads-engine-main", "receipt"); !ok || !strings.HasPrefix(u, "http://127.0.0.1") {
		t.Fatalf("self receipt target = %q ok=%v", u, ok)
	}
	// The receiver app entry (guanlan-order self-identifies as "orders").
	ord, ok := r.App("orders")
	if !ok || !ord.Enabled {
		t.Fatalf("receiver app orders must be registered and enabled")
	}
	for _, k := range ord.SupportedSourceKinds {
		if k != "standalone" {
			t.Fatalf("receiver supports standalone only, got %v", ord.SupportedSourceKinds)
		}
	}
	// Unregistered things never resolve.
	if _, ok := r.ResolveTarget("orders", "ti-orders-web", "receipt"); ok {
		t.Fatalf("kind-mismatched target must not resolve")
	}
	if _, ok := r.App("product-image"); ok {
		t.Fatalf("unregistered app must not resolve")
	}
}

func TestLoadRejectsBadManifests(t *testing.T) {
	good, err := Embedded()
	if err != nil {
		t.Fatalf("embedded must load: %v", err)
	}
	_ = good

	cases := map[string]string{
		"not json":          `{"manifest_version":`,
		"not object":        `[1,2]`,
		"unknown top field": `{"manifest_version":"app-registry/v1","apps":[],"extra":1}`,
		"wrong version":     `{"manifest_version":"app-registry/v2","apps":[]}`,
		"duplicate top key": `{"manifest_version":"app-registry/v1","manifest_version":"app-registry/v1","apps":[]}`,
		"trailing json":     `{"manifest_version":"app-registry/v1","apps":[]} {}`,
		"empty":             ``,
	}
	for name, raw := range cases {
		if _, err := Load([]byte(raw)); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestLoadRejectsBadAppsAndTargets(t *testing.T) {
	base := func(app string) []byte {
		return []byte(`{"manifest_version":"app-registry/v1","apps":[` + app + `]}`)
	}
	cases := map[string][]byte{
		"bad app_id": base(`{"app_id":"Leads Engine","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]}`),
		"duplicate app": []byte(`{"manifest_version":"app-registry/v1","apps":[` +
			`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]},` +
			`{"app_id":"a","display_name":"y","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t2","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]}]}`),
		"no capabilities":     base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]}`),
		"no launch target":    base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[],"receipt_targets":[]}`),
		"bad capability name": base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"leads_handoff","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]}`),
		"public http url":     base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"http://example.com/launch"}],"receipt_targets":[]}`),
		"url with query":      base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch?x=1"}],"receipt_targets":[]}`),
		"url without path":    base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com"}],"receipt_targets":[]}`),
		"loopback https ok but nonloopback http rejected": base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"http://10.0.0.1/launch"}],"receipt_targets":[]}`),
		"unknown source kind":                             base(`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["mystery"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]}`),
		"globally duplicated target_id": []byte(`{"manifest_version":"app-registry/v1","apps":[` +
			`{"app_id":"a","display_name":"x","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.com/launch"}],"receipt_targets":[]},` +
			`{"app_id":"b","display_name":"y","enabled":true,"supported_source_kinds":["standalone"],"capabilities":[{"name":"a.b","menu_visible":false,"requires_billing":false}],"launch_targets":[{"target_id":"t1","kind":"launch","url":"https://example.org/launch"}],"receipt_targets":[]}]}`),
	}
	for name, raw := range cases {
		if _, err := Load(raw); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}
