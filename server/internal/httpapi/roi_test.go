package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func roiOn(t *testing.T) *harness {
	t.Helper()
	return newHarnessOpts(t, harnessOpts{featureROI: true})
}

func TestROIFlagOff404(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	h.mustDo("POST", "/api/v1/roi/recalculate", sessionOwnerA, tenantA, roiBody(), http.StatusNotFound)
}

func TestROIRecalculateDoesNotRewriteFacts(t *testing.T) {
	h := roiOn(t)
	tenantA, _, _ := h.seed()
	var leadsBefore int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM leads`).Scan(&leadsBefore); err != nil {
		t.Fatal(err)
	}
	var tablesBefore int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '%roi%'`).Scan(&tablesBefore); err != nil {
		t.Fatal(err)
	}
	first := h.mustDo("POST", "/api/v1/roi/recalculate", sessionOwnerA, tenantA, roiBody(), http.StatusOK)
	second := h.mustDo("POST", "/api/v1/roi/recalculate", sessionOwnerA, tenantA, roiBody(), http.StatusOK)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("recalculation response diverged")
	}
	if first["marketing_lift_proof"] != false {
		t.Fatalf("proof = %v", first["marketing_lift_proof"])
	}
	if first["roi_status"] != "complete" {
		t.Fatalf("status = %v", first["roi_status"])
	}
	var leadsAfter, tablesAfter int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM leads`).Scan(&leadsAfter); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '%roi%'`).Scan(&tablesAfter); err != nil {
		t.Fatal(err)
	}
	if leadsBefore != leadsAfter || tablesBefore != tablesAfter {
		t.Fatalf("facts rewritten leads %d→%d tables %d→%d", leadsBefore, leadsAfter, tablesBefore, tablesAfter)
	}
	raw, _ := json.Marshal(first["revenues"])
	if containsString(string(raw), "66600") {
		t.Fatalf("revenues summed: %s", raw)
	}
}

func TestROICrossTenantRefused(t *testing.T) {
	h := roiOn(t)
	tenantA, _, _ := h.seed()
	body := `{
	  "business_category":"merchant_customer",
	  "window_start":"2026-01-01T00:00:00Z",
	  "window_end":"2026-02-01T00:00:00Z",
	  "events":[{"id":"x","tenant_id":"tnt_other","kind":"browse","occurred_at":"2026-01-04T00:00:00Z"}]
	}`
	out := h.mustDo("POST", "/api/v1/roi/recalculate", sessionOwnerA, tenantA, body, http.StatusForbidden)
	if out["error"] != "cross_tenant" {
		t.Fatalf("error = %v", out["error"])
	}
	out = h.mustDo("POST", "/api/v1/roi/recalculate", sessionOwnerA, tenantA, body, http.StatusForbidden)
	if out["error"] != "cross_tenant" {
		t.Fatal("cross-tenant refusal was not stable")
	}
}

func roiBody() string {
	return `{
	  "business_category":"merchant_customer",
	  "window_start":"2026-01-01T00:00:00Z",
	  "window_end":"2026-02-01T00:00:00Z",
	  "links":[
	    {"id":"src","kind":"source","ref":"touch"},
	    {"id":"cmp","kind":"campaign","parent_id":"src","campaign_id":"cmp"},
	    {"id":"store","kind":"store","parent_id":"cmp"},
	    {"id":"tag","kind":"tag","parent_id":"store"},
	    {"id":"asset","kind":"asset_version","parent_id":"tag","asset_id":"asset-1"},
	    {"id":"sub","kind":"submission","parent_id":"asset"},
	    {"id":"lead","kind":"lead","parent_id":"sub"},
	    {"id":"opp","kind":"opportunity","parent_id":"lead"}
	  ],
	  "events":[{"id":"won-1","kind":"won","link_id":"opp","occurred_at":"2026-01-20T00:00:00Z"}],
	  "costs":[{"id":"prod","kind":"production","authority":"billing","asset_id":"asset-1","amount_cents":10000,"occurred_at":"2026-01-02T00:00:00Z"}],
	  "revenues":[
	    {"id":"m","kind":"merchant_sales","authority":"manual","amount_cents":11100,"occurred_at":"2026-01-21T00:00:00Z"},
	    {"id":"p","kind":"painuo_service_order","authority":"platform_verified","amount_cents":22200,"occurred_at":"2026-01-21T00:00:00Z"},
	    {"id":"t","kind":"platform_tool","authority":"platform_verified","amount_cents":33300,"occurred_at":"2026-01-21T00:00:00Z"}
	  ]
	}`
}

func containsString(s, part string) bool {
	return len(s) >= len(part) && (s == part || len(part) == 0 || indexString(s, part) >= 0)
}

func indexString(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
