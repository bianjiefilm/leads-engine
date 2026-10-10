package notifyingest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseDeliveryAcceptsTouchPayload(t *testing.T) {
	body := touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, touchSubmitPayload("sub_trace01", 1, "7"), "访客乙", "13900000071", "wx-trace")
	d, err := ParseDelivery(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.TenantID != "tnt_a" || d.Profile.TenantScope != "tnt_a" {
		t.Fatalf("tenant = %s scope = %s", d.TenantID, d.Profile.TenantScope)
	}
	if d.Payload.BrandDisplayName != "磁石科技" || d.TenantID == d.Payload.BrandDisplayName || d.Profile.TenantScope == d.Payload.BrandDisplayName {
		t.Fatalf("brand display was used as tenant: %+v", d.Payload)
	}
	if d.Payload.TraceID != "lead:sub_trace01" || d.Payload.ReturnTarget != "/c/7TJYWDWH8N91" || d.Payload.GrantRef != "ord_keep" {
		t.Fatalf("trace = %+v", d.Payload)
	}
	if d.Payload.CampaignVersion != "7" || d.Payload.SourceVersion != 1 || d.Profile.SourceVersion != 1 {
		t.Fatalf("version campaign=%q source=%d profile=%d", d.Payload.CampaignVersion, d.Payload.SourceVersion, d.Profile.SourceVersion)
	}
	if d.Payload.CampaignRef != "cmp_1" || d.Payload.StoreRef != "sto_1" || d.Payload.AssetRef != "ast_trace" || d.Payload.Channel != "wecom" {
		t.Fatalf("campaign fields = %+v", d.Payload)
	}
	if d.Payload.ConsentRef != "touch://leads/sub_trace01" || d.Payload.ConsentVersion != "v1" || d.Payload.ConsentAt != "2026-10-10T00:00:00Z" || !d.Payload.MarketingOptin {
		t.Fatalf("consent = %+v", d.Payload)
	}
	if d.Payload.NotificationBrandRef != "notif-brand-a" {
		t.Fatalf("notification brand = %q", d.Payload.NotificationBrandRef)
	}
	retry := touchEnvelope("tnt_a", "sub_trace01-retry", "sub_trace01", EventLeadAuthorizedSubmitted, 1, touchSubmitPayload("sub_trace01", 1, "7"), "访客乙", "13900000071", "wx-trace")
	again, err := ParseDelivery(retry)
	if err != nil {
		t.Fatal(err)
	}
	if !SameFact(CanonicalFact(d), again) {
		t.Fatal("same trace and version with a different event id was a different fact")
	}
	again.Payload.GrantRef = "ord_other"
	if SameFact(CanonicalFact(d), again) {
		t.Fatal("changed grant_ref was treated as the same fact")
	}
	again = d
	again.Profile.SourceVersion = 2
	again.Payload.SourceVersion = 2
	if SameFact(CanonicalFact(d), again) {
		t.Fatal("source_version 2 was treated as the same fact")
	}
}

func TestParseDeliveryRejectsUntouchablePayload(t *testing.T) {
	base := touchSubmitPayload("sub_trace01", 1, "7")
	numbered := touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, base, "访客乙", "13900000071", "wx-trace")
	numbered = []byte(strings.Replace(string(numbered), `"campaign_version":"7"`, `"campaign_version":7`, 1))
	if _, err := ParseDelivery(numbered); err == nil {
		t.Fatal("numeric campaign_version was accepted")
	}
	asString := touchSubmitPayload("sub_trace01", 1, "7")
	raw := string(touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, asString, "访客乙", "13900000071", ""))
	raw = strings.Replace(raw, `"source_version":1`, `"source_version":"1"`, 1)
	if _, err := ParseDelivery([]byte(raw)); err == nil {
		t.Fatal("string source_version was accepted")
	}
	mismatch := touchEnvelope("tnt_a", "evt-mismatch", "sub_mismatch", EventLeadAuthorizedSubmitted, 1, map[string]any{
		"campaign_ref": "camp-1", "store_ref": "store-1", "channel": "nfc",
		"tag": "door", "asset_ref": "asset-1", "source_version": 2,
		"consent_ref": "touch://leads/sub_mismatch", "consent_version": "n1",
		"consent_at": "2026-10-10T00:00:00Z", "marketing_optin": false, "revoked": false,
	}, "乙", "13900000011", "")
	if _, err := ParseDelivery(mismatch); err == nil {
		t.Fatal("payload source_version differing from event_profile was accepted")
	}
	withPhone := touchSubmitPayload("sub_trace01", 1, "7")
	withPhone["phone"] = "13900000071"
	if _, err := ParseDelivery(touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, withPhone, "访客乙", "13900000071", "")); err == nil {
		t.Fatal("phone key was accepted")
	}
	withEmail := touchSubmitPayload("sub_trace01", 1, "7")
	withEmail["email"] = "a@example.com"
	if _, err := ParseDelivery(touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, withEmail, "访客乙", "13900000071", "")); err == nil {
		t.Fatal("email key was accepted")
	}
	withWechat := touchSubmitPayload("sub_trace01", 1, "7")
	withWechat["wechat"] = "wx"
	if _, err := ParseDelivery(touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, withWechat, "访客乙", "13900000071", "")); err == nil {
		t.Fatal("wechat key was accepted")
	}
	extra := touchSubmitPayload("sub_trace01", 1, "7")
	extra["visitor_note"] = "x"
	if _, err := ParseDelivery(touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, extra, "访客乙", "13900000071", "")); err == nil {
		t.Fatal("unknown payload field was accepted")
	}
	wrapped := touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, touchSubmitPayload("sub_trace01", 1, "7"), "访客乙", "13900000071", "")
	moved := strings.Replace(string(wrapped), `"payload":`, `"trace_id":"lead:sub_trace01","payload":`, 1)
	if _, err := ParseDelivery([]byte(moved)); err == nil {
		t.Fatal("trace_id on data was accepted")
	}
	badTrace := touchSubmitPayload("sub_trace01", 1, "7")
	badTrace["trace_id"] = "other:sub_trace01"
	if _, err := ParseDelivery(touchEnvelope("tnt_a", "sub_trace01", "sub_trace01", EventLeadAuthorizedSubmitted, 1, badTrace, "访客乙", "13900000071", "")); err == nil {
		t.Fatal("trace_id that is not lead:<source_ref> was accepted")
	}
}

func touchSubmitPayload(sourceRef string, version int, campaignVersion string) map[string]any {
	return map[string]any{
		"trace_id":               "lead:" + sourceRef,
		"source_version":         version,
		"return_target":          "/c/7TJYWDWH8N91",
		"campaign_ref":           "cmp_1",
		"store_ref":              "sto_1",
		"asset_ref":              "ast_trace",
		"campaign_version":       campaignVersion,
		"grant_ref":              "ord_keep",
		"channel":                "wecom",
		"consent_ref":            "touch://leads/" + sourceRef,
		"consent_version":        "v1",
		"consent_at":             "2026-10-10T00:00:00Z",
		"marketing_optin":        true,
		"brand_display_name":     "磁石科技",
		"notification_brand_ref": "notif-brand-a",
	}
}

func touchEnvelope(tenant, eventID, sourceRef, eventType string, version int, payload map[string]any, name, phone, wechat string) []byte {
	sha := RecordSHA256(name, phone, wechat)
	campaignRef, _ := payload["campaign_ref"].(string)
	profile := map[string]any{
		"profile_version": profileVersion,
		"event_id":        eventID,
		"event_type":      eventType,
		"source_app":      "touch-engine",
		"target_app":      "leads-engine",
		"tenant_scope":    tenant,
		"source_ref":      sourceRef,
		"source_version":  version,
		"correlation":     map[string]any{"ref": campaignRef},
		"payload_ref":     map[string]any{"ref": "touch://leads/" + sourceRef, "sha256": sha},
	}
	data, _ := json.Marshal(map[string]any{"event_profile": profile, "payload": payload})
	env := map[string]any{
		"type":           eventType,
		"schema_version": 1,
		"app_id":         "touch-engine",
		"tenant_id":      tenant,
		"data":           json.RawMessage(data),
	}
	b, _ := json.Marshal(env)
	return b
}
