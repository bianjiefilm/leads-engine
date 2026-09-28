package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLightCopyFeatureOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	status, _, _ := h.do("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "")
	if status != 404 {
		t.Fatalf("capability = %d", status)
	}
	status, _, _ = h.do("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"草稿"}`)
	if status != 404 {
		t.Fatalf("save = %d", status)
	}
}

func TestLightCopyManualDraftDoesNotSendOrInventText(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true, captureLog: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	secret := "手改回复不应进日志"

	cap := h.mustDo("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "", 200)
	if cap["model_ready"] == true || cap["live_charge"].(float64) != 0 {
		t.Fatalf("capability: %v", cap)
	}

	status, gen, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"]}`)
	if status != 409 || gen["error"] != "model_unavailable" {
		t.Fatalf("generate = %d %v", status, gen)
	}
	if body, _ := gen["body"].(string); strings.Contains(body, "您好") || strings.Contains(body, "{{") {
		t.Fatalf("generate returned placeholder copy: %v", gen)
	}

	saved := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/email", sessionOwnerA, tenant,
		`{"body":"  `+secret+`  "}`, 200)
	if saved["body"] != secret || saved["sent"] == true || saved["published"] == true || saved["marketing_permitted"] == true || saved["generated"] == true {
		t.Fatalf("saved: %v", saved)
	}
	edited := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/email", sessionOwnerA, tenant,
		`{"body":"手改后的邮件"}`, 200)
	if edited["body"] != "手改后的邮件" || edited["user_confirmed"] == true || edited["content_version"].(float64) != 2 {
		t.Fatalf("edited: %v", edited)
	}
	confirmed := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/email/confirm", sessionOwnerA, tenant, `{}`, 200)
	if confirmed["user_confirmed"] != true || confirmed["sent"] == true || confirmed["published"] == true || confirmed["marketing_permitted"] == true {
		t.Fatalf("confirm: %v", confirmed)
	}
	status, _, _ = h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/email/send", sessionOwnerA, tenant, `{}`)
	if status != 404 {
		t.Fatalf("send route exists: %d", status)
	}

	var bad int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_drafts WHERE sent!=0 OR published!=0 OR marketing_permitted!=0 OR generated!=0 OR live_charge!=0`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("stored a send, publish, consent, generation, or charge")
	}
	if strings.Contains(h.logs.String(), secret) {
		t.Fatalf("log leaked the draft")
	}

	status, _, _ = h.do("GET", "/api/v1/opportunities/"+opp+"/light-copy", sessionOwnerB, tenant, "")
	if status != 403 {
		t.Fatalf("cross tenant = %d", status)
	}
}

func TestLightCopyFactsAndHandoffStayInsideTheScene(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, _ := h.seed()
	intake := h.intake(sessionOwnerA, tenant, `{"source_app":"touch","source_ns":"landing","event_id":"evt-copy-1","contact":{"name":"不应进工具的联系人全文","phone":"13900001111","email":"secret@shop.cn"},"business_category":"merchant_customer","source_type":"touch_campaign","source":{"source_app":"touch","source_ref":"camp-9","auth_scope_snapshot":"profile-secret-9f3a"},"consent":{"source_submission_ref":"sub-copy","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}}`, 201)
	leadID, _ := intake["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("intake: %v", intake)
	}

	status, facts, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/light-copy/marketing_brief/facts", sessionOwnerA, tenant, `{"selected":["activity_ref","contact_text"]}`)
	if status != 400 || facts["error"] != "unauthorized_field" {
		t.Fatalf("unauthorized = %d %v", status, facts)
	}
	raw := jsonText(facts)
	if strings.Contains(raw, "不应进工具") || strings.Contains(raw, "13900001111") || strings.Contains(raw, "profile-secret-9f3a") || strings.Contains(raw, "secret@shop.cn") {
		t.Fatalf("refusal leaked contact or profile: %s", raw)
	}

	okFacts := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/light-copy/marketing_brief/facts", sessionOwnerA, tenant, `{"selected":["activity_ref"]}`, 200)
	packet, _ := okFacts["facts"].(map[string]any)
	if packet["activity_ref"] != "camp-9" || len(packet) != 1 {
		t.Fatalf("facts = %v", okFacts)
	}
	if strings.Contains(jsonText(okFacts), "不应进工具") || strings.Contains(jsonText(okFacts), "13900001111") || strings.Contains(jsonText(okFacts), "profile-secret-9f3a") || strings.Contains(jsonText(okFacts), "secret@shop.cn") {
		t.Fatalf("packet leaked contact: %v", okFacts)
	}

	opp := h.createOpp(t, sessionOwnerA, tenant, h.mustContact(t, tenant), "门店开业", "merchant_customer", "")
	status, price, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/facts", sessionOwnerA, tenant, `{"selected":["price_cents"]}`)
	if status != 400 || price["error"] != "price_expired" {
		t.Fatalf("price = %d %v", status, price)
	}

	h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"可保存的回复"}`, 200)
	declined := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"product_image","idempotency_key":"decline-1","decline":true}`, 200)
	if declined["status"] != "declined" || declined["project_created"] == true {
		t.Fatalf("decline: %v", declined)
	}
	still := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"拒绝后继续手改"}`, 200)
	if still["body"] != "拒绝后继续手改" || still["sent"] == true {
		t.Fatalf("edit after decline: %v", still)
	}

	first := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"goboost","idempotency_key":"open-1","selected":["title"]}`, 200)
	if first["status"] != "failed" || first["reason"] != "tool_entry_unavailable" || first["project_created"] == true || first["regenerated"] == true {
		t.Fatalf("handoff: %v", first)
	}
	if first["source_id"] != opp || first["source_kind"] != "opportunity" {
		t.Fatalf("source dropped: %v", first)
	}
	second := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"goboost","idempotency_key":"open-1","selected":["title"]}`, 200)
	if second["id"] != first["id"] || second["regenerated"] == true {
		t.Fatalf("replay: %v", second)
	}
	var projects int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_handoffs WHERE tenant_id=? AND idempotency_key='open-1'`, tenant).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 1 {
		t.Fatalf("handoff rows = %d", projects)
	}
	var generated int
	if err := h.api.St.DB.QueryRow(`SELECT generated FROM light_copy_drafts WHERE tenant_id=? AND subject_id=? AND kind='reply'`, tenant, opp).Scan(&generated); err != nil {
		t.Fatal(err)
	}
	if generated != 0 {
		t.Fatalf("failed handoff regenerated the draft")
	}

	status, unknown, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"video-engine","idempotency_key":"video-1","selected":["title"]}`)
	if status != 400 || unknown["error"] != "unknown_tool" {
		t.Fatalf("unknown tool = %d %v", status, unknown)
	}
}

func (h *harness) mustContact(t *testing.T, tenant string) string {
	t.Helper()
	out := h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, tenant, "", 200)
	items, _ := out["items"].([]any)
	if len(items) == 0 {
		t.Fatal("no contact")
	}
	id, _ := items[0].(map[string]any)["id"].(string)
	return id
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
