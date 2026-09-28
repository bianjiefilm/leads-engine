package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCRMTenantIsolationAndExport(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureFollowups: true})
	tenantA, tenantB, _ := h.seed()
	const phone = "13800001111"
	const token = "internal-token-should-not-export"

	brand := h.mustDo("POST", "/api/v1/brands", sessionOwnerA, tenantA,
		`{"brand_id":"brand_white","display_name":"白标旧名"}`, 201)
	if brand["tenant_id"] != nil && brand["tenant_id"] != "" {
		t.Fatalf("brand was stored as a tenant: %v", brand)
	}
	h.mustDo("POST", "/api/v1/brands", sessionOwnerB, tenantB,
		`{"brand_id":"brand_self","display_name":"自营"}`, 201)

	bodyA := fmt.Sprintf(`{"name":"甲","phone":%q,"email":"a@shop.test","business_category":"merchant_customer","source_type":"manual","consent_status":"granted","tenant_id":"brand_white","brand_id":"brand_white"}`, phone)
	contactA := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, bodyA, 201)
	if contactA["tenant_id"] != tenantA || contactA["origin_brand_id"] != "brand_white" {
		t.Fatalf("contact authority = %v", contactA)
	}
	bodyB := fmt.Sprintf(`{"name":"乙","phone":%q,"email":"b@shop.test","business_category":"merchant_customer","source_type":"manual","consent_status":"granted","tenant_id":"brand_self","brand_id":"brand_self"}`, phone)
	contactB := h.mustDo("POST", "/api/v1/contacts", sessionOwnerB, tenantB, bodyB, 201)
	if contactB["id"] == contactA["id"] || contactB["tenant_id"] != tenantB {
		t.Fatalf("same phone merged across tenants: a=%v b=%v", contactA["id"], contactB)
	}
	if h.count(`SELECT COUNT(1) FROM members WHERE principal_ref=?`, phone) != 0 {
		t.Fatal("phone was bound as a platform principal")
	}
	h.mustDo("POST", "/api/v1/contacts/"+contactA["id"].(string)+"/merge/"+contactB["id"].(string), sessionOwnerA, tenantA, "", 404)

	lead := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"tenant_id":"brand_white","brand_id":"brand_white"}`, contactA["id"]), 201)
	if lead["tenant_id"] != tenantA {
		t.Fatalf("lead tenant = %v", lead["tenant_id"])
	}
	opp := h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"title":"老商机","business_category":"merchant_customer","tenant_id":"brand_white","brand_id":"brand_white"}`, contactA["id"]), 201)
	follow := h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"历史跟进","tenant_id":"brand_white","brand_id":"brand_white"}`, contactA["id"], lead["id"]), 201)
	if follow["tenant_id"] != tenantA {
		t.Fatalf("follow-up tenant = %v", follow["tenant_id"])
	}
	consent := h.mustDo("POST", "/api/v1/contacts/"+contactA["id"].(string)+"/consents", sessionOwnerA, tenantA,
		`{"source_submission_ref":"sub-brand","source_channel":"landing","notice_version":"v1","purpose":"marketing","marketing_allowed":true}`, 200)

	h.mustDo("PATCH", "/api/v1/brands/brand_white", sessionOwnerA, tenantA, `{"display_name":"白标新名"}`, 200)
	again := h.mustDo("GET", "/api/v1/contacts/"+contactA["id"].(string), sessionOwnerA, tenantA, "", 200)
	if again["tenant_id"] != tenantA || again["origin_brand_id"] != "brand_white" {
		t.Fatalf("rename moved the contact: %v", again)
	}
	listed := h.mustDo("GET", "/api/v1/contacts/"+contactA["id"].(string)+"/consents", sessionOwnerA, tenantA, "", 200)
	if !strings.Contains(fmt.Sprint(listed), "sub-brand") || !strings.Contains(fmt.Sprint(listed), "landing") {
		t.Fatalf("consent source changed: %v", listed)
	}
	oppAgain := h.mustDo("GET", "/api/v1/opportunities/"+opp["id"].(string), sessionOwnerA, tenantA, "", 200)
	if oppAgain["stage"] != opp["stage"] {
		t.Fatalf("opportunity history changed: %v", oppAgain)
	}
	_ = consent

	h.mustDo("GET", "/api/v1/contacts/"+contactA["id"].(string), sessionOwnerB, tenantA, "", 403)
	h.mustDo("GET", "/api/v1/leads/"+lead["id"].(string), sessionOwnerB, tenantA, "", 403)
	h.mustDo("GET", "/api/v1/opportunities/"+opp["id"].(string), sessionOwnerB, tenantA, "", 403)
	h.mustDo("GET", "/api/v1/follow-ups/"+follow["id"].(string), sessionOwnerB, tenantA, "", 403)

	if _, err := h.api.St.RegisterChannelOperator(principalChannel, "ch_1"); err != nil {
		t.Fatalf("register channel operator: %v", err)
	}
	if _, err := h.api.St.PutChannelProcurement(tenantA, 8800, 0, token); err != nil {
		t.Fatalf("procurement: %v", err)
	}
	status, raw, _ := h.doRaw("GET", "/api/v1/contacts", sessionChannel, tenantA, "")
	if status != 403 || strings.Contains(string(raw), phone) {
		t.Fatalf("channel admin plaintext: status=%d body=%s", status, raw)
	}
	statusView := h.mustDo("GET", "/api/v1/channel/tenant-status", sessionChannel, tenantA, "", 200)
	view := fmt.Sprint(statusView)
	if strings.Contains(view, phone) || strings.Contains(view, token) || strings.Contains(view, "8800") {
		t.Fatalf("status leaked CRM or procurement: %v", statusView)
	}

	h.mustDo("POST", "/api/v1/admin/crm-delegations", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"principal_ref":%q,"kind":"support"}`, principalChannel), 201)
	seen := h.mustDo("GET", "/api/v1/contacts", sessionChannel, tenantA, "", 200)
	if !strings.Contains(fmt.Sprint(seen), phone) {
		t.Fatal("support delegation could not read this tenant")
	}
	h.api.St.DB.Exec(`UPDATE crm_delegations SET revoked_at='2000-01-01T00:00:00Z' WHERE principal_ref=? AND kind='support'`, principalChannel)
	status, raw, _ = h.doRaw("GET", "/api/v1/contacts", sessionChannel, tenantA, "")
	if status != 403 || strings.Contains(string(raw), phone) {
		t.Fatalf("revoked delegation still saw plaintext: %d %s", status, raw)
	}

	h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenantA,
		`{"plan":"crm_pro","status":"downgraded","wallet_balance_cents":100000,"org_billing_account_id":"ba_org","crm_subscription_cents":1200,"merchant_deal_cents":1}`, 200)
	stopped := h.mustDo("POST", "/api/v1/ai-usage/quotes", sessionOwnerA, tenantA,
		`{"action":"ai_score","payer_kind":"organization","payer_account_id":"ba_org","return_to":"/leads"}`, 409)
	if stopped["error"] != "paid_ai_stopped" || stopped["history_retained"] != true || stopped["data_lost"] == true {
		t.Fatalf("downgrade gate = %v", stopped)
	}
	h.mustDo("GET", "/api/v1/contacts/"+contactA["id"].(string), sessionOwnerA, tenantA, "", 200)
	h.mustDo("GET", "/api/v1/follow-ups/"+follow["id"].(string), sessionOwnerA, tenantA, "", 200)
	h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenantA,
		`{"plan":"crm_pro","status":"active","wallet_balance_cents":100000,"org_billing_account_id":"ba_org","crm_subscription_cents":1200,"merchant_deal_cents":1}`, 200)
	quotaStopped := h.mustDo("POST", "/api/v1/ai-usage/quotes", sessionOwnerA, tenantA,
		`{"action":"ai_score","payer_kind":"organization","payer_account_id":"ba_org","return_to":"/leads"}`, 409)
	if quotaStopped["error"] != "paid_ai_stopped" {
		t.Fatalf("quota gate = %v", quotaStopped)
	}
	if _, err := h.api.St.PutChannelProcurement(tenantA, 8800, 3, token); err != nil {
		t.Fatal(err)
	}
	h.mustDo("POST", "/api/v1/ai-usage/quotes", sessionOwnerA, tenantA,
		`{"action":"ai_score","payer_kind":"organization","payer_account_id":"ba_org","return_to":"/leads"}`, 200)

	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"suspended"}`, 200)
	h.mustDo("POST", "/api/v1/brands/brand_white/lifecycle", sessionOwnerA, tenantA, `{"status":"active"}`, 200)
	paused := h.mustDo("GET", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, "", 200)
	if paused["tenant_status"] != "suspended" || paused["brand_status"] == "suspended" {
		t.Fatalf("pauses coupled: %v", paused)
	}
	h.mustDo("GET", "/api/v1/contacts/"+contactA["id"].(string), sessionOwnerA, tenantA, "", 200)
	h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"暂停中","business_category":"merchant_customer","source_type":"manual"}`, 409)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"active"}`, 200)
	h.mustDo("POST", "/api/v1/brands/brand_white/lifecycle", sessionOwnerA, tenantA, `{"status":"suspended"}`, 200)
	h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"品牌暂停仍可建档","phone":"13700003333","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, 201)

	salesID := h.memberID(tenantA, principalSalesA1)
	h.mustDo("PATCH", "/api/v1/admin/members/"+salesID, sessionOwnerA, tenantA, `{"enabled":false}`, 200)
	revokedConsent := consent["consent"].(map[string]any)
	h.mustDo("POST", "/api/v1/contacts/"+contactA["id"].(string)+"/consents/"+revokedConsent["id"].(string)+"/revoke", sessionOwnerA, tenantA, `{"reason":"客户撤回"}`, 200)
	grant := h.mustDo("POST", "/api/v1/admin/crm-delegations", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"principal_ref":%q,"kind":"operation"}`, principalChannel), 201)
	h.mustDo("POST", "/api/v1/admin/crm-delegations/"+grant["id"].(string)+"/revoke", sessionOwnerA, tenantA, "", 200)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"suspended"}`, 200)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"active"}`, 200)
	if status, body, _ := h.do("GET", "/api/v1/contacts", sessionSalesA1, tenantA, ""); status != 403 {
		t.Fatalf("resume restored sales: %d %v", status, body)
	}
	replay := h.mustDo("POST", "/api/v1/contacts/"+contactA["id"].(string)+"/consents", sessionOwnerA, tenantA,
		`{"source_submission_ref":"sub-brand","source_channel":"landing","notice_version":"v1","purpose":"marketing","marketing_allowed":true}`, 200)
	if replay["state"] != "replay_unchanged" || replay["status"] != "revoked" {
		t.Fatalf("replay restored consent: %v", replay)
	}
	status, raw, _ = h.doRaw("GET", "/api/v1/contacts", sessionChannel, tenantA, "")
	if status != 403 || strings.Contains(string(raw), phone) {
		t.Fatalf("resume restored delegation: %d %s", status, raw)
	}

	job := h.mustDo("POST", "/api/v1/crm/exports", sessionOwnerA, tenantA, `{"purpose":"offboarding","brand_id":"brand_white"}`, 201)
	if job["tenant_id"] != tenantA || job["brand_id"] != "brand_white" || job["requester"] != principalOwnerA || job["purpose"] != "offboarding" || job["expires_at"] == "" {
		t.Fatalf("job binding = %v", job)
	}
	if strings.Contains(fmt.Sprint(job), phone) || strings.Contains(fmt.Sprint(job), token) {
		t.Fatal("create job returned the payload")
	}
	noConfirm, _, _ := h.do("GET", "/api/v1/crm/exports/"+job["id"].(string)+"/download", sessionOwnerA, tenantA, "")
	if noConfirm != 401 {
		t.Fatalf("download without reauth = %d", noConfirm)
	}
	h.api.St.DB.Exec(`UPDATE crm_export_jobs SET expires_at='2099-01-01T00:00:00Z' WHERE id=?`, job["id"])
	exported := h.downloadExport(job["id"].(string), sessionOwnerA, tenantA)
	dump := fmt.Sprint(exported)
	for _, want := range []string{phone, "历史跟进", "老商机", "sub-brand", "brand_white"} {
		if !strings.Contains(dump, want) {
			t.Fatalf("export missing %s: %s", want, dump)
		}
	}
	if strings.Contains(dump, token) || strings.Contains(dump, "8800") || strings.Contains(dump, "purchase_price") || strings.Contains(dump, contactB["id"].(string)) {
		t.Fatalf("export leaked forbidden data: %s", dump)
	}
	foreign := h.downloadStatus(job["id"].(string), sessionOwnerB, tenantB)
	if foreign != 403 && foreign != 404 {
		t.Fatalf("other tenant download = %d", foreign)
	}
	h.api.St.DB.Exec(`UPDATE crm_export_jobs SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, job["id"])
	if got := h.downloadStatus(job["id"].(string), sessionOwnerA, tenantA); got != 410 {
		t.Fatalf("expired download = %d", got)
	}
}

func (h *harness) downloadExport(id, session, tenant string) map[string]any {
	h.t.Helper()
	status, out := h.download(id, session, tenant)
	if status != 200 {
		h.t.Fatalf("download status %d body %v", status, out)
	}
	return out
}

func (h *harness) downloadStatus(id, session, tenant string) int {
	h.t.Helper()
	status, _ := h.download(id, session, tenant)
	return status
}

func (h *harness) download(id, session, tenant string) (int, map[string]any) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.srv.URL+"/api/v1/crm/exports/"+id+"/download", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", h.internalToken)
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("X-Session-Token", session)
	req.Header.Set("X-Export-Confirm", "1")
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}
