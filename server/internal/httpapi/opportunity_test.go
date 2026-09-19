// HUI-1693 / FEAT-0194 商机域测试:阶段审计链、幂等、类别隔离(无跨类别合计)、
// 金额「未知」语义(NULL 不当 0,无「已收款」语义)、阶段转换权限矩阵、
// FEATURE_SERVICE_DRAFT 挂载点默认关闭。
package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

// createOpp creates an opportunity and returns its id.
func (h *harness) createOpp(t *testing.T, session, tenant, contactID, title, category, extra string) string {
	t.Helper()
	body := fmt.Sprintf(`{"contact_id":%q,"title":%q,"business_category":%q%s}`, contactID, title, category, extra)
	created := h.mustDo("POST", "/api/v1/opportunities", session, tenant, body, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create opportunity: no id in %v", created)
	}
	return id
}

// historyStages returns the from->to pairs of the stage history, oldest first.
func historyStages(t *testing.T, out map[string]any) [][2]string {
	t.Helper()
	items, _ := out["items"].([]any)
	var pairs [][2]string
	for _, it := range items {
		m, _ := it.(map[string]any)
		from, _ := m["from_stage"].(string)
		to, _ := m["to_stage"].(string)
		pairs = append(pairs, [2]string{from, to})
	}
	return pairs
}

// ---- 阶段生命周期 + 审计链 + 幂等 --------------------------------------------------

func TestOpportunityStageLifecycleAndAudit(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contactA1 := h.seed()

	// 商家经营销售样例:由 salesA1 创建(自动 assign 自己),走完整阶段链。
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "首单合作", "merchant_customer", "")

	stagePath := "/api/v1/opportunities/" + opp + "/stage"

	t.Run("creation writes the initial audit row", func(t *testing.T) {
		out := h.mustDo("GET", stagePath+"-history", sessionSalesA1, tenantA, "", http.StatusOK)
		pairs := historyStages(t, out)
		if len(pairs) != 1 || pairs[0][0] != "" || pairs[0][1] != "open" {
			t.Fatalf("initial history = %v, want [['' open]]", pairs)
		}
	})

	t.Run("assigned sales advances the stage; audit row records who", func(t *testing.T) {
		got := h.mustDo("POST", stagePath, sessionSalesA1, tenantA,
			`{"to_stage":"qualified","note":"电话确认有预算"}`, http.StatusOK)
		if got["stage"] != "qualified" {
			t.Fatalf("stage = %v, want qualified", got["stage"])
		}
		out := h.mustDo("GET", stagePath+"-history", sessionSalesA1, tenantA, "", http.StatusOK)
		pairs := historyStages(t, out)
		if len(pairs) != 2 || pairs[1][0] != "open" || pairs[1][1] != "qualified" {
			t.Fatalf("history = %v", pairs)
		}
		items := out["items"].([]any)
		last := items[len(items)-1].(map[string]any)
		if last["changed_by"] != h.memberID(tenantA, principalSalesA1) {
			t.Fatalf("changed_by = %v, want salesA1 member id", last["changed_by"])
		}
		if last["note"] != "电话确认有预算" {
			t.Fatalf("note = %v", last["note"])
		}
	})

	t.Run("repeating the current stage is idempotent: 200 and no extra history row", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionSalesA1, tenantA, `{"to_stage":"qualified"}`, http.StatusOK)
		out := h.mustDo("GET", stagePath+"-history", sessionSalesA1, tenantA, "", http.StatusOK)
		if got := len(out["items"].([]any)); got != 2 {
			t.Fatalf("history rows = %d, want 2 (no duplicate audit row)", got)
		}
	})

	t.Run("unknown stage is 400", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionSalesA1, tenantA, `{"to_stage":"shipped"}`, http.StatusBadRequest)
	})

	t.Run("stage can no longer be changed via PATCH (audit bypass closed)", func(t *testing.T) {
		h.mustDo("PATCH", "/api/v1/opportunities/"+opp, sessionSalesA1, tenantA,
			`{"stage":"won"}`, http.StatusBadRequest)
	})

	t.Run("full chain proposal->negotiation->won then reopen is audited", func(t *testing.T) {
		for _, to := range []string{"proposal", "negotiation", "won"} {
			h.mustDo("POST", stagePath, sessionSalesA1, tenantA,
				fmt.Sprintf(`{"to_stage":%q}`, to), http.StatusOK)
		}
		// 人工标记成交 -> 重新打开(重开也是普通阶段转换,同一审计)
		h.mustDo("POST", stagePath, sessionSalesA1, tenantA, `{"to_stage":"open","note":"客户延期"}`, http.StatusOK)
		h.mustDo("POST", stagePath, sessionSalesA1, tenantA, `{"to_stage":"closed_lost"}`, http.StatusOK)
		out := h.mustDo("GET", stagePath+"-history", sessionSalesA1, tenantA, "", http.StatusOK)
		want := [][2]string{{"", "open"}, {"open", "qualified"}, {"qualified", "proposal"},
			{"proposal", "negotiation"}, {"negotiation", "won"}, {"won", "open"},
			{"open", "closed_lost"}}
		got := historyStages(t, out)
		if len(got) != len(want) {
			t.Fatalf("history len = %d, want %d (%v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("history[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})
}

// ---- 类别隔离:统计与列表都按 business_category 隔离,无跨类别合计端点 ---------------

func TestOpportunityCategoryIsolation(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contactA1 := h.seed()

	// 商家经营销售:2 单,一单有金额、一单无金额(NULL=未知)。
	merchantKnown := h.createOpp(t, sessionOwnerA, tenantA, contactA1, "商家进货", "merchant_customer",
		`,"amount_cents":10000,"probability":40`)
	h.createOpp(t, sessionOwnerA, tenantA, contactA1, "商家复购", "merchant_customer", "")
	// 创意服务:1 单已人工标记成交,金额 5000 分。
	creative := h.createOpp(t, sessionOwnerA, tenantA, contactA1, "品牌视频", "creative_service",
		`,"amount_cents":5000,"expected_close_at":"2026-10-01T00:00:00Z"`)
	h.mustDo("POST", "/api/v1/opportunities/"+creative+"/stage", sessionOwnerA, tenantA,
		`{"to_stage":"won"}`, http.StatusOK)

	t.Run("stats are per category only; category is required", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/opportunities/stats", sessionOwnerA, tenantA, "", http.StatusBadRequest)
		h.mustDo("GET", "/api/v1/opportunities/stats?category=bogus", sessionOwnerA, tenantA, "", http.StatusBadRequest)

		m := h.mustDo("GET", "/api/v1/opportunities/stats?category=merchant_customer", sessionOwnerA, tenantA, "", http.StatusOK)
		if m["business_category"] != "merchant_customer" {
			t.Fatalf("category = %v", m["business_category"])
		}
		funnel := m["funnel"].(map[string]any)
		if funnel["open"] != float64(2) || funnel["won"] != float64(0) {
			t.Fatalf("merchant funnel = %v", funnel)
		}
		amounts := m["amounts"].(map[string]any)
		// 10000 分是唯一可核实金额;无金额那单进「未知」桶,绝不当 0 计入合计。
		if amounts["known_total_cents"] != float64(10000) {
			t.Fatalf("merchant known_total_cents = %v, want 10000", amounts["known_total_cents"])
		}
		if amounts["unknown_count"] != float64(1) {
			t.Fatalf("merchant unknown_count = %v, want 1", amounts["unknown_count"])
		}

		c := h.mustDo("GET", "/api/v1/opportunities/stats?category=creative_service", sessionOwnerA, tenantA, "", http.StatusOK)
		cFunnel := c["funnel"].(map[string]any)
		if cFunnel["won"] != float64(1) || cFunnel["open"] != float64(0) {
			t.Fatalf("creative funnel = %v", cFunnel)
		}
		cAmounts := c["amounts"].(map[string]any)
		if cAmounts["known_total_cents"] != float64(5000) || cAmounts["unknown_count"] != float64(0) {
			t.Fatalf("creative amounts = %v", cAmounts)
		}
		// 每个类别的合计都只含本类别: merchant(10000) != creative(5000),互不混算。
	})

	t.Run("list requires and honors the category filter", func(t *testing.T) {
		h.mustDo("GET", "/api/v1/opportunities", sessionOwnerA, tenantA, "", http.StatusBadRequest)
		m := h.mustDo("GET", "/api/v1/opportunities?category=merchant_customer", sessionOwnerA, tenantA, "", http.StatusOK)
		items := m["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("merchant list len = %d, want 2", len(items))
		}
		for _, it := range items {
			o := it.(map[string]any)
			if o["business_category"] != "merchant_customer" {
				t.Fatalf("list leaked another category: %v", o)
			}
		}
		c := h.mustDo("GET", "/api/v1/opportunities?category=creative_service", sessionOwnerA, tenantA, "", http.StatusOK)
		if got := len(c["items"].([]any)); got != 1 {
			t.Fatalf("creative list len = %d, want 1", got)
		}
	})

	t.Run("NULL amount returns JSON null, never 0", func(t *testing.T) {
		got := h.mustDo("GET", "/api/v1/opportunities/"+merchantKnown, sessionOwnerA, tenantA, "", http.StatusOK)
		if got["amount_cents"] != float64(10000) || got["amount_source"] != "manual" {
			t.Fatalf("known opp = %v", got)
		}
	})
}

// ---- 金额/概率/预计成交语义 ---------------------------------------------------------

func TestOpportunityAmountSemantics(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contactA1 := h.seed()

	opp := h.createOpp(t, sessionOwnerA, tenantA, contactA1, "语义样例", "creative_service", "")

	t.Run("unset amount is unknown by construction", func(t *testing.T) {
		got := h.mustDo("GET", "/api/v1/opportunities/"+opp, sessionOwnerA, tenantA, "", http.StatusOK)
		if v, present := got["amount_cents"]; !present || v != nil {
			t.Fatalf("amount_cents = %v (present=%v), want null", v, present)
		}
		if got["amount_source"] != "unknown" {
			t.Fatalf("amount_source = %v, want unknown", got["amount_source"])
		}
	})

	t.Run("setting an amount marks the source manual; clearing reverts to unknown", func(t *testing.T) {
		got := h.mustDo("PATCH", "/api/v1/opportunities/"+opp, sessionOwnerA, tenantA,
			`{"amount_cents":88800,"probability":25,"expected_close_at":"2026-12-31T00:00:00Z"}`, http.StatusOK)
		if got["amount_cents"] != float64(88800) || got["amount_source"] != "manual" || got["probability"] != float64(25) {
			t.Fatalf("patched = %v", got)
		}
		if got["expected_close_at"] != "2026-12-31T00:00:00Z" {
			t.Fatalf("expected_close_at = %v", got["expected_close_at"])
		}
		cleared := h.mustDo("PATCH", "/api/v1/opportunities/"+opp, sessionOwnerA, tenantA,
			`{"amount_cents":null,"expected_close_at":null}`, http.StatusOK)
		if cleared["amount_cents"] != nil || cleared["amount_source"] != "unknown" || cleared["expected_close_at"] != nil {
			t.Fatalf("cleared = %v", cleared)
		}
		// 缺省字段不被误清
		kept := h.mustDo("PATCH", "/api/v1/opportunities/"+opp, sessionOwnerA, tenantA,
			`{"amount_cents":100,"title":"语义样例二"}`, http.StatusOK)
		if kept["probability"] != float64(25) {
			t.Fatalf("absent probability must be unchanged, got %v", kept["probability"])
		}
	})

	t.Run("probability out of range and bad dates are 400", func(t *testing.T) {
		h.mustDo("PATCH", "/api/v1/opportunities/"+opp, sessionOwnerA, tenantA,
			`{"probability":150}`, http.StatusBadRequest)
		h.mustDo("PATCH", "/api/v1/opportunities/"+opp, sessionOwnerA, tenantA,
			`{"expected_close_at":"next tuesday"}`, http.StatusBadRequest)
		h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"title":"x","business_category":"creative_service","probability":-1}`, contactA1),
			http.StatusBadRequest)
	})

	t.Run("legacy-free enum: only the six stages validate at create", func(t *testing.T) {
		h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"title":"x","business_category":"creative_service","stage":"proposal"}`, contactA1),
			http.StatusCreated)
		h.mustDo("POST", "/api/v1/opportunities", sessionOwnerA, tenantA,
			fmt.Sprintf(`{"contact_id":%q,"title":"x","business_category":"creative_service","stage":"lost"}`, contactA1),
			http.StatusBadRequest)
	})
}

// ---- 阶段转换权限矩阵(阶段动作与 L0 更新权限同构) ---------------------------------

func TestOpportunityStagePermissionMatrix(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contactA1 := h.seed()

	// opp 归 salesA1(assignee)。
	opp := h.createOpp(t, sessionSalesA1, tenantA, contactA1, "权限矩阵样例", "merchant_customer", "")
	stagePath := "/api/v1/opportunities/" + opp + "/stage"
	historyPath := stagePath + "-history"

	t.Run("record assignee may transition", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionSalesA1, tenantA, `{"to_stage":"qualified"}`, http.StatusOK)
	})
	t.Run("tenant owner may transition any record", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionOwnerA, tenantA, `{"to_stage":"proposal"}`, http.StatusOK)
	})
	t.Run("non-assignee sales is masked 404", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionSalesA2, tenantA, `{"to_stage":"won"}`, http.StatusNotFound)
		h.mustDo("GET", historyPath, sessionSalesA2, tenantA, "", http.StatusNotFound)
	})
	t.Run("cross-tenant is 403 on stage, history and stats", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionOwnerB, tenantA, `{"to_stage":"won"}`, http.StatusForbidden)
		h.mustDo("GET", historyPath, sessionOwnerB, tenantA, "", http.StatusForbidden)
		h.mustDo("GET", "/api/v1/opportunities/stats?category=merchant_customer", sessionOwnerB, tenantA, "", http.StatusForbidden)
	})
	t.Run("disabled member is 403", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionDisabled, tenantA, `{"to_stage":"won"}`, http.StatusForbidden)
	})
	t.Run("agent without grant is 403", func(t *testing.T) {
		h.mustDo("POST", stagePath, sessionAgentA, tenantA, `{"to_stage":"won"}`, http.StatusForbidden)
	})

	t.Run("stats honor the assignment filter for non-owners", func(t *testing.T) {
		// owner 名下单据不该出现在 salesA1 的漏斗里(不泄漏他人漏斗)。
		h.createOpp(t, sessionOwnerA, tenantA, contactA1, "owner 单", "merchant_customer", "")
		sales := h.mustDo("GET", "/api/v1/opportunities/stats?category=merchant_customer", sessionSalesA1, tenantA, "", http.StatusOK)
		if sales["total"] != float64(1) {
			t.Fatalf("sales stats total = %v, want 1 (own only)", sales["total"])
		}
		owner := h.mustDo("GET", "/api/v1/opportunities/stats?category=merchant_customer", sessionOwnerA, tenantA, "", http.StatusOK)
		if owner["total"] != float64(2) {
			t.Fatalf("owner stats total = %v, want 2 (tenant-wide)", owner["total"])
		}
	})
}

// ---- L1 挂载点:FEATURE_SERVICE_DRAFT(归 HUI-1749/1751) ----------------------------

func TestServiceDraftIntentMount(t *testing.T) {
	intent := func(h *harness, oppID string) string {
		return "/api/v1/opportunities/" + oppID + "/service-draft-intent"
	}

	t.Run("default off: the route does not exist (404, invisible)", func(t *testing.T) {
		h := newHarness(t)
		tenantA, _, contactA1 := h.seed()
		opp := h.createOpp(t, sessionOwnerA, tenantA, contactA1, "挂载点样例", "creative_service", "")
		// 有会话也是 404:未注册路由,功能不可见。
		h.mustDo("POST", intent(h, opp), sessionOwnerA, tenantA, `{}`, http.StatusNotFound)
	})

	t.Run("on without eco deployment facts: 503 config_gate_eco (fail-closed, never fakes delivery)", func(t *testing.T) {
		h := newHarnessOpts(t, harnessOpts{featureServiceDraft: true})
		tenantA, _, contactA1 := h.seed()
		opp := h.createOpp(t, sessionOwnerA, tenantA, contactA1, "挂载点样例", "creative_service", "")
		out := h.mustDo("POST", intent(h, opp), sessionOwnerA, tenantA, `{}`, http.StatusServiceUnavailable)
		if out["error"] != "config_gate_eco" {
			t.Fatalf("error = %v, want config_gate_eco", out["error"])
		}
		// 未认证仍 401:挂载点也不向匿名者泄露存在性。
		h.mustDo("POST", intent(h, opp), "", tenantA, `{}`, http.StatusUnauthorized)
	})
}
