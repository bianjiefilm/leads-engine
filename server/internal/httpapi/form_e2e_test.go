// HUI-1679 / FEAT-0180 验收主线(真实服务全流程):
// 发布表单 → 公共提交(marketing 独立勾选)→ contact+lead+consent 原子落库;
// 同 source_ref 重试 → 原 submission;伪造租户参数 → 被忽略;
// 未勾选营销 → marketing_allowed=false;schema 契约导出。
// 本文件是验收证据的一等公民:输出一段可引用的断言轨迹。
package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHUI1679FormV1Acceptance(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()

	// ---- 1. owner 发布表单(版本化,v1) --------------------------------
	created := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA,
		`{"form_key":"landing-main","store_id":"store-001"}`, 201)
	draftID, _ := created["id"].(string)
	fields := `{"schema_version":1,"consent_required":true,"fields":[{"name":"name","required":true},{"name":"phone","required":true},{"name":"wechat","required":false}]}`
	h.mustDo("PATCH", "/api/v1/forms/"+draftID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"notice_version":"privacy-2026-v3","purpose":"留资咨询与活动回访","marketing_prompt":"我同意接收该商家的营销信息(可随时撤销)","fields":%q}`, fields), 200)
	form := h.mustDo("POST", "/api/v1/forms/"+draftID+"/publish", sessionOwnerA, tenantA, "", 200)
	formID, _ := form["id"].(string)
	if form["status"] != "published" || form["published_at"] == "" {
		t.Fatalf("publish failed: %v", form)
	}
	t.Logf("[1] form published id=%s version=%d notice=privacy-2026-v3", formID, int(form["version"].(float64)))

	// ---- 2. schema 契约导出(对 T1 碰一碰复用的唯一接口) -----------------
	schema := h.mustDo("GET", "/api/v1/forms/"+formID+"/schema", sessionOwnerA, tenantA, "", 200)
	submitSpec := schema["submit"].(map[string]any)
	if submitSpec["path"] != "/api/v1/public/forms/"+formID+"/submissions" {
		t.Fatalf("schema submit path = %v", submitSpec["path"])
	}
	t.Logf("[2] schema contract: fields=%v limits=%v", schema["schema"].(map[string]any)["fields"], schema["limits"])

	// ---- 3. 公共提交(无会话;marketing 独立勾选)→ 原子落库 -------------
	path := "/api/v1/public/forms/" + formID + "/submissions"
	out := h.mustDo("POST", path, "", "", submitBody("客户甲", "13900006001", "wx_kehu", true, "douyin", "lead-20260919-001"), 201)
	if out["class"] != "new" {
		t.Fatalf("class = %v, want new", out["class"])
	}
	contactID, leadID, consentID := out["contact_id"].(string), out["lead_id"].(string), out["consent_id"].(string)
	if contactID == "" || leadID == "" || consentID == "" {
		t.Fatalf("atomic write incomplete: %v", out)
	}
	// contact 落在表单归属租户;lead 带来源/活动 provenance。
	if _, err := h.api.St.GetContact(contactID, tenantA); err != nil {
		t.Fatalf("contact not in the form owner tenant: %v", err)
	}
	lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", 200)
	if lead["contact_id"] != contactID {
		t.Fatalf("lead does not reference the contact: %v", lead)
	}
	consents := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", 200)
	c := consents["items"].([]any)[0].(map[string]any)
	if c["marketing_allowed"] != true || c["notice_version"] != "privacy-2026-v3" || c["purpose"] != "留资咨询与活动回访" {
		t.Fatalf("consent does not carry the form's notice/purpose: %v", c)
	}
	t.Logf("[3] submission atomic: contact=%s lead=%s consent=%s marketing=true", contactID, leadID, consentID)

	// ---- 4. 重试同 source_ref → 原 submission(200 幂等) -----------------
	retry := h.mustDo("POST", path, "", "", submitBody("客户甲", "13900006001", "wx_kehu", true, "douyin", "lead-20260919-001"), 200)
	if retry["submission_id"] != out["submission_id"] || retry["duplicate_kind"] != "idempotent" {
		t.Fatalf("retry did not answer the original submission: %v", retry)
	}
	t.Logf("[4] same source_ref retry -> original submission %v", retry["submission_id"])

	// ---- 5. 伪造租户参数(header B + body tenant 字段)→ 不改变接收租户 ----
	_, forged, _ := h.do("POST", path, "", tenantB,
		`{"name":"冒名","phone":"13900006002","tenant":"tnt_b","marketing_allowed":true,"source":"s","source_ref":"forged-1"}`)
	if forged["error"] != "unknown_fields" {
		t.Fatalf("forged tenant body key must be rejected: %v", forged)
	}
	okForgedHeader := h.mustDo("POST", path, "", tenantB,
		submitBody("客户乙", "13900006002", "", false, "zhihu", "lead-20260919-002"), 201)
	if _, err := h.api.St.GetContact(okForgedHeader["contact_id"].(string), tenantA); err != nil {
		t.Fatalf("forged X-Tenant-ID changed the receiving tenant")
	}
	t.Logf("[5] forged tenant params ignored: body key 400, header ignored, contact stays in tenant A")

	// ---- 6. 未勾选营销 → marketing_allowed=false(不进营销池) ------------
	if okForgedHeader["marketing_allowed"] != false {
		t.Fatalf("unchecked marketing must be false: %v", okForgedHeader)
	}
	cidB := okForgedHeader["contact_id"].(string)
	consentsB := h.mustDo("GET", "/api/v1/contacts/"+cidB+"/consents", sessionOwnerA, tenantA, "", 200)
	cB := consentsB["items"].([]any)[0].(map[string]any)
	if cB["marketing_allowed"] != false {
		t.Fatalf("marketing_allowed must be false: %v", cB)
	}
	t.Logf("[6] unchecked marketing -> consent marketing_allowed=false (建档,不进营销池)")

	// ---- 7. 撤销后重放:revoked 不变(1691 回归) ---------------------------
	h.mustDo("POST", "/api/v1/contacts/"+contactID+"/consents/"+consentID+"/revoke",
		sessionOwnerA, tenantA, `{"reason":"测试撤销"}`, 200)
	h.mustDo("POST", path, "", "", submitBody("客户甲", "13900006001", "", true, "douyin", "lead-20260919-001"), 200)
	consentsAgain := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", 200)
	cAgain := consentsAgain["items"].([]any)[0].(map[string]any)
	if rev, _ := cAgain["revoked_at"].(string); rev == "" {
		t.Fatalf("replay must not clear the revocation: %v", cAgain)
	}
	t.Logf("[7] revoke + replay -> revoked_at stays (%v)", cAgain["revoked_at"])

	// ---- 8. 重复咨询窗外不丢次数(intake 三分类) ----------------------------
	h2 := newHarness(t)
	tenant2, _, _ := h2.seed()
	form2 := publishFormViaAPI(t, h2, tenant2, "main", formFieldsWithWechat)
	path2 := "/api/v1/public/forms/" + form2["id"].(string) + "/submissions"
	body2 := submitBody("客户丙", "13900006003", "", false, "wx", "rl-ref")
	first2 := h2.mustDo("POST", path2, "", "", body2, 201)
	contact2, _ := first2["contact_id"].(string)
	h2.mustDo("POST", path2, "", "", submitBody("客户丙", "13900006003", "", false, "wx", "rl-ref-win"), 200) // 短窗内 → 幂等提示
	h2.api.FormResubmitWindow = time.Nanosecond
	re2 := h2.mustDo("POST", path2, "", "", submitBody("客户丙", "13900006003", "", false, "wx", "rl-ref-2"), 201)
	if re2["class"] != "repeat_consult" {
		t.Fatalf("re-consult class = %v", re2["class"])
	}
	if re2["contact_id"] != contact2 {
		t.Fatalf("re-consult must attach to the existing contact: %v vs %v", re2["contact_id"], contact2)
	}
	if re2["lead_id"] == first2["lead_id"] {
		t.Fatal("re-consult must create a NEW lead")
	}
	t.Logf("[8] window hit -> original submission; outside-window re-consult -> repeat_consult, new lead %v (次数不丢)", re2["lead_id"])

	// ---- 9. 限流:第 31 次/min → 429 ---------------------------------------
	// 第 8 步已用 3 次配额;再补 27 次到 30,第 31 次必须 429。
	for i := 0; i < 27; i++ {
		h2.mustDo("POST", path2, "", "", body2, 200) // 同键幂等,但消耗限流配额
	}
	h2.mustDo("POST", path2, "", "", body2, 429)
	t.Logf("[9] rate limit: 30/min ok, 31st -> 429")

	// ---- 10. web 纪律:提交 URL 不承载联系方式/租户 -------------------------
	if strings.Contains(path, "139") || strings.Contains(path, "tenant") {
		t.Fatal("contact data / tenant must never appear in the submission URL")
	}
	t.Logf("[10] submission path %s carries no contact data and no tenant (BFF catch-all relays as-is)", path)
}
