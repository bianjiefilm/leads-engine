package httpapi

import (
	"strings"
	"testing"
)

func TestSOPFeatureOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenant, _, contact := h.seed()
	status, _, _ := h.do("GET", "/api/v1/sop/capability", sessionOwnerA, tenant, "")
	if status != 404 {
		t.Fatalf("flag off capability = %d", status)
	}
	status, _, _ = h.do("POST", "/api/v1/sop/reminders", sessionOwnerA, tenant, `{"contact_id":"`+contact+`"}`)
	if status != 404 {
		t.Fatalf("flag off reminder = %d", status)
	}
}

func TestSOPReminderDraftConfirmStaysUndelivered(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureSOP: true, featureReception: true, captureLog: true})
	tenant, other, contact := h.seed()
	h.openSOPWindow(tenant)
	consent := h.sopConsent(tenant, contact, "follow_up", "sms", false)

	cap := h.mustDo("GET", "/api/v1/sop/capability", sessionOwnerA, tenant, "", 200)
	if cap["unattended"] == true || cap["verification"] != "unverified" || cap["live_charge"].(float64) != 0 {
		t.Fatalf("capability: %v", cap)
	}
	if providers, _ := cap["live_channels"].([]any); len(providers) != 0 {
		t.Fatalf("live channels: %v", cap["live_channels"])
	}

	secret := "下次跟进草稿不应出现在日志里"
	remind := h.mustDo("POST", "/api/v1/sop/reminders", sessionOwnerA, tenant, `{
		"contact_id":"`+contact+`","channel":"sms","recipient":"13800001111","purpose":"follow_up",
		"consent_id":"`+consent+`","content_version":1,"budget_cents":0,"body":"`+secret+`"
	}`, 201)
	if remind["delivered"] == true || remind["kind"] != "internal_reminder" || remind["status"] != "recorded" {
		t.Fatalf("reminder: %v", remind)
	}
	draft := h.mustDo("POST", "/api/v1/sop/drafts", sessionOwnerA, tenant, `{
		"contact_id":"`+contact+`","channel":"sms","recipient":"13800001111","purpose":"follow_up",
		"consent_id":"`+consent+`","content_version":1,"budget_cents":0,"parent_id":"`+remind["id"].(string)+`",
		"body":"`+secret+`"
	}`, 201)
	if draft["kind"] != "pending_draft" || draft["delivered"] == true {
		t.Fatalf("draft: %v", draft)
	}
	confirmed := h.mustDo("POST", "/api/v1/sop/drafts/"+draft["id"].(string)+"/confirm", sessionOwnerA, tenant, `{}`, 200)
	if confirmed["delivered"] == true || confirmed["live_charge"].(float64) != 0 {
		t.Fatalf("confirm charged or delivered: %v", confirmed)
	}
	if !sopHas(confirmed["records"], "channel_submission", "pending_send") || !sopHas(confirmed["records"], "channel_delivery", "undelivered") {
		t.Fatalf("confirm records: %v", confirmed["records"])
	}
	if sopHasStatus(confirmed["records"], "delivered") {
		t.Fatalf("confirm wrote delivered: %v", confirmed)
	}
	reply := h.mustDo("POST", "/api/v1/sop/replies", sessionOwnerA, tenant, `{
		"contact_id":"`+contact+`","channel":"sms","recipient":"13800001111","purpose":"follow_up",
		"consent_id":"`+consent+`","content_version":1,"budget_cents":0,"body":"用户回了一句"
	}`, 201)
	if reply["kind"] != "user_reply" || reply["status"] != "recorded" || reply["delivered"] == true {
		t.Fatalf("user reply: %v", reply)
	}

	var delivered int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM sop_actions WHERE status='delivered' OR delivered!=0 OR live_charge!=0`).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 0 {
		t.Fatalf("stored a delivery or a charge")
	}
	if strings.Contains(h.logs.String(), secret) || strings.Contains(h.logs.String(), "13800001111") {
		t.Fatalf("log leaked draft or recipient")
	}

	status, leaked, _ := h.do("GET", "/api/v1/sop/actions?contact_id="+contact, sessionOwnerB, other, "")
	if status != 200 {
		t.Fatalf("other tenant list: %d %v", status, leaked)
	}
	if items, _ := leaked["items"].([]any); len(items) != 0 {
		t.Fatalf("actions leaked across tenants: %v", leaked)
	}

	auto := h.mustDo("POST", "/api/v1/sop/auto", sessionOwnerA, tenant, `{
		"contact_id":"`+contact+`","channel":"sms","recipient":"13800001111","purpose":"follow_up",
		"consent_id":"`+consent+`","content_version":1,"budget_cents":0,"balance_cents":9900,"ai_score":99,"body":"自动"
	}`, 409)
	if auto["error"] != "unattended_closed" || auto["delivered"] == true || auto["unattended"] == true {
		t.Fatalf("score opened auto: %v", auto)
	}
}

func TestSOPUnsubscribeRejectionRetryAndStops(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureSOP: true, featureReception: true, captureLog: true})
	tenant, _, contact := h.seed()
	h.openSOPWindow(tenant)
	consent := h.sopConsent(tenant, contact, "follow_up", "sms", false)
	draft := h.sopDraft(tenant, contact, consent, "follow_up", "sms")

	h.mustDo("POST", "/api/v1/sop/stops", sessionOwnerA, tenant, `{
		"kind":"unsubscribe","contact_id":"`+contact+`","channel":"sms","purpose":"follow_up"
	}`, 201)
	blocked := h.mustDo("POST", "/api/v1/sop/drafts/"+draft+"/confirm", sessionOwnerA, tenant, `{}`, 409)
	if blocked["error"] != "unsubscribed" || blocked["delivered"] == true {
		t.Fatalf("unsubscribe: %v", blocked)
	}

	contact2 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		`{"name":"乙","phone":"13900002222","business_category":"merchant_customer","source_type":"manual","consent_status":"granted"}`, 201)["id"].(string)
	consent2 := h.sopConsent(tenant, contact2, "follow_up", "sms", false)
	draft2 := h.sopDraft(tenant, contact2, consent2, "follow_up", "sms")
	h.mustDo("POST", "/api/v1/sop/stops", sessionOwnerA, tenant, `{
		"kind":"reject","contact_id":"`+contact2+`","channel":"sms","purpose":"follow_up"
	}`, 201)
	rejected := h.mustDo("POST", "/api/v1/sop/drafts/"+draft2+"/confirm", sessionOwnerA, tenant, `{}`, 409)
	if rejected["error"] != "rejected_schedule" || rejected["delivered"] == true {
		t.Fatalf("rejected schedule: %v", rejected)
	}

	contact3 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		`{"name":"丙","phone":"13700003333","business_category":"merchant_customer","source_type":"manual","consent_status":"granted"}`, 201)["id"].(string)
	consent3 := h.sopConsent(tenant, contact3, "follow_up", "sms", false)
	draft3 := h.sopDraft(tenant, contact3, consent3, "follow_up", "sms")
	first := h.mustDo("POST", "/api/v1/sop/drafts/"+draft3+"/confirm", sessionOwnerA, tenant, `{}`, 200)
	if first["delivered"] == true || !sopHas(first["records"], "channel_delivery", "undelivered") {
		t.Fatalf("first confirm: %v", first)
	}
	early := h.mustDo("POST", "/api/v1/sop/drafts/"+draft3+"/retry", sessionOwnerA, tenant, `{"timed_out":true}`, 409)
	if early["error"] != "original_unreconciled" || early["delivered"] == true {
		t.Fatalf("retry before reconcile: %v", early)
	}
	once := h.mustDo("POST", "/api/v1/sop/drafts/"+draft3+"/retry", sessionOwnerA, tenant, `{"timed_out":true,"original_reconciled":true}`, 200)
	if once["delivered"] == true || !sopHas(once["records"], "channel_delivery", "undelivered") {
		t.Fatalf("reconciled retry: %v", once)
	}
	again := h.mustDo("POST", "/api/v1/sop/drafts/"+draft3+"/retry", sessionOwnerA, tenant, `{"timed_out":true,"original_reconciled":true}`, 409)
	if again["error"] != "attempt_exhausted" || again["delivered"] == true {
		t.Fatalf("second retry: %v", again)
	}

	market := h.sopDraft(tenant, contact3, consent3, "marketing", "email")
	consult := h.mustDo("POST", "/api/v1/sop/drafts/"+market+"/confirm", sessionOwnerA, tenant, `{}`, 409)
	if consult["error"] != "consent_scope" || consult["delivered"] == true {
		t.Fatalf("consultation marketed: %v", consult)
	}

	contact4 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		`{"name":"丁","phone":"13600004444","business_category":"merchant_customer","source_type":"manual","consent_status":"granted"}`, 201)["id"].(string)
	consent4 := h.sopConsent(tenant, contact4, "follow_up", "sms", false)
	draft4 := h.sopDraft(tenant, contact4, consent4, "follow_up", "sms")
	h.mustDo("POST", "/api/v1/sop/stops", sessionOwnerA, tenant, `{"kind":"global_stop"}`, 201)
	stopped := h.mustDo("POST", "/api/v1/sop/drafts/"+draft4+"/confirm", sessionOwnerA, tenant, `{}`, 409)
	if stopped["error"] != "global_stop" || stopped["delivered"] == true {
		t.Fatalf("global stop: %v", stopped)
	}

	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenant, `{"default_mode":"ai"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-sop1689a"}`, 201)
	sessionID := opened["session"].(map[string]any)["id"].(string)
	h.mustDo("POST", "/api/v1/reception/sessions/"+sessionID+"/takeover", sessionOwnerA, tenant, `{}`, 200)
	h.mustDo("POST", "/api/v1/sop/policy", sessionOwnerA, tenant, `{"level":"unattended","explicit_unattended":true,"window_start":0,"window_end":24}`, 200)
	taken := h.mustDo("POST", "/api/v1/sop/auto", sessionOwnerA, tenant, `{
		"contact_id":"`+contact4+`","session_id":"`+sessionID+`","channel":"sms","recipient":"13600004444",
		"purpose":"follow_up","consent_id":"`+consent4+`","content_version":1,"budget_cents":0,"balance_cents":100,"ai_score":100
	}`, 409)
	if taken["error"] != "human_takeover" || taken["delivered"] == true {
		t.Fatalf("takeover auto: %v", taken)
	}
	var sessions, knowledge int
	_ = h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM reception_sessions WHERE tenant_id=?`, tenant).Scan(&sessions)
	_ = h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM knowledge_sources WHERE tenant_id=?`, tenant).Scan(&knowledge)
	if sessions != 1 || knowledge != 0 {
		t.Fatalf("sop created a second conversation or knowledge base: sessions=%d knowledge=%d", sessions, knowledge)
	}
}

func (h *harness) openSOPWindow(tenant string) {
	h.t.Helper()
	h.mustDo("POST", "/api/v1/sop/policy", sessionOwnerA, tenant, `{"level":"remind_draft_confirm","explicit_unattended":false,"window_start":0,"window_end":24}`, 200)
}

func (h *harness) sopConsent(tenant, contact, purpose, channel string, marketing bool) string {
	h.t.Helper()
	flag := "false"
	if marketing {
		flag = "true"
	}
	body := `{
		"source_submission_ref":"sop-` + purpose + `-` + channel + `-` + contact + `",
		"source_channel":"` + channel + `","notice_version":"n1","purpose":"` + purpose + `","marketing_allowed":` + flag + `
	}`
	out := h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenant, body, 200)
	consent, _ := out["consent"].(map[string]any)
	id, _ := consent["id"].(string)
	if id == "" {
		h.t.Fatalf("consent: %v", out)
	}
	return id
}

func (h *harness) sopDraft(tenant, contact, consent, purpose, channel string) string {
	h.t.Helper()
	out := h.mustDo("POST", "/api/v1/sop/drafts", sessionOwnerA, tenant, `{
		"contact_id":"`+contact+`","channel":"`+channel+`","recipient":"13800001111","purpose":"`+purpose+`",
		"consent_id":"`+consent+`","content_version":2,"budget_cents":0,"body":"草稿"
	}`, 201)
	id, _ := out["id"].(string)
	if id == "" || out["kind"] != "pending_draft" {
		h.t.Fatalf("draft: %v", out)
	}
	return id
}

func sopHas(records any, kind, status string) bool {
	list, _ := records.([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["kind"] == kind && row["status"] == status {
			return true
		}
	}
	return false
}

func sopHasStatus(records any, status string) bool {
	list, _ := records.([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["status"] == status {
			return true
		}
	}
	return false
}
