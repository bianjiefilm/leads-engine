// HUI-1694 / FEAT-0195 全漏斗分析 HTTP E2E(登记制开关 FEATURE_FUNNEL):
//   - off(默认):路由不注册,GET /api/v1/funnel 一律 404(不可见);
//   - on:窗口参数必填且 RFC3339;owner 租户全量 / sales 只看自己人群;
//     UNKNOWN 触点阶段 available=false + 中文 reason;同窗口重算幂等;
//     跨租户零串行; granularities。
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func funnelOn(t *testing.T) *harness {
	t.Helper()
	return newHarnessOpts(t, harnessOpts{featureFunnel: true})
}

// funnelSeed 写入确定时间戳的事实行(直接 SQL,窗口数学完全确定)。
func funnelSeedContact(t *testing.T, h *harness, tenant, id, phone, sourceType, assignee, createdAt string) {
	t.Helper()
	var assigned any
	if assignee != "" {
		assigned = assignee // 外键开启:空指派必须是 NULL,不能是空串
	}
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,notes,tags,deleted_at,source_ref_id,assigned_member_id,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, tenant, "漏斗"+id, phone, "", "merchant_customer", sourceType, "pending", "", "",
		nil, nil, assigned, "test", createdAt, createdAt); err != nil {
		t.Fatalf("seed contact %s: %v", id, err)
	}
}

func funnelSeedFollowUp(t *testing.T, h *harness, tenant, id, contactID, createdAt string) {
	t.Helper()
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO follow_ups(id,tenant_id,contact_id,lead_id,note,next_follow_up_at,completed_at,created_by,created_at,updated_at)
		 VALUES(?,?,?,NULL,'跟进','',NULL,?,?,?)`,
		id, tenant, contactID, h.memberID(tenant, principalOwnerA), createdAt, createdAt); err != nil {
		t.Fatalf("seed follow-up %s: %v", id, err)
	}
}

func funnelSeedOpp(t *testing.T, h *harness, tenant, id, contactID, assignee, createdAt string) {
	t.Helper()
	var assigned any
	if assignee != "" {
		assigned = assignee // 外键开启:空指派必须是 NULL,不能是空串
	}
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO opportunities(id,tenant_id,contact_id,title,stage,business_category,amount_cents,amount_source,probability,expected_close_at,assigned_member_id,created_by,created_at,updated_at)
		 VALUES(?,?,?,'商机','open','merchant_customer',NULL,'unknown',0,NULL,?,'test',?,?)`,
		id, tenant, contactID, assigned, createdAt, createdAt); err != nil {
		t.Fatalf("seed opportunity %s: %v", id, err)
	}
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO opportunity_stage_history(id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note)
		 VALUES(?,?,?,'','open',?,?,'created')`, "ohs_"+id, tenant, id,
		h.memberID(tenant, principalOwnerA), createdAt); err != nil {
		t.Fatalf("seed opp history %s: %v", id, err)
	}
}

func funnelSeedWon(t *testing.T, h *harness, tenant, id, oppID, changedAt string) {
	t.Helper()
	if _, err := h.api.St.DB.Exec(
		`INSERT INTO opportunity_stage_history(id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note)
		 VALUES(?,?,?,'open','won',?,?,'')`, id, tenant, oppID,
		h.memberID(tenant, principalOwnerA), changedAt); err != nil {
		t.Fatalf("seed won event %s: %v", id, err)
	}
}

// funnelStages extracts the stages array as maps keyed by stage key.
func funnelStages(t *testing.T, out map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := out["stages"].([]any)
	if !ok {
		t.Fatalf("response has no stages array: %v", out)
	}
	byKey := map[string]map[string]any{}
	for _, it := range raw {
		m, _ := it.(map[string]any)
		k, _ := m["key"].(string)
		byKey[k] = m
	}
	return byKey
}

func stageCount(t *testing.T, st map[string]any) float64 {
	t.Helper()
	v, ok := st["count"].(float64)
	if !ok {
		t.Fatalf("stage %v count = %v, want number", st["key"], st["count"])
	}
	return v
}

func TestFunnelFlagOff404(t *testing.T) {
	h := newHarness(t) // FEATURE_FUNNEL 缺省 off
	tenantA, _, _ := h.seed()
	h.mustDo("GET", "/api/v1/funnel?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z",
		sessionOwnerA, tenantA, "", http.StatusNotFound)
	// 任何方法都不可见:路由族整体未注册。
	h.mustDo("POST", "/api/v1/funnel", sessionOwnerA, tenantA, "{}", http.StatusNotFound)
}

func TestFunnelParamsValidation(t *testing.T) {
	h := funnelOn(t)
	tenantA, _, _ := h.seed()
	base := "/api/v1/funnel"

	// 窗口必填。
	h.mustDo("GET", base, sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	// RFC3339 必须合法。
	h.mustDo("GET", base+"?window_start=2026-01-01&window_end=2026-02-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=not-a-time", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	// end 必须晚于 start([start,end) 半开窗口,空窗口直接拒绝,绝不静默夹逼)。
	h.mustDo("GET", base+"?window_start=2026-02-01T00:00:00Z&window_end=2026-01-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-01-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	// 粒度白名单。
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z&granularity=week",
		sessionOwnerA, tenantA, "", http.StatusBadRequest)
	// 来源维度必须是既有 source_type 域。
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z&source_type=wechat",
		sessionOwnerA, tenantA, "", http.StatusBadRequest)

	// 合法请求 200(空租户全零 + UNKNOWN 阶段照常披露)。
	out := h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z",
		sessionOwnerA, tenantA, "", http.StatusOK)
	stages := funnelStages(t, out)
	if len(stages) != 6 {
		t.Fatalf("stages = %d, want 6", len(stages))
	}
}

func TestFunnelFlagOnE2E(t *testing.T) {
	h := funnelOn(t)
	tenantA, tenantB, _ := h.seed()

	// 已知事实集(全部确定时间戳,窗口 [2026-01-01, 2026-02-01)):
	//   建档 = 2:c1(01-05)与 c1b(01-06 同号孪生,去重计 1)+ ... = c1 身份 + c2 无号
	funnelSeedContact(t, h, tenantA, "fc1", "13900000001", "form", "", "2026-01-05T10:00:00Z")
	funnelSeedContact(t, h, tenantA, "fc1b", "13900000001", "form", "", "2026-01-06T10:00:00Z")
	funnelSeedContact(t, h, tenantA, "fc2", "", "manual", "", "2026-01-08T10:00:00Z")
	// 窗口外/墓碑对照。
	funnelSeedContact(t, h, tenantA, "fc3", "13900000002", "form", "", "2025-12-20T10:00:00Z")
	funnelSeedContact(t, h, tenantA, "fc4", "13900000003", "form", "", "2026-01-12T10:00:00Z")
	if _, err := h.api.St.DB.Exec(
		`UPDATE contacts SET deleted_at='2026-01-13T00:00:00Z', phone='' WHERE id='fc4'`); err != nil {
		t.Fatal(err)
	}
	//   跟进 = 2:c1(01-15,同联系人第二条不重复计)+ fc3(01-16,档案在窗口前)
	funnelSeedFollowUp(t, h, tenantA, "ff1", "fc1", "2026-01-15T09:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "ff2", "fc1", "2026-01-20T09:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "ff3", "fc3", "2026-01-16T09:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "ff4", "fc4", "2026-01-18T09:00:00Z") // 墓碑:排除
	//   商机 = 1:o1(01-18);o2 恰在 window_end 不含
	funnelSeedOpp(t, h, tenantA, "fo1", "fc1", "", "2026-01-18T09:00:00Z")
	funnelSeedOpp(t, h, tenantA, "fo2", "fc3", "", "2026-02-01T00:00:00Z")
	//   成交 = 1:fo1 于 01-25 won(先 closed_lost 再 won 也照常计入)
	funnelSeedWon(t, h, tenantA, "fh1", "fo1", "2026-01-25T09:00:00Z")

	// 独立 SQL 复算 vs API 输出。
	var wantFollow, wantWon int
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(DISTINCT f.contact_id) FROM follow_ups f JOIN contacts c ON c.id=f.contact_id AND c.deleted_at IS NULL
		 WHERE f.tenant_id=? AND f.created_at>='2026-01-01T00:00:00+00:00'`, tenantA).Scan(&wantFollow); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(DISTINCT h.opportunity_id) FROM opportunity_stage_history h
		 WHERE h.tenant_id=? AND h.to_stage='won' AND h.changed_at>='2026-01-01' AND h.changed_at<'2026-02-01'`, tenantA).Scan(&wantWon); err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/funnel?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z"
	out := h.mustDo("GET", path, sessionOwnerA, tenantA, "", http.StatusOK)
	if out["scope"] != "tenant" {
		t.Fatalf("owner scope = %v, want tenant", out["scope"])
	}
	stages := funnelStages(t, out)

	// UNKNOWN 触点阶段:available=false + 中文 reason,无 count 键值(null)。
	for key, cite := range map[string]string{"exposure": "HUI-1677", "lead_form": "HUI-1680"} {
		st := stages[key]
		if st["available"] != false {
			t.Fatalf("stage %s must be available=false", key)
		}
		if _, present := st["count"]; !present || st["count"] != nil {
			t.Fatalf("stage %s count must be JSON null(绝不置 0), got %v", key, st["count"])
		}
		reason, _ := st["reason"].(string)
		if !strings.Contains(reason, cite) {
			t.Fatalf("stage %s reason must cite %s: %s", key, cite, reason)
		}
	}

	if got := stageCount(t, stages["profile_created"]); got != 2 {
		t.Fatalf("profile_created = %v, want 2(同号孪生去重 + 无号独立)", got)
	}
	if got := stageCount(t, stages["followed_up"]); got != float64(wantFollow) || wantFollow != 2 {
		t.Fatalf("followed_up = %v, SQL recompute = %d, want 2", got, wantFollow)
	}
	if got := stageCount(t, stages["opportunity_created"]); got != 1 {
		t.Fatalf("opportunity_created = %v, want 1(window_end 不含)", got)
	}
	if got := stageCount(t, stages["won"]); got != float64(wantWon) || wantWon != 1 {
		t.Fatalf("won = %v, SQL recompute = %d, want 1", got, wantWon)
	}

	// 同窗口重算幂等:两次响应逐字节一致。
	out2 := h.mustDo("GET", path, sessionOwnerA, tenantA, "", http.StatusOK)
	raw1, raw2 := mustMarshal(t, out), mustMarshal(t, out2)
	if raw1 != raw2 {
		t.Fatalf("same-window recompute must be identical:\n%s\n%s", raw1, raw2)
	}

	// 跨租户零串行:owner B 查自己(空)租户,绝不出现 A 的事实。
	outB := h.mustDo("GET", path, sessionOwnerB, tenantB, "", http.StatusOK)
	stagesB := funnelStages(t, outB)
	if got := stageCount(t, stagesB["profile_created"]); got != 0 {
		t.Fatalf("tenant B profile_created = %v, want 0(跨租户零串行)", got)
	}
	// owner B 冒充 A 租户:无成员行 -> 403(既有中间件 fail-closed)。
	h.mustDo("GET", path, sessionOwnerB, tenantA, "", http.StatusForbidden)

	// granularity=none:无 series;默认 day:series 可加和到 count。
	outNone := h.mustDo("GET", path+"&granularity=none", sessionOwnerA, tenantA, "", http.StatusOK)
	stNone := funnelStages(t, outNone)["profile_created"]
	if _, present := stNone["series"]; present {
		t.Fatalf("granularity=none must omit series, got %v", stNone["series"])
	}
	stDay := stages["profile_created"]
	series, _ := stDay["series"].([]any)
	sum := 0.0
	for _, p := range series {
		pm, _ := p.(map[string]any)
		sum += pm["count"].(float64)
	}
	if sum != stageCount(t, stDay) {
		t.Fatalf("series sum %v != count %v", sum, stageCount(t, stDay))
	}
}

// sales 只看自己被指派人群的漏斗(复用既有记录级作用域;OppStats 同款先例)。
func TestFunnelSalesScope(t *testing.T) {
	h := funnelOn(t)
	tenantA, _, _ := h.seed()
	sales1 := h.memberID(tenantA, principalSalesA1)
	sales2 := h.memberID(tenantA, principalSalesA2)

	funnelSeedContact(t, h, tenantA, "sc1", "13900000004", "form", sales1, "2026-01-05T00:00:00Z")
	funnelSeedContact(t, h, tenantA, "sc2", "13900000005", "form", sales2, "2026-01-06T00:00:00Z")
	funnelSeedOpp(t, h, tenantA, "so1", "sc1", sales1, "2026-01-07T00:00:00Z")

	path := "/api/v1/funnel?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z"
	out := h.mustDo("GET", path, sessionSalesA1, tenantA, "", http.StatusOK)
	if out["scope"] != "assigned_to_me" {
		t.Fatalf("sales scope = %v, want assigned_to_me", out["scope"])
	}
	stages := funnelStages(t, out)
	if got := stageCount(t, stages["profile_created"]); got != 1 {
		t.Fatalf("sales1 profile_created = %v, want 1(只含自己人群)", got)
	}
	if got := stageCount(t, stages["opportunity_created"]); got != 1 {
		t.Fatalf("sales1 opportunity_created = %v, want 1", got)
	}
}

// mustMarshal marshals any value deterministically for comparison.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
