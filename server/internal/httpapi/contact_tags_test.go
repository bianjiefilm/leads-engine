// HUI-1690 / FEAT-0191 客户画像标签体系 HTTP E2E(登记制开关 FEATURE_CONTACT_TAGS):
//   - off(默认):/contact-tags 全族路由与联系人标签子路径一律不注册(404 不可见,
//     任何方法都不可见);
//   - on:人工标签定义 CRUD(owner 专属 manage_contact_tags;重名 409;校验 400)、
//     打标/去标幂等 + 留痕可回查(谁/何时/哪标签)、派生标签目录与单联系人派生键、
//     分群组合查询(组间 AND 组内 OR,须披露);sales 只看自己人群(记录级作用域),
//     非 assignee 404 掩码,跨租户零串行;
//   - PII 纪律:分群结果/派生结果零联系方式、零姓名原文(裸 contact_id 引用),
//     以原始响应体逐字节断言。
package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func tagsOn(t *testing.T) *harness {
	t.Helper()
	return newHarnessOpts(t, harnessOpts{featureContactTags: true})
}

// tagSeedIntakeHTTP writes one lead_intake_events ledger row with an explicit
// time (HTTP-side fact seed; store-side matrix uses the same shape).
func tagSeedIntakeHTTP(t *testing.T, h *harness, tenant, id, contactID, createdAt string) {
	t.Helper()
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO leads(id,tenant_id,contact_id,status,created_by,created_at,updated_at)
		 VALUES(?,?,?,'new','test',?,?)`, "lead_"+id, tenant, contactID, createdAt, createdAt); err != nil {
		t.Fatalf("seed intake lead %s: %v", id, err)
	}
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO lead_intake_events(id,tenant_id,source_app,source_ns,event_id,content_sha256,phone_fpr,class,lead_id,contact_id,created_at)
		 VALUES(?,?,'tags','http',?,'sha','','new',?,?,?)`, id, tenant, "ev_"+id, "lead_"+id, contactID, createdAt); err != nil {
		t.Fatalf("seed intake event %s: %v", id, err)
	}
}

// tagCreateDef creates a manual tag definition via the API; the session must
// be the tenant's owner (manage_contact_tags 是 owner 专属动作)。
func tagCreateDef(t *testing.T, h *harness, session, tenant, name, color string) map[string]any {
	t.Helper()
	out := h.mustDo("POST", "/api/v1/contact-tags", session, tenant,
		fmt.Sprintf(`{"name":%q,"color":%q}`, name, color), http.StatusCreated)
	return out
}

func tagDefID(t *testing.T, def map[string]any) string {
	t.Helper()
	id, _ := def["id"].(string)
	if !strings.HasPrefix(id, "ctd_") {
		t.Fatalf("tag def id = %q, want ctd_ prefix", id)
	}
	return id
}

func tagTag(t *testing.T, h *harness, tenant, contactID, tagID, session string) map[string]any {
	t.Helper()
	return h.mustDo("POST", "/api/v1/contacts/"+contactID+"/tags", session, tenant,
		fmt.Sprintf(`{"tag_id":%q}`, tagID), http.StatusOK)
}

func tagSegmentIDs(t *testing.T, out map[string]any) []string {
	t.Helper()
	raw, ok := out["items"].([]any)
	if !ok {
		t.Fatalf("segment response has no items array: %v", out)
	}
	ids := []string{}
	for _, it := range raw {
		m, _ := it.(map[string]any)
		id, _ := m["contact_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func tagRecentRFC3339() string {
	// 相对真实 now 的「窗内」时间:活动度断言不依赖测试运行日期。
	return time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
}

// TestContactTagsFlagOff404:FEATURE_CONTACT_TAGS 缺省 off,标签路由族整体
// 未注册——任何方法任何子路径一律 404(与 funnel 同款登记制纪律)。
func TestContactTagsFlagOff404(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contactA1 := h.seed()
	cases := []struct{ method, path string }{
		{"GET", "/api/v1/contact-tags"},
		{"POST", "/api/v1/contact-tags"},
		{"GET", "/api/v1/contact-tags/derived"},
		{"GET", "/api/v1/contact-tags/segment?lifecycle=opp_active"},
		{"PATCH", "/api/v1/contact-tags/ctd_x"},
		{"DELETE", "/api/v1/contact-tags/ctd_x"},
		{"GET", "/api/v1/contacts/" + contactA1 + "/tags"},
		{"POST", "/api/v1/contacts/" + contactA1 + "/tags"},
		{"DELETE", "/api/v1/contacts/" + contactA1 + "/tags/ctd_x"},
		{"GET", "/api/v1/contacts/" + contactA1 + "/derived-tags"},
	}
	for _, c := range cases {
		status, out, _ := h.do(c.method, c.path, sessionOwnerA, tenantA, "{}")
		if status != http.StatusNotFound {
			t.Fatalf("%s %s: off 时必须 404(不可见), got %d (%v)", c.method, c.path, status, out)
		}
	}
}

// TestContactTagDefCRUDHTTP:定义 CRUD 的权限、校验、重名冲突、在用拒删与跨租户隔离。
func TestContactTagDefCRUDHTTP(t *testing.T) {
	h := tagsOn(t)
	tenantA, tenantB, contactA1 := h.seed()

	// 非 owner 无权建/改/删定义(authz 新增动作 manage_contact_tags,owner 专属)。
	h.mustDo("POST", "/api/v1/contact-tags", sessionSalesA1, tenantA, `{"name":"高意向"}`, http.StatusForbidden)
	h.mustDo("POST", "/api/v1/contact-tags", sessionAgentA, tenantA, `{"name":"高意向"}`, http.StatusForbidden)
	h.mustDo("POST", "/api/v1/contact-tags", sessionDisabled, tenantA, `{"name":"高意向"}`, http.StatusForbidden)

	// owner 创建 → 201,回显全字段。
	def := tagCreateDef(t, h, sessionOwnerA, tenantA, "高意向", "#ff5252")
	id := tagDefID(t, def)
	if def["name"] != "高意向" || def["color"] != "#ff5252" {
		t.Fatalf("created def = %v", def)
	}
	if _, ok := def["created_at"].(string); !ok {
		t.Fatalf("created def missing created_at: %v", def)
	}

	// 重名 → 409(租户内唯一;错误码机器可判)。
	out := h.mustDo("POST", "/api/v1/contact-tags", sessionOwnerA, tenantA, `{"name":"高意向"}`, http.StatusConflict)
	if out["error"] != "tag_name_conflict" {
		t.Fatalf("duplicate name error = %v, want tag_name_conflict", out["error"])
	}

	// 校验:空名 / 超 50 字 / 颜色非法 / 描述超 200 字 → 400。
	h.mustDo("POST", "/api/v1/contact-tags", sessionOwnerA, tenantA, `{"name":"  "}`, http.StatusBadRequest)
	h.mustDo("POST", "/api/v1/contact-tags", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"name":%q}`, strings.Repeat("标", 51)), http.StatusBadRequest)
	h.mustDo("POST", "/api/v1/contact-tags", sessionOwnerA, tenantA, `{"name":"x","color":"red"}`, http.StatusBadRequest)
	h.mustDo("POST", "/api/v1/contact-tags", sessionOwnerA, tenantA, `{"name":"x","color":"#ff52"}`, http.StatusBadRequest)
	h.mustDo("POST", "/api/v1/contact-tags", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"name":"x","description":%q}`, strings.Repeat("描", 201)), http.StatusBadRequest)

	// 列表回读。
	list := h.mustDo("GET", "/api/v1/contact-tags", sessionOwnerA, tenantA, "", http.StatusOK)
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items = %d, want 1", len(items))
	}

	// 改名:撞已有名 → 409;sales → 403;正常改名 → 200。
	def2 := tagCreateDef(t, h, sessionOwnerA, tenantA, "售后", "")
	id2 := tagDefID(t, def2)
	h.mustDo("PATCH", "/api/v1/contact-tags/"+id2, sessionOwnerA, tenantA, `{"name":"高意向"}`, http.StatusConflict)
	h.mustDo("PATCH", "/api/v1/contact-tags/"+id2, sessionSalesA1, tenantA, `{"name":"x"}`, http.StatusForbidden)
	patched := h.mustDo("PATCH", "/api/v1/contact-tags/"+id2, sessionOwnerA, tenantA,
		`{"name":"重点客户","color":"#00ff00","description":"成交后回访"}`, http.StatusOK)
	if patched["name"] != "重点客户" || patched["color"] != "#00ff00" || patched["description"] != "成交后回访" {
		t.Fatalf("patched def = %v", patched)
	}

	// 删除:在用 → 409;去标后 → 200;重复删 → 404。
	tagTag(t, h, tenantA, contactA1, id, sessionOwnerA)
	inuse := h.mustDo("DELETE", "/api/v1/contact-tags/"+id, sessionOwnerA, tenantA, "", http.StatusConflict)
	if inuse["error"] != "tag_in_use" {
		t.Fatalf("in-use delete error = %v, want tag_in_use", inuse["error"])
	}
	h.mustDo("DELETE", "/api/v1/contacts/"+contactA1+"/tags/"+id, sessionOwnerA, tenantA, "", http.StatusOK)
	h.mustDo("DELETE", "/api/v1/contact-tags/"+id, sessionOwnerA, tenantA, "", http.StatusOK)
	h.mustDo("DELETE", "/api/v1/contact-tags/"+id, sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("PATCH", "/api/v1/contact-tags/"+id, sessionOwnerA, tenantA, `{"name":"y"}`, http.StatusNotFound)

	// 跨租户:同名在 B 租户合法(租户内唯一);B 只见自己的定义;B 冒充操作 A 的 id → 404 掩码。
	defB := tagCreateDef(t, h, sessionOwnerB, tenantB, "高意向", "")
	listB := h.mustDo("GET", "/api/v1/contact-tags", sessionOwnerB, tenantB, "", http.StatusOK)
	itemsB, _ := listB["items"].([]any)
	if len(itemsB) != 1 {
		t.Fatalf("tenant B list items = %d, want 1(跨租户零串行)", len(itemsB))
	}
	if got := tagDefID(t, itemsB[0].(map[string]any)); got != defB["id"] {
		t.Fatalf("tenant B list contains foreign def: %v", itemsB)
	}
	h.mustDo("PATCH", "/api/v1/contact-tags/"+id2, sessionOwnerB, tenantB, `{"name":"冒充"}`, http.StatusNotFound)
	h.mustDo("DELETE", "/api/v1/contact-tags/"+id2, sessionOwnerB, tenantB, "", http.StatusNotFound)
}

// TestContactTaggingIdempotencyAndAuditHTTP:打标幂等(首戳留痕不被改写)、
// 留痕可回查、去标幂等、坏引用 400、非 assignee 404 掩码、agent 403、跨租户 404。
func TestContactTaggingIdempotencyAndAuditHTTP(t *testing.T) {
	h := tagsOn(t)
	tenantA, tenantB, contactA1 := h.seed() // contactA1 assignee = sales1
	def := tagCreateDef(t, h, sessionOwnerA, tenantA, "重点客户", "")
	id := tagDefID(t, def)
	sales1 := h.memberID(tenantA, principalSalesA1)

	// assignee 打标 → changed=true + 完整留痕。
	first := tagTag(t, h, tenantA, contactA1, id, sessionSalesA1)
	if first["changed"] != true {
		t.Fatalf("first tag changed = %v, want true", first["changed"])
	}
	link, _ := first["link"].(map[string]any)
	if link["tag_id"] != id || link["contact_id"] != contactA1 {
		t.Fatalf("link refs = %v", link)
	}
	if link["tag_name"] != "重点客户" {
		t.Fatalf("link tag_name = %v, want 重点客户(可读留痕)", link["tag_name"])
	}
	if link["applied_by"] != sales1 {
		t.Fatalf("applied_by = %v, want sales1 member", link["applied_by"])
	}
	appliedAt, _ := link["applied_at"].(string)
	if appliedAt == "" {
		t.Fatalf("applied_at missing in %v", link)
	}

	// 重放(不同操作人)→ changed=false,首戳留痕不被改写。
	again := tagTag(t, h, tenantA, contactA1, id, sessionOwnerA)
	if again["changed"] != false {
		t.Fatalf("replay changed = %v, want false(幂等)", again["changed"])
	}
	againLink, _ := again["link"].(map[string]any)
	if againLink["applied_at"] != appliedAt || againLink["applied_by"] != sales1 {
		t.Fatalf("replay rewrote stamp: first=%v again=%v", link, againLink)
	}

	// 留痕可回查:GET /contacts/{id}/tags。
	tags := h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/tags", sessionOwnerA, tenantA, "", http.StatusOK)
	links, _ := tags["items"].([]any)
	if len(links) != 1 {
		t.Fatalf("contact tags = %d, want 1", len(links))
	}
	row, _ := links[0].(map[string]any)
	if row["tag_id"] != id || row["applied_by"] != sales1 || row["applied_at"] != appliedAt {
		t.Fatalf("audit row = %v", row)
	}

	// 去标幂等:第二次 changed=false;列表回空。
	untag1 := h.mustDo("DELETE", "/api/v1/contacts/"+contactA1+"/tags/"+id, sessionOwnerA, tenantA, "", http.StatusOK)
	if untag1["changed"] != true {
		t.Fatalf("untag changed = %v, want true", untag1["changed"])
	}
	untag2 := h.mustDo("DELETE", "/api/v1/contacts/"+contactA1+"/tags/"+id, sessionOwnerA, tenantA, "", http.StatusOK)
	if untag2["changed"] != false {
		t.Fatalf("untag replay changed = %v, want false", untag2["changed"])
	}
	tags2 := h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/tags", sessionOwnerA, tenantA, "", http.StatusOK)
	if items, _ := tags2["items"].([]any); len(items) != 0 {
		t.Fatalf("tags after untag = %d, want 0", len(items))
	}

	// 坏引用:未知/缺省 tag_id → 400。
	h.mustDo("POST", "/api/v1/contacts/"+contactA1+"/tags", sessionOwnerA, tenantA, `{"tag_id":"ctd_missing"}`, http.StatusBadRequest)
	h.mustDo("POST", "/api/v1/contacts/"+contactA1+"/tags", sessionOwnerA, tenantA, `{}`, http.StatusBadRequest)

	// 跨租户:B 租户标签打 A 租户联系人 → 404(联系人不在 B 租户,fail-closed)。
	defB := tagCreateDef(t, h, sessionOwnerB, tenantB, "B标签", "")
	h.mustDo("POST", "/api/v1/contacts/"+contactA1+"/tags", sessionOwnerB, tenantB,
		fmt.Sprintf(`{"tag_id":%q}`, tagDefID(t, defB)), http.StatusNotFound)

	// 非 assignee sales → 404 掩码;agent(无 grant)→ 403。
	h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/tags", sessionSalesA2, tenantA, "", http.StatusNotFound)
	h.mustDo("POST", "/api/v1/contacts/"+contactA1+"/tags", sessionSalesA2, tenantA,
		fmt.Sprintf(`{"tag_id":%q}`, id), http.StatusNotFound)
	h.mustDo("GET", "/api/v1/contacts/"+contactA1+"/tags", sessionAgentA, tenantA, "", http.StatusForbidden)
}

// TestDerivedTagCatalogHTTP:派生标签目录披露纪律(10 条,四要素齐备,
// 活跃度阈值披露 30 常量),读取权限为业务角色,响应零 PII。
func TestDerivedTagCatalogHTTP(t *testing.T) {
	h := tagsOn(t)
	tenantA, _, contactA1 := h.seed()

	out := h.mustDo("GET", "/api/v1/contact-tags/derived", sessionSalesA1, tenantA, "", http.StatusOK)
	if out["activity_recent_days"] != float64(30) {
		t.Fatalf("activity_recent_days = %v, want 30(常量随响应披露)", out["activity_recent_days"])
	}
	tags, _ := out["tags"].([]any)
	if len(tags) != 10 {
		t.Fatalf("catalog tags = %d, want 10", len(tags))
	}
	families := map[string]int{}
	for _, it := range tags {
		m, _ := it.(map[string]any)
		for _, k := range []string{"key", "family", "label", "fact_source", "rule"} {
			if s, _ := m[k].(string); s == "" {
				t.Fatalf("catalog entry missing %s: %v", k, m)
			}
		}
		f, _ := m["family"].(string)
		families[f]++
	}
	if len(families) != 4 {
		t.Fatalf("catalog families = %v, want 4", families)
	}
	if families["lifecycle"] != 3 || families["activity"] != 2 || families["source"] != 3 || families["followup"] != 2 {
		t.Fatalf("catalog family sizes = %v", families)
	}

	// PII 纪律:目录是定义披露,响应体不得携带任何档案原文。
	_, raw, _ := h.doRaw("GET", "/api/v1/contact-tags/derived", sessionOwnerA, tenantA, "")
	for _, leak := range []string{"13812345678", "jia@shop.cn", "甲商家", contactA1} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Fatalf("catalog response leaks PII %q: %s", leak, raw)
		}
	}
}

// TestContactDerivedTagsHTTP:单联系人派生标签按需重算;assignee 可读、
// 非 assignee 404 掩码、agent 403、跨租户 404;响应零 PII。
func TestContactDerivedTagsHTTP(t *testing.T) {
	h := tagsOn(t)
	tenantA, tenantB, contactA1 := h.seed() // contactA1: manual 来源, assignee = sales1
	funnelSeedOpp(t, h, tenantA, "tgo1", contactA1, "", "2026-01-05T00:00:00Z")
	tagSeedIntakeHTTP(t, h, tenantA, "tgi1", contactA1, tagRecentRFC3339())

	path := "/api/v1/contacts/" + contactA1 + "/derived-tags"
	out := h.mustDo("GET", path, sessionOwnerA, tenantA, "", http.StatusOK)
	if out["contact_id"] != contactA1 {
		t.Fatalf("contact_id = %v", out["contact_id"])
	}
	if out["activity_recent_days"] != float64(30) {
		t.Fatalf("activity_recent_days = %v, want 30", out["activity_recent_days"])
	}
	got := map[string]bool{}
	for _, k := range toStrSlice(t, out["keys"]) {
		got[k] = true
	}
	for _, want := range []string{"opp_active", "recent_intake", "src_manual"} {
		if !got[want] {
			t.Fatalf("derived keys missing %q: %v", want, out["keys"])
		}
	}
	defs, _ := out["definitions"].([]any)
	if len(defs) != 10 {
		t.Fatalf("definitions = %d, want 10", len(defs))
	}

	// assignee 可读;非 assignee 404 掩码;agent 403;跨租户 404。
	h.mustDo("GET", path, sessionSalesA1, tenantA, "", http.StatusOK)
	h.mustDo("GET", path, sessionSalesA2, tenantA, "", http.StatusNotFound)
	h.mustDo("GET", path, sessionAgentA, tenantA, "", http.StatusForbidden)
	h.mustDo("GET", path, sessionOwnerB, tenantB, "", http.StatusNotFound)

	// PII 零外泄(逐字节断言原始响应体)。
	_, raw, _ := h.doRaw("GET", path, sessionOwnerA, tenantA, "")
	for _, leak := range []string{"13812345678", "jia@shop.cn", "甲商家"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Fatalf("derived response leaks PII %q: %s", leak, raw)
		}
	}
}

func toStrSlice(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("not a string array: %v", v)
	}
	out := []string{}
	for _, it := range raw {
		s, _ := it.(string)
		out = append(out, s)
	}
	return out
}

// TestContactSegmentE2E:分群组合查询(组间 AND、组内 OR、语义披露)、
// 记录级作用域、参数校验、跨租户零串行、结果零 PII(裸 contact_id 引用)。
func TestContactSegmentE2E(t *testing.T) {
	h := tagsOn(t)
	tenantA, tenantB, contactA1 := h.seed() // contactA1: manual, assignee = sales1
	sales1 := h.memberID(tenantA, principalSalesA1)

	// 事实面(form/manual 来源 + 商机在途/已成交 + 窗内 intake)。
	funnelSeedContact(t, h, tenantA, "ts1", "13900000011", "form", "", "2026-01-05T00:00:00Z")
	funnelSeedContact(t, h, tenantA, "ts2", "13900000012", "form", "", "2026-01-05T00:00:00Z")
	funnelSeedContact(t, h, tenantA, "ts3", "13900000013", "manual", "", "2026-01-05T00:00:00Z")
	funnelSeedContact(t, h, tenantA, "ts4", "13900000014", "form", sales1, "2026-01-05T00:00:00Z")
	tagSeedIntakeHTTP(t, h, tenantA, "ti1", "ts1", tagRecentRFC3339())
	funnelSeedOpp(t, h, tenantA, "to2", "ts2", "", "2026-01-05T00:00:00Z")
	funnelSeedOpp(t, h, tenantA, "to3", "ts3", "", "2026-01-05T00:00:00Z")
	funnelSeedWon(t, h, tenantA, "th3", "to3", "2026-01-06T00:00:00Z")
	funnelSeedOpp(t, h, tenantA, "to4", contactA1, "", "2026-01-05T00:00:00Z")

	// 人工标签:ts1/contactA1 打「高意向」;ts3 打「售后」。
	hi := tagDefID(t, tagCreateDef(t, h, sessionOwnerA, tenantA, "高意向", ""))
	svc := tagDefID(t, tagCreateDef(t, h, sessionOwnerA, tenantA, "售后", ""))
	tagTag(t, h, tenantA, "ts1", hi, sessionOwnerA)
	tagTag(t, h, tenantA, contactA1, hi, sessionOwnerA)
	tagTag(t, h, tenantA, "ts3", svc, sessionOwnerA)

	// 单组(manual):组内多值 OR——manual 无多键,改用 lifecycle 组内 OR。
	out := h.mustDo("GET", "/api/v1/contact-tags/segment?lifecycle=opp_active,opp_won",
		sessionOwnerA, tenantA, "", http.StatusOK)
	if out["scope"] != "tenant" {
		t.Fatalf("owner scope = %v, want tenant", out["scope"])
	}
	if got := tagSegmentIDs(t, out); len(got) != 3 {
		t.Fatalf("lifecycle segment = %v, want 3 (ts2+ts3+contactA1,组内 OR)", got)
	}

	// 组间 AND:lifecycle=opp_active ∧ manual=hi → 只有 contactA1。
	path := "/api/v1/contact-tags/segment?lifecycle=opp_active&manual=" + hi
	out = h.mustDo("GET", path, sessionOwnerA, tenantA, "", http.StatusOK)
	ids := tagSegmentIDs(t, out)
	if len(ids) != 1 || ids[0] != contactA1 {
		t.Fatalf("AND segment = %v, want [%s](组间 AND)", ids, contactA1)
	}
	if out["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", out["total"])
	}
	// 组合语义与定义披露必须随响应给出(机器可核对)。
	if cr, _ := out["combine_rule"].(string); cr == "" {
		t.Fatalf("combine_rule must be disclosed: %v", out)
	}
	groups, _ := out["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("groups echo = %v, want 2 groups", out["groups"])
	}
	defs, _ := out["definitions"].([]any)
	if len(defs) != 10 {
		t.Fatalf("definitions = %d, want 10", len(defs))
	}

	// source 单组 + 无匹配 → 空结果(200,items=[] 而非错误)。
	empty := h.mustDo("GET", "/api/v1/contact-tags/segment?source=src_touch_campaign",
		sessionOwnerA, tenantA, "", http.StatusOK)
	if got := tagSegmentIDs(t, empty); len(got) != 0 {
		t.Fatalf("no-match segment = %v, want empty", got)
	}

	// sales 作用域:只含自己人群(ts1 未指派,即便命中 manual 组也不可见)。
	outS := h.mustDo("GET", path, sessionSalesA1, tenantA, "", http.StatusOK)
	if outS["scope"] != "assigned_to_me" {
		t.Fatalf("sales scope = %v, want assigned_to_me", outS["scope"])
	}
	if got := tagSegmentIDs(t, outS); len(got) != 1 || got[0] != contactA1 {
		t.Fatalf("sales1 segment = %v, want [%s](ts1 未指派不可见)", got, contactA1)
	}
	// sales2 人群为空 → 0 行。
	outS2 := h.mustDo("GET", path, sessionSalesA2, tenantA, "", http.StatusOK)
	if got := tagSegmentIDs(t, outS2); len(got) != 0 {
		t.Fatalf("sales2 segment = %v, want 0 行", got)
	}
	// agent(无 grant)→ 403。
	h.mustDo("GET", path, sessionAgentA, tenantA, "", http.StatusForbidden)

	// 跨租户:B 查自己的空租户 → 0 行;B 引用 A 的标签 id → 400(不泄露存在性之外的信息)。
	outB := h.mustDo("GET", "/api/v1/contact-tags/segment?lifecycle=opp_won", sessionOwnerB, tenantB, "", http.StatusOK)
	if got := tagSegmentIDs(t, outB); len(got) != 0 {
		t.Fatalf("tenant B segment = %v, want 0 行(跨租户零串行)", got)
	}
	h.mustDo("GET", "/api/v1/contact-tags/segment?manual="+hi, sessionOwnerB, tenantB, "", http.StatusBadRequest)

	// 参数校验:无组 / 未知派生键 / 键放错族 / 未知人工标签 / 手机号形状值 → 400。
	h.mustDo("GET", "/api/v1/contact-tags/segment", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", "/api/v1/contact-tags/segment?lifecycle=", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", "/api/v1/contact-tags/segment?lifecycle=opp_missing", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", "/api/v1/contact-tags/segment?activity=opp_active", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", "/api/v1/contact-tags/segment?manual=ctd_missing", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", "/api/v1/contact-tags/segment?source=13900000011", sessionOwnerA, tenantA, "", http.StatusBadRequest)

	// PII 零外泄:分群结果是裸 contact_id 引用,响应体不得含手机号/邮箱/姓名原文。
	_, raw, _ := h.doRaw("GET", path, sessionOwnerA, tenantA, "")
	for _, leak := range []string{"13812345678", "13900000011", "13900000012", "13900000013", "13900000014",
		"jia@shop.cn", "甲商家", "高意向"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Fatalf("segment response leaks %q: %s", leak, raw)
		}
	}
	if !bytes.Contains(raw, []byte(contactA1)) {
		t.Fatalf("segment response must carry contact reference %s: %s", contactA1, raw)
	}
}
