package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTodayTouchLeadStaysOnTheDesk(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()
	waitingID := h.todayTouchLead(tenantA, "碰一碰客户", "13700001111")
	nextID := h.todayTouchLead(tenantA, "碰一碰回头客", "13700002222")
	other := h.todayTouchLead(tenantB, "乙租户客户", "13700003333")

	desk := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", 200)
	raw := mustJSON(desk)
	if strings.Contains(raw, "13700001111") || strings.Contains(raw, "自动触达已成功") {
		t.Fatalf("desk leaked a phone or a fake send: %s", raw)
	}
	if desk["automation"].(map[string]any)["auto_call"] == true || desk["automation"].(map[string]any)["auto_message"] == true {
		t.Fatalf("automation = %v", desk["automation"])
	}
	if !todayHas(desk, "new_inquiry", waitingID) || !todayHas(desk, "new_inquiry", nextID) || todayHas(desk, "new_inquiry", other) {
		t.Fatalf("new inquiries = %s", raw)
	}
	item := todayFind(desk, "new_inquiry", waitingID)
	source, _ := item["source"].(map[string]any)
	if source["channel"] != "碰一碰" || source["activity"] != "camp-touch" {
		t.Fatalf("touch source = %v", source)
	}
	ctx, _ := item["context"].(map[string]any)
	if ctx["customer"] != "碰一碰客户" {
		t.Fatalf("context = %v", ctx)
	}
	asks, _ := ctx["ask"].([]any)
	for _, ask := range asks {
		if ask == "客户姓名" {
			t.Fatal("confirmed name was asked again")
		}
	}

	waiting := h.mustDo("POST", "/api/v1/leads/"+waitingID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"已回复，等客户","complete":true,"disposition":"waiting_customer","ai_score":99}`, 201)
	next, _ := waiting["next"].(map[string]any)
	if next["source"] != "manual" || next["auto_call"] == true || next["auto_message"] == true || waiting["today_group"] != "waiting_customer" {
		t.Fatalf("waiting follow-through = %v", waiting)
	}
	afterWait := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", 200)
	if todayHas(afterWait, "new_inquiry", waitingID) || !todayHas(afterWait, "waiting_customer", waitingID) {
		t.Fatalf("waiting placement = %s", mustJSON(afterWait["today"]))
	}

	todayAt := shanghaiWall(15, 0)
	scheduled := h.mustDo("POST", "/api/v1/leads/"+nextID+"/follow-through", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"note":"约今天再联系","complete":true,"next_follow_up_at":%q,"ai_score":99}`, todayAt), 201)
	if scheduled["today_group"] != "due_today" {
		t.Fatalf("same-day next = %v", scheduled["today_group"])
	}
	schedNext, _ := scheduled["next"].(map[string]any)
	if schedNext["source"] != "manual" || schedNext["auto_call"] == true {
		t.Fatalf("scheduled next = %v", schedNext)
	}
	afterNext := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", 200)
	if todayHas(afterNext, "new_inquiry", nextID) || !todayHas(afterNext, "due_today", nextID) {
		t.Fatalf("due placement = %s", mustJSON(afterNext["today"]))
	}

	laterID := h.todayTouchLead(tenantA, "碰一碰以后", "13700004444")
	laterAt := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02T15:04:05Z")
	later := h.mustDo("POST", "/api/v1/leads/"+laterID+"/follow-through", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"note":"约后天","complete":true,"next_follow_up_at":%q}`, laterAt), 201)
	if later["today_group"] != "scheduled_next" {
		t.Fatalf("future next = %v", later["today_group"])
	}
	afterLater := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", 200)
	if todayHas(afterLater, "new_inquiry", laterID) || todayHas(afterLater, "due_today", laterID) || todayHas(afterLater, "waiting_customer", laterID) {
		t.Fatalf("future lead stayed in today's work: %s", mustJSON(afterLater["today"]))
	}

	foreign := h.mustDo("GET", "/api/v1/workbench", sessionOwnerB, tenantB, "", 200)
	foreignRaw := mustJSON(foreign)
	if strings.Contains(foreignRaw, waitingID) || strings.Contains(foreignRaw, nextID) || !todayHas(foreign, "new_inquiry", other) {
		t.Fatalf("tenant B residual = %s", foreignRaw)
	}
	status, _, _ := h.do("GET", "/api/v1/workbench", sessionSalesA1, tenantB, "")
	if status != 403 {
		t.Fatalf("cross-tenant workbench = %d", status)
	}
}

func TestTodayDraftCanBeEditedOrIgnoredWithoutSending(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, tenantB, _ := h.seed()
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"assist"}`, 201)
	h.mustDo("POST", "/api/v1/reception/knowledge", sessionOwnerA, tenantA, `{"question":"营业时间","answer":"每天 9:00 到 18:00"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-today01"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)
	msg := h.mustDo("POST", "/api/v1/public/reception/sessions/"+sid+"/messages", "", "",
		`{"visitor_key":"visitor-key-today01","client_msg_id":"m-today","text":"营业时间"}`, 200)
	if msg["reply"] != nil {
		t.Fatalf("draft was sent to the visitor: %v", msg["reply"])
	}
	staff := h.mustDo("GET", "/api/v1/reception/sessions/"+sid, sessionOwnerA, tenantA, "", 200)
	draft := staff["replies"].([]any)[0].(map[string]any)
	draftID := draft["id"].(string)
	desk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", 200)
	if !todayHas(desk, "ai_draft", draftID) {
		t.Fatalf("draft missing from today: %s", mustJSON(desk["today"]))
	}
	revised := h.mustDo("POST", "/api/v1/workbench/drafts/"+draftID+"/revise", sessionOwnerA, tenantA, `{"body":"改成人工确认的营业时间"}`, 200)
	if revised["sent"] == true || revised["status"] != "generated" || revised["body"] != "改成人工确认的营业时间" {
		t.Fatalf("revise = %v", revised)
	}
	ignored := h.mustDo("POST", "/api/v1/workbench/drafts/"+draftID+"/ignore", sessionOwnerA, tenantA, `{}`, 200)
	if ignored["sent"] == true || ignored["status"] != "superseded" {
		t.Fatalf("ignore = %v", ignored)
	}
	after := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", 200)
	if todayHas(after, "ai_draft", draftID) || strings.Contains(mustJSON(after), "自动触达已成功") {
		t.Fatalf("ignored draft still queued: %s", mustJSON(after["today"]))
	}
	publicRaw := h.mustDo("GET", "/api/v1/public/reception/sessions/"+sid+"?visitor_key=visitor-key-today01", "", "", "", 200)
	if strings.Contains(mustJSON(publicRaw), "改成人工确认的营业时间") || strings.Contains(mustJSON(publicRaw), "9:00") {
		t.Fatalf("ignored draft reached the visitor: %v", publicRaw)
	}
	other := h.mustDo("GET", "/api/v1/workbench", sessionOwnerB, tenantB, "", 200)
	if strings.Contains(mustJSON(other), draftID) {
		t.Fatal("draft crossed tenants")
	}
}

func TestTodaySessionReplyDraftIsSavedAndNotSent(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, tenantB, _ := h.seed()
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"assist"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-draft01"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)
	const body = "先记一版回复，不要发给访客"
	saved := h.mustDo("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-draft-1","body":"`+body+`"}`, 201)
	if saved["sent"] == true || saved["status"] != "generated" || saved["body"] != body || saved["duplicate"] == true {
		t.Fatalf("save = %v", saved)
	}
	draftID, _ := saved["id"].(string)
	cont, _ := saved["continue"].(map[string]any)
	if draftID == "" || cont["focus_id"] != draftID || cont["session_id"] != sid || cont["today_group"] != "ai_draft" {
		t.Fatalf("continue = %v id=%s", cont, draftID)
	}
	again := h.mustDo("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-draft-1","body":"`+body+`"}`, 200)
	if again["duplicate"] != true || again["id"] != draftID || again["sent"] == true {
		t.Fatalf("duplicate = %v", again)
	}
	if status, out, _ := h.do("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-draft-1","body":"另一版"}`); status != 409 || out["error"] != "reply_conflict" {
		t.Fatalf("conflict = %d %v", status, out)
	}
	desk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", 200)
	if !todayHas(desk, "ai_draft", draftID) {
		t.Fatalf("saved draft missing from today: %s", mustJSON(desk["today"]))
	}
	publicRaw := h.mustDo("GET", "/api/v1/public/reception/sessions/"+sid+"?visitor_key=visitor-key-draft01", "", "", "", 200)
	if strings.Contains(mustJSON(publicRaw), body) {
		t.Fatalf("draft reached the visitor: %v", publicRaw)
	}
	if status, _, _ := h.do("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerB, tenantB,
		`{"client_reply_id":"desk-draft-x","body":"越租户"}`); status != 404 {
		t.Fatalf("cross-tenant save = %d", status)
	}
	if status, out, _ := h.do("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-empty","body":"  "}`); status != 400 {
		t.Fatalf("empty = %d %v", status, out)
	}
}

func TestSalesReplyDraftFollowsSessionOwner(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, _, _ := h.seed()
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"assist"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-owner01"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)
	const body = "销售先记一版，不要发送"
	if status, _, _ := h.do("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionSalesA1, tenantA,
		`{"client_reply_id":"desk-unassigned","body":"`+body+`"}`); status != 404 {
		t.Fatalf("unassigned sales save = %d", status)
	}
	h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/takeover", sessionSalesA1, tenantA, `{}`, 200)
	saved := h.mustDo("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionSalesA1, tenantA,
		`{"client_reply_id":"desk-own-1","body":"`+body+`"}`, 201)
	if saved["status"] != "generated" || saved["sent"] == true || saved["body"] != body {
		t.Fatalf("save = %v", saved)
	}
	staff := h.mustDo("GET", "/api/v1/reception/sessions/"+sid, sessionSalesA1, tenantA, "", 200)
	replies, _ := staff["replies"].([]any)
	if len(replies) != 1 {
		t.Fatalf("replies = %v", staff["replies"])
	}
	reply, _ := replies[0].(map[string]any)
	if reply["kind"] != "draft" || reply["status"] != "generated" || reply["sent_at"] != "" {
		t.Fatalf("stored reply = %v", reply)
	}
	publicRaw := h.mustDo("GET", "/api/v1/public/reception/sessions/"+sid+"?visitor_key=visitor-key-owner01", "", "", "", 200)
	if strings.Contains(mustJSON(publicRaw), body) {
		t.Fatalf("draft reached the visitor: %v", publicRaw)
	}
	if status, _, _ := h.do("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionSalesA2, tenantA,
		`{"client_reply_id":"desk-other","body":"别人的草稿"}`); status != 404 {
		t.Fatalf("other sales save = %d", status)
	}
}

func TestTodaySiblingFollowUpDoesNotStealNewInquiry(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureFollowups: true})
	tenantA, _, _ := h.seed()
	contactID, oldID := h.todayTouchContactLead(tenantA, "回头客", "13700005555")
	freshID := h.todayAnotherTouchLead(tenantA, contactID, "碰一碰新咨询")
	h.mustDo("POST", "/api/v1/leads/"+oldID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"等旧线索的客户","complete":true,"disposition":"waiting_customer"}`, 201)
	past := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"note":"接触级旧跟进","next_follow_up_at":%q}`, contactID, past), 201)

	desk := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", 200)
	if !todayHas(desk, "new_inquiry", freshID) {
		t.Fatalf("new touch lead left new_inquiry: %s", mustJSON(desk["today"]))
	}
	if !todayHas(desk, "waiting_customer", oldID) {
		t.Fatalf("old lead left waiting_customer: %s", mustJSON(desk["today"]))
	}
	if todayHas(desk, "waiting_customer", freshID) || todayHas(desk, "due_today", freshID) || todayHas(desk, "overdue", freshID) {
		t.Fatalf("sibling or contact follow-up moved the new lead: %s", mustJSON(desk["today"]))
	}
}

func (h *harness) todayTouchLead(tenant, name, phone string) string {
	h.t.Helper()
	session := sessionOwnerA
	if tenant != "" {
		// owner B creates tenant B rows; owner A creates tenant A rows.
	}
	if name == "乙租户客户" {
		session = sessionOwnerB
	}
	ref := "touch-" + name
	source := fmt.Sprintf(`{"source_app":"touch","source_ref":%q,"auth_scope_snapshot":"{\"campaign_ref\":\"camp-touch\"}"}`, ref)
	contact := h.mustDo("POST", "/api/v1/contacts", session, tenant,
		fmt.Sprintf(`{"name":%q,"phone":%q,"business_category":"merchant_customer","source_type":"touch_campaign","consent_status":"granted","source":%s}`, name, phone, source), 201)
	contactID, _ := contact["id"].(string)
	leadSession := sessionSalesA1
	if session == sessionOwnerB {
		leadSession = sessionOwnerB
	}
	lead := h.mustDo("POST", "/api/v1/leads", leadSession, tenant,
		fmt.Sprintf(`{"contact_id":%q,"status":"new","source":%s}`, contactID, source), 201)
	id, _ := lead["id"].(string)
	if id == "" {
		h.t.Fatalf("touch lead missing id: %v", lead)
	}
	return id
}

func (h *harness) todayTouchContactLead(tenant, name, phone string) (string, string) {
	h.t.Helper()
	ref := "touch-" + name
	source := fmt.Sprintf(`{"source_app":"touch","source_ref":%q,"auth_scope_snapshot":"{\"campaign_ref\":\"camp-touch\"}"}`, ref)
	contact := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenant,
		fmt.Sprintf(`{"name":%q,"phone":%q,"business_category":"merchant_customer","source_type":"touch_campaign","consent_status":"granted","source":%s}`, name, phone, source), 201)
	contactID, _ := contact["id"].(string)
	lead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenant,
		fmt.Sprintf(`{"contact_id":%q,"status":"new","source":%s}`, contactID, source), 201)
	leadID, _ := lead["id"].(string)
	if contactID == "" || leadID == "" {
		h.t.Fatalf("touch pair = %v %v", contact, lead)
	}
	return contactID, leadID
}

func (h *harness) todayAnotherTouchLead(tenant, contactID, name string) string {
	h.t.Helper()
	ref := "touch-" + name
	source := fmt.Sprintf(`{"source_app":"touch","source_ref":%q,"auth_scope_snapshot":"{\"campaign_ref\":\"camp-touch\"}"}`, ref)
	lead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenant,
		fmt.Sprintf(`{"contact_id":%q,"status":"new","source":%s}`, contactID, source), 201)
	id, _ := lead["id"].(string)
	if id == "" {
		h.t.Fatalf("second touch lead = %v", lead)
	}
	return id
}

func shanghaiWall(hour, minute int) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	now := time.Now().In(loc)
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	return at.UTC().Format(time.RFC3339)
}

func todayHas(desk map[string]any, group, id string) bool {
	return todayFind(desk, group, id) != nil
}

func todayFind(desk map[string]any, group, id string) map[string]any {
	today, _ := desk["today"].(map[string]any)
	rows, _ := today[group].([]any)
	for _, row := range rows {
		item, _ := row.(map[string]any)
		if item["id"] == id || item["lead_id"] == id || item["session_id"] == id {
			return item
		}
	}
	return nil
}
