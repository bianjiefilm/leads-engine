package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestChannelInteractionConfirmStaysInTenantAndUnverified(t *testing.T) {
	const secret = "客户私信全文不应出现在日志或地址里"
	h := newHarnessOpts(t, harnessOpts{captureLog: true})
	tenantA, tenantB, _ := h.seed()
	grant := `{
		"provider":"connector","account_id":"acct_1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"capabilities":["message.read"]
	}`
	h.mustDo("POST", "/api/v1/channel-grants", sessionOwnerA, tenantA, grant, http.StatusCreated)
	h.mustDo("POST", "/api/v1/channel-grants", sessionOwnerB, tenantB, strings.Replace(grant, "acct_1", "acct_b", 1), http.StatusCreated)

	cap := h.mustDo("GET", "/api/v1/channel-interactions/capability", sessionOwnerA, tenantA, "", http.StatusOK)
	if cap["verification"] != "unverified" || cap["publish_implies_message_read"] != false {
		t.Fatalf("capability: %v", cap)
	}
	if providers, _ := cap["live_providers"].([]any); len(providers) != 0 {
		t.Fatalf("live providers: %v", cap["live_providers"])
	}

	body := `{
		"target_tenant_id":"` + tenantA + `","provider":"connector","account_id":"acct_1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_1","kind":"direct_message","subject_id":"sub_1","nickname":"小王","phone":"13800000000",
		"text":"` + secret + `","explicit_intent":true,"purpose":"sales_inquiry","score":99
	}`
	forged := strings.Replace(body, tenantA, tenantB, 1)
	status, forgedOut, _ := h.do("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, forged)
	if status != http.StatusForbidden || forgedOut["error"] != "forged_tenant" {
		t.Fatalf("forged tenant: %d %v", status, forgedOut)
	}
	created := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, body, http.StatusCreated)
	if created["auto_reach"] == true || created["phone_marketing"] == true || created["sms_marketing"] == true || created["delivered"] == true {
		t.Fatalf("message armed outreach: %v", created)
	}
	replay := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, body, http.StatusOK)
	if replay["idempotent"] != true || replay["interaction_id"] != created["interaction_id"] || replay["candidate_id"] != created["candidate_id"] {
		t.Fatalf("replay: %v", replay)
	}
	if strings.Contains(h.logs.String(), secret) {
		t.Fatalf("log leaked customer text: %s", h.logs.String())
	}

	lead := h.mustDo("POST", "/api/v1/channel-candidates/"+created["candidate_id"].(string)+"/confirm", sessionOwnerA, tenantA, "", http.StatusCreated)
	leadID, _ := lead["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("confirm: %v", lead)
	}
	listed := h.mustDo("GET", "/api/v1/leads", sessionOwnerA, tenantA, "", http.StatusOK)
	if !jsonListHasID(listed["items"], leadID) {
		t.Fatalf("lead missing from tenant A: %v", listed)
	}
	other := h.mustDo("GET", "/api/v1/leads", sessionOwnerB, tenantB, "", http.StatusOK)
	if jsonListHasID(other["items"], leadID) {
		t.Fatalf("lead leaked to tenant B: %v", other)
	}
	contactID, _ := lead["contact_id"].(string)
	contact := h.mustDo("GET", "/api/v1/contacts/"+contactID, sessionOwnerA, tenantA, "", http.StatusOK)
	if contact["phone"] != "" {
		t.Fatalf("phone copied onto contact: %v", contact["phone"])
	}
}

func TestChannelRevokeBlocksOldReplay(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	h.mustDo("POST", "/api/v1/channel-grants", sessionOwnerA, tenantA, `{
		"provider":"connector","account_id":"acct_1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"capabilities":["publish"]
	}`, http.StatusCreated)
	msg := `{
		"target_tenant_id":"` + tenantA + `","provider":"connector","account_id":"acct_1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_pub","kind":"direct_message","subject_id":"sub_1","nickname":"小王",
		"text":"你好","explicit_intent":true,"purpose":"sales_inquiry"
	}`
	status, out, _ := h.do("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, msg)
	if status != http.StatusForbidden || out["error"] != "capability_missing" {
		t.Fatalf("publish grant read a message: %d %v", status, out)
	}
	h.mustDo("POST", "/api/v1/channel-grants", sessionOwnerA, tenantA, `{
		"provider":"connector","account_id":"acct_1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"capabilities":["comment.read","reply"]
	}`, http.StatusCreated)
	h.mustDo("POST", "/api/v1/channel-floor", sessionOwnerA, tenantA, `{
		"provider":"connector","account_id":"acct_1","subject_id":"sub_1","holder":"matrix_bot"
	}`, http.StatusOK)
	reply := `{
		"target_tenant_id":"` + tenantA + `","provider":"connector","account_id":"acct_1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_c","kind":"comment","subject_id":"sub_1","nickname":"小王",
		"text":"多少钱","want_reply":true,"score":100
	}`
	got := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, reply, http.StatusCreated)
	if got["reply_allowed"] == true || got["reply_refusal"] != "speaker_conflict" || got["delivered"] == true {
		t.Fatalf("two bots: %v", got)
	}
	h.mustDo("POST", "/api/v1/channel-grants/revoke", sessionOwnerA, tenantA, `{"provider":"connector","account_id":"acct_1"}`, http.StatusOK)
	status, again, _ := h.do("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, reply)
	if status != http.StatusForbidden || again["error"] != "permission_revoked" {
		t.Fatalf("revoked replay: %d %v", status, again)
	}
}

func jsonListHasID(items any, id string) bool {
	list, _ := items.([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["id"] == id {
			return true
		}
	}
	return false
}
