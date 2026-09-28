package httpapi

import (
	"fmt"
	"testing"
)

func TestSubscriptionIsolation(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, contactA := h.seed()
	beforeMembers := h.count(`SELECT COUNT(1) FROM members WHERE tenant_id=?`, tenantA)

	t.Run("contact is not a platform account", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"同号","phone":"13812345678","email":"someone@example.com","principal_ref":"usr_owner_a","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`,
			400)
		for i := 0; i < 100; i++ {
			body := fmt.Sprintf(`{"name":"客户%d","phone":"139%08d","email":"c%d@shop.example","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, i, i, i)
			h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA, body, 201)
		}
		if got := h.count(`SELECT COUNT(1) FROM members WHERE tenant_id=?`, tenantA); got != beforeMembers {
			t.Fatalf("members = %d, want %d", got, beforeMembers)
		}
		if h.count(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name IN ('wallets','billing_accounts','principals')`) != 0 {
			t.Fatal("CRM rows created a platform wallet or principal table")
		}
		report := h.mustDo("GET", "/api/v1/subscription", sessionOwnerA, tenantA, "", 200)
		identity, _ := report["identity"].(map[string]any)
		if identity["platform_accounts_from_contacts"] != float64(0) || identity["wallets_from_contacts"] != float64(0) {
			t.Fatalf("identity footprint = %v", identity)
		}
		if identity["contacts"] == float64(0) {
			t.Fatalf("identity report dropped the contacts: %v", identity)
		}
	})

	t.Run("staff see only this tenant and revocation is immediate", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/subscription", sessionOwnerA, tenantB, "", 403)
		h.mustDo("GET", "/api/v1/subscription", sessionAgentA, tenantA, "", 403)
		h.mustDo("GET", "/api/v1/subscription", sessionAgentA, tenantB, "", 403)
		grant := h.mustDo("POST", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"principal_ref":%q}`, principalAgentA), 201)
		h.mustDo("GET", "/api/v1/subscription", sessionAgentA, tenantA, "", 200)
		h.mustDo("DELETE", "/api/v1/admin/agent-grants/"+grant["id"].(string), sessionOwnerA, tenantA, "", 200)
		if status, _, _ := h.do("GET", "/api/v1/subscription", sessionAgentA, tenantA, ""); status != 403 {
			t.Fatalf("revoked agent status = %d", status)
		}
		salesID := h.memberID(tenantA, principalSalesA1)
		h.mustDo("POST", "/api/v1/contacts/"+contactA+"/followups", sessionSalesA1, tenantA, `{"note":"还在职"}`, 201)
		h.mustDo("PATCH", "/api/v1/admin/members/"+salesID, sessionOwnerA, tenantA, `{"enabled":false}`, 200)
		if status, body, _ := h.do("POST", "/api/v1/contacts/"+contactA+"/followups", sessionSalesA1, tenantA, `{"note":"已退出"}`); status != 403 {
			t.Fatalf("disabled sales write = %d %v", status, body)
		}
		if status, body, _ := h.do("GET", "/api/v1/contacts/"+contactA, sessionSalesA1, tenantA, ""); status != 403 {
			t.Fatalf("disabled sales read = %d %v", status, body)
		}
	})

	t.Run("expired plan keeps history and limits advanced work", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts/"+contactA+"/followups", sessionOwnerA, tenantA, `{"note":"过期前的跟进"}`, 201)
		opp := h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"title":"老商机","business_category":"merchant_customer"}`, contactA), 201)
		contactsBefore := h.count(`SELECT COUNT(1) FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`, tenantA)
		followBefore := h.count(`SELECT COUNT(1) FROM contact_followups WHERE tenant_id=?`, tenantA)
		oppBefore := h.count(`SELECT COUNT(1) FROM opportunities WHERE tenant_id=?`, tenantA)
		h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenantA,
			`{"plan":"crm_pro","status":"expired","wallet_balance_cents":5000000,"org_billing_account_id":"ba_org","authorized_payer_account_id":"ba_payer","crm_subscription_cents":1200,"merchant_deal_cents":8000000}`,
			200)
		view := h.mustDo("GET", "/api/v1/subscription", sessionOwnerA, tenantA, "", 200)
		if view["crm_opened"] == true || view["opened_by_wallet"] == true || view["data_lost"] == true || view["history_retained"] != true || view["merchant_revenue_in_wallet"] == true || view["live_charge"] != float64(0) {
			t.Fatalf("expired view = %v", view)
		}
		ledgers, _ := view["ledgers"].(map[string]any)
		if ledgers["crm_subscription_cents"] != float64(1200) || ledgers["merchant_deal_cents"] != float64(8000000) || ledgers["wallet_cents"] != float64(5000000) {
			t.Fatalf("ledgers mixed = %v", ledgers)
		}
		if ledgers["wallet_cents"] == ledgers["merchant_deal_cents"] {
			t.Fatal("merchant revenue was copied into the wallet")
		}
		h.mustDo("GET", "/api/v1/contacts/"+contactA, sessionOwnerA, tenantA, "", 200)
		h.mustDo("GET", "/api/v1/contacts/"+contactA+"/followups", sessionOwnerA, tenantA, "", 200)
		h.mustDo("GET", "/api/v1/opportunities/"+opp["id"].(string), sessionOwnerA, tenantA, "", 200)
		blocked := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA, `{"form_key":"extra"}`, 403)
		if blocked["error"] != "entitlement_expired" || blocked["data_lost"] == true || blocked["history_retained"] != true {
			t.Fatalf("advanced gate = %v", blocked)
		}
		if h.count(`SELECT COUNT(1) FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`, tenantA) != contactsBefore ||
			h.count(`SELECT COUNT(1) FROM contact_followups WHERE tenant_id=?`, tenantA) != followBefore ||
			h.count(`SELECT COUNT(1) FROM opportunities WHERE tenant_id=?`, tenantA) != oppBefore {
			t.Fatal("expiry deleted CRM history")
		}
	})

	t.Run("ordinary work is free and AI quotes do not debit or repeat", func(t *testing.T) {
		h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenantA,
			`{"plan":"crm_pro","status":"active","wallet_balance_cents":10,"org_billing_account_id":"ba_org","authorized_payer_account_id":"ba_payer","crm_subscription_cents":1200,"merchant_deal_cents":8000000}`,
			200)
		ordinary := h.mustDo("POST", "/api/v1/subscription/usage", sessionOwnerA, tenantA, `{"action":"manual_follow_up"}`, 200)
		if ordinary["billable"] == true || ordinary["cents"] != float64(0) || ordinary["live_charge"] != float64(0) {
			t.Fatalf("ordinary usage = %v", ordinary)
		}
		if h.count(`SELECT COUNT(1) FROM ai_usage_quotes WHERE tenant_id=?`, tenantA) != 0 {
			t.Fatal("ordinary follow-up opened an AI quote")
		}
		quoted := h.mustDo("POST", "/api/v1/ai-usage/quotes", sessionOwnerA, tenantA,
			`{"action":"ai_score","payer_kind":"organization","payer_account_id":"ba_org","return_to":"/leads/lead_1"}`, 200)
		if quoted["execute"] == true || quoted["reason"] != "insufficient_balance" || quoted["billing_center"] != "billing_center" || quoted["return_to"] != "/leads/lead_1" || quoted["live_charge"] != float64(0) {
			t.Fatalf("quote = %v", quoted)
		}
		quoteID, _ := quoted["id"].(string)
		seen, _ := quoted["revision"].(string)
		h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenantA,
			`{"plan":"crm_pro","status":"active","wallet_balance_cents":100000,"org_billing_account_id":"ba_org","authorized_payer_account_id":"ba_payer","crm_subscription_cents":1200,"merchant_deal_cents":8000000}`,
			200)
		stale := h.mustDo("POST", "/api/v1/ai-usage/quotes/"+quoteID+"/commit", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"seen_revision":%q,"explicit_confirm":true}`, seen), 409)
		if stale["execute"] == true || stale["reread"] != true || stale["live_charge"] != float64(0) {
			t.Fatalf("stale commit = %v", stale)
		}
		fresh := h.mustDo("POST", "/api/v1/ai-usage/quotes/"+quoteID+"/requote", sessionOwnerA, tenantA, "", 200)
		if fresh["revision"] == seen || fresh["execute"] == true {
			t.Fatalf("requote = %v", fresh)
		}
		next, _ := fresh["revision"].(string)
		held := h.mustDo("POST", "/api/v1/ai-usage/quotes/"+quoteID+"/commit", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"seen_revision":%q,"explicit_confirm":false}`, next), 409)
		if held["execute"] == true || held["reason"] != "confirmation_required" {
			t.Fatalf("unconfirmed commit = %v", held)
		}
		done := h.mustDo("POST", "/api/v1/ai-usage/quotes/"+quoteID+"/commit", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"seen_revision":%q,"explicit_confirm":true}`, next), 200)
		if done["execute"] != true || done["live_charge"] != float64(0) || done["side_effects"] != float64(0) {
			t.Fatalf("commit = %v", done)
		}
		again := h.mustDo("POST", "/api/v1/ai-usage/quotes/"+quoteID+"/commit", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"seen_revision":%q,"explicit_confirm":true}`, next), 409)
		if again["duplicate"] != true || again["execute"] == true || again["live_charge"] != float64(0) {
			t.Fatalf("repeat = %v", again)
		}
		refused := h.mustDo("POST", "/api/v1/ai-usage/quotes", sessionOwnerA, tenantA,
			`{"action":"ai_outbound","payer_kind":"personal","payer_account_id":"ba_person","return_to":"/leads/lead_1"}`, 200)
		if refused["execute"] == true || refused["reason"] != "payer_not_authorized" || refused["live_charge"] != float64(0) {
			t.Fatalf("personal payer = %v", refused)
		}
		if h.count(`SELECT COUNT(1) FROM ai_usage_quotes WHERE tenant_id=? AND live_charge<>0`, tenantA) != 0 {
			t.Fatal("a quote row recorded a live charge")
		}
	})
}

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.api.St.DB.QueryRow(query, args...).Scan(&n); err != nil {
		h.t.Fatalf("count %s: %v", query, err)
	}
	return n
}
