// HUI-1695 / FEAT-0196 渠道效果分析 HTTP E2E(登记制开关
// FEATURE_CHANNEL_ANALYTICS,与 FEATURE_FUNNEL 相互独立):
//   - off(默认):路由不注册,GET/POST /api/v1/funnel/channels 一律 404;
//   - on:窗口参数必填且 RFC3339([start,end) 与 FEAT-0195 同款语义);
//     granularity/source_type 与渠道 group-by 互斥,显式 400;
//   - 播种已知事实 -> 分渠道独立 SQL 复算 vs API 逐项一致;low_sample 阈上/
//     阈下两侧机器标注;ROI UNKNOWN(available=false + reason 引 HUI-1696,
//     spend/roi/cac 为 JSON null);同窗口重算幂等;渠道枚举披露;跨租户零串行;
//     owner 租户全量 / sales 只看自己人群(复用既有记录级作用域)。
package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func channelOn(t *testing.T) *harness {
	t.Helper()
	return newHarnessOpts(t, harnessOpts{featureChannelAnalytics: true})
}

// channelEntries extracts the channels array as maps keyed by channel name.
func channelEntries(t *testing.T, out map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := out["channels"].([]any)
	if !ok {
		t.Fatalf("response has no channels array: %v", out)
	}
	byName := map[string]map[string]any{}
	for _, it := range raw {
		m, _ := it.(map[string]any)
		k, _ := m["channel"].(string)
		byName[k] = m
	}
	return byName
}

// entryStages extracts one channel entry's stages array as maps keyed by stage.
func entryStages(t *testing.T, entry map[string]any) map[string]map[string]any {
	t.Helper()
	return funnelStages(t, entry)
}

// channelSQLCountsHTTP recomputes one channel's five stage counts with direct
// SQL (independent of the implementation) for row-by-row reconciliation.
func channelSQLCountsHTTP(t *testing.T, h *harness, tenant, channel string) (forms, profiles, followups, opps, won int) {
	t.Helper()
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(DISTINCT s.contact_id) FROM form_submissions s JOIN contacts c ON c.id=s.contact_id AND c.deleted_at IS NULL
		 WHERE s.tenant_id=? AND c.source_type=? AND s.created_at>='2026-01-01' AND s.created_at<'2026-02-01'`,
		tenant, channel).Scan(&forms); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(1) FROM contacts WHERE tenant_id=? AND source_type=? AND deleted_at IS NULL
		 AND created_at>='2026-01-01' AND created_at<'2026-02-01'`,
		tenant, channel).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(DISTINCT f.contact_id) FROM follow_ups f JOIN contacts c ON c.id=f.contact_id AND c.deleted_at IS NULL
		 WHERE f.tenant_id=? AND c.source_type=? AND f.created_at>='2026-01-01T00:00:00+00:00' AND f.created_at<'2026-02-01T00:00:00+00:00'`,
		tenant, channel).Scan(&followups); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(1) FROM opportunities o JOIN contacts c ON c.id=o.contact_id AND c.deleted_at IS NULL
		 WHERE o.tenant_id=? AND c.source_type=? AND o.created_at>='2026-01-01' AND o.created_at<'2026-02-01'`,
		tenant, channel).Scan(&opps); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(
		`SELECT COUNT(DISTINCT h.opportunity_id) FROM opportunity_stage_history h
		 JOIN opportunities o ON o.id=h.opportunity_id JOIN contacts c ON c.id=o.contact_id AND c.deleted_at IS NULL
		 WHERE h.tenant_id=? AND c.source_type=? AND h.to_stage='won' AND h.changed_at>='2026-01-01' AND h.changed_at<'2026-02-01'`,
		tenant, channel).Scan(&won); err != nil {
		t.Fatal(err)
	}
	return forms, profiles, followups, opps, won
}

func TestChannelFunnelFlagOff404(t *testing.T) {
	h := newHarness(t) // FEATURE_CHANNEL_ANALYTICS 缺省 off
	tenantA, _, _ := h.seed()
	h.mustDo("GET", "/api/v1/funnel/channels?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z",
		sessionOwnerA, tenantA, "", http.StatusNotFound)
	// 任何方法都不可见:路由族整体未注册。
	h.mustDo("POST", "/api/v1/funnel/channels", sessionOwnerA, tenantA, "{}", http.StatusNotFound)
}

func TestChannelFunnelParamsValidation(t *testing.T) {
	h := channelOn(t) // 只开渠道开关:与 FEATURE_FUNNEL 相互独立
	tenantA, _, _ := h.seed()
	base := "/api/v1/funnel/channels"

	// 独立登记制:渠道开关 on 而漏斗开关 off 时,漏斗端点仍 404,渠道端点可用。
	h.mustDo("GET", "/api/v1/funnel?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z",
		sessionOwnerA, tenantA, "", http.StatusNotFound)

	// 窗口必填且合法(与 FEAT-0195 同款 [start,end) 语义)。
	h.mustDo("GET", base, sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01&window_end=2026-02-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=not-a-time", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-02-01T00:00:00Z&window_end=2026-01-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-01-01T00:00:00Z", sessionOwnerA, tenantA, "", http.StatusBadRequest)
	// 渠道 group-by 的互斥参数:显式 400,绝不静默忽略。
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z&granularity=day",
		sessionOwnerA, tenantA, "", http.StatusBadRequest)
	h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z&source_type=form",
		sessionOwnerA, tenantA, "", http.StatusBadRequest)

	// 合法请求:种子联系人建档时间在窗口外(2026-09)-> 窗口内零事实 ->
	// channels 为空数组(JSON [],不是 null)。
	out := h.mustDo("GET", base+"?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z",
		sessionOwnerA, tenantA, "", http.StatusOK)
	arr, ok := out["channels"].([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("empty-window channels must be JSON [], got %v", out["channels"])
	}
}

func TestChannelFunnelOnE2E(t *testing.T) {
	h := channelOn(t)
	tenantA, tenantB, _ := h.seed()
	sales1 := h.memberID(tenantA, principalSalesA1)

	// 已知事实集(全部确定时间戳,窗口 [2026-01-01, 2026-02-01)):
	//   form 渠道:建档 10(阈上 low_sample=false)/ 留资 6 / 跟进 3 /
	//   商机 3(obs 恰在 start 含,obe 恰在 end 不含)/ 成交 1。
	for i := 1; i <= 10; i++ {
		assignee := ""
		if i == 1 {
			assignee = sales1 // sales 作用域断言用
		}
		funnelSeedContact(t, h, tenantA, "cc"+twoDigits(i),
			"139000000"+twoDigits(60+i), "form", assignee,
			"2026-01-"+twoDigits(i+1)+"T10:00:00Z")
	}
	funnelSeedFormSubmission(t, h, tenantA, "cfs1", "cc01", "2026-01-12T09:00:00Z")
	funnelSeedFormSubmission(t, h, tenantA, "cfs2", "cc02", "2026-01-12T09:00:00Z")
	funnelSeedFormSubmission(t, h, tenantA, "cfs3", "cc03", "2026-01-12T09:00:00Z")
	funnelSeedFormSubmission(t, h, tenantA, "cfs4", "cc04", "2026-01-12T09:00:00Z")
	funnelSeedFormSubmission(t, h, tenantA, "cfs5", "cc05", "2026-01-12T09:00:00Z")
	funnelSeedFormSubmission(t, h, tenantA, "cfs6", "cc06", "2026-01-12T09:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "cff1", "cc01", "2026-01-14T09:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "cff2", "cc02", "2026-01-14T09:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "cff3", "cc03", "2026-01-14T09:00:00Z")
	funnelSeedOpp(t, h, tenantA, "cof1", "cc01", sales1, "2026-01-15T09:00:00Z")
	funnelSeedOpp(t, h, tenantA, "cof2", "cc02", "", "2026-01-16T09:00:00Z")
	funnelSeedOpp(t, h, tenantA, "cobs", "cc03", "", "2026-01-01T00:00:00Z") // 恰在 start:含
	funnelSeedOpp(t, h, tenantA, "cobe", "cc04", "", "2026-02-01T00:00:00Z") // 恰在 end:不含
	funnelSeedWon(t, h, tenantA, "ch1", "cof1", "2026-01-20T09:00:00Z")
	//   touch_campaign 渠道:全链路小样本(建档 2 阈下 low_sample=true)。
	funnelSeedContact(t, h, tenantA, "ct1", "13900000071", "touch_campaign", "", "2026-01-03T11:00:00Z")
	funnelSeedContact(t, h, tenantA, "ct2", "13900000072", "touch_campaign", "", "2026-01-04T11:00:00Z")
	funnelSeedFormSubmission(t, h, tenantA, "cft1", "ct1", "2026-01-05T11:00:00Z")
	funnelSeedFollowUp(t, h, tenantA, "cfft1", "ct1", "2026-01-06T11:00:00Z")
	funnelSeedOpp(t, h, tenantA, "cot1", "ct1", "", "2026-01-08T11:00:00Z")
	funnelSeedWon(t, h, tenantA, "cht1", "cot1", "2026-01-10T11:00:00Z")

	path := "/api/v1/funnel/channels?window_start=2026-01-01T00:00:00Z&window_end=2026-02-01T00:00:00Z"
	out := h.mustDo("GET", path, sessionOwnerA, tenantA, "", http.StatusOK)
	if out["scope"] != "tenant" {
		t.Fatalf("owner scope = %v, want tenant", out["scope"])
	}

	// ROI 诚实降级(机器可断言):available=false + reason 引 HUI-1696 +
	// spend/roi/cac 键存在且为 JSON null(绝不置 0)。
	roi, ok := out["roi"].(map[string]any)
	if !ok {
		t.Fatalf("response has no roi object: %v", out)
	}
	if roi["available"] != false {
		t.Fatalf("roi.available = %v, want false", roi["available"])
	}
	if reason, _ := roi["reason"].(string); !strings.Contains(reason, "HUI-1696") {
		t.Fatalf("roi.reason must cite HUI-1696: %v", roi["reason"])
	}
	for _, k := range []string{"spend", "roi", "cac"} {
		if v, present := roi[k]; !present || v != nil {
			t.Fatalf("roi.%s must be present and JSON null(绝不置 0), got %v", k, v)
		}
	}
	// 样本量纪律披露:常量阈值 + 规则文本 + 维度披露。
	if th, _ := out["low_sample_threshold"].(float64); th != 10 {
		t.Fatalf("low_sample_threshold = %v, want 10", out["low_sample_threshold"])
	}
	for _, k := range []string{"channel_rule", "rate_rule", "low_sample_rule"} {
		if s, _ := out[k].(string); s == "" {
			t.Fatalf("%s disclosure missing", k)
		}
	}

	// 渠道枚举披露:只有窗口内出现过事实的渠道,按 sample_size 降序。
	chans := channelEntries(t, out)
	if len(chans) != 2 {
		t.Fatalf("channels = %d, want 2 (form/touch_campaign)", len(chans))
	}
	raw, _ := out["channels"].([]any)
	first, _ := raw[0].(map[string]any)
	if first["channel"] != "form" {
		t.Fatalf("channels[0] = %v, want form (sample_size 降序)", first["channel"])
	}

	// 分渠道独立 SQL 复算 vs API 逐项一致。
	for name, want := range map[string]struct {
		sample int
		low    bool
	}{
		"form":           {10, false},
		"touch_campaign": {2, true},
	} {
		e := chans[name]
		wantForms, wantProfiles, wantFollow, wantOpps, wantWon := channelSQLCountsHTTP(t, h, tenantA, name)
		if got, _ := e["sample_size"].(float64); got != float64(wantProfiles) || wantProfiles != want.sample {
			t.Fatalf("channel %s sample_size = %v, SQL recompute = %d, want %d", name, e["sample_size"], wantProfiles, want.sample)
		}
		if e["low_sample"] != want.low {
			t.Fatalf("channel %s low_sample = %v, want %v(阈值 10 两侧)", name, e["low_sample"], want.low)
		}
		st := entryStages(t, e)
		if len(st) != 6 {
			t.Fatalf("channel %s stages = %d, want 6", name, len(st))
		}
		// 每渠道曝光 UNKNOWN 照常降级。
		exp := st["exposure"]
		if exp["available"] != false {
			t.Fatalf("channel %s exposure must be available=false", name)
		}
		if _, present := exp["count"]; !present || exp["count"] != nil {
			t.Fatalf("channel %s exposure count must be JSON null, got %v", name, exp["count"])
		}
		// 五阶段计数与 SQL 复算一致。
		for key, wantCount := range map[string]float64{
			"lead_form":           float64(wantForms),
			"profile_created":     float64(wantProfiles),
			"followed_up":         float64(wantFollow),
			"opportunity_created": float64(wantOpps),
			"won":                 float64(wantWon),
		} {
			if got := stageCount(t, st[key]); got != wantCount {
				t.Fatalf("channel %s stage %s = %v, SQL recompute = %v", name, key, got, wantCount)
			}
		}
		// 窗口边界([start,end) 与 FEAT-0195 一致):form 商机恰在 start 的计入、
		// 恰在 end 的不计入 -> 3(SQL 复算已对照,这里固定人工推演值防双错)。
		if name == "form" {
			if got := stageCount(t, st["opportunity_created"]); got != 3 {
				t.Fatalf("form opportunity_created = %v, want 3(start 含/end 不含)", got)
			}
		}
	}

	// 同窗口重算幂等:两次响应逐字节一致。
	out2 := h.mustDo("GET", path, sessionOwnerA, tenantA, "", http.StatusOK)
	if raw1, raw2 := mustMarshal(t, out), mustMarshal(t, out2); raw1 != raw2 {
		t.Fatalf("same-window recompute must be identical:\n%s\n%s", raw1, raw2)
	}

	// 跨租户零串行:owner B 查自己(空)租户 -> 空数组;冒充 A 租户 -> 403。
	outB := h.mustDo("GET", path, sessionOwnerB, tenantB, "", http.StatusOK)
	if arrB, ok := outB["channels"].([]any); !ok || len(arrB) != 0 {
		t.Fatalf("tenant B channels = %v, want empty(跨租户零串行)", outB["channels"])
	}
	h.mustDo("GET", path, sessionOwnerB, tenantA, "", http.StatusForbidden)

	// sales 只看自己被指派人群(复用既有记录级作用域推导):form 渠道只剩
	// cc01 一条链路,touch 渠道整体不可见。
	outS := h.mustDo("GET", path, sessionSalesA1, tenantA, "", http.StatusOK)
	if outS["scope"] != "assigned_to_me" {
		t.Fatalf("sales scope = %v, want assigned_to_me", outS["scope"])
	}
	chansS := channelEntries(t, outS)
	if len(chansS) != 1 {
		t.Fatalf("sales channels = %d, want 1(只含自己人群的 form)", len(chansS))
	}
	eS := chansS["form"]
	if eS["low_sample"] != true {
		t.Fatalf("sales form low_sample = %v, want true(样本 1)", eS["low_sample"])
	}
	stS := entryStages(t, eS)
	for key, want := range map[string]float64{
		"lead_form": 1, "profile_created": 1, "followed_up": 1, "opportunity_created": 1, "won": 1,
	} {
		if got := stageCount(t, stS[key]); got != want {
			t.Fatalf("sales form stage %s = %v, want %v", key, got, want)
		}
	}
}

// twoDigits renders 0..99 as two digits (test fixture helper).
func twoDigits(n int) string {
	return string(rune('0'+n/10%10)) + string(rune('0'+n%10))
}
