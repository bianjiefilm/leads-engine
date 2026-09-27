package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNotifyIngestOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	res, err := http.Post(h.srv.URL+"/internal/v1/notify/ingest", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 when FEATURE_NOTIFY_INGEST is off", res.StatusCode)
	}
}

func TestNotifyIngestReceiver(t *testing.T) {
	secret := "test-ingest-secret"
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{
		featureNotifyIngest: true,
		ingestFetchBase:     src.url,
		ingestSecret:        secret,
		ingestFetchToken:    "test-fetch-token",
		captureLog:          true,
	})
	tenantA, tenantB, _ := h.seed()
	before := h.contactCount(sessionOwnerA, tenantA)

	t.Run("bad signature does not create a lead", func(t *testing.T) {
		body := ingestEvent(tenantA, "evt-bad", "sub_bad", 1, "lead.authorized_submitted", "browse", false, "n", "13900000001", "")
		res := postIngest(t, h, body, signBody("other-secret", body))
		if res.status != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
		if h.contactCount(sessionOwnerA, tenantA) != before {
			t.Fatal("bad signature created a contact")
		}
	})

	t.Run("tampered body is rejected", func(t *testing.T) {
		body := ingestEvent(tenantA, "evt-tamp", "sub_tamp", 1, "lead.authorized_submitted", "nfc", false, "丙", "13900000002", "")
		sig := signBody(secret, body)
		body[len(body)-2] = 'x'
		res := postIngest(t, h, body, sig)
		if res.status != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
	})

	t.Run("unknown tenant", func(t *testing.T) {
		body := ingestEvent("tnt_missing", "evt-miss", "sub_miss", 1, "lead.authorized_submitted", "nfc", false, "丁", "13900000003", "")
		src.put("sub_miss", recordJSON("tnt_missing", "sub_miss", "丁", "13900000003", "", "", "granted", "lead_submission", true, false, false))
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
	})

	t.Run("wrong target app", func(t *testing.T) {
		body := ingestEvent(tenantA, "evt-tgt", "sub_tgt", 1, "lead.authorized_submitted", "nfc", false, "戊", "13900000004", "")
		body = bytes.Replace(body, []byte(`"target_app":"leads-engine"`), []byte(`"target_app":"orders"`), 1)
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
	})

	t.Run("browse does not create a contact", func(t *testing.T) {
		ref := "sub_browse"
		body := ingestEvent(tenantA, "evt-browse", ref, 1, "lead.authorized_submitted", "browse", false, "浏览", "13900000005", "")
		src.put(ref, recordJSON(tenantA, ref, "浏览", "13900000005", "", "", "granted", "browse", false, false, false))
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
		if strings.Contains(h.logs.String(), "13900000005") {
			t.Fatal("phone leaked into the log")
		}
	})

	var leadID, contactID string
	ref := "sub_ok"
	body := ingestEvent(tenantA, "evt-ok", ref, 1, "lead.authorized_submitted", "nfc", false, "乙商家", "13900000011", "wx-yi")
	src.put(ref, recordJSON(tenantA, ref, "乙商家", "13900000011", "", "wx-yi", "granted", "lead_submission", true, false, false))

	t.Run("authorized touch submission is visible as a lead", func(t *testing.T) {
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
		if strings.Contains(res.raw, "13900000011") || strings.Contains(res.raw, "wx-yi") {
			t.Fatalf("receipt leaked contact data: %s", res.raw)
		}
		leadID, _ = res.json["lead_id"].(string)
		contactID, _ = res.json["contact_id"].(string)
		if leadID == "" || contactID == "" || res.json["ingestion"] != "accepted" {
			t.Fatalf("receipt=%v", res.json)
		}
		lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["status"] != "new" {
			t.Fatalf("lead=%v", lead)
		}
		contact := h.mustDo("GET", "/api/v1/contacts/"+contactID, sessionOwnerA, tenantA, "", http.StatusOK)
		if contact["phone"] != "13900000011" || contact["source_type"] != "touch_campaign" {
			t.Fatalf("contact=%v", contact)
		}
		if contact["assigned_member_id"] != nil && contact["assigned_member_id"] != "" {
			t.Fatalf("new ingest must stay unassigned, got %v", contact["assigned_member_id"])
		}
		consents := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		seen := map[string]bool{}
		for _, it := range consents["items"].([]any) {
			row := it.(map[string]any)
			seen[row["source_channel"].(string)] = row["marketing_allowed"].(bool)
		}
		if !seen["form_authorization"] || seen["marketing_phone"] || seen["marketing_sms"] {
			t.Fatalf("consents=%v", seen)
		}
	})

	t.Run("replay returns the same receipt and no second lead", func(t *testing.T) {
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != http.StatusOK || res.json["lead_id"] != leadID || res.json["ingestion"] != "accepted" {
			t.Fatalf("replay=%d %v", res.status, res.json)
		}
		ids := h.leadIDsForContact(sessionOwnerA, tenantA, contactID)
		if len(ids) != 1 {
			t.Fatalf("leads=%v", ids)
		}
	})

	t.Run("same event different content conflicts", func(t *testing.T) {
		alt := ingestEvent(tenantA, "evt-ok", ref, 2, "lead.authorized_submitted", "nfc", false, "乙商家", "13900000011", "wx-yi")
		res := postIngest(t, h, alt, signBody(secret, alt))
		if res.status != http.StatusConflict {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
	})

	t.Run("lost response is recoverable by query", func(t *testing.T) {
		path := "/internal/v1/notify/receipts/evt-ok"
		req, _ := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
		req.Header.Set("X-Notify-App-ID", "touch-engine")
		req.Header.Set("X-Notify-Signature", signBody(secret, []byte("GET\n"+path)))
		res, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(leadID)) {
			t.Fatalf("query=%d %s", res.StatusCode, raw)
		}
		if h.leadIDsForContact(sessionOwnerA, tenantA, contactID) == nil {
			t.Fatal("query created or dropped the lead")
		}
	})

	t.Run("other tenant with the same phone is a different contact", func(t *testing.T) {
		refB := "sub_b"
		bodyB := ingestEvent(tenantB, "evt-b", refB, 1, "lead.authorized_submitted", "nfc", false, "乙商家", "13900000011", "")
		src.put(refB, recordJSON(tenantB, refB, "乙商家", "13900000011", "", "", "granted", "lead_submission", true, false, false))
		res := postIngest(t, h, bodyB, signBody(secret, bodyB))
		if res.status != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
		if res.json["contact_id"] == contactID {
			t.Fatal("cross-tenant phone was merged")
		}
		st, _ := h.contactByID(sessionOwnerA, tenantA, res.json["contact_id"].(string))
		if st == http.StatusOK {
			t.Fatal("tenant A can read tenant B contact")
		}
	})

	t.Run("revocation then a late submit does not grant marketing", func(t *testing.T) {
		refR := "sub_revoke"
		revokeBody := ingestEvent(tenantA, "evt-revoke", refR, 2, "lead.consent_revoked", "nfc", true, "己", "13900000021", "")
		src.put(refR, recordJSON(tenantA, refR, "己", "13900000021", "", "", "revoked", "lead_submission", false, false, false))
		res := postIngest(t, h, revokeBody, signBody(secret, revokeBody))
		if res.status != http.StatusOK || res.json["ingestion"] != "revocation_accepted" {
			t.Fatalf("revoke=%d %v", res.status, res.json)
		}
		submit := ingestEvent(tenantA, "evt-late", refR, 1, "lead.authorized_submitted", "nfc", false, "己", "13900000021", "")
		src.put(refR, recordJSON(tenantA, refR, "己", "13900000021", "", "", "granted", "lead_submission", true, true, true))
		res = postIngest(t, h, submit, signBody(secret, submit))
		if res.status != http.StatusOK {
			t.Fatalf("late=%d %s", res.status, res.raw)
		}
		cid, _ := res.json["contact_id"].(string)
		consents := h.mustDo("GET", "/api/v1/contacts/"+cid+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		for _, it := range consents["items"].([]any) {
			row := it.(map[string]any)
			ch, _ := row["source_channel"].(string)
			if ch == "marketing_phone" || ch == "marketing_sms" {
				if row["marketing_allowed"] == true && row["revoked_at"] == nil {
					t.Fatalf("late submit restored marketing: %v", row)
				}
			}
		}
	})

	t.Run("source outage does not acknowledge success", func(t *testing.T) {
		src.fail = true
		defer func() { src.fail = false }()
		body := ingestEvent(tenantA, "evt-down", "sub_down", 1, "lead.authorized_submitted", "nfc", false, "庚", "13900000031", "")
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", res.status, res.raw)
		}
	})

	if strings.Contains(h.logs.String(), "13900000011") {
		t.Fatal("successful ingest logged a phone number")
	}
	_ = time.Now()
}

type ingestResult struct {
	status int
	raw    string
	json   map[string]any
}

func postIngest(t *testing.T, h *harness, body []byte, sig string) ingestResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/internal/v1/notify/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Notify-Signature", sig)
	req.Header.Set("X-Notify-Delivery-Id", "del-test")
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	return ingestResult{status: res.StatusCode, raw: string(raw), json: parsed}
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func recordSHA(name, phone, wechat string) string {
	b, _ := json.Marshal(map[string]string{"name": name, "phone": phone, "wechat": wechat})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func ingestEvent(tenant, eventID, sourceRef string, version int, eventType, channel string, revoked bool, name, phone, wechat string) []byte {
	sha := recordSHA(name, phone, wechat)
	payload := map[string]any{
		"campaign_ref": "camp-1", "store_ref": "store-1", "channel": channel,
		"tag": "door", "asset_ref": "asset-1", "source_version": version,
		"consent_ref": "touch://leads/" + sourceRef, "consent_version": "n1",
		"consent_at": time.Now().UTC().Format(time.RFC3339), "marketing_optin": false, "revoked": revoked,
	}
	profile := map[string]any{
		"profile_version": "directed-event/v1",
		"event_id":        eventID,
		"event_type":      eventType,
		"source_app":      "touch-engine",
		"target_app":      "leads-engine",
		"tenant_scope":    tenant,
		"source_ref":      sourceRef,
		"source_version":  version,
		"correlation":     map[string]any{"ref": "camp-1"},
		"payload_ref":     map[string]any{"ref": "touch://leads/" + sourceRef, "sha256": sha},
	}
	data, _ := json.Marshal(map[string]any{"event_profile": profile, "payload": payload})
	env := map[string]any{
		"id": "nev_" + eventID, "type": eventType, "schema_version": 1,
		"app_id": "touch-engine", "tenant_id": tenant,
		"occurred_at":     time.Now().UTC().Format(time.RFC3339),
		"idempotency_key": "dir_" + eventID,
		"data":            json.RawMessage(data),
	}
	b, _ := json.Marshal(env)
	return b
}

func recordJSON(tenant, ref, name, phone, email, wechat, auth, purpose string, form, phoneOK, sms bool) string {
	b, _ := json.Marshal(map[string]any{
		"submission_ref": ref, "tenant_id": tenant, "name": name, "phone": phone,
		"email": email, "wechat": wechat, "channel_subject_ref": "",
		"authorization": auth, "purpose": purpose,
		"consents": map[string]any{
			"form_authorized": form, "channel_reply": false,
			"marketing_phone": phoneOK, "marketing_sms": sms, "notice_version": "n1",
		},
	})
	return string(b)
}

func (h *harness) contactCount(session, tenant string) int {
	h.t.Helper()
	out := h.mustDo("GET", "/api/v1/contacts", session, tenant, "", http.StatusOK)
	items, _ := out["items"].([]any)
	return len(items)
}

type recordSource struct {
	url   string
	mu    sync.Mutex
	recs  map[string]string
	fail  bool
	token string
}

func newRecordSource(t *testing.T) *recordSource {
	t.Helper()
	s := &recordSource{recs: map[string]string{}, token: "test-fetch-token"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != s.token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.fail {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		ref := strings.TrimPrefix(r.URL.Path, "/internal/v1/lead-records/")
		raw, ok := s.recs[ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *recordSource) put(ref, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[ref] = raw
}
