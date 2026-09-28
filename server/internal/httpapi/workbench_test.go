package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWorkbenchManualDesk(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, contactA1 := h.seed()

	lead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, contactA1), http.StatusCreated)
	leadID, _ := lead["id"].(string)
	opp := h.mustDo("POST", "/api/v1/opportunities", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"title":"门店复购","business_category":"merchant_customer","stage":"open"}`, contactA1), http.StatusCreated)
	oppID, _ := opp["id"].(string)
	creative := h.mustDo("POST", "/api/v1/opportunities", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"title":"品牌片","business_category":"creative_service","stage":"proposal"}`, contactA1), http.StatusCreated)
	creativeID, _ := creative["id"].(string)

	desk := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", http.StatusOK)
	if desk["scope"] != "own" {
		t.Fatalf("scope = %v", desk["scope"])
	}
	assertChargeAndAutomation(t, desk)
	if !bucketHas(desk, "unprocessed", leadID) {
		t.Fatalf("sales missed own new lead: %v", desk["buckets"])
	}
	if !bucketHas(desk, "needs_schedule", oppID) || bucketFlag(desk, "needs_schedule", oppID, "show_service_draft") {
		t.Fatalf("open store opportunity = %v", desk["buckets"])
	}
	if !bucketHas(desk, "needs_schedule", creativeID) || !bucketFlag(desk, "needs_schedule", creativeID, "show_service_draft") {
		t.Fatalf("creative opportunity hidden the service draft: %v", desk["buckets"])
	}
	raw, _ := json.Marshal(desk)
	if strings.Contains(string(raw), "13812345678") {
		t.Fatal("workbench list exposed a phone number")
	}

	other := h.mustDo("GET", "/api/v1/workbench", sessionSalesA2, tenantA, "", http.StatusOK)
	if bucketHas(other, "unprocessed", leadID) || bucketHas(other, "needs_schedule", oppID) {
		t.Fatal("sales saw another seller's work")
	}
	owner := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", http.StatusOK)
	if owner["scope"] != "tenant" || !bucketHas(owner, "unprocessed", leadID) {
		t.Fatalf("owner tenant view = %v", owner["scope"])
	}

	scheduled := h.mustDo("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"已联系","next_follow_up_at":"2026-09-29T02:00:00Z","ai_score":99}`, http.StatusCreated)
	assertChargeAndAutomation(t, scheduled)
	next, _ := scheduled["next"].(map[string]any)
	if next["source"] != "manual" || next["kind"] != "manual_follow_up" || next["auto_call"] == true {
		t.Fatalf("manual next = %v", next)
	}
	afterSchedule := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", http.StatusOK)
	if bucketHas(afterSchedule, "needs_schedule", oppID) {
		t.Fatal("scheduled opportunity disappeared into nowhere — it left needs_schedule while a next exists, then must not be reported missing from the manual next")
	}
	if !bucketHas(afterSchedule, "unprocessed", leadID) && !workbenchHasLead(afterSchedule, leadID) {
		t.Fatal("lead vanished after scheduling")
	}

	h.mustDo("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"这次做完，先不约下次","complete":true}`, http.StatusCreated)
	afterComplete := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", http.StatusOK)
	if !bucketHas(afterComplete, "needs_schedule", oppID) {
		t.Fatal("active opportunity with no next action disappeared")
	}

	timeline := h.mustDo("GET", "/api/v1/leads/"+leadID+"/timeline", sessionSalesA1, tenantA, "", http.StatusOK)
	assertChargeAndAutomation(t, timeline)
	kinds := eventKinds(timeline)
	for _, kind := range []string{"source", "assignment", "follow_up"} {
		if !kinds[kind] {
			t.Fatalf("timeline missing %s: %v", kind, timeline["events"])
		}
	}
	source, _ := timeline["source"].(map[string]any)
	if source["channel"] != "手工录入" || source["at"] == "" {
		t.Fatalf("source line = %v", source)
	}
	statuses, _ := timeline["statuses"].(map[string]any)
	if statuses["received"] != true || statuses["assigned"] != true || statuses["followed"] != true ||
		statuses["submitted"] == true || statuses["won"] == true || statuses["paid"] == true {
		t.Fatalf("statuses = %v", statuses)
	}
	tlRaw, _ := json.Marshal(timeline)
	if !strings.Contains(string(tlRaw), "13812345678") {
		t.Fatal("authorized timeline hid the business-domain phone")
	}
	h.mustDo("GET", "/api/v1/leads/"+leadID+"/timeline", sessionSalesA2, tenantA, "", http.StatusNotFound)
	foreign := h.mustDo("GET", "/api/v1/workbench", sessionOwnerB, tenantB, "", http.StatusOK)
	if bucketHas(foreign, "unprocessed", leadID) || strings.Contains(wbJSON(foreign), contactA1) {
		t.Fatal("other tenant kept this customer's list")
	}

	channelContact := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"渠道客","phone":"","business_category":"merchant_customer","source_type":"manual","assigned_member_id":"`+h.memberID(tenantA, principalSalesA1)+`"}`, http.StatusCreated)
	channelContactID, _ := channelContact["id"].(string)
	h.mustDo("POST", "/api/v1/contacts/"+channelContactID+"/consents", sessionOwnerA, tenantA,
		`{"source_submission_ref":"ch-1","source_channel":"wechat_dm","notice_version":"n1","purpose":"channel_reply","marketing_allowed":false}`, http.StatusOK)
	channelLead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, channelContactID), http.StatusCreated)
	channelLeadID, _ := channelLead["id"].(string)
	markFalseInvalid(t, h, channelLeadID)
	channelDesk := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", http.StatusOK)
	if bucketFilter(channelDesk, channelLeadID) == "invalid_phone" {
		t.Fatal("channel lead was reported invalid for a missing phone")
	}
	channelNext := h.mustDo("POST", "/api/v1/leads/"+channelLeadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"渠道内问候","next_follow_up_at":"2026-09-30T02:00:00Z","channel":"in_channel","ai_score":99}`, http.StatusCreated)
	gotNext, _ := channelNext["next"].(map[string]any)
	if gotNext["kind"] != "channel_follow_up" || gotNext["auto_call"] == true || gotNext["auto_message"] == true || gotNext["create_order"] == true {
		t.Fatalf("channel next = %v", gotNext)
	}
	consents := h.mustDo("GET", "/api/v1/contacts/"+channelContactID+"/consents", sessionSalesA1, tenantA, "", http.StatusOK)
	if strings.Contains(wbJSON(consents), `"marketing_allowed":true`) {
		t.Fatal("channel follow-up widened into a marketing permit")
	}

	refusedContact := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"name":"售后","business_category":"merchant_customer","source_type":"manual","assigned_member_id":%q}`, h.memberID(tenantA, principalSalesA1)), http.StatusCreated)
	refusedID, _ := refusedContact["id"].(string)
	h.mustDo("POST", "/api/v1/contacts/"+refusedID+"/consents", sessionOwnerA, tenantA,
		`{"source_submission_ref":"as-1","source_channel":"wechat_dm","purpose":"after_sales","marketing_allowed":false}`, http.StatusOK)
	refusedLead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, refusedID), http.StatusCreated)
	beforeOpps := h.mustDo("GET", "/api/v1/opportunities?category=merchant_customer", sessionOwnerA, tenantA, "", http.StatusOK)
	h.mustDo("POST", "/api/v1/leads/"+refusedLead["id"].(string)+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"只处理售后","create_opportunity":true}`, http.StatusConflict)
	afterOpps := h.mustDo("GET", "/api/v1/opportunities?category=merchant_customer", sessionOwnerA, tenantA, "", http.StatusOK)
	if len(afterOpps["items"].([]any)) != len(beforeOpps["items"].([]any)) {
		t.Fatal("after-sales consult was forced into a sales opportunity")
	}

	clearAssignee(t, h, leadID)
	ownerDesk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", http.StatusOK)
	if !bucketHas(ownerDesk, "assignment_exception", leadID) {
		t.Fatal("owner missed the unassigned lead")
	}
	salesDesk := h.mustDo("GET", "/api/v1/workbench", sessionSalesA1, tenantA, "", http.StatusOK)
	if bucketHas(salesDesk, "assignment_exception", leadID) || bucketHas(salesDesk, "unprocessed", leadID) {
		t.Fatal("sales still sees a lead outside their scope")
	}

	won := h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"title":"成交","business_category":"merchant_customer","stage":"won","amount_cents":8800}`, contactA1), http.StatusCreated)
	_ = won
	moneyDesk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", http.StatusOK)
	money, _ := moneyDesk["money"].(map[string]any)
	if money["customer_deal_cents"] != float64(8800) || money["painuo_service_order_cents"] != nil || money["platform_tool_spend_cents"] != nil {
		t.Fatalf("money mixed = %v", money)
	}
	if _, ok := money["total_cents"]; ok {
		t.Fatal("workbench published a combined amount")
	}
}

func assertChargeAndAutomation(t *testing.T, body map[string]any) {
	t.Helper()
	billing, _ := body["billing"].(map[string]any)
	auto, _ := body["automation"].(map[string]any)
	if billing["ordinary_crm_charge_cents"] != float64(0) || auto["auto_call"] == true || auto["auto_message"] == true || auto["create_order"] == true {
		t.Fatalf("billing/automation = %v %v", billing, auto)
	}
}

func bucketHas(body map[string]any, bucket, id string) bool {
	buckets, _ := body["buckets"].(map[string]any)
	items, _ := buckets[bucket].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["id"] == id || item["lead_id"] == id || item["opportunity_id"] == id {
			return true
		}
	}
	return false
}

func workbenchHasLead(body map[string]any, id string) bool {
	buckets, _ := body["buckets"].(map[string]any)
	for _, raw := range buckets {
		items, _ := raw.([]any)
		for _, row := range items {
			item, _ := row.(map[string]any)
			if item["lead_id"] == id {
				return true
			}
		}
	}
	return false
}

func bucketFlag(body map[string]any, bucket, id, field string) bool {
	buckets, _ := body["buckets"].(map[string]any)
	items, _ := buckets[bucket].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["id"] == id || item["opportunity_id"] == id {
			return item[field] == true
		}
	}
	return false
}

func bucketFilter(body map[string]any, id string) string {
	buckets, _ := body["buckets"].(map[string]any)
	for _, raw := range buckets {
		items, _ := raw.([]any)
		for _, row := range items {
			item, _ := row.(map[string]any)
			if item["lead_id"] == id {
				reason, _ := item["filter_reason"].(string)
				return reason
			}
		}
	}
	return ""
}

func eventKinds(body map[string]any) map[string]bool {
	out := map[string]bool{}
	events, _ := body["events"].([]any)
	for _, raw := range events {
		ev, _ := raw.(map[string]any)
		kind, _ := ev["kind"].(string)
		out[kind] = true
	}
	return out
}

func wbJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func markFalseInvalid(t *testing.T, h *harness, leadID string) {
	t.Helper()
	if _, err := h.api.St.DB.Exec(`UPDATE leads SET filter_reason='invalid_phone' WHERE id=?`, leadID); err != nil {
		t.Fatal(err)
	}
}

func clearAssignee(t *testing.T, h *harness, leadID string) {
	t.Helper()
	if _, err := h.api.St.DB.Exec(`UPDATE leads SET assigned_member_id=NULL WHERE id=?`, leadID); err != nil {
		t.Fatal(err)
	}
}
