// HUI-1679 / FEAT-0180 表单域 HTTP 面:管理面权限矩阵、发布/版本/不可变规则、
// schema 契约、公共提交的校验/幂等/短窗/租户归属/状态门/限流,与 1691 撤销回归。
// 全部走真实服务路径(fake identity + 真 sqlite)。
package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// publishFormViaAPI drives draft -> patch -> publish through the real owner API.
func publishFormViaAPI(t *testing.T, h *harness, tenant, formKey, fields string) map[string]any {
	t.Helper()
	created := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenant,
		fmt.Sprintf(`{"form_key":%q}`, formKey), 201)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("form create: no id in %v", created)
	}
	patch := fmt.Sprintf(`{"notice_version":"v1.2","purpose":"留资咨询与回访","marketing_prompt":"是否愿意接收营销信息?",
	  "fields":%q}`, fields)
	h.mustDo("PATCH", "/api/v1/forms/"+id, sessionOwnerA, tenant, patch, 200)
	return h.mustDo("POST", "/api/v1/forms/"+id+"/publish", sessionOwnerA, tenant, "", 200)
}

const formFieldsWithWechat = `{"schema_version":1,"consent_required":true,"fields":[{"name":"name","required":true},{"name":"phone","required":true},{"name":"wechat","required":false}]}`

func submitBody(name, phone, wechat string, marketing bool, source, ref string) string {
	return fmt.Sprintf(`{"name":%q,"phone":%q,"wechat":%q,"marketing_allowed":%t,"source":%q,"source_ref":%q}`,
		name, phone, wechat, marketing, source, ref)
}

// ---- 管理面 ----------------------------------------------------------------------

func TestFormAdminCRUDMatrix(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()

	t.Run("sales and agent cannot manage forms (owner 专属)", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/forms", sessionSalesA1, tenantA, `{}`, 403)
		h.provision("grant", tenantA, principalAgentA)
		h.mustDo("POST", "/api/v1/forms", sessionAgentA, tenantA, `{}`, 403)
		h.mustDo("GET", "/api/v1/forms", sessionSalesA1, tenantA, "", 403)
	})

	created := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA,
		`{"form_key":"main"}`, 201)
	formID, _ := created["id"].(string)

	t.Run("draft starts at version 1 with fixed default fields", func(t *testing.T) {
		if created["status"] != "draft" {
			t.Fatalf("status = %v, want draft", created["status"])
		}
		if int(created["version"].(float64)) != 1 {
			t.Fatalf("version = %v, want 1", created["version"])
		}
	})

	t.Run("publish without notice/purpose is refused (发布校验)", func(t *testing.T) {
		out := h.mustDo("POST", "/api/v1/forms/"+formID+"/publish", sessionOwnerA, tenantA, "", 400)
		if out["error"] != "form_incomplete" {
			t.Fatalf("error = %v, want form_incomplete", out["error"])
		}
	})

	t.Run("draft is editable; publish freezes the version", func(t *testing.T) {
		h.mustDo("PATCH", "/api/v1/forms/"+formID, sessionOwnerA, tenantA,
			fmt.Sprintf(`{"notice_version":"v1.2","purpose":"留资咨询","fields":%q}`, formFieldsWithWechat), 200)
		pub := h.mustDo("POST", "/api/v1/forms/"+formID+"/publish", sessionOwnerA, tenantA, "", 200)
		if pub["status"] != "published" {
			t.Fatalf("status = %v, want published", pub["status"])
		}
		h.mustDo("PATCH", "/api/v1/forms/"+formID, sessionOwnerA, tenantA,
			`{"purpose":"越权改"}`, 409)
	})

	t.Run("same family increments the version; other keys start at 1", func(t *testing.T) {
		v2 := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA, `{"form_key":"main"}`, 201)
		if int(v2["version"].(float64)) != 2 {
			t.Fatalf("v2 version = %v, want 2", v2["version"])
		}
		other := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA, `{"form_key":"qr"}`, 201)
		if int(other["version"].(float64)) != 1 {
			t.Fatalf("qr version = %v, want 1", other["version"])
		}
	})

	t.Run("form_key charset discipline", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA, `{"form_key":"bad key!"}`, 400)
	})

	t.Run("list and detail are tenant-scoped", func(t *testing.T) {
		list := h.mustDo("GET", "/api/v1/forms", sessionOwnerA, tenantA, "", 200)
		items := list["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("items = %d, want 3", len(items))
		}
		h.mustDo("GET", "/api/v1/forms/"+formID, sessionOwnerA, tenantA, "", 200)
		// 跨租户 owner B 只会得到 404(不泄露存在性)
		h.mustDo("GET", "/api/v1/forms/"+formID, sessionOwnerB, tenantB, "", 404)
	})

	t.Run("schema contract export (owner only, T1 对齐锚)", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/forms/"+formID+"/schema", sessionSalesA1, tenantA, "", 403)
		schema := h.mustDo("GET", "/api/v1/forms/"+formID+"/schema", sessionOwnerA, tenantA, "", 200)
		if schema["submit"] == nil || schema["limits"] == nil || schema["schema"] == nil {
			t.Fatalf("schema contract incomplete: %v", schema)
		}
		sch := schema["schema"].(map[string]any)
		fields := sch["fields"].([]any)
		if len(fields) != 3 {
			t.Fatalf("fields = %v, want name/phone/wechat", fields)
		}
		limits := schema["limits"].(map[string]any)
		if int(limits["per_ip_per_form_per_minute"].(float64)) != 30 {
			t.Fatalf("rate limit in contract = %v, want 30", limits["per_ip_per_form_per_minute"])
		}
	})

	t.Run("disable stops a published form and blocks re-publish", func(t *testing.T) {
		dis := h.mustDo("POST", "/api/v1/forms/"+formID+"/disable", sessionOwnerA, tenantA, "", 200)
		if dis["status"] != "disabled" {
			t.Fatalf("status = %v, want disabled", dis["status"])
		}
		h.mustDo("POST", "/api/v1/forms/"+formID+"/publish", sessionOwnerA, tenantA, "", 409)
	})
}

// ---- 公共渲染描述符 -------------------------------------------------------------

func TestPublicFormDescriptor(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	created := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA, `{"form_key":"main"}`, 201)
	formID, _ := created["id"].(string)

	t.Run("draft answers 404 (公共不可见)", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/public/forms/"+formID, "", "", "", 404)
	})
	publishFormViaAPI(t, h, tenantA, "main", formFieldsWithWechat)
	// formID 是该族 version 1 的 id;publishFormViaAPI 新建了 version 2,取它的 id。
	list := h.mustDo("GET", "/api/v1/forms", sessionOwnerA, tenantA, "", 200)
	var publishedID string
	for _, it := range list["items"].([]any) {
		f := it.(map[string]any)
		if f["status"] == "published" && int(f["version"].(float64)) == 2 {
			publishedID, _ = f["id"].(string)
		}
	}
	t.Run("descriptor carries the marketing surface only", func(t *testing.T) {
		desc := h.mustDo("GET", "/api/v1/public/forms/"+publishedID, "", "", "", 200)
		if desc["notice_version"] != "v1.2" || desc["marketing_default"] != false {
			t.Fatalf("descriptor wrong: %v", desc)
		}
		if _, hasTenant := desc["tenant_id"]; hasTenant {
			t.Fatal("public descriptor must not carry the tenant id")
		}
		sch := desc["schema"].(map[string]any)
		if len(sch["fields"].([]any)) != 3 {
			t.Fatalf("fields = %v", sch["fields"])
		}
	})
	t.Run("unknown form answers 404", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/public/forms/frm_missing", "", "", "", 404)
	})
}

// ---- 公共提交:状态门/校验/租户归属 --------------------------------------------------

func TestPublicFormSubmitGating(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()

	// 草稿:404
	draft := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA, `{"form_key":"g"}`, 201)
	draftID, _ := draft["id"].(string)
	h.mustDo("POST", "/api/v1/public/forms/"+draftID+"/submissions", "", "",
		submitBody("甲", "13900001001", "", false, "wx", "r1"), 404)

	form := publishFormViaAPI(t, h, tenantA, "g", formFieldsWithWechat)
	formID, _ := form["id"].(string)

	t.Run("happy path: no session, no tenant header", func(t *testing.T) {
		out := h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			submitBody("张三", "139 0000 1001", "wx_zhang", true, "wx_post", "camp-1"), 201)
		if out["class"] != "new" || out["marketing_allowed"] != true {
			t.Fatalf("submit out = %v", out)
		}
	})

	t.Run("forged tenant header is structurally ignored", func(t *testing.T) {
		out := h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", tenantB,
			submitBody("李四", "13900001002", "", false, "wx_post", "camp-2"), 201)
		contactID, _ := out["contact_id"].(string)
		// 联系人落在表单归属租户 tenantA,owner B 在 tenantB 看不到任何新联系人。
		if _, err := h.api.St.GetContact(contactID, tenantA); err != nil {
			t.Fatalf("contact must land in the form owner tenant: %v", err)
		}
		listB := h.mustDo("GET", "/api/v1/contacts", sessionOwnerB, tenantB, "", 200)
		if n := len(listB["items"].([]any)); n != 0 {
			t.Fatalf("tenant B must not receive anything, got %d contacts", n)
		}
	})

	t.Run("forged tenant body field is rejected by the whitelist", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			`{"tenant_id":"tnt_b","name":"王五","phone":"13900001003","source":"s","source_ref":"r3"}`, 400)
	})

	t.Run("itemized validation (逐项明确)", func(t *testing.T) {
		_, out, _ := h.do("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			`{"name":"","phone":"12345","marketing_allowed":false,"source":"","source_ref":""}`)
		if out["error"] != "validation_failed" {
			t.Fatalf("error = %v", out["error"])
		}
		details := out["details"].([]any)
		seen := map[string]bool{}
		for _, d := range details {
			f := d.(map[string]any)
			seen[f["field"].(string)] = true
		}
		for _, want := range []string{"name", "phone", "source", "source_ref"} {
			if !seen[want] {
				t.Fatalf("missing detail for %s in %v", want, details)
			}
		}
	})

	t.Run("overlong name and unknown free-field key are 400 (无自由字段)", func(t *testing.T) {
		longName := strings.Repeat("名", 101)
		h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			fmt.Sprintf(`{"name":%q,"phone":"13900001004","source":"s","source_ref":"r4"}`, longName), 400)
		h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			`{"name":"甲","phone":"13900001005","address":"某某路","source":"s","source_ref":"r5"}`, 400)
	})

	t.Run("disabled then expired forms answer 410", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/forms/"+formID+"/disable", sessionOwnerA, tenantA, "", 200)
		h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			submitBody("甲", "13900001006", "", false, "s", "r6"), 410)
		// 过期:published 且 expires_at 已过 → 功能性过期 → 410。
		past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		exp := h.mustDo("POST", "/api/v1/forms", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"form_key":"exp","notice_version":"v1","purpose":"c","expires_at":%q}`, past), 201)
		expID, _ := exp["id"].(string)
		h.mustDo("POST", "/api/v1/forms/"+expID+"/publish", sessionOwnerA, tenantA, "", 200)
		h.mustDo("POST", "/api/v1/public/forms/"+expID+"/submissions", "", "",
			submitBody("甲", "13900001007", "", false, "s", "r7"), 410)
	})
}

// ---- 公共提交:幂等 / 短窗 / 咨询次数 / 撤销回归 -------------------------------------

func TestPublicFormSubmitIdempotencyAndConsent(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	form := publishFormViaAPI(t, h, tenantA, "main", formFieldsWithWechat)
	formID, _ := form["id"].(string)
	path := "/api/v1/public/forms/" + formID + "/submissions"

	first := h.mustDo("POST", path, "", "", submitBody("赵六", "13900002001", "", true, "wx_post", "ref-1"), 201)
	subID, _ := first["submission_id"].(string)
	leadID, _ := first["lead_id"].(string)
	contactID, _ := first["contact_id"].(string)
	consentID, _ := first["consent_id"].(string)

	t.Run("same source_ref replays the original submission regardless of content", func(t *testing.T) {
		retry := h.mustDo("POST", path, "", "", submitBody("赵六改", "13900002001", "", false, "wx_post", "ref-1"), 200)
		if retry["duplicate"] != true || retry["duplicate_kind"] != "idempotent" {
			t.Fatalf("retry out = %v", retry)
		}
		if retry["submission_id"] != subID || retry["lead_id"] != leadID || retry["contact_id"] != contactID {
			t.Fatalf("replay must return the original references: %v", retry)
		}
		leads := h.mustDo("GET", "/api/v1/leads", sessionOwnerA, tenantA, "", 200)
		if n := len(leads["items"].([]any)); n != 1 {
			t.Fatalf("leads = %d, want 1 (零写入)", n)
		}
	})

	t.Run("same contact+form inside the window answers the original (幂等提示不新建)", func(t *testing.T) {
		again := h.mustDo("POST", path, "", "", submitBody("赵六", "13900002001", "", false, "wx_post", "ref-2"), 200)
		if again["duplicate_kind"] != "recent_window" {
			t.Fatalf("window out = %v", again)
		}
		if again["submission_id"] != subID || again["lead_id"] != leadID {
			t.Fatalf("window must answer the original submission: %v", again)
		}
	})

	t.Run("marketing consent: checked stores granted", func(t *testing.T) {
		consents := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", 200)
		items := consents["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("consents = %d, want 1", len(items))
		}
		c := items[0].(map[string]any)
		if c["id"] != consentID || c["marketing_allowed"] != true || c["notice_version"] != "v1.2" {
			t.Fatalf("consent wrong: %v", c)
		}
		if c["source_channel"] != "public_form" {
			t.Fatalf("channel = %v, want public_form", c["source_channel"])
		}
	})

	t.Run("revoked stays revoked after replay (1691 语义回归)", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/contacts/"+contactID+"/consents/"+consentID+"/revoke",
			sessionOwnerA, tenantA, `{"reason":"用户来电要求停止"}`, 200)
		h.mustDo("POST", path, "", "", submitBody("赵六", "13900002001", "", true, "wx_post", "ref-1"), 200)
		consents := h.mustDo("GET", "/api/v1/contacts/"+contactID+"/consents", sessionOwnerA, tenantA, "", 200)
		c := consents["items"].([]any)[0].(map[string]any)
		// revoked_at 是持久标记:重放零写入,绝不恢复,也不改写撤销时间。
		if rev, _ := c["revoked_at"].(string); rev == "" {
			t.Fatalf("consent must remain revoked after replay: %v", c)
		}
	})

	t.Run("outside the window a genuine re-consult keeps the count (intake 三分类)", func(t *testing.T) {
		h.api.FormResubmitWindow = time.Nanosecond // 视为窗外
		second := h.mustDo("POST", path, "", "", submitBody("赵六", "13900002001", "", false, "wx_post", "ref-3"), 201)
		if second["class"] != "repeat_consult" || second["duplicate"] != false {
			t.Fatalf("re-consult out = %v", second)
		}
		if second["contact_id"] != contactID {
			t.Fatalf("must attach to the existing contact: %v", second)
		}
		if second["lead_id"] == leadID {
			t.Fatal("must create a NEW lead (咨询次数不丢)")
		}
		leads := h.mustDo("GET", "/api/v1/leads", sessionOwnerA, tenantA, "", 200)
		if n := len(leads["items"].([]any)); n != 2 {
			t.Fatalf("leads = %d, want 2", n)
		}
	})

	t.Run("unchecked marketing never enters the marketing pool", func(t *testing.T) {
		out := h.mustDo("POST", path, "", "", submitBody("钱七", "13900002002", "", false, "wx_post", "ref-4"), 201)
		if out["marketing_allowed"] != false {
			t.Fatalf("marketing snapshot = %v", out["marketing_allowed"])
		}
		cid, _ := out["contact_id"].(string)
		consents := h.mustDo("GET", "/api/v1/contacts/"+cid+"/consents", sessionOwnerA, tenantA, "", 200)
		c := consents["items"].([]any)[0].(map[string]any)
		if c["marketing_allowed"] != false {
			t.Fatalf("unchecked marketing must store marketing_allowed=false: %v", c)
		}
	})
}

// ---- 限流与配置门 -----------------------------------------------------------------

func TestFormRateLimitAndConfigGate(t *testing.T) {
	t.Run("31st submission within the minute is 429", func(t *testing.T) {
		h := newHarness(t)
		tenantA, _, _ := h.seed()
		form := publishFormViaAPI(t, h, tenantA, "main", formFieldsWithWechat)
		formID, _ := form["id"].(string)
		path := "/api/v1/public/forms/" + formID + "/submissions"
		body := submitBody("孙八", "13900003001", "", false, "wx", "rate-ref")
		h.mustDo("POST", path, "", "", body, 201) // 首次新建
		for i := 1; i < 30; i++ {
			h.mustDo("POST", path, "", "", body, 200) // 同键幂等
		}
		h.mustDo("POST", path, "", "", body, 429) // 第 31 次
		// 换一个表单不受同表单配额影响;同表单换 IP 也不受影响。
		h.mustDo("POST", path, "", "", body, 429)
	})

	t.Run("missing dedup pepper fails closed (config_gate_dedup)", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{omitDedupPepper: true})
		tenantA, _, _ := h.seed()
		form := publishFormViaAPI(t, h, tenantA, "main", formFieldsWithWechat)
		formID, _ := form["id"].(string)
		_, out, _ := h.do("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			submitBody("周九", "13900004001", "", false, "wx", "r1"))
		if out["error"] != "config_gate_dedup" {
			t.Fatalf("error = %v, want config_gate_dedup", out["error"])
		}
	})

	t.Run("logs never carry the phone in plaintext", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{captureLog: true})
		tenantA, _, _ := h.seed()
		form := publishFormViaAPI(t, h, tenantA, "main", formFieldsWithWechat)
		formID, _ := form["id"].(string)
		h.mustDo("POST", "/api/v1/public/forms/"+formID+"/submissions", "", "",
			submitBody("吴十", "13900005001", "", true, "wx", "log-ref"), 201)
		logs := h.logs.String()
		if strings.Contains(logs, "13900005001") {
			t.Fatalf("phone plaintext leaked into logs: %s", logs)
		}
		if !strings.Contains(logs, "form submit") {
			t.Fatalf("expected a form submit audit line: %s", logs)
		}
	})
}
