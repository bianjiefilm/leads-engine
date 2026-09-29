package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/workbench"
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

func TestSamePrincipalIsolationExportAndSuspend(t *testing.T) {
	chain := workbench.ProjectJointChain()
	if chain.Status != "incomplete" || chain.Label != workbench.JointChainIncomplete || chain.TouchDelivered {
		t.Fatalf("joint chain = %+v", chain)
	}
	notice := workbench.OutreachNotice(true, true)
	for _, bad := range []string{"自动触达已成功", "销售已收到", "白标经营链完成", "白标经营链已完成"} {
		if strings.Contains(chain.Label, bad) || strings.Contains(notice, bad) {
			t.Fatalf("desk copy leaked %s in %q / %q", bad, chain.Label, notice)
		}
	}

	h := newHarnessOpts(t, harnessOpts{featureFollowups: true})
	tenantA, tenantB, _ := h.seed()
	if _, err := h.api.St.CreateMember(tenantB, principalOwnerA, "owner", "Owner A", "test", true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.St.CreateMember(tenantA, principalStranger, "owner", "Second Owner", "test", true); err != nil {
		t.Fatal(err)
	}
	const phone = "13600001111"
	const email = "same@shop.test"
	const token = "internal-token-should-not-export"
	h.mustDo("POST", "/api/v1/brands", sessionOwnerA, tenantA, `{"brand_id":"brand_white","display_name":"白标旧名"}`, 201)
	h.mustDo("POST", "/api/v1/brands", sessionOwnerA, tenantB, `{"brand_id":"brand_self","display_name":"自营"}`, 201)

	contactA := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, crmContact("甲同号", phone, email, "brand_white", "brand_white", "src_from_a", "camp_from_a"), 201)
	contactB := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantB, crmContact("乙同号", phone, email, "brand_self", "brand_self", "src_from_b", "camp_from_b"), 201)
	idA, idB := contactA["id"].(string), contactB["id"].(string)
	if idA == idB || contactA["tenant_id"] != tenantA || contactB["tenant_id"] != tenantB {
		t.Fatalf("same phone and email merged: a=%v b=%v", contactA, contactB)
	}
	mailA := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, crmContact("甲同邮", "13600002221", "same-mail@shop.test", "brand_white", "brand_white", "", ""), 201)
	mailB := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantB, crmContact("乙同邮", "13600002222", "same-mail@shop.test", "brand_self", "brand_self", "", ""), 201)
	if mailA["id"] == mailB["id"] || mailA["tenant_id"] != tenantA || mailB["tenant_id"] != tenantB {
		t.Fatalf("same email merged: a=%v b=%v", mailA, mailB)
	}
	if h.count(`SELECT COUNT(1) FROM contacts WHERE phone=?`, phone) != 2 || h.count(`SELECT COUNT(1) FROM contacts WHERE email=?`, email) != 2 || h.count(`SELECT COUNT(1) FROM contacts WHERE email=?`, "same-mail@shop.test") != 2 {
		t.Fatal("phone or email was collapsed")
	}
	if h.count(`SELECT COUNT(1) FROM members WHERE principal_ref=? OR principal_ref=? OR principal_ref=?`, phone, email, "same-mail@shop.test") != 0 {
		t.Fatal("phone or email was bound as a platform principal")
	}
	h.mustDo("POST", "/api/v1/contacts/"+idA+"/merge/"+idB, sessionOwnerA, tenantA, "", 404)
	h.mustDo("POST", "/api/v1/contacts/"+mailA["id"].(string)+"/merge/"+mailB["id"].(string), sessionOwnerA, tenantA, "", 404)
	if h.scalar(`SELECT tenant_id FROM contacts WHERE id=?`, idA) != tenantA || h.scalar(`SELECT tenant_id FROM contact_origins WHERE contact_id=?`, idA) != tenantA {
		t.Fatal("contact A origin moved off the member tenant")
	}
	if h.scalar(`SELECT tenant_id FROM contacts WHERE id=?`, idB) != tenantB || h.scalar(`SELECT campaign_id FROM contact_origins WHERE contact_id=?`, idB) != "camp_from_b" {
		t.Fatal("contact B did not keep its own campaign")
	}

	smuggle := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, crmContact("夹带", "13600003333", "alias@shop.test", "camp_alias", "camp_alias", "camp_alias", "camp_alias"), 201)
	if smuggle["tenant_id"] != tenantA || smuggle["origin_brand_id"] != "camp_alias" || smuggle["source_tag"] != "camp_alias" || smuggle["campaign_id"] != "camp_alias" {
		t.Fatalf("campaign alias became the tenant: %v", smuggle)
	}
	if h.scalar(`SELECT tenant_id FROM contacts WHERE id=?`, smuggle["id"]) != tenantA || h.scalar(`SELECT tenant_id FROM contact_origins WHERE contact_id=?`, smuggle["id"]) != tenantA {
		t.Fatal("alias contact was stored under the campaign")
	}
	copied := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, crmContact("标签照抄", "13600004444", "tagcopy@shop.test", "brand_white", "brand_white", tenantB, tenantB), 201)
	if copied["tenant_id"] != tenantA || copied["source_tag"] != tenantB || copied["campaign_id"] != tenantB {
		t.Fatalf("source tag rewrote the tenant: %v", copied)
	}
	if h.scalar(`SELECT tenant_id FROM contact_origins WHERE contact_id=?`, copied["id"]) != tenantA {
		t.Fatal("origin row adopted the other tenant id")
	}
	refused := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, crmContact("越租户", "13600005555", "move@shop.test", tenantB, "brand_white", "src_from_a", "camp_from_a"), 400)
	if refused["error"] != "cross_tenant" || h.count(`SELECT COUNT(1) FROM contacts WHERE phone=?`, "13600005555") != 0 {
		t.Fatalf("foreign tenant was stored: %v", refused)
	}

	if h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"tenant_id":"camp_from_a","brand_id":"brand_white","source_tag":"src_from_a","campaign_id":"camp_from_a"}`, idA), 400)["error"] != "cross_tenant" {
		t.Fatal("campaign id was accepted as the lead tenant")
	}
	leadA := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"tenant_id":"brand_white","brand_id":"brand_white","source_tag":"src_from_a","campaign_id":"camp_from_a"}`, idA), 201)
	leadAlias := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"tenant_id":"camp_alias","brand_id":"camp_alias","source_tag":"camp_alias","campaign_id":"camp_alias"}`, smuggle["id"]), 201)
	leadB := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantB, fmt.Sprintf(`{"contact_id":%q,"tenant_id":"brand_self","brand_id":"brand_self","source_tag":"src_from_b","campaign_id":"camp_from_b"}`, idB), 201)
	if leadA["tenant_id"] != tenantA || leadAlias["tenant_id"] != tenantA || leadB["tenant_id"] != tenantB {
		t.Fatalf("leads moved: a=%v alias=%v b=%v", leadA["tenant_id"], leadAlias["tenant_id"], leadB["tenant_id"])
	}
	oppA := h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"title":"商机只在甲","business_category":"merchant_customer","tenant_id":"brand_white","brand_id":"brand_white","source_tag":"src_from_a","campaign_id":"camp_from_a"}`, idA), 201)
	oppB := h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantB, fmt.Sprintf(`{"contact_id":%q,"title":"商机只在乙","business_category":"merchant_customer","tenant_id":"brand_self","brand_id":"brand_self","campaign_id":"camp_from_b"}`, idB), 201)
	followA := h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"跟进只在甲","tenant_id":"brand_white","brand_id":"brand_white","source_tag":"src_from_a","campaign_id":"camp_from_a"}`, idA, leadA["id"]), 201)
	followB := h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantB, fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"跟进只在乙","tenant_id":"brand_self","brand_id":"brand_self","campaign_id":"camp_from_b"}`, idB, leadB["id"]), 201)
	if followA["tenant_id"] != tenantA || followB["tenant_id"] != tenantB {
		t.Fatalf("follow-ups moved: %v %v", followA["tenant_id"], followB["tenant_id"])
	}
	consentA := h.mustDo("POST", "/api/v1/contacts/"+idA+"/consents", sessionOwnerA, tenantA, `{"source_submission_ref":"sub-a-only","source_channel":"landing","notice_version":"v1","purpose":"marketing","marketing_allowed":true}`, 200)
	h.mustDo("POST", "/api/v1/contacts/"+idB+"/consents", sessionOwnerA, tenantB, `{"source_submission_ref":"sub-b-only","source_channel":"landing","notice_version":"v1","purpose":"marketing","marketing_allowed":true}`, 200)

	own := fmt.Sprint(h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, tenantA, "", 200))
	other := fmt.Sprint(h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, tenantB, "", 200))
	if !strings.Contains(own, idA) || strings.Contains(own, idB) || strings.Contains(own, "乙同号") || !strings.Contains(other, idB) || strings.Contains(other, idA) || strings.Contains(other, "甲同号") {
		t.Fatal("same principal saw the other tenant's contacts")
	}
	if leads := fmt.Sprint(h.mustDo("GET", "/api/v1/leads", sessionOwnerA, tenantA, "", 200)); !strings.Contains(leads, leadA["id"].(string)) || strings.Contains(leads, leadB["id"].(string)) {
		t.Fatalf("lead list crossed tenants: %s", leads)
	}
	if opps := fmt.Sprint(h.mustDo("GET", "/api/v1/opportunities?category=merchant_customer", sessionOwnerA, tenantA, "", 200)); !strings.Contains(opps, oppA["id"].(string)) || strings.Contains(opps, "商机只在乙") {
		t.Fatalf("opportunity list crossed tenants: %s", opps)
	}
	if notes := fmt.Sprint(h.mustDo("GET", "/api/v1/contacts/"+idA+"/follow-ups", sessionOwnerA, tenantA, "", 200)); !strings.Contains(notes, "跟进只在甲") || strings.Contains(notes, "跟进只在乙") {
		t.Fatalf("follow-up page crossed tenants: %s", notes)
	}
	h.refusePlaintextStatus("GET", "/api/v1/contacts/"+idB, sessionOwnerA, tenantA, "", 404, "乙同号")
	h.refusePlaintextStatus("GET", "/api/v1/leads/"+leadB["id"].(string), sessionOwnerA, tenantA, "", 404, "跟进只在乙")
	h.refusePlaintextStatus("GET", "/api/v1/opportunities/"+oppB["id"].(string), sessionOwnerA, tenantA, "", 404, "商机只在乙")
	h.refusePlaintextStatus("GET", "/api/v1/follow-ups/"+followB["id"].(string), sessionOwnerA, tenantA, "", 404, "跟进只在乙")
	h.refusePlaintextStatus("GET", "/api/v1/contacts/"+idB+"/follow-ups", sessionOwnerA, tenantA, "", 404, "跟进只在乙")
	if h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q}`, idB), 400)["error"] != "bad_contact" {
		t.Fatal("cross-tenant lead was accepted")
	}
	if h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"title":"越租户商机","business_category":"merchant_customer"}`, idB), 400)["error"] != "bad_contact" {
		t.Fatal("cross-tenant opportunity was accepted")
	}
	if h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA, fmt.Sprintf(`{"contact_id":%q,"note":"越租户跟进"}`, idB), 400)["error"] != "bad_contact" {
		t.Fatal("cross-tenant follow-up was accepted")
	}

	h.mustDo("PATCH", "/api/v1/brands/brand_white", sessionOwnerA, tenantA, `{"display_name":"白标新名"}`, 200)
	renamed := h.mustDo("GET", "/api/v1/contacts/"+idA, sessionOwnerA, tenantA, "", 200)
	if renamed["tenant_id"] != tenantA || renamed["origin_brand_id"] != "brand_white" || renamed["source_tag"] != "src_from_a" || renamed["campaign_id"] != "camp_from_a" {
		t.Fatalf("rename moved source: %v", renamed)
	}
	if err := h.api.St.SetContactOrigin(idA, tenantB, "brand_white", "src_from_a", "camp_from_a"); err != nil {
		t.Fatal(err)
	}
	if h.scalar(`SELECT tenant_id FROM contact_origins WHERE contact_id=?`, idA) != tenantA {
		t.Fatal("origin update adopted the other tenant")
	}

	if _, err := h.api.St.RegisterChannelOperator(principalChannel, "ch_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.St.PutChannelProcurement(tenantA, 8800, 0, token); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/contacts",
		"/api/v1/contacts/" + idA,
		"/api/v1/leads",
		"/api/v1/leads/" + leadA["id"].(string),
		"/api/v1/opportunities?category=merchant_customer",
		"/api/v1/opportunities/" + oppA["id"].(string),
		"/api/v1/follow-ups/" + followA["id"].(string),
		"/api/v1/contacts/" + idA + "/follow-ups",
		"/api/v1/workbench",
		"/api/v1/contacts/export?format=csv",
		"/api/v1/admin/export",
	} {
		h.refusePlaintextStatus("GET", path, sessionChannel, tenantA, "", 403, phone)
	}
	h.refusePlaintextStatus("POST", "/api/v1/crm/exports", sessionChannel, tenantA, `{"purpose":"offboarding","brand_id":"brand_white"}`, 403, phone)
	h.refusePlaintextStatus("GET", "/api/v1/crm/exports/exp_missing/download", sessionChannel, tenantA, "", 403, phone)
	statusView := fmt.Sprint(h.mustDo("GET", "/api/v1/channel/tenant-status", sessionChannel, tenantA, "", 200))
	if strings.Contains(statusView, phone) || strings.Contains(statusView, token) || strings.Contains(statusView, "8800") {
		t.Fatalf("status leaked CRM or procurement: %s", statusView)
	}
	grant := h.mustDo("POST", "/api/v1/admin/crm-delegations", sessionOwnerA, tenantA, fmt.Sprintf(`{"principal_ref":%q,"kind":"support"}`, principalChannel), 201)
	seen := fmt.Sprint(h.mustDo("GET", "/api/v1/contacts", sessionChannel, tenantA, "", 200))
	if !strings.Contains(seen, phone) || !strings.Contains(seen, "甲同号") || strings.Contains(seen, idB) || strings.Contains(seen, "乙同号") {
		t.Fatalf("support delegation saw the wrong tenant: %s", seen)
	}
	h.refusePlaintextStatus("GET", "/api/v1/contacts/"+idB, sessionChannel, tenantB, "", 403, "乙同号")
	h.mustDo("POST", "/api/v1/admin/crm-delegations/"+grant["id"].(string)+"/revoke", sessionOwnerA, tenantA, "", 200)
	h.refusePlaintextStatus("GET", "/api/v1/contacts", sessionChannel, tenantA, "", 403, phone)
	h.mustDo("POST", "/api/v1/admin/crm-delegations", sessionOwnerA, tenantA, fmt.Sprintf(`{"principal_ref":%q,"kind":"offboarding"}`, principalChannel), 201)
	h.refusePlaintextStatus("GET", "/api/v1/contacts", sessionChannel, tenantA, "", 403, phone)

	h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenantA, `{"plan":"crm_pro","status":"active","wallet_balance_cents":100000,"org_billing_account_id":"ba_org","crm_subscription_cents":1200,"merchant_deal_cents":1}`, 200)
	stopped := h.mustDo("POST", "/api/v1/ai-usage/quotes", sessionOwnerA, tenantA, `{"action":"ai_score","payer_kind":"organization","payer_account_id":"ba_org","return_to":"/leads"}`, 409)
	if stopped["error"] != "paid_ai_stopped" || stopped["history_retained"] != true || stopped["data_lost"] == true {
		t.Fatalf("quota gate = %v", stopped)
	}
	h.mustDo("GET", "/api/v1/contacts/"+idA, sessionOwnerA, tenantA, "", 200)
	h.mustDo("GET", "/api/v1/follow-ups/"+followA["id"].(string), sessionOwnerA, tenantA, "", 200)

	h.mustDo("POST", "/api/v1/brands/brand_white/lifecycle", sessionOwnerA, tenantA, `{"status":"suspended"}`, 200)
	h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, `{"name":"品牌暂停仍可建档","phone":"13700006666","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, 201)
	h.mustDo("POST", "/api/v1/brands/brand_white/lifecycle", sessionOwnerA, tenantA, `{"status":"active"}`, 200)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"suspended"}`, 200)
	blocked := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, `{"name":"暂停中","business_category":"merchant_customer","source_type":"manual"}`, 409)
	if blocked["error"] != "tenant_suspended" || blocked["history_retained"] != true {
		t.Fatalf("tenant pause = %v", blocked)
	}
	h.mustDo("GET", "/api/v1/contacts/"+idA, sessionOwnerA, tenantA, "", 200)
	h.mustDo("GET", "/api/v1/contacts/"+idB, sessionOwnerA, tenantB, "", 200)
	h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantB, `{"name":"乙在暂停期","phone":"13600007777","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, 201)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"active"}`, 200)

	salesID := h.memberID(tenantA, principalSalesA1)
	h.mustDo("PATCH", "/api/v1/admin/members/"+salesID, sessionOwnerA, tenantA, `{"enabled":false}`, 200)
	revokedConsent := consentA["consent"].(map[string]any)
	h.mustDo("POST", "/api/v1/contacts/"+idA+"/consents/"+revokedConsent["id"].(string)+"/revoke", sessionOwnerA, tenantA, `{"reason":"客户撤回"}`, 200)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"suspended"}`, 200)
	h.mustDo("POST", "/api/v1/admin/tenant-lifecycle", sessionOwnerA, tenantA, `{"status":"active"}`, 200)
	if status, body, _ := h.do("GET", "/api/v1/contacts", sessionSalesA1, tenantA, ""); status != 403 {
		t.Fatalf("resume restored sales: %d %v", status, body)
	}
	replay := h.mustDo("POST", "/api/v1/contacts/"+idA+"/consents", sessionOwnerA, tenantA, `{"source_submission_ref":"sub-a-only","source_channel":"landing","notice_version":"v1","purpose":"marketing","marketing_allowed":true}`, 200)
	if replay["state"] != "replay_unchanged" || replay["status"] != "revoked" {
		t.Fatalf("resume restored consent: %v", replay)
	}
	h.refusePlaintextStatus("GET", "/api/v1/contacts", sessionChannel, tenantA, "", 403, phone)

	jobA := h.mustDo("POST", "/api/v1/crm/exports", sessionOwnerA, tenantA, `{"purpose":"offboarding","brand_id":"brand_white"}`, 201)
	jobB := h.mustDo("POST", "/api/v1/crm/exports", sessionOwnerA, tenantB, `{"purpose":"offboarding","brand_id":"brand_self"}`, 201)
	if noConfirm, _, _ := h.do("GET", "/api/v1/crm/exports/"+jobA["id"].(string)+"/download", sessionOwnerA, tenantA, ""); noConfirm != 401 {
		t.Fatalf("download without reauth = %d", noConfirm)
	}
	status, header, raw := h.downloadRaw(jobA["id"].(string), sessionOwnerA, tenantA)
	if status != 200 || !strings.Contains(header.Get("Content-Type"), "application/json") || strings.Contains(strings.ToLower(header.Get("Content-Disposition")), "sqlite") || strings.Contains(header.Get("Content-Disposition"), ".db") || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) || bytes.Contains(raw, []byte("SQLite format 3")) {
		t.Fatalf("export was not a JSON body: status=%d type=%q disp=%q prefix=%q", status, header.Get("Content-Type"), header.Get("Content-Disposition"), raw[:min(40, len(raw))])
	}
	var exported map[string]any
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	assertExportTenant(t, exported, tenantA)
	assertExportText(t, exported, []string{"camp_from_a", "src_from_a", "sub-a-only", "客户撤回", "跟进只在甲", "商机只在甲"}, []string{idB, "camp_from_b", "src_from_b", "sub-b-only", "跟进只在乙", "商机只在乙", "13600002222", "13600007777", token, "8800", "purchase_price"})
	var copiedRow bool
	for _, item := range exported["contacts"].([]any) {
		row := item.(map[string]any)
		if row["id"] == copied["id"] {
			copiedRow = row["tenant_id"] == tenantA && row["source_tag"] == tenantB && row["campaign_id"] == tenantB
		}
	}
	if !copiedRow {
		t.Fatal("export dropped the source tag or moved its tenant")
	}
	if h.downloadStatus(jobA["id"].(string), sessionStranger, tenantA) != 404 {
		t.Fatal("another owner downloaded tenant A's job")
	}
	if h.downloadStatus(jobA["id"].(string), sessionOwnerA, tenantB) != 404 {
		t.Fatal("the same principal downloaded tenant A's job with tenant B selected")
	}
	exportedB := h.downloadExport(jobB["id"].(string), sessionOwnerA, tenantB)
	assertExportTenant(t, exportedB, tenantB)
	assertExportText(t, exportedB, []string{"camp_from_b", "sub-b-only", "跟进只在乙", "商机只在乙"}, []string{idA, tenantA, "camp_from_a", "sub-a-only", "跟进只在甲", "客户撤回", "13600002221", token, "8800", "purchase_price"})
	if h.downloadStatus(jobB["id"].(string), sessionOwnerA, tenantA) != 404 {
		t.Fatal("tenant A's header downloaded tenant B's job")
	}

	desk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", 200)
	joint, _ := desk["joint_chain"].(map[string]any)
	if joint["status"] != "incomplete" || joint["label"] != "联合经营链未完成" || joint["touch_delivered"] != false {
		t.Fatalf("workbench chain = %v", desk["joint_chain"])
	}
	blob := fmt.Sprint(desk)
	for _, bad := range []string{"自动触达已成功", "销售已收到", "白标经营链完成", "白标经营链已完成"} {
		if strings.Contains(blob, bad) {
			t.Fatalf("workbench emitted %s", bad)
		}
	}
}

func crmContact(name, phone, email, tenantField, brand, tag, campaign string) string {
	return fmt.Sprintf(`{"name":%q,"phone":%q,"email":%q,"business_category":"merchant_customer","source_type":"manual","consent_status":"granted","tenant_id":%q,"brand_id":%q,"source_tag":%q,"campaign_id":%q}`,
		name, phone, email, tenantField, brand, tag, campaign)
}

func assertExportTenant(t *testing.T, bundle map[string]any, tenant string) {
	t.Helper()
	if bundle["tenant_id"] != tenant {
		t.Fatalf("bundle tenant = %v", bundle["tenant_id"])
	}
	for _, key := range []string{"contacts", "leads", "opportunities", "follow_ups", "assignments", "sources", "consents"} {
		rows, ok := bundle[key].([]any)
		if !ok {
			t.Fatalf("%s = %T", key, bundle[key])
		}
		for i, item := range rows {
			row, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("%s[%d] = %T", key, i, item)
			}
			if row["tenant_id"] != tenant {
				t.Fatalf("%s[%d] tenant = %v", key, i, row["tenant_id"])
			}
		}
	}
}

func assertExportText(t *testing.T, bundle map[string]any, present, absent []string) {
	t.Helper()
	dump := fmt.Sprint(bundle)
	for _, want := range present {
		if !strings.Contains(dump, want) {
			t.Fatalf("export missing %s", want)
		}
	}
	for _, bad := range absent {
		if bad != "" && strings.Contains(dump, bad) {
			t.Fatalf("export leaked %s", bad)
		}
	}
}

func (h *harness) refusePlaintextStatus(method, path, session, tenant, body string, want int, secret string) {
	h.t.Helper()
	status, raw, _ := h.doRaw(method, path, session, tenant, body)
	if status != want || (secret != "" && strings.Contains(string(raw), secret)) {
		h.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, status, want, raw)
	}
}

func (h *harness) scalar(query string, args ...any) string {
	h.t.Helper()
	var v string
	if err := h.api.St.DB.QueryRow(query, args...).Scan(&v); err != nil {
		h.t.Fatalf("query %s: %v", query, err)
	}
	return v
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

func (h *harness) downloadRaw(id, session, tenant string) (int, http.Header, []byte) {
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
	return res.StatusCode, res.Header, raw
}
