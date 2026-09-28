package httpapi

import (
	"strings"
	"testing"
	"time"
)

func TestOutboundFeatureOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenant, _, contact := h.seed()
	status, _, _ := h.do("GET", "/api/v1/outbound/capability", sessionOwnerA, tenant, "")
	if status != 404 {
		t.Fatalf("flag off capability = %d", status)
	}
	status, _, _ = h.do("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{"contact_id":"`+contact+`","task_key":"k","mode":"isolation"}`)
	if status != 404 {
		t.Fatalf("flag off dial = %d", status)
	}
}

func TestOutboundIsolationDoesNotClaimARealCall(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureOutbound: true, captureLog: true})
	tenant, other, contact := h.seed()
	h.openOutboundWindow(tenant)
	consent := h.voiceConsent(tenant, contact, true)

	cap := h.mustDo("GET", "/api/v1/outbound/capability", sessionOwnerA, tenant, "", 200)
	if cap["production_auto"] == true || cap["real_line"] == true || cap["cost"] != "unknown" || cap["live_charge"].(float64) != 0 || cap["verification"] != "unverified" {
		t.Fatalf("capability: %v", cap)
	}
	forced := h.mustDo("POST", "/api/v1/outbound/policy", sessionOwnerA, tenant, `{"production_auto":true,"window_start":0,"window_end":24}`, 200)
	if forced["production_auto"] == true {
		t.Fatalf("policy enabled auto: %v", forced)
	}

	secretPhone := "13800138000"
	secretRec := "rec-secret-audio"
	secretText := "客户说了预算八十万"
	body := `{
		"task_key":"task-iso-1","contact_id":"` + contact + `","campaign_id":"camp-a","consent_id":"` + consent + `",
		"mode":"isolation","simulation":false,"budget_cents":0,"op":"dial","ai_score":99,"public_source":true,
		"phone":"` + secretPhone + `"
	}`
	dialed := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, body, 201)
	if dialed["simulation"] != true || dialed["dial_succeeded"] == true || dialed["real_connected"] == true || dialed["cost_known"] == true || dialed["live_charge"].(float64) != 0 {
		t.Fatalf("dial: %v", dialed)
	}
	if !outboundHas(dialed["receipts"], "dial_submission", "simulated") {
		t.Fatalf("submission: %v", dialed["receipts"])
	}
	taskID := dialed["id"].(string)

	auto := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{
		"task_key":"task-auto","contact_id":"`+contact+`","campaign_id":"camp-a","consent_id":"`+consent+`",
		"mode":"isolation","budget_cents":0,"op":"auto","ai_score":99,"phone":"`+secretPhone+`"
	}`, 409)
	if auto["error"] != "production_auto_closed" || auto["dial_succeeded"] == true {
		t.Fatalf("auto: %v", auto)
	}

	prod := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{
		"task_key":"task-prod","contact_id":"`+contact+`","campaign_id":"camp-a","consent_id":"`+consent+`",
		"mode":"production","budget_cents":0,"op":"dial","phone":"`+secretPhone+`"
	}`, 409)
	if prod["error"] != "no_real_line" || prod["dial_succeeded"] == true {
		t.Fatalf("production: %v", prod)
	}

	replay := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{
		"task_key":"task-iso-1","contact_id":"`+contact+`","campaign_id":"camp-b","consent_id":"`+consent+`",
		"mode":"isolation","budget_cents":0,"op":"dial"
	}`, 200)
	if replay["replay"] != true || replay["id"] != taskID || replay["dial_succeeded"] == true {
		t.Fatalf("replay: %v", replay)
	}

	ack := h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/receipts", sessionOwnerA, tenant, `{
		"kind":"connected","provider_http":200,"claims_dial_success":true,"claims_empty_number":true,"claims_connected":true,
		"recording":"`+secretRec+`","transcript":"`+secretText+`","phone":"`+secretPhone+`"
	}`, 200)
	if ack["dial_succeeded"] == true || ack["empty_number_detected"] == true || ack["real_connected"] == true || ack["simulation"] != true || ack["cost_known"] == true {
		t.Fatalf("ack: %v", ack)
	}
	if !outboundHas(ack["receipts"], "connected", "simulated") {
		t.Fatalf("connected receipt: %v", ack["receipts"])
	}
	events := h.mustDo("GET", "/api/v1/outbound/tasks/"+taskID+"/events", sessionOwnerA, tenant, "", 200)
	blob := eventsJSON(events)
	for _, secret := range []string{secretPhone, secretRec, secretText} {
		if strings.Contains(blob, secret) {
			t.Fatalf("public event leaked %s: %s", secret, blob)
		}
	}
	var recCount int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM outbound_recordings WHERE task_id=? AND content=?`, taskID, secretRec).Scan(&recCount); err != nil {
		t.Fatal(err)
	}
	if recCount != 1 {
		t.Fatalf("recording row = %d", recCount)
	}
	if h.logs == nil || strings.Contains(h.logs.String(), secretPhone) || strings.Contains(h.logs.String(), secretRec) || strings.Contains(h.logs.String(), secretText) {
		t.Fatalf("log leaked private text")
	}
	status, _, _ := h.do("GET", "/api/v1/outbound/tasks/"+taskID+"/events", sessionOwnerB, other, "")
	if status != 404 && status != 403 {
		t.Fatalf("cross tenant events = %d", status)
	}
}

func TestOutboundRecheckStopsCampaignSwitch(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureOutbound: true, featureSOP: true})
	tenant, _, contact := h.seed()
	h.openOutboundWindow(tenant)
	h.openSOPWindow(tenant)

	noConsent := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{
		"task_key":"no-consent","contact_id":"`+contact+`","campaign_id":"camp-a","mode":"isolation","budget_cents":0,"op":"dial",
		"phone":"13800138000","public_source":true,"ai_score":100
	}`, 409)
	if noConsent["error"] != "consent_missing" || noConsent["dial_succeeded"] == true {
		t.Fatalf("public source: %v", noConsent)
	}

	consent := h.voiceConsent(tenant, contact, true)
	unbound := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{
		"task_key":"no-budget","contact_id":"`+contact+`","campaign_id":"camp-a","consent_id":"`+consent+`","mode":"isolation","op":"dial"
	}`, 409)
	if unbound["error"] != "budget_unbound" {
		t.Fatalf("budget: %v", unbound)
	}

	hour := shanghaiHour()
	start := (hour + 2) % 24
	end := start + 1
	h.mustDo("POST", "/api/v1/outbound/policy", sessionOwnerA, tenant, `{"window_start":`+itoa(start)+`,"window_end":`+itoa(end)+`}`, 200)
	closed := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("outside", contact, consent, "camp-a"), 409)
	if closed["error"] != "outside_window" {
		t.Fatalf("window: %v", closed)
	}
	h.openOutboundWindow(tenant)

	h.mustDo("POST", "/api/v1/sop/stops", sessionOwnerA, tenant, `{
		"kind":"unsubscribe","contact_id":"`+contact+`","channel":"voice","purpose":"marketing"
	}`, 201)
	fromSOP := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("sop-stop", contact, consent, "camp-a"), 409)
	if fromSOP["error"] != "unsubscribed" {
		t.Fatalf("sop unsubscribe: %v", fromSOP)
	}

	contact2 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		`{"name":"乙","phone":"13900002222","email":"yi@shop.cn","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, 201)["id"].(string)
	consent2 := h.voiceConsent(tenant, contact2, true)
	h.mustDo("POST", "/api/v1/outbound/stops", sessionOwnerA, tenant, `{"kind":"reject","contact_id":"`+contact2+`"}`, 201)
	rejected := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("rej-a", contact2, consent2, "camp-a"), 409)
	switched := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("rej-b", contact2, consent2, "camp-b"), 409)
	if rejected["error"] != "rejected_subject" || switched["error"] != "rejected_subject" {
		t.Fatalf("reject stick: %v %v", rejected, switched)
	}

	contact3 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		`{"name":"丙","phone":"13700003333","email":"bing@shop.cn","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, 201)["id"].(string)
	consent3 := h.voiceConsent(tenant, contact3, true)
	h.mustDo("POST", "/api/v1/outbound/stops", sessionOwnerA, tenant, `{"kind":"suppression","contact_id":"`+contact3+`"}`, 201)
	suppressed := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("sup", contact3, consent3, "camp-z"), 409)
	if suppressed["error"] != "suppressed" {
		t.Fatalf("suppression: %v", suppressed)
	}

	contact4 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		`{"name":"丁","phone":"13600004444","email":"ding@shop.cn","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, 201)["id"].(string)
	consent4 := h.voiceConsent(tenant, contact4, true)
	h.mustDo("POST", "/api/v1/outbound/stops", sessionOwnerA, tenant, `{"kind":"global_stop"}`, 201)
	stopped := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("halt", contact4, consent4, "camp-a"), 409)
	if stopped["error"] != "global_stop" || stopped["dial_succeeded"] == true {
		t.Fatalf("global stop: %v", stopped)
	}
}

func TestOutboundCancelTransferAndUnknownRetry(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureOutbound: true, featureReception: true})
	tenant, _, contact := h.seed()
	h.openOutboundWindow(tenant)
	consent := h.voiceConsent(tenant, contact, true)

	first := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("live-1", contact, consent, "camp-a"), 201)
	taskID := first["id"].(string)
	early := h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/retry", sessionOwnerA, tenant, `{"original_reconciled":true}`, 409)
	if early["error"] != "original_unreconciled" || early["dial_succeeded"] == true {
		t.Fatalf("blind retry: %v", early)
	}
	h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/receipts", sessionOwnerA, tenant, `{"kind":"ringing"}`, 200)
	h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/receipts", sessionOwnerA, tenant, `{"kind":"call_completed"}`, 200)
	h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/receipts", sessionOwnerA, tenant, `{"kind":"intent_suggestion"}`, 200)
	listed := h.mustDo("GET", "/api/v1/outbound/tasks/"+taskID, sessionOwnerA, tenant, "", 200)
	for _, kind := range []string{"dial_submission", "ringing", "call_completed", "intent_suggestion"} {
		if !outboundHasKind(listed["receipts"], kind) {
			t.Fatalf("missing %s in %v", kind, listed["receipts"])
		}
	}

	cancelled := h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/cancel", sessionOwnerA, tenant, `{}`, 200)
	if cancelled["state"] != "cancelled" {
		t.Fatalf("cancel: %v", cancelled)
	}
	afterCancel := h.mustDo("POST", "/api/v1/outbound/tasks/"+taskID+"/receipts", sessionOwnerA, tenant, `{"kind":"connected","provider_http":200,"claims_connected":true}`, 409)
	if afterCancel["error"] != "cancelled" || afterCancel["real_connected"] == true {
		t.Fatalf("cancel receipt: %v", afterCancel)
	}

	second := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, dialBody("live-2", contact, consent, "camp-a"), 201)
	secondID := second["id"].(string)
	moved := h.mustDo("POST", "/api/v1/outbound/tasks/"+secondID+"/transfer", sessionOwnerA, tenant, `{}`, 200)
	if moved["state"] != "transferred" {
		t.Fatalf("transfer: %v", moved)
	}
	afterMove := h.mustDo("POST", "/api/v1/outbound/tasks/"+secondID+"/retry", sessionOwnerA, tenant, `{}`, 409)
	if afterMove["error"] != "human_transfer" || afterMove["dial_succeeded"] == true {
		t.Fatalf("transfer retry: %v", afterMove)
	}

	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenant, `{"default_mode":"ai"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-out1687a"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)
	h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/takeover", sessionOwnerA, tenant, `{}`, 200)
	held := h.mustDo("POST", "/api/v1/outbound/tasks", sessionOwnerA, tenant, `{
		"task_key":"human-session","contact_id":"`+contact+`","campaign_id":"camp-a","consent_id":"`+consent+`",
		"mode":"isolation","budget_cents":0,"op":"dial","session_id":"`+sid+`"
	}`, 409)
	if held["error"] != "human_transfer" {
		t.Fatalf("session takeover: %v", held)
	}
	var sessions int
	_ = h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM reception_sessions WHERE tenant_id=?`, tenant).Scan(&sessions)
	if sessions != 1 {
		t.Fatalf("outbound created a session: %d", sessions)
	}
}

func (h *harness) openOutboundWindow(tenant string) {
	h.t.Helper()
	h.mustDo("POST", "/api/v1/outbound/policy", sessionOwnerA, tenant, `{"window_start":0,"window_end":24}`, 200)
}

func (h *harness) voiceConsent(tenant, contact string, marketing bool) string {
	h.t.Helper()
	flag := "false"
	if marketing {
		flag = "true"
	}
	out := h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenant, `{
		"source_submission_ref":"voice-`+contact+`","source_channel":"voice","notice_version":"n1",
		"purpose":"marketing","marketing_allowed":`+flag+`
	}`, 200)
	consent, _ := out["consent"].(map[string]any)
	id, _ := consent["id"].(string)
	if id == "" {
		h.t.Fatalf("voice consent: %v", out)
	}
	return id
}

func dialBody(key, contact, consent, campaign string) string {
	return `{
		"task_key":"` + key + `","contact_id":"` + contact + `","campaign_id":"` + campaign + `","consent_id":"` + consent + `",
		"mode":"isolation","budget_cents":0,"op":"dial"
	}`
}

func outboundHas(records any, kind, status string) bool {
	list, _ := records.([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["kind"] == kind && row["status"] == status {
			return true
		}
	}
	return false
}

func outboundHasKind(records any, kind string) bool {
	list, _ := records.([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["kind"] == kind {
			return true
		}
	}
	return false
}

func eventsJSON(body map[string]any) string {
	items, _ := body["items"].([]any)
	var b strings.Builder
	for _, item := range items {
		row, _ := item.(map[string]any)
		b.WriteString(row["body"].(string))
		b.WriteByte('\n')
	}
	return b.String()
}

func shanghaiHour() int {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return time.Now().In(loc).Hour()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [4]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
