package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFollowUpSummaryUsesExistingTenantBinding(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	unknown := "tnt_not_in_this_database"
	code, _, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-x", "", unknown, "")
	if code != 404 {
		t.Fatalf("unbound unknown tenant = %d, want 404", code)
	}
	h.api.Cfg.TenantBindings = map[string]string{unknown: tenantA}
	code, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-x", "", unknown, "")
	raw, _ := json.Marshal(parsed)
	if code != 200 || parsed["known"] != true {
		t.Fatalf("bound summary = %d %s", code, raw)
	}
	camps, _ := parsed["campaigns"].([]any)
	if len(camps) != 1 || camps[0].(map[string]any)["pending_follow_up"] != float64(0) {
		t.Fatalf("binding invented leads: %s", raw)
	}
}

func TestCampaignFollowUpSummary(t *testing.T) {
	secret := "test-ingest-secret"
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{
		featureNotifyIngest: true,
		ingestFetchBase:     src.url,
		ingestSecret:        secret,
		ingestFetchToken:    "test-fetch-token",
	})
	tenantA, tenantB, _ := h.seed()
	ref := "sub_chain"
	body := ingestEvent(tenantA, "evt-chain", ref, 1, "lead.authorized_submitted", "nfc", false, "本地访客", "13900002222", "")
	src.put(ref, recordJSON(tenantA, ref, "本地访客", "13900002222", "", "", "granted", "lead_submission", true, false, false))
	res := postIngest(t, h, body, signBody(secret, body))
	if res.status != 200 {
		t.Fatalf("ingest=%d %s", res.status, res.raw)
	}
	leadID, _ := res.json["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("receipt=%v", res.json)
	}

	t.Run("summary names the campaign and hides the phone", func(t *testing.T) {
		code, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-1", "", tenantA, "")
		raw, _ := json.Marshal(parsed)
		if code != 200 || parsed["known"] != true {
			t.Fatalf("summary=%d %s", code, raw)
		}
		if strings.Contains(string(raw), "13900002222") || strings.Contains(string(raw), "本地访客") {
			t.Fatalf("summary leaked contact: %s", raw)
		}
		camps, _ := parsed["campaigns"].([]any)
		if len(camps) != 1 {
			t.Fatalf("campaigns=%v", parsed)
		}
		row := camps[0].(map[string]any)
		if row["campaign_ref"] != "camp-1" || row["pending_follow_up"] != float64(1) {
			t.Fatalf("row=%v", row)
		}
		ids, _ := row["lead_ids"].([]any)
		if len(ids) != 1 || ids[0] != leadID {
			t.Fatalf("lead ids=%v want %s", ids, leadID)
		}
	})

	t.Run("desk shows the touch campaign", func(t *testing.T) {
		desk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", 200)
		if !deskMentionsActivity(desk, "camp-1") {
			t.Fatalf("desk missing campaign: %v", desk["buckets"])
		}
	})

	t.Run("first follow-up clears the pending count", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionOwnerA, tenantA,
			`{"note":"首次跟进，约到店看看","next_follow_up_at":"2030-01-02T09:00:00Z"}`, 201)
		_, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-1", "", tenantA, "")
		raw, _ := json.Marshal(parsed)
		camps, _ := parsed["campaigns"].([]any)
		if len(camps) != 1 || camps[0].(map[string]any)["pending_follow_up"] != float64(0) {
			t.Fatalf("after follow-up: %s", raw)
		}
		ids, _ := camps[0].(map[string]any)["lead_ids"].([]any)
		if len(ids) != 0 {
			t.Fatalf("pending ids remained: %v", ids)
		}
	})

	t.Run("other tenant cannot read the count", func(t *testing.T) {
		code, parsed, _ := h.do("GET", "/internal/v1/campaign-follow-ups?campaign_ref=camp-1", "", tenantB, "")
		raw, _ := json.Marshal(parsed)
		if code != 200 {
			t.Fatalf("status=%d %s", code, raw)
		}
		if strings.Contains(string(raw), leadID) {
			t.Fatalf("other tenant saw the lead: %s", raw)
		}
	})
}

func deskMentionsActivity(desk map[string]any, campaign string) bool {
	buckets, _ := desk["buckets"].(map[string]any)
	for _, items := range buckets {
		list, _ := items.([]any)
		for _, item := range list {
			row, _ := item.(map[string]any)
			source, _ := row["source"].(map[string]any)
			if source["activity"] == campaign {
				return true
			}
		}
	}
	return false
}
