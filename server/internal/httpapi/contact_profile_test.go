// HUI-1691 / FEAT-0192 客户档案域测试:
//   - consent 按 (contact, 来源提交, 渠道) 维度保留,撤销持久 —— 重放旧事件
//     只能幂等返回,revoked_at 逐字节不变,绝不恢复营销权限;
//   - 权限矩阵全覆盖(输入/读取/修改/导出/删除/停用成员后访问);
//   - 日志零 PII:手机号/跟进全文/备注全文不入日志(断言);
//   - URL 纪律:phone 查询参数与疑似手机号搜索值一律 400;
//   - 软删除:脱敏占位、全角色 404、consents/followups 行保留(最小审计);
//   - A/B 商家隔离(同一销售跨租户切换);
//   - 保存/重登恢复。
package httpapi

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/db"
	"github.com/bianjiefilm/leads-engine/server/internal/provision"
)

// consentState extracts state + id + revoked_at from an upsert response.
func consentState(t *testing.T, out map[string]any) (string, string, string) {
	t.Helper()
	state, _ := out["state"].(string)
	cs, _ := out["consent"].(map[string]any)
	id, _ := cs["id"].(string)
	revokedAt, _ := cs["revoked_at"].(string)
	if id == "" {
		t.Fatalf("consent response missing id: %v", out)
	}
	return state, id, revokedAt
}

// grantConsent records a consent and fails the test on non-200.
func (h *harness) grantConsent(t *testing.T, session, tenant, contactID, ref, channel, notice string, marketing bool) map[string]any {
	t.Helper()
	body := fmt.Sprintf(`{"source_submission_ref":%q,"source_channel":%q,"notice_version":%q,"purpose":"marketing","marketing_allowed":%t}`,
		ref, channel, notice, marketing)
	return h.mustDo("POST", "/api/v1/contacts/"+contactID+"/consents", session, tenant, body, http.StatusOK)
}

// ---- consent 生命周期:多来源并存、撤销持久、重放不恢复 -------------------------------

func TestConsentLifecyclePerSource(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contact := h.seed()

	t.Run("two sources keep independent consent rows", func(t *testing.T) {
		out1 := h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-100", "landing_page", "notice-v1", true)
		if state, _, _ := consentState(t, out1); state != "created" {
			t.Fatalf("first consent state = %q, want created", state)
		}
		h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-200", "offline_event", "notice-v2", true)
		list := h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		items := list["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("consent rows = %d, want 2", len(items))
		}
		summary := list["summary"].(map[string]any)
		if summary["marketing_active"] != float64(2) || summary["revoked"] != float64(0) {
			t.Fatalf("summary = %v, want 2 active / 0 revoked", summary)
		}
	})

	// 撤销来源 A;来源 B 独立不受影响。
	var consentA, revokedAtA string
	t.Run("revoking source A does not touch source B", func(t *testing.T) {
		list := h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		for _, it := range list["items"].([]any) {
			m := it.(map[string]any)
			if m["source_submission_ref"] == "sub-100" {
				consentA, _ = m["id"].(string)
			}
		}
		if consentA == "" {
			t.Fatal("consent A not found")
		}
		out := h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents/"+consentA+"/revoke",
			sessionOwnerA, tenantA, `{"reason":"客户明确拒接营销电话"}`, http.StatusOK)
		if out["changed"] != true || out["status"] != "revoked" {
			t.Fatalf("revoke A = %v", out)
		}
		cs := out["consent"].(map[string]any)
		revokedAtA, _ = cs["revoked_at"].(string)
		if revokedAtA == "" {
			t.Fatal("revoked_at not stamped")
		}
		list = h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		summary := list["summary"].(map[string]any)
		if summary["marketing_active"] != float64(1) || summary["revoked"] != float64(1) {
			t.Fatalf("summary = %v, want 1 active (B) / 1 revoked (A)", summary)
		}
	})

	t.Run("RED LINE: replaying the revoked event cannot restore it", func(t *testing.T) {
		out := h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-100", "landing_page", "notice-v1", true)
		state, _, revokedAfter := consentState(t, out)
		if state != "replay_unchanged" {
			t.Fatalf("replay state = %q, want replay_unchanged", state)
		}
		if revokedAfter != revokedAtA {
			t.Fatalf("revoked_at changed on replay: %q -> %q (revocation must be persistent)", revokedAtA, revokedAfter)
		}
		cs := out["consent"].(map[string]any)
		if cs["status"] != "revoked" && out["status"] != "revoked" {
			t.Fatalf("replayed consent must stay revoked: %v", out)
		}
		// 刷新告知版本也必须被拒绝(重放零写入)
		if cs["notice_version"] != "notice-v1" {
			t.Fatalf("replay must not refresh notice_version, got %v", cs["notice_version"])
		}
	})

	t.Run("double revoke is idempotent: original marker kept", func(t *testing.T) {
		out := h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents/"+consentA+"/revoke",
			sessionOwnerA, tenantA, `{"reason":"再撤一次"}`, http.StatusOK)
		if out["changed"] != false {
			t.Fatalf("double revoke must not change anything: %v", out)
		}
		cs := out["consent"].(map[string]any)
		if cs["revoked_at"] != revokedAtA {
			t.Fatalf("revoked_at moved: %v -> %v", revokedAtA, cs["revoked_at"])
		}
	})

	t.Run("stop-marketing revokes the other source; replays stay dead; idempotent", func(t *testing.T) {
		out := h.mustDo("POST", "/api/v1/contacts/"+contact+"/revoke-marketing",
			sessionOwnerA, tenantA, `{"reason":"客户要求停止营销"}`, http.StatusOK)
		if out["marketing_revoked"] != true || out["sources_revoked"] != float64(1) {
			t.Fatalf("revoke-marketing = %v, want 1 source newly revoked (B)", out)
		}
		// 来源 B 的事件重放同样不得恢复
		outB := h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-200", "offline_event", "notice-v2", true)
		if state, _, revokedB := consentState(t, outB); state != "replay_unchanged" || revokedB == "" {
			t.Fatalf("source B replay = %v", outB)
		}
		// 幂等:再停一次,没有新撤销
		out2 := h.mustDo("POST", "/api/v1/contacts/"+contact+"/revoke-marketing",
			sessionOwnerA, tenantA, `{"reason":"again"}`, http.StatusOK)
		if out2["sources_revoked"] != float64(0) {
			t.Fatalf("second revoke-marketing = %v, want 0", out2)
		}
		list := h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
		summary := list["summary"].(map[string]any)
		if summary["revoked"] != float64(2) || summary["marketing_active"] != float64(0) {
			t.Fatalf("final summary = %v, want all revoked", summary)
		}
	})

	t.Run("CRM consent/followup calls never create members (identity discipline)", func(t *testing.T) {
		before := h.memberCount(tenantA)
		h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-300", "webinar", "notice-v1", true)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionOwnerA, tenantA,
			`{"note":"例会后跟进"}`, http.StatusCreated)
		if after := h.memberCount(tenantA); after != before {
			t.Fatalf("member count changed %d -> %d", before, after)
		}
	})

	t.Run("validation: missing submission ref / empty note / oversize reason", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents", sessionOwnerA, tenantA,
			`{"source_channel":"x"}`, http.StatusBadRequest)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionOwnerA, tenantA,
			`{"note":"   "}`, http.StatusBadRequest)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/revoke-marketing", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"reason":%q}`, strings.Repeat("长", 201)), http.StatusBadRequest)
		h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-over", "", "", false) // channel/notice 可空,ref 必填已过
	})
}

// ---- 权限矩阵:consents / followups / export / delete ---------------------------

func TestContactProfilePermissionMatrix(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contact := h.seed()

	t.Run("consent read/write: owner and assignee yes, non-assignee 404, cross-tenant 403, disabled 403", func(t *testing.T) {
		h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-m1", "form", "notice-v1", true)
		h.grantConsent(t, sessionSalesA1, tenantA, contact, "sub-m2", "referral", "notice-v1", true)
		h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionSalesA1, tenantA, "", http.StatusOK)
		// 非 assignee 一律 404 掩码(存在性不可探测)
		h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents", sessionSalesA2, tenantA,
			`{"source_submission_ref":"probe"}`, http.StatusNotFound)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/revoke-marketing", sessionSalesA2, tenantA,
			`{}`, http.StatusNotFound)
		// 跨租户:owner B 在租户 A 无成员行 → 403
		h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents", sessionOwnerB, tenantA,
			`{"source_submission_ref":"cross"}`, http.StatusForbidden)
		// 停用成员 403
		h.mustDo("GET", "/api/v1/contacts/"+contact+"/consents", sessionDisabled, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents", sessionDisabled, tenantA,
			`{"source_submission_ref":"dead"}`, http.StatusForbidden)
	})

	t.Run("followups follow the same matrix", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionSalesA1, tenantA,
			`{"note":"电话沟通顺利"}`, http.StatusCreated)
		h.mustDo("GET", "/api/v1/contacts/"+contact+"/followups", sessionSalesA1, tenantA, "", http.StatusOK)
		h.mustDo("GET", "/api/v1/contacts/"+contact+"/followups", sessionSalesA2, tenantA, "", http.StatusNotFound)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionSalesA2, tenantA,
			`{"note":"不该写进去"}`, http.StatusNotFound)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionOwnerB, tenantA,
			`{"note":"跨租户"}`, http.StatusForbidden)
		h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionDisabled, tenantA,
			`{"note":"停用"}`, http.StatusForbidden)
	})

	t.Run("CSV export is owner-only and streams profile fields", func(t *testing.T) {
		status, raw, headers := h.doRaw("GET", "/api/v1/contacts/export?format=csv", sessionOwnerA, tenantA, "")
		if status != http.StatusOK {
			t.Fatalf("owner export status = %d", status)
		}
		if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Fatalf("content-type = %q", ct)
		}
		body := string(raw)
		if !strings.Contains(body, "id,name,phone,email") {
			t.Fatalf("csv header missing: %q", body[:min(120, len(body))])
		}
		if !strings.Contains(body, "甲商家") {
			t.Fatalf("csv must contain tenant contacts: %q", body)
		}
		// sales / disabled / 跨租户 / agent(即使有 grant)一律 403
		h.mustDo("GET", "/api/v1/contacts/export?format=csv", sessionSalesA1, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/contacts/export?format=csv", sessionDisabled, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/contacts/export?format=csv", sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"principal_ref":%q}`, principalAgentA), http.StatusCreated)
		h.mustDo("GET", "/api/v1/contacts/export?format=csv", sessionAgentA, tenantA, "", http.StatusForbidden)
		// format 必填且仅 csv
		h.mustDo("GET", "/api/v1/contacts/export", sessionOwnerA, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts/export?format=json", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	})

	t.Run("soft delete is owner-only and masks the tombstone", func(t *testing.T) {
		created := h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
			`{"name":"待删客户","phone":"13677778888","email":"del@x.cn","business_category":"merchant_customer",
			  "source_type":"manual","consent_status":"pending","notes":"删前备注","tags":"vip,重点"}`,
			http.StatusCreated)
		delID, _ := created["id"].(string)
		h.grantConsent(t, sessionSalesA1, tenantA, delID, "sub-del", "form", "notice-v1", true)
		h.mustDo("POST", "/api/v1/contacts/"+delID+"/followups", sessionSalesA1, tenantA,
			`{"note":"删除前跟进"}`, http.StatusCreated)

		// sales(即使 assignee)不可删除
		h.mustDo("DELETE", "/api/v1/contacts/"+delID, sessionSalesA1, tenantA, "", http.StatusForbidden)
		// 非 assignee 403(delete 不区分 assignee,统一 owner-only,不泄漏拓扑)
		h.mustDo("DELETE", "/api/v1/contacts/"+delID, sessionSalesA2, tenantA, "", http.StatusForbidden)

		out := h.mustDo("DELETE", "/api/v1/contacts/"+delID, sessionOwnerA, tenantA, "", http.StatusOK)
		if out["deleted"] != true {
			t.Fatalf("delete = %v", out)
		}
		// 删除后全角色 404:owner、assignee、consents、followups、PATCH
		h.mustDo("GET", "/api/v1/contacts/"+delID, sessionOwnerA, tenantA, "", http.StatusNotFound)
		h.mustDo("GET", "/api/v1/contacts/"+delID, sessionSalesA1, tenantA, "", http.StatusNotFound)
		h.mustDo("GET", "/api/v1/contacts/"+delID+"/consents", sessionOwnerA, tenantA, "", http.StatusNotFound)
		h.mustDo("GET", "/api/v1/contacts/"+delID+"/followups", sessionOwnerA, tenantA, "", http.StatusNotFound)
		h.mustDo("PATCH", "/api/v1/contacts/"+delID, sessionOwnerA, tenantA, `{"name":"复活"}`, http.StatusNotFound)
		// 列表不再出现
		list := h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, tenantA, "", http.StatusOK)
		for _, it := range list["items"].([]any) {
			if it.(map[string]any)["id"] == delID {
				t.Fatal("deleted contact still listed")
			}
		}
		// 导出不含已删行
		_, raw, _ := h.doRaw("GET", "/api/v1/contacts/export?format=csv", sessionOwnerA, tenantA, "")
		if strings.Contains(string(raw), delID) {
			t.Fatal("deleted contact still in export")
		}
		// 最小审计:consents/followups 行保留;档案字段已脱敏占位
		d, err := db.Open(h.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		var consents, followups int
		var name, phone, notes string
		if err := d.QueryRow(`SELECT COUNT(1) FROM contact_consents WHERE contact_id=?`, delID).Scan(&consents); err != nil {
			t.Fatal(err)
		}
		if err := d.QueryRow(`SELECT COUNT(1) FROM contact_followups WHERE contact_id=?`, delID).Scan(&followups); err != nil {
			t.Fatal(err)
		}
		if consents != 1 || followups != 1 {
			t.Fatalf("audit rows lost: consents=%d followups=%d", consents, followups)
		}
		if err := d.QueryRow(`SELECT name, phone, notes FROM contacts WHERE id=?`, delID).Scan(&name, &phone, &notes); err != nil {
			t.Fatal(err)
		}
		if name != "[已删除联系人]" || phone != "" || notes != "" {
			t.Fatalf("tombstone not masked: name=%q phone=%q notes=%q", name, phone, notes)
		}
	})
}

// ---- 停用成员:历史 followups 保留、不可再写 --------------------------------------

func TestDisabledMemberFollowupsPreserved(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contact := h.seed()
	f := h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionSalesA1, tenantA,
		`{"note":"停用前的跟进记录"}`, http.StatusCreated)
	authorID, _ := f["member_id"].(string)
	if authorID == "" {
		t.Fatalf("followup missing member_id: %v", f)
	}
	sales1 := h.memberID(tenantA, principalSalesA1)
	h.mustDo("PATCH", "/api/v1/admin/members/"+sales1, sessionOwnerA, tenantA,
		`{"enabled":false}`, http.StatusOK)
	// 停用后 403,不可再追加/编辑(时间线本身无编辑端点)
	h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionSalesA1, tenantA,
		`{"note":"停用后写入"}`, http.StatusForbidden)
	// 历史跟进保留,owner 仍可读,作者字段不变
	items := h.mustDo("GET", "/api/v1/contacts/"+contact+"/followups", sessionOwnerA, tenantA, "", http.StatusOK)["items"].([]any)
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["member_id"] == authorID && m["note"] == "停用前的跟进记录" {
			found = true
		}
	}
	if !found {
		t.Fatalf("disabled member's followup lost: %v", items)
	}
}

// ---- URL 纪律:phone 参数与疑似手机号搜索值一律 400 --------------------------------

func TestContactSearchURIDiscipline(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()

	t.Run("phone query parameter is rejected outright", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts?phone=13812345678", sessionOwnerA, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts?phone=x", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	})
	t.Run("phone-like search values are rejected (7+ consecutive digits)", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts?name=13812345678", sessionOwnerA, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/contacts?tag=1234567", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	})
	t.Run("name/tag search works and stays tenant/assignee scoped", func(t *testing.T) {
		out := h.mustDo("GET", "/api/v1/contacts?name="+"%E7%94%B2", sessionOwnerA, tenantA, "", http.StatusOK) // "甲"
		if got := len(out["items"].([]any)); got != 1 {
			t.Fatalf("name search hits = %d, want 1", got)
		}
		out = h.mustDo("GET", "/api/v1/contacts?name=不存在", sessionOwnerA, tenantA, "", http.StatusOK)
		if got := len(out["items"].([]any)); got != 0 {
			t.Fatalf("name search hits = %d, want 0", got)
		}
	})
	t.Run("tags roundtrip: normalized, searchable by single tag", func(t *testing.T) {
		created := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
			`{"name":"标签客","business_category":"merchant_customer","source_type":"manual",
			  "tags":" vip ， 重点 ， vip , 华东 "}`, http.StatusCreated)
		if got := created["tags"]; got != "vip,重点,华东" {
			t.Fatalf("tags normalized = %v, want vip,重点,华东", got)
		}
		out := h.mustDo("GET", "/api/v1/contacts?tag=重点", sessionOwnerA, tenantA, "", http.StatusOK)
		if len(out["items"].([]any)) != 1 {
			t.Fatalf("tag search should hit exactly the tagged contact")
		}
	})
}

// ---- 日志零 PII(断言) -----------------------------------------------------------

func TestLogsNeverCarryPIIOrFollowupContent(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{captureLog: true})
	tenantA, _, contact := h.seed()

	h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
		`{"name":"日志客","phone":"13755556666","email":"log@x.cn","business_category":"merchant_customer",
		  "source_type":"manual","notes":"NOTES-MARKER-AB12 客户住址某某路","tags":"secret-tag"}`,
		http.StatusCreated)
	h.mustDo("POST", "/api/v1/contacts/"+contact+"/followups", sessionOwnerA, tenantA,
		`{"note":"FOLLOWUP-MARKER-QQ77 客户预算与家庭情况……"}`, http.StatusCreated)
	out := h.grantConsent(t, sessionOwnerA, tenantA, contact, "sub-log", "form", "notice-v1", true)
	_, cid, _ := consentState(t, out)
	h.mustDo("POST", "/api/v1/contacts/"+contact+"/consents/"+cid+"/revoke",
		sessionOwnerA, tenantA, `{"reason":"REVOKE-MARKER-ZZ99"}`, http.StatusOK)
	status, _, _ := h.doRaw("GET", "/api/v1/contacts/export?format=csv", sessionOwnerA, tenantA, "")
	if status != http.StatusOK {
		t.Fatalf("export status = %d", status)
	}

	logs := h.logs.String()
	for _, forbidden := range []string{
		"13755556666",          // 手机号明文
		"13812345678",          // seed 手机号明文
		"log@x.cn",             // 邮箱明文
		"FOLLOWUP-MARKER-QQ77", // 跟进全文
		"NOTES-MARKER-AB12",    // 备注全文
		"客户预算与家庭情况",            // 跟进内容片段
		"REVOKE-MARKER-ZZ99",   // 撤销原因全文
		"客户住址",                 // 备注内容片段
	} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("log leaks %q:\n%s", forbidden, logs)
		}
	}
	for _, required := range []string{
		"137****6666",            // 手机号掩码形态
		"contact export tenant=", // 导出审计行
		"note=redacted(len=",     // 跟进长度摘要
	} {
		if !strings.Contains(logs, required) {
			t.Fatalf("log missing %q:\n%s", required, logs)
		}
	}
}

// ---- A/B 商家隔离:同一销售跨租户切换按各自租户权限工作 --------------------------------

func TestCrossTenantSalesIsolation(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, contactA := h.seed()
	// salesA1 同时是租户 B 的成员(跨租户切换场景)
	h.provision("member", tenantB, principalSalesA1, "sales", "Sales A1 dual tenant")

	// 在租户 B 建自己的档案与授权(自动 assign 自己)
	createdB := h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantB,
		`{"name":"B租户客户","business_category":"merchant_customer","source_type":"manual"}`, http.StatusCreated)
	contactB, _ := createdB["id"].(string)
	h.grantConsent(t, sessionSalesA1, tenantB, contactB, "sub-b1", "walk_in", "notice-v1", true)

	t.Run("list is per-tenant: A records never appear in B and vice versa", func(t *testing.T) {
		listA := h.mustDo("GET", "/api/v1/contacts", sessionSalesA1, tenantA, "", http.StatusOK)
		for _, it := range listA["items"].([]any) {
			if it.(map[string]any)["id"] == contactB {
				t.Fatal("tenant B record leaked into tenant A list")
			}
		}
		listB := h.mustDo("GET", "/api/v1/contacts", sessionSalesA1, tenantB, "", http.StatusOK)
		for _, it := range listB["items"].([]any) {
			if it.(map[string]any)["id"] == contactA {
				t.Fatal("tenant A record leaked into tenant B list")
			}
		}
		if got := len(listB["items"].([]any)); got != 1 {
			t.Fatalf("tenant B list = %d records, want 1 (own only)", got)
		}
	})

	t.Run("consents are tenant-bound: B context cannot read A consent rows", func(t *testing.T) {
		// contactA 不属于租户 B → 404;租户 B 上下文拿不到租户 A 的授权明细
		h.mustDo("GET", "/api/v1/contacts/"+contactA+"/consents", sessionSalesA1, tenantB, "", http.StatusNotFound)
		// 反向同理
		h.mustDo("GET", "/api/v1/contacts/"+contactB+"/consents", sessionSalesA1, tenantA, "", http.StatusNotFound)
	})

	t.Run("export stays owner-only in both tenants for the dual-tenant sales", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/contacts/export?format=csv", sessionSalesA1, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/contacts/export?format=csv", sessionSalesA1, tenantB, "", http.StatusForbidden)
	})
}

// ---- 保存/重登恢复:consent + followup 跨进程重启,重放红线在重启后仍成立 -------------

func TestContactProfilePersistenceAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "leads.db")
	fake := fakeIdentity(t)
	mkCfg := func() config.Config {
		return config.Load(func(k string) string {
			switch k {
			case "LEADS_DB_PATH":
				return dbPath
			case "LEADS_INTERNAL_TOKEN":
				return "tok"
			case "PLATFORM_IDENTITY_BASE_URL":
				return fake.URL
			case "PLATFORM_IDENTITY_TOKEN":
				return "tok"
			}
			return ""
		})
	}
	tenantA, err := provision.Tenant(dbPath, "Persist A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provision.Member(dbPath, tenantA, principalOwnerA, "owner", "Owner A", true); err != nil {
		t.Fatal(err)
	}

	s1, err := Open(mkCfg(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	hs1 := httptest.NewServer(s1.Handler())
	h1 := &harness{t: t, srv: hs1, cfg: mkCfg(), identitySrv: fake, dbPath: dbPath, internalToken: "tok"}
	created := h1.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"档案留存客","business_category":"merchant_customer","source_type":"manual","consent_status":"granted"}`,
		http.StatusCreated)
	contactID, _ := created["id"].(string)
	h1.grantConsent(t, sessionOwnerA, tenantA, contactID, "sub-p1", "landing_page", "notice-v1", true)
	h1.mustDo("POST", "/api/v1/contacts/"+contactID+"/followups", sessionOwnerA, tenantA,
		`{"note":"重启前跟进"}`, http.StatusCreated)
	hs1.Close()
	s1.Close()

	s2, err := Open(mkCfg(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	hs2 := httptest.NewServer(s2.Handler())
	t.Cleanup(func() { hs2.Close() })
	h2 := &harness{t: t, srv: hs2, cfg: mkCfg(), identitySrv: fake, dbPath: dbPath, internalToken: "tok"}

	got := h2.mustDo("GET", "/api/v1/contacts/"+contactID, sessionOwnerA, tenantA, "", http.StatusOK)
	if got["name"] != "档案留存客" {
		t.Fatalf("contact lost across restart: %v", got)
	}
	consents := h2.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", http.StatusOK)
	if n := len(consents["items"].([]any)); n != 1 {
		t.Fatalf("consents lost across restart: %d rows", n)
	}
	followups := h2.mustDo("GET", "/api/v1/contacts/"+contactID+"/followups", sessionOwnerA, tenantA, "", http.StatusOK)
	if n := len(followups["items"].([]any)); n != 1 {
		t.Fatalf("followups lost across restart: %d rows", n)
	}
	// 重启后撤销,再重放旧事件:红线仍成立
	list := consents["items"].([]any)
	cid, _ := list[0].(map[string]any)["id"].(string)
	rev := h2.mustDo("POST", "/api/v1/contacts/"+contactID+"/consents/"+cid+"/revoke",
		sessionOwnerA, tenantA, `{"reason":"重启后撤销"}`, http.StatusOK)
	revokedAt, _ := rev["consent"].(map[string]any)["revoked_at"].(string)
	out := h2.grantConsent(t, sessionOwnerA, tenantA, contactID, "sub-p1", "landing_page", "notice-v1", true)
	if state, _, after := consentState(t, out); state != "replay_unchanged" || after != revokedAt {
		t.Fatalf("post-restart replay broke the red line: state=%q revoked_at %q -> %q", state, revokedAt, after)
	}
}
