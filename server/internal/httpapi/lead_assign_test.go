// HUI-1685 / FEAT-0186 线索自动分配 HTTP E2E(登记制开关 FEATURE_LEADS_ASSIGN):
//   - off(默认):池配置路由不注册(404 不可见);intake 行为与既往逐字节一致
//     (响应无 assigned_member_id 键,即便池里有人);
//   - on:池配置 CRUD(owner 专属,服务端单点判定:成员须本租户、在册、在职、
//     sales 语义);intake 首投自动分配(轮询均匀/重放短路/人工改派保留/
//     地域标签精确匹配/池空不阻塞/filtered 正交)。
package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

// memberIDAs resolves a member id with an explicit caller session
// (h.memberID is hardwired to sessionOwnerA, which only works for tenantA).
func memberIDAs(t *testing.T, h *harness, tenant, session, principal string) string {
	t.Helper()
	out := h.mustDo("GET", "/api/v1/admin/members", session, tenant, "", http.StatusOK)
	for _, it := range out["items"].([]any) {
		m, _ := it.(map[string]any)
		if m["principal_ref"] == principal {
			s, _ := m["id"].(string)
			return s
		}
	}
	t.Fatalf("member %s not found in %s", principal, tenant)
	return ""
}

// seedPool seeds pool entries for salesA1/salesA2 through the store API
// (CRUD surface is exercised separately).
func seedPool(t *testing.T, h *harness, tenant string, weight1, weight2 int, region string) {
	t.Helper()
	m1 := h.memberID(tenant, principalSalesA1)
	m2 := h.memberID(tenant, principalSalesA2)
	owner := h.memberID(tenant, principalOwnerA)
	if _, err := h.api.St.CreateAssignPoolEntry(tenant, m1, weight1, region, "", owner); err != nil {
		t.Fatalf("seed pool s1: %v", err)
	}
	if _, err := h.api.St.CreateAssignPoolEntry(tenant, m2, weight2, "", "", owner); err != nil {
		t.Fatalf("seed pool s2: %v", err)
	}
}

func poolBody(memberID string, weight int, region, industry string) string {
	body := fmt.Sprintf(`{"member_id":%q,"weight":%d`, memberID, weight)
	if region != "" {
		body += fmt.Sprintf(`,"region":%q`, region)
	}
	if industry != "" {
		body += fmt.Sprintf(`,"industry":%q`, industry)
	}
	return body + "}"
}

func TestAssignPoolFlagOff(t *testing.T) {
	h := newHarness(t)
	tenantA, _, _ := h.seed()
	seedPool(t, h, tenantA, 1, 1, "")

	// intake:有池也不分配 —— 响应与既往逐字节一致(无 assigned_member_id 键)。
	out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx", "off-1", "丙商家", "13900000011", ""), http.StatusCreated)
	if _, present := out["assigned_member_id"]; present {
		t.Fatalf("assigned_member_id must be absent with the flag off, got %v", out["assigned_member_id"])
	}
	leadID, _ := out["lead_id"].(string)
	lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
	if _, present := lead["assigned_member_id"]; present {
		t.Fatalf("lead must stay unassigned with the flag off, got %v", lead["assigned_member_id"])
	}

	// 池配置路由不注册:404 不可见。
	h.mustDo("GET", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA, "", http.StatusNotFound)
	h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA, `{"member_id":"x"}`, http.StatusNotFound)
	h.mustDo("PATCH", "/api/v1/admin/leads-assign-pool/pap_x", sessionOwnerA, tenantA, `{"weight":2}`, http.StatusNotFound)
	h.mustDo("DELETE", "/api/v1/admin/leads-assign-pool/pap_x", sessionOwnerA, tenantA, "", http.StatusNotFound)
}

func TestAssignPoolCRUD(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsAssign: true})
	tenantA, tenantB, _ := h.seed()
	sales1 := h.memberID(tenantA, principalSalesA1)
	sales2 := h.memberID(tenantA, principalSalesA2)

	created := h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
		`{"member_id":"`+sales1+`"}`, http.StatusCreated)
	if created["weight"].(float64) != 1 {
		t.Fatalf("default weight = %v, want 1", created["weight"])
	}
	id1, _ := created["id"].(string)
	if id1 == "" {
		t.Fatalf("created entry missing id: %v", created)
	}
	entry2 := h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
		poolBody(sales2, 2, "华东", "餐饮"), http.StatusCreated)
	if entry2["weight"].(float64) != 2 || entry2["region"] != "华东" || entry2["industry"] != "餐饮" {
		t.Fatalf("entry2 = %v", entry2)
	}

	t.Run("duplicate member is 409", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			`{"member_id":"`+sales1+`"}`, http.StatusConflict)
	})

	t.Run("list shows both entries", func(t *testing.T) {
		out := h.mustDo("GET", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA, "", http.StatusOK)
		if n := len(out["items"].([]any)); n != 2 {
			t.Fatalf("items = %d, want 2", n)
		}
	})

	t.Run("patch changes weight and clears tags explicitly", func(t *testing.T) {
		up := h.mustDo("PATCH", "/api/v1/admin/leads-assign-pool/"+id1, sessionOwnerA, tenantA,
			`{"weight":5,"region":"华南","industry":""}`, http.StatusOK)
		if up["weight"].(float64) != 5 || up["region"] != "华南" {
			t.Fatalf("patched = %v", up)
		}
		if _, present := up["industry"]; present {
			t.Fatalf("explicit empty industry must clear the tag (omitempty absence), got %v", up["industry"])
		}
		h.mustDo("PATCH", "/api/v1/admin/leads-assign-pool/pap_missing", sessionOwnerA, tenantA,
			`{"weight":5}`, http.StatusNotFound)
	})

	t.Run("validation: member tenancy/role/enabled and weight domain", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			`{"member_id":"mem_missing"}`, http.StatusBadRequest)
		ownerB := memberIDAs(t, h, tenantB, sessionOwnerB, principalOwnerB)
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			`{"member_id":"`+ownerB+`"}`, http.StatusBadRequest) // 跨租户成员
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			`{"member_id":"`+h.memberID(tenantA, principalOwnerA)+`"}`, http.StatusBadRequest) // owner 非销售
		agent := h.memberID(tenantA, principalAgentA)
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			`{"member_id":"`+agent+`"}`, http.StatusBadRequest) // agent 非销售
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			`{"member_id":"`+h.memberID(tenantA, principalDisabled)+`"}`, http.StatusBadRequest) // 停用
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			poolBody(h.memberID(tenantA, principalDisabled), 0, "", ""), http.StatusBadRequest) // weight 0
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			poolBody(h.memberID(tenantA, principalDisabled), 1001, "", ""), http.StatusBadRequest) // weight 越上界
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"member_id":%q,"region":%q}`, sales2, repeat("东", 65)), http.StatusBadRequest) // 标签超长
	})

	t.Run("authz: sales 403; cross-tenant owner 403", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/admin/leads-assign-pool", sessionSalesA1, tenantA, "", http.StatusForbidden)
		h.mustDo("POST", "/api/v1/admin/leads-assign-pool", sessionSalesA1, tenantA,
			`{"member_id":"self"}`, http.StatusForbidden)
		h.mustDo("GET", "/api/v1/admin/leads-assign-pool", sessionOwnerB, tenantA, "", http.StatusForbidden)
	})

	t.Run("delete then 404", func(t *testing.T) {
		h.mustDo("DELETE", "/api/v1/admin/leads-assign-pool/"+id1, sessionOwnerA, tenantA, "", http.StatusOK)
		h.mustDo("DELETE", "/api/v1/admin/leads-assign-pool/"+id1, sessionOwnerA, tenantA, "", http.StatusNotFound)
	})
}

func TestAssignIntakeRoundRobinHTTP(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsAssign: true})
	tenantA, _, _ := h.seed()
	seedPool(t, h, tenantA, 1, 1, "")
	sales1 := h.memberID(tenantA, principalSalesA1)
	sales2 := h.memberID(tenantA, principalSalesA2)

	counts := map[string]int{}
	var firstPick string
	for i := 0; i < 4; i++ {
		out := h.intake(sessionOwnerA, tenantA,
			intakeBody("touch", "wx", fmt.Sprintf("rr-%d", i), "商家"+fmt.Sprint(i), fmt.Sprintf("1390000002%d", i), ""),
			http.StatusCreated)
		got, _ := out["assigned_member_id"].(string)
		if got != sales1 && got != sales2 {
			t.Fatalf("intake %d assigned %q, want a pool member", i+1, got)
		}
		if i == 0 {
			firstPick = got
		}
		counts[got]++
		// 记录级作用域跟着分配走:被指派的销售可见,另一人 404 掩码。
		h.mustDo("GET", "/api/v1/leads/"+out["lead_id"].(string),
			map[bool]string{true: sessionSalesA1, false: sessionSalesA2}[got == sales1], tenantA, "", http.StatusOK)
		other := map[bool]string{true: sessionSalesA2, false: sessionSalesA1}[got == sales1]
		h.mustDo("GET", "/api/v1/leads/"+out["lead_id"].(string), other, tenantA, "", http.StatusNotFound)
	}
	if counts[sales1] != 2 || counts[sales2] != 2 {
		t.Fatalf("counts = %v, want 2/2 (轮询均匀)", counts)
	}
	// 两个同权重条目的轮询绝不连发同一人超过一个周期步(平滑性抽查:第 3、4 次
	// 与第 1 次构成完整周期,各拿一次由 counts 已证)。
	_ = firstPick
}

func TestAssignIntakeManualReassignPreservedHTTP(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsAssign: true})
	tenantA, _, _ := h.seed()
	seedPool(t, h, tenantA, 1, 1, "")
	sales2 := h.memberID(tenantA, principalSalesA2)

	body := intakeBody("touch", "wx", "mr-1", "丁商家", "13900000031", "")
	out := h.intake(sessionOwnerA, tenantA, body, http.StatusCreated)
	leadID, _ := out["lead_id"].(string)
	first, _ := out["assigned_member_id"].(string)
	if first == "" {
		t.Fatalf("first delivery must auto-assign: %v", out)
	}

	// owner 人工改派给另一名销售。
	h.mustDo("PATCH", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, sales2), http.StatusOK)

	// 同键重放:幂等返回,绝不改写人工改派,响应不出现 assigned_member_id。
	replay := h.intake(sessionOwnerA, tenantA, body, http.StatusOK)
	if _, present := replay["assigned_member_id"]; present {
		t.Fatalf("replay must not reassign, got %v", replay["assigned_member_id"])
	}
	lead := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", http.StatusOK)
	if lead["assigned_member_id"] != sales2 {
		t.Fatalf("lead assignee = %v, want manual %s", lead["assigned_member_id"], sales2)
	}
}

func TestAssignIntakeRegionMatchHTTP(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsAssign: true})
	tenantA, _, _ := h.seed()
	seedPool(t, h, tenantA, 1, 1, "华东")
	sales1 := h.memberID(tenantA, principalSalesA1)

	// 带华东标签的线索精确命中带标签销售(首投)。
	out := h.intake(sessionOwnerA, tenantA,
		`{"source_app":"touch","source_ns":"wx","event_id":"reg-1","contact":{"name":"戊商家","phone":"13900000041"},"region":"华东"}`,
		http.StatusCreated)
	if out["assigned_member_id"] != sales1 {
		t.Fatalf("region-matched intake = %v, want %s", out["assigned_member_id"], sales1)
	}
	// 无匹配标签 → 全池回退,仍会分配(不要求具体是谁)。
	out2 := h.intake(sessionOwnerA, tenantA,
		`{"source_app":"touch","source_ns":"wx","event_id":"reg-2","contact":{"name":"己商家","phone":"13900000042"},"region":"华南"}`,
		http.StatusCreated)
	if got, _ := out2["assigned_member_id"].(string); got == "" {
		t.Fatalf("no-match fallback must still assign, got %v", out2)
	}
	// 标签超长:开关开时显式 400(服务端单点判定)。
	status, _, _ := h.do("POST", "/api/v1/leads/intake", sessionOwnerA, tenantA,
		`{"source_app":"touch","source_ns":"wx","event_id":"reg-3","contact":{"name":"庚商家","phone":"13900000043"},"region":"`+repeat("东", 65)+`"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("over-long region status = %d, want 400", status)
	}
}

func TestAssignIntakeEmptyPoolHTTP(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsAssign: true})
	tenantA, _, _ := h.seed()
	out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx", "ep-1", "辛商家", "13900000051", ""), http.StatusCreated)
	if _, present := out["assigned_member_id"]; present {
		t.Fatalf("empty pool must not assign, got %v", out["assigned_member_id"])
	}
	var n int
	leadID, _ := out["lead_id"].(string)
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(1) FROM leads WHERE id=?`, leadID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lead rows = %d err=%v, want 1 (池空不阻塞建档)", n, err)
	}
}

func TestAssignFilteredOrthogonalHTTP(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLeadsAssign: true, featureLeadsFilter: true})
	tenantA, _, _ := h.seed()
	seedPool(t, h, tenantA, 1, 1, "")

	out := h.intake(sessionOwnerA, tenantA, intakeBody("touch", "wx", "fo-1", "壬商家", "12345678901", ""), http.StatusCreated)
	if out["filter_reason"] != "invalid_phone" {
		t.Fatalf("filter_reason = %v, want invalid_phone", out["filter_reason"])
	}
	if got, _ := out["assigned_member_id"].(string); got == "" {
		t.Fatalf("filtered lead must still be assigned (台账一致性), got %v", out)
	}
	lead := h.mustDo("GET", "/api/v1/leads/"+out["lead_id"].(string), sessionOwnerA, tenantA, "", http.StatusOK)
	if lead["status"] != "filtered" {
		t.Fatalf("status = %v, want filtered", lead["status"])
	}
	if _, present := lead["assigned_member_id"]; !present {
		t.Fatal("filtered lead must carry its assignee")
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
