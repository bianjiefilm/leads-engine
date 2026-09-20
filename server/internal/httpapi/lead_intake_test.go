// HUI-1683 / FEAT-0184 线索 intake 去重与联系人合并 —— 真实服务 HTTP E2E:
//   - 事件幂等:同键同内容重投返回原引用(零新行);同键异内容显式 409 不静默覆盖;
//   - 三分类:new / repeat_consult(新 lead 保留新来源+授权,真实咨询次数不丢)/
//     ambiguous(共享号码多命中 → 候选池,绝不静默合并);
//   - 并发:同键 8 路并发恰好一条 lead;
//   - 跨租户:同号码各租户独立 contact,互不可见、不可跨租户合并;
//   - consent 红线在 intake 与合并后都成立:撤销持久,旧事件重放不恢复;
//   - 合并审计:字段级 provenance、重指计数、两侧快照;undo 纠错且后续写保护;
//   - 日志零手机号(只记指纹前缀);owner 专属权限矩阵。
package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// intakeBody builds an intake request body; the exact string is the content
// identity for idempotency, so callers keep one instance for replays.
func intakeBody(app, ns, eventID, name, phone, email string) string {
	return fmt.Sprintf(`{"source_app":%q,"source_ns":%q,"event_id":%q,"contact":{"name":%q,"phone":%q,"email":%q},"business_category":"merchant_customer","source_type":"form"}`,
		app, ns, eventID, name, phone, email)
}

// intake posts one event and fails the test unless the status matches.
func (h *harness) intake(session, tenant, body string, wantStatus int) map[string]any {
	h.t.Helper()
	return h.mustDo("POST", "/api/v1/leads/intake", session, tenant, body, wantStatus)
}

// leadIDsForContact lists the tenant's leads and returns the ids pointing at
// the given contact (真实咨询次数的断言基础)。
func (h *harness) leadIDsForContact(session, tenant, contactID string) []string {
	h.t.Helper()
	out := h.mustDo("GET", "/api/v1/leads", session, tenant, "", http.StatusOK)
	items, _ := out["items"].([]any)
	var ids []string
	for _, it := range items {
		l, _ := it.(map[string]any)
		if cid, _ := l["contact_id"].(string); cid == contactID {
			id, _ := l["id"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}

func (h *harness) contactByID(session, tenant, id string) (int, map[string]any) {
	st, out, _ := h.do("GET", "/api/v1/contacts/"+id, session, tenant, "")
	return st, out
}

// ---- 三分类矩阵 -----------------------------------------------------------------

func TestLeadIntakeClassification(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	var newLeadID, newContactID string
	t.Run("zero hit creates a new contact and lead (class=new)", func(t *testing.T) {
		body := intakeBody("touch", "wx-oa", "evt-1", "乙商家", "13900000001", "yi@shop.cn")
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
		if out["class"] != "new" || out["duplicate"] != false {
			t.Fatalf("first intake class = %v dup = %v, want new/false", out["class"], out["duplicate"])
		}
		newLeadID, _ = out["lead_id"].(string)
		newContactID, _ = out["contact_id"].(string)
		if newLeadID == "" || newContactID == "" {
			t.Fatalf("intake response missing ids: %v", out)
		}
		lead := h.mustDo("GET", "/api/v1/leads/"+newLeadID, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["contact_id"] != newContactID || lead["status"] != "new" {
			t.Fatalf("lead = %v, want contact %s status new", lead, newContactID)
		}
	})

	t.Run("exact replay returns the original reference and writes nothing", func(t *testing.T) {
		body := intakeBody("touch", "wx-oa", "evt-1", "乙商家", "13900000001", "yi@shop.cn")
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusOK)
		if out["class"] != "exact_duplicate" || out["duplicate"] != true {
			t.Fatalf("replay class = %v, want exact_duplicate", out["class"])
		}
		if out["lead_id"] != newLeadID || out["contact_id"] != newContactID {
			t.Fatalf("replay references drifted: got lead=%v contact=%v, want %s/%s",
				out["lead_id"], out["contact_id"], newLeadID, newContactID)
		}
		if ids := h.leadIDsForContact(sessionOwnerA, tenantA, newContactID); len(ids) != 1 {
			t.Fatalf("lead count after replay = %d, want 1 (重复投递不重复建线索)", len(ids))
		}
	})

	t.Run("same key with different content is an explicit 409 conflict", func(t *testing.T) {
		body := intakeBody("touch", "wx-oa", "evt-1", "乙商家", "13900000099", "yi@shop.cn")
		status, out, _ := h.do("POST", "/api/v1/leads/intake", sessionOwnerA, tenantA, body)
		if status != http.StatusConflict || out["error"] != "event_content_conflict" {
			t.Fatalf("conflicting content: status=%d body=%v, want 409 event_content_conflict", status, out)
		}
		if ids := h.leadIDsForContact(sessionOwnerA, tenantA, newContactID); len(ids) != 1 {
			t.Fatalf("lead count after conflict = %d, want 1 (绝不静默覆盖)", len(ids))
		}
		// 原联系人的联系方式未被覆盖。
		st, ct := h.contactByID(sessionOwnerA, tenantA, newContactID)
		if st != http.StatusOK || ct["phone"] != "13900000001" {
			t.Fatalf("original contact mutated by conflict: %d %v", st, ct)
		}
	})

	t.Run("same phone via another campaign is repeat_consult with a NEW lead", func(t *testing.T) {
		body := `{"source_app":"touch","source_ns":"landing","event_id":"evt-2","contact":{"name":"乙商家","phone":"139 0000 0001","email":"yi@shop.cn"},"business_category":"merchant_customer","source_type":"touch_campaign","source":{"source_app":"touch","source_ref":"camp-B","auth_scope_snapshot":"campaign:b"},"consent":{"source_submission_ref":"sub-camp-B","source_channel":"landing_page","notice_version":"notice-v2","marketing_allowed":true}}`
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
		if out["class"] != "repeat_consult" {
			t.Fatalf("class = %v, want repeat_consult (同一联系人再次咨询)", out["class"])
		}
		if out["contact_id"] != newContactID {
			t.Fatalf("repeat consult must link the SAME contact: got %v want %s", out["contact_id"], newContactID)
		}
		repeatLead, _ := out["lead_id"].(string)
		if repeatLead == newLeadID {
			t.Fatal("repeat consult must create a NEW lead (咨询次数不丢)")
		}
		ids := h.leadIDsForContact(sessionOwnerA, tenantA, newContactID)
		if len(ids) != 2 {
			t.Fatalf("lead count = %d, want 2", len(ids))
		}
		// 新 lead 保留自己的活动来源。
		lead := h.mustDo("GET", "/api/v1/leads/"+repeatLead, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["source_ref_id"] == "" {
			t.Fatalf("repeat lead lost its campaign provenance: %v", lead)
		}
		// 新事件自己的授权记录独立成行。
		list := h.mustDo("GET", "/api/v1/contacts/"+newContactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		if got := len(list["items"].([]any)); got != 1 {
			t.Fatalf("consent rows = %d, want 1", got)
		}
		h.intake(sessionOwnerA, tenantA, `{"source_app":"touch","source_ns":"landing","event_id":"evt-2b","contact":{"name":"乙商家","phone":"13900000001","email":"yi@shop.cn"},"business_category":"merchant_customer","consent":{"source_submission_ref":"sub-camp-C","source_channel":"sms","notice_version":"notice-v3","marketing_allowed":true}}`, http.StatusCreated)
		list = h.mustDo("GET", "/api/v1/contacts/"+newContactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		if got := len(list["items"].([]any)); got != 2 {
			t.Fatalf("consent rows = %d, want 2 (新来源新授权行)", got)
		}
	})

	t.Run("shared phone ambiguity lands in the candidate pool, never a silent merge", func(t *testing.T) {
		// 两个既有 contact 共享号码(手工建档,其中一个带分隔符验证归一化)。
		c1 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"丙商家","phone":"13777776666","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, http.StatusCreated)["id"].(string)
		c2 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"丁商家","phone":"137-7777-6666","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, http.StatusCreated)["id"].(string)
		body := intakeBody("touch", "wx-oa", "evt-3", "疑似丙丁", "13777776666", "")
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
		if out["class"] != "ambiguous" {
			t.Fatalf("class = %v, want ambiguous (共享号码歧义)", out["class"])
		}
		ambContact, _ := out["contact_id"].(string)
		if ambContact == c1 || ambContact == c2 {
			t.Fatal("ambiguous intake must NOT attach to either existing contact")
		}
		// 候选池:三个共享号码的 contact 两两成对(3 行),全部 pending。
		list := h.mustDo("GET", "/api/v1/merge-candidates?status=pending", sessionOwnerA, tenantA, "", http.StatusOK)
		items := list["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("pending candidates = %d, want 3", len(items))
		}
		pairs := map[string]bool{}
		for _, it := range items {
			m := it.(map[string]any)
			if m["reason"] != "shared_phone" {
				t.Fatalf("reason = %v, want shared_phone", m["reason"])
			}
			a, _ := m["contact_a"].(string)
			b, _ := m["contact_b"].(string)
			pairs[a+"|"+b] = true
		}
		for _, want := range []struct{ a, b string }{{ambContact, c1}, {ambContact, c2}, {c1, c2}} {
			x, y := want.a, want.b
			if x > y {
				x, y = y, x
			}
			if !pairs[x+"|"+y] {
				t.Fatalf("missing candidate pair %s/%s in %v", x, y, pairs)
			}
		}
		// 没有任何静默合并:三个 contact 均仍独立可查。
		for _, id := range []string{c1, c2, ambContact} {
			if st, _ := h.contactByID(sessionOwnerA, tenantA, id); st != http.StatusOK {
				t.Fatalf("contact %s vanished without an explicit merge", id)
			}
		}
		if merges := h.mustDo("GET", "/api/v1/merges", sessionOwnerA, tenantA, "", http.StatusOK); len(merges["items"].([]any)) != 0 {
			t.Fatal("no merge may exist without an explicit owner action")
		}
	})
}

// ---- 并发:同键 8 路并发恰好一条 lead ---------------------------------------------

func TestLeadIntakeConcurrentSameKey(t *testing.T) {
	h := newHarness(t)
	tenantA := h.provision("tenant", "Tenant A")
	h.provision("member", tenantA, principalOwnerA, "owner", "Owner A")

	body := intakeBody("touch", "wx-oa", "evt-race", "并发商家", "13655554444", "")
	const n = 8
	type raceResult struct {
		status int
		leadID string
		class  string
	}
	results := make([]raceResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, out, _ := h.do("POST", "/api/v1/leads/intake", sessionOwnerA, tenantA, body)
			lead, _ := out["lead_id"].(string)
			class, _ := out["class"].(string)
			results[i] = raceResult{status: status, leadID: lead, class: class}
		}(i)
	}
	wg.Wait()
	leadIDs := map[string]int{}
	for i, r := range results {
		if r.status != http.StatusOK && r.status != http.StatusCreated {
			t.Fatalf("worker %d: status %d, want 200/201", i, r.status)
		}
		if r.leadID == "" {
			t.Fatalf("worker %d: missing lead id", i)
		}
		leadIDs[r.leadID]++
	}
	if len(leadIDs) != 1 {
		t.Fatalf("distinct lead ids = %v, want exactly one lead", leadIDs)
	}
	created := 0
	for _, r := range results {
		if r.status == http.StatusCreated {
			created++
			if r.class != "new" {
				t.Fatalf("first delivery class = %q, want new", r.class)
			}
		}
	}
	if created != 1 {
		t.Fatalf("201 responses = %d, want exactly 1", created)
	}
	contact := ""
	for id := range leadIDs {
		l := h.mustDo("GET", "/api/v1/leads/"+id, sessionOwnerA, tenantA, "", http.StatusOK)
		contact, _ = l["contact_id"].(string)
	}
	if ids := h.leadIDsForContact(sessionOwnerA, tenantA, contact); len(ids) != 1 {
		t.Fatalf("tenant lead count for contact = %d, want 1", len(ids))
	}
}

// ---- 跨租户:同号码独立,不合并 -----------------------------------------------------

func TestLeadIntakeCrossTenantIsolation(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()

	outA := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx-oa", "evt-shared", "A侧商家", "13522223333", ""), http.StatusCreated)
	if outA["class"] != "new" {
		t.Fatalf("tenant A first intake class = %v, want new", outA["class"])
	}
	// 同一号码在租户 B 首次到达:B 无从知晓 A,必须是 new。
	outB := h.intake(sessionOwnerB, tenantB, intakeBody("touch", "wx-oa", "evt-shared", "B侧商家", "13522223333", ""), http.StatusCreated)
	if outB["class"] != "new" {
		t.Fatalf("tenant B intake class = %v, want new (跨租户同号码互不可见)", outB["class"])
	}
	if outB["contact_id"] == outA["contact_id"] {
		t.Fatal("cross-tenant same phone must never share a contact")
	}
	// 各租户只能看到自己的 contact。
	if st, _ := h.contactByID(sessionOwnerB, tenantB, outA["contact_id"].(string)); st != http.StatusNotFound {
		t.Fatalf("tenant B saw tenant A contact: %d", st)
	}
	// 跨租户合并一律 404 掩码(不存在任何跨租户合并路径)。
	status, out, _ := h.do("POST", "/api/v1/contacts/"+outB["contact_id"].(string)+"/merge/"+outA["contact_id"].(string),
		sessionOwnerB, tenantB, "")
	if status != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("cross-tenant merge = %d %v, want 404 not_found", status, out)
	}
	// 幂等键按租户隔离:同键在 B 是首次投递,不是 A 的重放。
	if outB["duplicate"] == true {
		t.Fatal("event keys must be scoped per tenant")
	}
}

// ---- 撤销后旧事件重放(intake 全链)--------------------------------------------------

func TestLeadIntakeRevokedConsentReplay(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	firstBody := `{"source_app":"touch","source_ns":"wx-oa","event_id":"evt-consent","contact":{"name":"戊商家","phone":"13411110000","email":"wu@shop.cn"},"business_category":"merchant_customer","consent":{"source_submission_ref":"sub-replay","source_channel":"wx_oa","notice_version":"notice-v1","marketing_allowed":true}}`
	out := h.intake(sessionOwnerA, tenantA, firstBody, http.StatusCreated)
	contactID, _ := out["contact_id"].(string)

	list := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("consent rows = %d, want 1", len(items))
	}
	row := items[0].(map[string]any)
	consentID, _ := row["id"].(string)
	rev := h.mustDo("POST", "/api/v1/contacts/"+contactID+"/consents/"+consentID+"/revoke",
		sessionOwnerA, tenantA, `{"reason":"客户拒绝"}`, http.StatusOK)
	revokedAt, _ := rev["consent"].(map[string]any)["revoked_at"].(string)
	if revokedAt == "" {
		t.Fatal("revoke failed")
	}

	t.Run("exact replay of the revoked event stays revoked", func(t *testing.T) {
		h.intake(sessionOwnerA, tenantA, firstBody, http.StatusOK)
		list := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		row := list["items"].([]any)[0].(map[string]any)
		if row["revoked_at"] != revokedAt {
			t.Fatalf("revoked_at drifted: %v want %s (重放不得清除撤销)", row["revoked_at"], revokedAt)
		}
		if list["summary"].(map[string]any)["marketing_active"] != float64(0) {
			t.Fatalf("marketing active after replay = %v, want 0", list["summary"])
		}
	})

	t.Run("a NEW event replaying the same consent key cannot revive marketing", func(t *testing.T) {
		// 换 event_id、同 phone → repeat_consult;consent 键相同 → 幂等返回现状。
		body := `{"source_app":"touch","source_ns":"landing","event_id":"evt-consent-2","contact":{"name":"戊商家","phone":"13411110000","email":"wu@shop.cn"},"business_category":"merchant_customer","consent":{"source_submission_ref":"sub-replay","source_channel":"wx_oa","notice_version":"notice-v9","marketing_allowed":true}}`
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
		if out["class"] != "repeat_consult" {
			t.Fatalf("class = %v, want repeat_consult", out["class"])
		}
		list := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		items := list["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("consent rows = %d, want 1 (同键重放不新建)", len(items))
		}
		row := items[0].(map[string]any)
		if row["revoked_at"] != revokedAt || row["notice_version"] != "notice-v1" {
			t.Fatalf("revoked row was refreshed: %v (重放零写入)", row)
		}
	})
}

// ---- D-L1 回归:repeat_consult 重投同一未撤销 consent 块 ----------------------------

// 跨仓共测(touch T1 × leads L0)L0-06 缺陷回归:同联系人新 event_id 携带
// 相同 (source_submission_ref, source_channel) 的未撤销 consent 块 —— 共测时
// 曾稳定 500 "intake commit failed"(store 域函数 UPDATE 分支越权 Commit,
// HTTP 层外层二次提交失败,且 lead+event 行已部分可见)。契约:201
// class=repeat_consult,consent 同键更新不新建,咨询次数不丢。
func TestLeadIntakeRepeatConsultSameConsentBlock(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	first := `{"source_app":"touch","source_ns":"landing","event_id":"evt-dl1-1","contact":{"name":"庚商家","phone":"13988887777","email":"geng@shop.cn"},"business_category":"merchant_customer","source_type":"touch_campaign","consent":{"source_submission_ref":"sub-dl1","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":true}}`
	out := h.intake(sessionOwnerA, tenantA, first, http.StatusCreated)
	if out["class"] != "new" {
		t.Fatalf("first intake class = %v, want new", out["class"])
	}
	contactID, _ := out["contact_id"].(string)

	// 同一 consent 块重投,仅换 event_id —— T1 常规业务路径。
	second := `{"source_app":"touch","source_ns":"landing","event_id":"evt-dl1-2","contact":{"name":"庚商家","phone":"13988887777","email":"geng@shop.cn"},"business_category":"merchant_customer","source_type":"touch_campaign","consent":{"source_submission_ref":"sub-dl1","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":true}}`
	status, out2, _ := h.do("POST", "/api/v1/leads/intake", sessionOwnerA, tenantA, second)
	if status != http.StatusCreated {
		t.Fatalf("repeat consult with same consent block: status %d body %v, want 201 (D-L1 曾 500 intake commit failed)", status, out2)
	}
	if out2["class"] != "repeat_consult" {
		t.Fatalf("class = %v, want repeat_consult", out2["class"])
	}
	if out2["contact_id"] != contactID {
		t.Fatalf("contact drifted: got %v want %s", out2["contact_id"], contactID)
	}
	// 咨询次数不丢 + consent 同键更新不翻倍。
	if ids := h.leadIDsForContact(sessionOwnerA, tenantA, contactID); len(ids) != 2 {
		t.Fatalf("lead count = %d, want 2", len(ids))
	}
	list := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
	if got := len(list["items"].([]any)); got != 1 {
		t.Fatalf("consent rows = %d, want 1 (同键更新,不新建)", got)
	}
	// 同字节第三次投递 → exact_duplicate 幂等 200(零写入路径不受影响)。
	if out3 := h.intake(sessionOwnerA, tenantA, second, http.StatusOK); out3["class"] != "exact_duplicate" {
		t.Fatalf("replay class = %v, want exact_duplicate", out3["class"])
	}
}

// ---- 日志零手机号 -------------------------------------------------------------------

func TestLeadIntakeLogRedaction(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{captureLog: true})
	tenantA, _, _ := h.seed()
	h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx-oa", "evt-log", "己商家", "13344445555", ""), http.StatusCreated)
	h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx-oa", "evt-log", "己商家", "13344445555", ""), http.StatusOK)

	logs := h.logs.String()
	if !strings.Contains(logs, "lead intake") {
		t.Fatalf("intake log line missing: %q", logs)
	}
	if strings.Contains(logs, "13344445555") {
		t.Fatalf("raw phone leaked into logs: %q", logs)
	}
	if !strings.Contains(logs, "phone_fpr=") {
		t.Fatalf("expected the phone fingerprint prefix in logs: %q", logs)
	}
}

// ---- fail-closed 与入参校验 -----------------------------------------------------------

func TestLeadIntakeValidationAndGate(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	t.Run("bad input shapes are 400", func(t *testing.T) {
		cases := []struct {
			name string
			body string
		}{
			{"missing event_id", `{"source_app":"a","source_ns":"n","contact":{"name":"x"}}`},
			{"missing source_app", `{"source_ns":"n","event_id":"e","contact":{"name":"x"}}`},
			{"illegal key charset", intakeBody("touch", "wx oa", "evt-x", "x", "", "")},
			{"missing contact name", `{"source_app":"a","source_ns":"n","event_id":"e","contact":{"phone":"123"}}`},
			{"bad category", `{"source_app":"a","source_ns":"n","event_id":"e","contact":{"name":"x"},"business_category":"other"}`},
			{"consent without submission ref", `{"source_app":"a","source_ns":"n","event_id":"e","contact":{"name":"x"},"consent":{"source_channel":"c"}}`},
		}
		for _, tc := range cases {
			status, out, _ := h.do("POST", "/api/v1/leads/intake", sessionOwnerA, tenantA, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s: status %d body %v, want 400", tc.name, status, out)
			}
		}
	})

	t.Run("missing pepper fails closed with 503", func(t *testing.T) {
		h2 := newHarnessOpts(t, harnessOpts{omitDedupPepper: true})
		tenant := h2.provision("tenant", "Tenant P")
		h2.provision("member", tenant, principalOwnerA, "owner", "Owner P")
		status, out, _ := h2.do("POST", "/api/v1/leads/intake", sessionOwnerA, tenant,
			intakeBody("touch", "wx-oa", "evt-nope", "某商家", "13222221111", ""))
		if status != http.StatusServiceUnavailable || out["error"] != "config_gate_dedup" {
			t.Fatalf("intake without pepper = %d %v, want 503 config_gate_dedup", status, out)
		}
	})
}

// ---- 合并:provenance / 审计 / consent 最严格 ------------------------------------------

// mergePair builds one (target, source) contact pair with the merge-relevant
// furniture: target has 2 leads + a revoked consent (coarse granted before
// merge); source has 1 lead + a followup + an active consent and fills the
// target's empty email. Returns (targetID, sourceID, sourceLeadID).
var mergePairSeq int

// mergePair builds one isolated (target, source) pair inside tenantA: unique
// phones per call (同租户内号码即查重信号,必须按场景隔离), unique event keys.
// Returns (targetID, sourceID, sourceLeadID, targetPhone, sourcePhone).
func (h *harness) mergePair(tenantA, tag string) (string, string, string, string, string) {
	h.t.Helper()
	mergePairSeq++
	tPhone := fmt.Sprintf("131000011%02d", mergePairSeq)
	sPhone := fmt.Sprintf("131999988%02d", mergePairSeq)
	outT := h.intake(sessionOwnerA, tenantA, fmt.Sprintf(`{"source_app":"touch","source_ns":"wx-oa","event_id":"evt-%s-T1","contact":{"name":"目标商家","phone":"%s","email":""},"business_category":"merchant_customer","consent":{"source_submission_ref":"sub-T","source_channel":"wx_oa","notice_version":"notice-v1","marketing_allowed":true}}`, tag, tPhone), http.StatusCreated)
	target, _ := outT["contact_id"].(string)
	h.intake(sessionOwnerA, tenantA, fmt.Sprintf(`{"source_app":"touch","source_ns":"landing","event_id":"evt-%s-T2","contact":{"name":"目标商家","phone":"%s","email":""},"business_category":"merchant_customer"}`, tag, tPhone), http.StatusCreated)
	list := h.mustDo("GET", "/api/v1/contacts/"+target+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
	consentT := list["items"].([]any)[0].(map[string]any)["id"].(string)
	h.mustDo("POST", "/api/v1/contacts/"+target+"/consents/"+consentT+"/revoke", sessionOwnerA, tenantA, `{"reason":"拒绝"}`, http.StatusOK)
	h.mustDo("PATCH", "/api/v1/contacts/"+target, sessionOwnerA, tenantA, `{"consent_status":"granted"}`, http.StatusOK)

	outS := h.intake(sessionOwnerA, tenantA, fmt.Sprintf(`{"source_app":"touch","source_ns":"offline","event_id":"evt-%s-S1","contact":{"name":"源头商家","phone":"%s","email":"source@shop.cn"},"business_category":"merchant_customer","consent":{"source_submission_ref":"sub-S","source_channel":"offline_event","notice_version":"notice-v2","marketing_allowed":true}}`, tag, sPhone), http.StatusCreated)
	source, _ := outS["contact_id"].(string)
	h.mustDo("POST", "/api/v1/contacts/"+source+"/followups", sessionOwnerA, tenantA, `{"note":"老客户回访"}`, http.StatusCreated)
	sLeads := h.leadIDsForContact(sessionOwnerA, tenantA, source)
	if len(sLeads) != 1 || len(h.leadIDsForContact(sessionOwnerA, tenantA, target)) != 2 {
		h.t.Fatalf("pair fixture wrong: source leads=%d target leads=%d",
			len(sLeads), len(h.leadIDsForContact(sessionOwnerA, tenantA, target)))
	}
	return target, source, sLeads[0], tPhone, sPhone
}

func TestContactMergeProvenanceAndStrictestConsent(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	t.Run("merge is owner-only and writes complete provenance", func(t *testing.T) {
		target, source, _, _, _ := h.mergePair(tenantA, "p1")
		if status, _, _ := h.do("POST", "/api/v1/contacts/"+target+"/merge/"+source, sessionSalesA1, tenantA, ""); status != http.StatusForbidden {
			t.Fatalf("sales merge status %d, want 403", status)
		}
		out := h.mustDo("POST", "/api/v1/contacts/"+target+"/merge/"+source, sessionOwnerA, tenantA, "", http.StatusOK)
		mergeID, _ := out["id"].(string)
		if mergeID == "" {
			t.Fatalf("merge response missing id: %v", out)
		}
		// 字段级 provenance:target 留自己的值,source 只补空位。
		fields := out["field_provenance"].(map[string]any)["fields"].(map[string]any)
		if fields["name"].(map[string]any)["from"] != "target" {
			t.Fatalf("name provenance = %v, want target", fields["name"])
		}
		if fields["email"].(map[string]any)["from"] != "source" {
			t.Fatalf("email provenance = %v, want source (target 为空由 source 补)", fields["email"])
		}
		if out["lead_repoint_count"] != float64(1) || out["consent_repoint_count"] != float64(1) || out["followup_repoint_count"] != float64(1) {
			t.Fatalf("repoint counts = leads %v consents %v followups %v, want 1/1/1",
				out["lead_repoint_count"], out["consent_repoint_count"], out["followup_repoint_count"])
		}
		// 两侧快照是 undo 的依据,必须存在。
		if len(out["target_before"].(map[string]any)) == 0 || len(out["source_before"].(map[string]any)) == 0 {
			t.Fatal("merge audit missing pre-merge snapshots")
		}
	})

	t.Run("merged target holds strictest consent and repointed records", func(t *testing.T) {
		target, source, sourceLead, tPhone, _ := h.mergePair(tenantA, "p2")
		h.mustDo("POST", "/api/v1/contacts/"+target+"/merge/"+source, sessionOwnerA, tenantA, "", http.StatusOK)
		st, ct := h.contactByID(sessionOwnerA, tenantA, target)
		if st != http.StatusOK {
			t.Fatalf("target gone after merge: %d", st)
		}
		if ct["email"] != "source@shop.cn" {
			t.Fatalf("merged email = %v, want source-filled", ct["email"])
		}
		if ct["phone"] != tPhone || ct["name"] != "目标商家" {
			t.Fatalf("merged identity drifted: %v", ct)
		}
		// consent 最严格:任一侧存在 revoked → 合并后 coarse=denied(合并前是 granted)。
		if ct["consent_status"] != "denied" {
			t.Fatalf("merged coarse consent = %v, want denied (最严格)", ct["consent_status"])
		}
		// source 成为墓碑,对所有角色 404。
		if st, _ := h.contactByID(sessionOwnerA, tenantA, source); st != http.StatusNotFound {
			t.Fatalf("merged source still resolvable: %d", st)
		}
		// leads 全部指向 target(2+1=3)。
		if got := len(h.leadIDsForContact(sessionOwnerA, tenantA, target)); got != 3 {
			t.Fatalf("target lead count after merge = %d, want 3", got)
		}
		lead := h.mustDo("GET", "/api/v1/leads/"+sourceLead, sessionOwnerA, tenantA, "", http.StatusOK)
		if lead["contact_id"] != target {
			t.Fatalf("repointed lead still points at source: %v", lead)
		}
		// per-source 授权行重指不归并:sub-S 原样(独立 notice_version,未撤销)。
		list := h.mustDo("GET", "/api/v1/contacts/"+target+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		summary := list["summary"].(map[string]any)
		if summary["revoked"] != float64(1) || summary["marketing_active"] != float64(1) {
			t.Fatalf("consent summary after merge = %v, want 1 revoked + 1 active", summary)
		}
		for _, it := range list["items"].([]any) {
			row := it.(map[string]any)
			if row["source_submission_ref"] == "sub-S" && (row["notice_version"] != "notice-v2" || row["revoked_at"] != nil) {
				t.Fatalf("per-source row was merged/rewritten: %v", row)
			}
		}
		// 审计时间线可查,merged_by 记录操作者,undone 为空。
		merges := h.mustDo("GET", "/api/v1/merges", sessionOwnerA, tenantA, "", http.StatusOK)
		mrow := merges["items"].([]any)[0].(map[string]any)
		if mrow["merged_by"] == "" || mrow["merged_at"] == "" || mrow["undone_at"] != nil {
			t.Fatalf("merge audit row incomplete: %v", mrow)
		}
	})

	t.Run("post-merge consent replay on the target stays revoked", func(t *testing.T) {
		// 合并后重放 target 的旧 consent 事件(同 consent 键,新 event):
		// 撤销依旧不可恢复 —— 1691 红线在合并后逐字成立。
		target, source, _, tPhone, _ := h.mergePair(tenantA, "p3")
		h.mustDo("POST", "/api/v1/contacts/"+target+"/merge/"+source, sessionOwnerA, tenantA, "", http.StatusOK)
		body := fmt.Sprintf(`{"source_app":"touch","source_ns":"wx-oa","event_id":"evt-p3-T3","contact":{"name":"目标商家","phone":"%s","email":"source@shop.cn"},"business_category":"merchant_customer","consent":{"source_submission_ref":"sub-T","source_channel":"wx_oa","notice_version":"notice-v1","marketing_allowed":true}}`, tPhone)
		out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
		if out["class"] != "repeat_consult" {
			t.Fatalf("class = %v, want repeat_consult", out["class"])
		}
		list := h.mustDo("GET", "/api/v1/contacts/"+target+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		for _, it := range list["items"].([]any) {
			row := it.(map[string]any)
			if row["source_submission_ref"] == "sub-T" && row["revoked_at"] == nil {
				t.Fatalf("revoked consent revived through merge+replay: %v", row)
			}
		}
		// 重放只产生新 lead(真实咨询次数 +1),不触碰撤销行。
		if got := len(h.leadIDsForContact(sessionOwnerA, tenantA, target)); got != 4 {
			t.Fatalf("target lead count = %d, want 4 (2 既有 + 1 重指 + 1 重放)", got)
		}
	})

	t.Run("undo restores both sides and stamps the audit", func(t *testing.T) {
		target, source, sourceLead, _, sPhone := h.mergePair(tenantA, "p4")
		out := h.mustDo("POST", "/api/v1/contacts/"+target+"/merge/"+source, sessionOwnerA, tenantA, "", http.StatusOK)
		mergeID, _ := out["id"].(string)
		undone := h.mustDo("POST", "/api/v1/merges/"+mergeID+"/undo", sessionOwnerA, tenantA, "", http.StatusOK)
		if undone["undone_at"] == "" || undone["undone_by"] == "" {
			t.Fatalf("undo audit missing: %v", undone)
		}
		// source 复活,身份字段与快照一致。
		st, src := h.contactByID(sessionOwnerA, tenantA, source)
		if st != http.StatusOK {
			t.Fatalf("source not restored: %d", st)
		}
		if src["name"] != "源头商家" || src["phone"] != sPhone || src["email"] != "source@shop.cn" {
			t.Fatalf("source restored fields drifted: %v", src)
		}
		// target 回到合并前(空 email、名字/电话不变、coarse consent 还原)。
		_, tgt := h.contactByID(sessionOwnerA, tenantA, target)
		if tgt["email"] != "" || tgt["name"] != "目标商家" || tgt["consent_status"] != "granted" {
			t.Fatalf("target restore drifted: %v", tgt)
		}
		// leads 回指 source。
		if got := h.leadIDsForContact(sessionOwnerA, tenantA, source); len(got) != 1 || got[0] != sourceLead {
			t.Fatalf("source leads after undo = %v, want [%s]", got, sourceLead)
		}
		// consent 行回指 source;target 恢复自己的 1 行(sub-T,仍 revoked)。
		sList := h.mustDo("GET", "/api/v1/contacts/"+source+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		if got := len(sList["items"].([]any)); got != 1 {
			t.Fatalf("source consent rows after undo = %d, want 1", got)
		}
		tList := h.mustDo("GET", "/api/v1/contacts/"+target+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		tRows := tList["items"].([]any)
		if len(tRows) != 1 {
			t.Fatalf("target consent rows after undo = %d, want 1", len(tRows))
		}
		if tRows[0].(map[string]any)["source_submission_ref"] != "sub-T" {
			t.Fatalf("target rows after undo = %v", tRows)
		}
	})
}

// ---- undo 保护与边界 -----------------------------------------------------------------

func TestContactMergeUndoGuards(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()

	mk := func(name, phone string) string {
		return h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"name":%q,"phone":%q,"business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, name, phone), http.StatusCreated)["id"].(string)
	}

	t.Run("merge into itself is 400", func(t *testing.T) {
		a := mk("自合并", "13000000001")
		if status, _, _ := h.do("POST", "/api/v1/contacts/"+a+"/merge/"+a, sessionOwnerA, tenantA, ""); status != http.StatusBadRequest {
			t.Fatalf("self merge status %d, want 400", status)
		}
	})

	t.Run("cross-category merge is refused", func(t *testing.T) {
		a := mk("商家A", "13000000002")
		b := h.intake(sessionOwnerA, tenantA, `{"source_app":"touch","source_ns":"wx-oa","event_id":"evt-cc","contact":{"name":"创意B","phone":"13000000003"},"business_category":"creative_service"}`, http.StatusCreated)["contact_id"].(string)
		status, out, _ := h.do("POST", "/api/v1/contacts/"+a+"/merge/"+b, sessionOwnerA, tenantA, "")
		if status != http.StatusConflict || out["error"] != "category_mismatch" {
			t.Fatalf("cross-category merge = %d %v, want 409 category_mismatch", status, out)
		}
	})

	t.Run("unknown or cross-tenant contacts are 404", func(t *testing.T) {
		a := mk("正常商家", "13000000004")
		if status, _, _ := h.do("POST", "/api/v1/contacts/"+a+"/merge/con_missing", sessionOwnerA, tenantA, ""); status != http.StatusNotFound {
			t.Fatalf("unknown merge status %d, want 404", status)
		}
		b := h.mustDo("POST", "/api/v1/contacts", sessionOwnerB, tenantB,
			`{"name":"B家","phone":"13000000005","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, http.StatusCreated)["id"].(string)
		if status, _, _ := h.do("POST", "/api/v1/contacts/"+a+"/merge/"+b, sessionOwnerA, tenantA, ""); status != http.StatusNotFound {
			t.Fatalf("cross-tenant merge status %d, want 404", status)
		}
	})

	t.Run("undo refuses when the target was written after the merge", func(t *testing.T) {
		a, b := mk("后续写A", "13000000006"), mk("后续写B", "13000000007")
		out := h.mustDo("POST", "/api/v1/contacts/"+a+"/merge/"+b, sessionOwnerA, tenantA, "", http.StatusOK)
		mergeID, _ := out["id"].(string)
		h.mustDo("PATCH", "/api/v1/contacts/"+a, sessionOwnerA, tenantA, `{"notes":"合并后又写了跟进备注"}`, http.StatusOK)
		status, out2, _ := h.do("POST", "/api/v1/merges/"+mergeID+"/undo", sessionOwnerA, tenantA, "")
		if status != http.StatusConflict || out2["error"] != "merge_has_subsequent_writes" {
			t.Fatalf("undo after write = %d %v, want 409 merge_has_subsequent_writes", status, out2)
		}
		// 状态原样:b 仍是墓碑,合并未被撤销。
		if st, _ := h.contactByID(sessionOwnerA, tenantA, b); st != http.StatusNotFound {
			t.Fatalf("refused undo must not change state: b=%d", st)
		}
	})

	t.Run("undo is one-shot", func(t *testing.T) {
		a, b := mk("一次性A", "13000000008"), mk("一次性B", "13000000009")
		out := h.mustDo("POST", "/api/v1/contacts/"+a+"/merge/"+b, sessionOwnerA, tenantA, "", http.StatusOK)
		mergeID, _ := out["id"].(string)
		h.mustDo("POST", "/api/v1/merges/"+mergeID+"/undo", sessionOwnerA, tenantA, "", http.StatusOK)
		status, out2, _ := h.do("POST", "/api/v1/merges/"+mergeID+"/undo", sessionOwnerA, tenantA, "")
		if status != http.StatusConflict || out2["error"] != "merge_already_undone" {
			t.Fatalf("second undo = %d %v, want 409 merge_already_undone", status, out2)
		}
	})

	t.Run("merge surface is owner-only end to end", func(t *testing.T) {
		a, b := mk("权限A", "13000000010"), mk("权限B", "13000000011")
		posts := []string{
			"/api/v1/contacts/" + a + "/merge/" + b,
			"/api/v1/merges/mrg_x/undo",
		}
		gets := []string{"/api/v1/merge-candidates", "/api/v1/dedup/stats"}
		for _, path := range posts {
			if status, _, _ := h.do("POST", path, sessionSalesA1, tenantA, ""); status != http.StatusForbidden {
				t.Fatalf("sales POST %s, status %d, want 403", path, status)
			}
		}
		for _, path := range gets {
			if status, _, _ := h.do("GET", path, sessionSalesA1, tenantA, ""); status != http.StatusForbidden {
				t.Fatalf("sales GET %s, status %d, want 403", path, status)
			}
		}
	})
}

// ---- 候选池处置与统计 -----------------------------------------------------------------

func TestMergeCandidatesDismissAndStats(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	c1 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"候选一","phone":"13800007777","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, http.StatusCreated)["id"].(string)
	c2 := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"候选二","phone":"13800007777","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, http.StatusCreated)["id"].(string)
	out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx-oa", "evt-stats", "候选三", "13800007777", ""), http.StatusCreated)
	if out["class"] != "ambiguous" {
		t.Fatalf("class = %v, want ambiguous", out["class"])
	}
	c3, _ := out["contact_id"].(string)

	allCandidates := func() []map[string]any {
		h.t.Helper()
		list := h.mustDo("GET", "/api/v1/merge-candidates", sessionOwnerA, tenantA, "", http.StatusOK)
		items := list["items"].([]any)
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	pairRow := func(a, b string) map[string]any {
		h.t.Helper()
		x, y := a, b
		if x > y {
			x, y = y, x
		}
		for _, m := range allCandidates() {
			if m["contact_a"] == x && m["contact_b"] == y {
				return m
			}
		}
		h.t.Fatalf("candidate pair %s/%s not found", x, y)
		return nil
	}
	if got := len(allCandidates()); got != 3 {
		t.Fatalf("candidates = %d, want 3", got)
	}

	// dismiss (c1,c3) 对(人工判定不是同人)。
	row := pairRow(c1, c3)
	dismissed := h.mustDo("POST", "/api/v1/merge-candidates/"+row["id"].(string)+"/dismiss", sessionOwnerA, tenantA, "", http.StatusOK)
	if dismissed["status"] != "dismissed" || dismissed["resolved_by"] == "" {
		t.Fatalf("dismissed candidate = %v", dismissed)
	}
	if status, _, _ := h.do("POST", "/api/v1/merge-candidates/"+row["id"].(string)+"/dismiss", sessionOwnerA, tenantA, ""); status != http.StatusNotFound {
		t.Fatalf("re-dismiss status %d, want 404 (已处置)", status)
	}

	// 显式合并 (c1,c2) → 对应候选自动置 merged。
	if status, out2, _ := h.do("POST", "/api/v1/contacts/"+c1+"/merge/"+c2, sessionOwnerA, tenantA, ""); status != http.StatusOK {
		t.Fatalf("explicit merge failed: %d %v", status, out2)
	}
	if m := pairRow(c1, c2)["status"]; m != "merged" {
		t.Fatalf("merged pair candidate status = %v, want merged", m)
	}
	if m := pairRow(c2, c3)["status"]; m != "pending" {
		t.Fatalf("untouched pair = %v, want pending", m)
	}

	// 统计:intake 1 条(ambiguous);merges 1;pending 候选 1(c2,c3)。
	st := h.mustDo("GET", "/api/v1/dedup/stats", sessionOwnerA, tenantA, "", http.StatusOK)
	if st["intake_total"] != float64(1) {
		t.Fatalf("intake_total = %v, want 1", st["intake_total"])
	}
	byClass := st["by_class"].(map[string]any)
	if byClass["ambiguous"] != float64(1) {
		t.Fatalf("by_class = %v, want ambiguous=1", byClass)
	}
	if st["pending_candidates"] != float64(1) || st["merges_total"] != float64(1) || st["merges_undone"] != float64(0) {
		t.Fatalf("stats = %v, want pending=1 merges=1 undone=0", st)
	}

	// undo 后:该对候选回到 pending,合并计数保留 undone=1。
	merges := h.mustDo("GET", "/api/v1/merges", sessionOwnerA, tenantA, "", http.StatusOK)
	mergeID := merges["items"].([]any)[0].(map[string]any)["id"].(string)
	h.mustDo("POST", "/api/v1/merges/"+mergeID+"/undo", sessionOwnerA, tenantA, "", http.StatusOK)
	if m := pairRow(c1, c2)["status"]; m != "pending" {
		t.Fatalf("candidate after undo = %v, want pending again", m)
	}
	st2 := h.mustDo("GET", "/api/v1/dedup/stats", sessionOwnerA, tenantA, "", http.StatusOK)
	if st2["pending_candidates"] != float64(2) || st2["merges_undone"] != float64(1) {
		t.Fatalf("stats after undo = %v, want pending=2 undone=1", st2)
	}
	_ = c3
}
