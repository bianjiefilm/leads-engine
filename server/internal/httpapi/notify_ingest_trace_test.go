package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestNotifyIngestTouchTraceVersion(t *testing.T) {
	secret := "test-ingest-secret"
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{
		featureNotifyIngest: true,
		ingestFetchBase:     src.url,
		ingestSecret:        secret,
		ingestFetchToken:    "test-fetch-token",
	})
	tenantA, _, _ := h.seed()
	brandsBefore := tableCount(t, h, "brands")
	handoffsBefore := tableCount(t, h, "opportunity_handoffs")
	entitlementBefore := tableCount(t, h, "crm_entitlement_cache")
	procurementBefore := tableCount(t, h, "channel_procurement")
	tenantsBefore := tableCount(t, h, "tenants")

	ref := "sub_trace01"
	name, phone, wechat := "访客乙", "13900000071", "wx-trace"
	src.put(ref, recordJSON(tenantA, ref, name, phone, "", wechat, "granted", "lead_submission", true, false, false))
	body := touchIngest(tenantA, ref, ref, "lead.authorized_submitted", 1, touchSubmitPayload(ref, 1, "7"), name, phone, wechat)
	first := postIngest(t, h, body, signBody(secret, body))
	if first.status != http.StatusOK {
		t.Fatalf("submit=%d %s", first.status, first.raw)
	}
	leadID, _ := first.json["lead_id"].(string)
	if leadID == "" || first.json["ingestion"] != "accepted" {
		t.Fatalf("receipt=%v", first.json)
	}
	row := inboxTrace(t, h, ref)
	if row.TraceID != "lead:"+ref || row.SourceVersion != 1 || row.TenantID != tenantA || row.Count != 1 {
		t.Fatalf("stored trace = %+v", row)
	}
	if row.CampaignVersion != "7" || row.GrantRef != "ord_keep" || row.ReturnTarget != "/c/7TJYWDWH8N91" || row.Brand != "磁石科技" {
		t.Fatalf("stored payload = %+v", row)
	}
	if row.NotificationBrand != "notif-brand-a" || row.CampaignRef != "cmp_1" || row.StoreRef != "sto_1" || row.AssetRef != "ast_trace" || row.Channel != "wecom" {
		t.Fatalf("stored source fields = %+v", row)
	}
	if row.ConsentRef != "touch://leads/"+ref || row.ConsentVersion != "v1" || !row.MarketingOptin {
		t.Fatalf("stored consent = %+v", row)
	}
	if row.EventID != ref {
		t.Fatalf("event id = %s", row.EventID)
	}
	snap := sourceSnapshot(t, h, ref)
	if snap["campaign_version"] != "7" || snap["trace_id"] != "lead:"+ref || snap["return_target"] != "/c/7TJYWDWH8N91" || snap["grant_ref"] != "ord_keep" {
		t.Fatalf("snapshot = %v", snap)
	}
	if snap["brand_display_name"] != "磁石科技" || snap["source_tenant_id"] == "磁石科技" || snap["source_version"] != float64(1) {
		t.Fatalf("snapshot tenant/version = %v", snap)
	}

	retry := touchIngest(tenantA, ref+"-retry", ref, "lead.authorized_submitted", 1, touchSubmitPayload(ref, 1, "7"), name, phone, wechat)
	again := postIngest(t, h, retry, signBody(secret, retry))
	if again.status != http.StatusOK || again.json["lead_id"] != leadID {
		t.Fatalf("replay=%d %v", again.status, again.json)
	}
	if got := inboxTrace(t, h, ref); got.Count != 1 || got.EventID != ref {
		t.Fatalf("replay inserted another row: %+v", got)
	}

	changed := touchSubmitPayload(ref, 1, "7")
	changed["grant_ref"] = "ord_other"
	changed["return_target"] = "/c/CHANGED"
	conflict := touchIngest(tenantA, ref+"-conflict", ref, "lead.authorized_submitted", 1, changed, name, phone, wechat)
	denied := postIngest(t, h, conflict, signBody(secret, conflict))
	if denied.status != http.StatusConflict {
		t.Fatalf("conflict=%d %s", denied.status, denied.raw)
	}
	if got := inboxTrace(t, h, ref); got.Count != 1 || got.GrantRef != "ord_keep" || got.ReturnTarget != "/c/7TJYWDWH8N91" || got.SourceVersion != 1 {
		t.Fatalf("conflict overwrote the row: %+v", got)
	}

	src.put(ref, recordJSON(tenantA, ref, name, phone, "", wechat, "revoked", "lead_submission", false, false, false))
	revokeBody := touchIngest(tenantA, ref+"-revoke", ref, "lead.consent_revoked", 2, touchRevokePayload(ref), name, phone, wechat)
	revoked := postIngest(t, h, revokeBody, signBody(secret, revokeBody))
	if revoked.status != http.StatusOK || revoked.json["source_version"] != float64(2) {
		t.Fatalf("revoke=%d %v", revoked.status, revoked.json)
	}
	rows := inboxTraceRows(t, h, "lead:"+ref)
	if len(rows) != 2 || rows[0].SourceVersion != 1 || rows[1].SourceVersion != 2 {
		t.Fatalf("versions = %+v", rows)
	}
	if rows[0].ReturnTarget != rows[1].ReturnTarget || rows[1].ReturnTarget != "/c/7TJYWDWH8N91" {
		t.Fatalf("return_target changed on revoke: %+v", rows)
	}
	if rows[0].EventType == rows[1].EventType {
		t.Fatalf("revoke collapsed onto the submit row: %+v", rows)
	}
	if ids := h.leadIDsForContact(sessionOwnerA, tenantA, first.json["contact_id"].(string)); len(ids) != 1 {
		t.Fatalf("leads=%v", ids)
	}
	revokeRetry := touchIngest(tenantA, ref+"-revoke-retry", ref, "lead.consent_revoked", 2, touchRevokePayload(ref), name, phone, wechat)
	if retryRevoke := postIngest(t, h, revokeRetry, signBody(secret, revokeRetry)); retryRevoke.status != http.StatusOK {
		t.Fatalf("revoke replay=%d %s", retryRevoke.status, retryRevoke.raw)
	}
	if againRows := inboxTraceRows(t, h, "lead:"+ref); len(againRows) != 2 {
		t.Fatalf("revoke replay inserted a third row: %+v", againRows)
	}

	if tableCount(t, h, "brands") != brandsBefore || tableCount(t, h, "tenants") != tenantsBefore {
		t.Fatal("brand display created a brand or tenant")
	}
	if tableCount(t, h, "opportunity_handoffs") != handoffsBefore || tableCount(t, h, "crm_entitlement_cache") != entitlementBefore || tableCount(t, h, "channel_procurement") != procurementBefore {
		t.Fatal("grant_ref changed billing or orders")
	}
	var named int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(1) FROM tenants WHERE id=? OR name=?`, "磁石科技", "磁石科技").Scan(&named); err != nil || named != 0 {
		t.Fatalf("brand tenant rows=%d err=%v", named, err)
	}
}

type traceInbox struct {
	Count             int
	TraceID           string
	SourceVersion     int
	TenantID          string
	EventID           string
	EventType         string
	CampaignVersion   string
	GrantRef          string
	ReturnTarget      string
	Brand             string
	NotificationBrand string
	CampaignRef       string
	StoreRef          string
	AssetRef          string
	Channel           string
	ConsentRef        string
	ConsentVersion    string
	MarketingOptin    bool
}

func inboxTrace(t *testing.T, h *harness, eventID string) traceInbox {
	t.Helper()
	rows := inboxTraceRows(t, h, "lead:sub_trace01")
	var got traceInbox
	got.Count = len(rows)
	for _, row := range rows {
		if row.EventID == eventID || (got.EventID == "" && row.SourceVersion == 1) {
			got = row
			got.Count = len(rows)
		}
	}
	return got
}

func inboxTraceRows(t *testing.T, h *harness, traceID string) []traceInbox {
	t.Helper()
	rows, err := h.api.St.DB.Query(`SELECT trace_id, source_version, tenant_id, profile_event_id, event_type, payload_json
		FROM notify_inbox WHERE trace_id=? ORDER BY source_version`, traceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []traceInbox
	for rows.Next() {
		var row traceInbox
		var payload string
		if err := rows.Scan(&row.TraceID, &row.SourceVersion, &row.TenantID, &row.EventID, &row.EventType, &payload); err != nil {
			t.Fatal(err)
		}
		var fact struct {
			Payload struct {
				CampaignVersion      string `json:"campaign_version"`
				GrantRef             string `json:"grant_ref"`
				ReturnTarget         string `json:"return_target"`
				BrandDisplayName     string `json:"brand_display_name"`
				NotificationBrandRef string `json:"notification_brand_ref"`
				CampaignRef          string `json:"campaign_ref"`
				StoreRef             string `json:"store_ref"`
				AssetRef             string `json:"asset_ref"`
				Channel              string `json:"channel"`
				ConsentRef           string `json:"consent_ref"`
				ConsentVersion       string `json:"consent_version"`
				MarketingOptin       bool   `json:"marketing_optin"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(payload), &fact); err != nil {
			t.Fatalf("payload_json: %v body=%s", err, payload)
		}
		row.CampaignVersion = fact.Payload.CampaignVersion
		row.GrantRef = fact.Payload.GrantRef
		row.ReturnTarget = fact.Payload.ReturnTarget
		row.Brand = fact.Payload.BrandDisplayName
		row.NotificationBrand = fact.Payload.NotificationBrandRef
		row.CampaignRef = fact.Payload.CampaignRef
		row.StoreRef = fact.Payload.StoreRef
		row.AssetRef = fact.Payload.AssetRef
		row.Channel = fact.Payload.Channel
		row.ConsentRef = fact.Payload.ConsentRef
		row.ConsentVersion = fact.Payload.ConsentVersion
		row.MarketingOptin = fact.Payload.MarketingOptin
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sourceSnapshot(t *testing.T, h *harness, sourceRef string) map[string]any {
	t.Helper()
	var raw string
	if err := h.api.St.DB.QueryRow(`SELECT auth_scope_snapshot FROM source_refs WHERE source_ref=?`, sourceRef).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatal(err)
	}
	return snap
}

func tableCount(t *testing.T, h *harness, table string) int {
	t.Helper()
	var n int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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

func touchRevokePayload(sourceRef string) map[string]any {
	return map[string]any{
		"campaign_ref":     "cmp_1",
		"source_version":   2,
		"consent_ref":      "touch://leads/" + sourceRef,
		"consent_at":       "2026-10-10T00:00:00Z",
		"revoked":          true,
		"asset_ref":        "ast_trace",
		"trace_id":         "lead:" + sourceRef,
		"grant_ref":        "ord_keep",
		"return_target":    "/c/7TJYWDWH8N91",
		"campaign_version": "7",
	}
}

func touchIngest(tenant, eventID, sourceRef, eventType string, version int, payload map[string]any, name, phone, wechat string) []byte {
	sha := recordSHA(name, phone, wechat)
	campaignRef, _ := payload["campaign_ref"].(string)
	profile := map[string]any{
		"profile_version": "directed-event/v1",
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
