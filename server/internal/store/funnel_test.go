// HUI-1694 / FEAT-0195 全漏斗分析 store 层单测:
// 已知事实集直接 SQL 播种 -> 手工独立复算 vs FunnelAnalysis 输出逐项一致。
// 覆盖:唯一联系人去重(同号孪生合并/窗口前已建档不计/无手机号独立身份)、
// 窗口边界(start 含/end 不含)、跟进/商机/成交各阶段分母、won 重开再 won 的
// 确定语义、同窗口重算幂等、晚到事件按事件时间计入、来源/成员作用域下钻、
// 跨租户零串行、UNKNOWN 触点阶段(count=null + 中文 reason)、空租户全零。
package store

import (
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/db"
)

// marshalForTest renders a deterministic JSON view for deep-equality asserts.
func marshalForTest(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// funnelTestDB opens a migration-backed sqlite with two tenants (and one
// member each — follow_ups/stage-history created_by carry member FKs) seeded.
func funnelTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	for _, tn := range []string{"tnt_1", "tnt_2"} {
		if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES(?,?,'2025-01-01T00:00:00Z')`, tn, tn); err != nil {
			t.Fatalf("seed tenant: %v", err)
		}
		if _, err := d.Exec(
			`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
			 VALUES(?,?,'usr_fx_'||?,'owner',1,'x','','2025-01-01T00:00:00Z','2025-01-01T00:00:00Z')`,
			"mem_fx_"+tn, tn, tn); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	return d
}

// funnelActorID resolves the seeded member of the tenant (FK subject for
// follow_ups.created_by / opportunity_stage_history.changed_by).
func funnelActorID(t *testing.T, d *sql.DB, tenant string) string {
	t.Helper()
	var id string
	if err := d.QueryRow(`SELECT id FROM members WHERE tenant_id=? LIMIT 1`, tenant).Scan(&id); err != nil {
		t.Fatalf("member for %s: %v", tenant, err)
	}
	return id
}

// funnelSeedContact inserts a live (or tombstoned) contacts row with explicit
// created_at so the window math is fully deterministic in tests.
func funnelSeedContact(t *testing.T, d *sql.DB, tenant, id, phone, sourceType, assignee, createdAt string, deleted bool) {
	t.Helper()
	deletedAt := any(nil)
	if deleted {
		deletedAt = createdAt
	}
	if _, err := d.Exec(
		`INSERT INTO contacts(id,tenant_id,name,phone,email,business_category,source_type,consent_status,notes,tags,deleted_at,source_ref_id,assigned_member_id,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, tenant, "联系人"+id, phone, "", "merchant_customer", sourceType, "pending", "", "",
		deletedAt, nil, nullable(assignee), "test", createdAt, createdAt); err != nil {
		t.Fatalf("seed contact %s: %v", id, err)
	}
}

// funnelSeedFollowUp inserts a follow_ups row (FEAT-0193) with explicit time.
func funnelSeedFollowUp(t *testing.T, d *sql.DB, tenant, id, contactID, createdAt string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO follow_ups(id,tenant_id,contact_id,lead_id,note,next_follow_up_at,completed_at,created_by,created_at,updated_at)
		 VALUES(?,?,?,NULL,'跟进','',NULL,?,?,?)`, id, tenant, contactID,
		funnelActorID(t, d, tenant), createdAt, createdAt); err != nil {
		t.Fatalf("seed follow-up %s: %v", id, err)
	}
}

// funnelSeedOpp inserts an opportunities row plus its creation stage-history
// row (from_stage=”) exactly as CreateOpportunity writes them.
func funnelSeedOpp(t *testing.T, d *sql.DB, tenant, id, contactID, stage, assignee, createdAt string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO opportunities(id,tenant_id,contact_id,title,stage,business_category,amount_cents,amount_source,probability,expected_close_at,assigned_member_id,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,'merchant_customer',NULL,'unknown',0,NULL,?,'test',?,?)`,
		id, tenant, contactID, "商机"+id, stage, nullable(assignee), createdAt, createdAt); err != nil {
		t.Fatalf("seed opportunity %s: %v", id, err)
	}
	if _, err := d.Exec(
		`INSERT INTO opportunity_stage_history(id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note)
		 VALUES(?,?,?,'',?,?,?,'created')`,
		"ohs_"+id, tenant, id, stage, funnelActorID(t, d, tenant), createdAt); err != nil {
		t.Fatalf("seed opp history %s: %v", id, err)
	}
}

// funnelSeedStageEvent appends one audited stage transition row.
func funnelSeedStageEvent(t *testing.T, d *sql.DB, tenant, id, oppID, from, to, changedAt string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO opportunity_stage_history(id,tenant_id,opportunity_id,from_stage,to_stage,changed_by,changed_at,note)
		 VALUES(?,?,?,?,?,?,?,'')`, id, tenant, oppID, from, to, funnelActorID(t, d, tenant), changedAt); err != nil {
		t.Fatalf("seed stage event %s: %v", id, err)
	}
}

// funnelMembers seeds owner/sales members for scope tests and returns ids.
func funnelMembers(t *testing.T, d *sql.DB) (m1, m2 string) {
	t.Helper()
	s := New(d)
	m1 = assignMember(t, s, "tnt_1", "usr_f1", "sales", true)
	m2 = assignMember(t, s, "tnt_1", "usr_f2", "sales", true)
	return m1, m2
}

func funnelQuery(tenant string) FunnelQuery {
	start, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	end, _ := time.Parse(time.RFC3339, "2026-02-01T00:00:00Z")
	return FunnelQuery{TenantID: tenant, WindowStart: start, WindowEnd: end, WithSeries: true}
}

func stageOf(t *testing.T, r *FunnelReport, key string) FunnelStage {
	t.Helper()
	for _, st := range r.Stages {
		if st.Key == key {
			return st
		}
	}
	t.Fatalf("stage %q missing from report %+v", key, r.Stages)
	return FunnelStage{}
}

func wantCount(t *testing.T, r *FunnelReport, key string, want int) {
	t.Helper()
	st := stageOf(t, r, key)
	if st.Count == nil {
		t.Fatalf("stage %s count = null, want %d", key, want)
	}
	if *st.Count != want {
		t.Fatalf("stage %s count = %d, want %d", key, *st.Count, want)
	}
}

func wantRate(t *testing.T, r *FunnelReport, key string, want float64) {
	t.Helper()
	st := stageOf(t, r, key)
	if st.RateFromPrevious == nil {
		t.Fatalf("stage %s rate = null, want %v", key, want)
	}
	if math.Abs(*st.RateFromPrevious-want) > 1e-9 {
		t.Fatalf("stage %s rate = %v, want %v", key, *st.RateFromPrevious, want)
	}
}

func wantNilRate(t *testing.T, r *FunnelReport, key string) {
	t.Helper()
	if st := stageOf(t, r, key); st.RateFromPrevious != nil {
		t.Fatalf("stage %s rate = %v, want null", key, *st.RateFromPrevious)
	}
}

// knownFactSet seeds the full deterministic scenario for tnt_1:
//
//	窗口 [2026-01-01, 2026-02-01)
//	建档 = 3:cA(01-05,同号孪生 cA2 01-06 合并计 1)+ cC(01-08,无手机号独立身份)
//	         + cD(01-09,"+86 139-0000-0003" 与 cD2 "13900000003" 归一化同身份)
//	排除:cB(窗口前 2025-12-20 已建档)、cE(02-05 窗口后)、cF(墓碑)
//	跟进 = 3:A(01-15/01-20 两条计 1)+ B(01-16)+ E(01-17;档案建在窗口外但
//	         跟进发生在窗口内,阶段独立计数)
//	排除:cC(唯一跟进在窗口前)、cF(墓碑档案 JOIN 掉)
//	商机 = 4:o7(恰在 window_start,含)+ o1(01-18)+ o2(01-19)+ o6(01-23)
//	排除:o4(恰在 window_end,不含)、o3(窗口前创建)、o5(墓碑档案)
//	成交 = 2:o1(01-25 won)+ o2(01-26 首次 won;01-27 重开、01-28 再 won 不重复计)
//	排除:o4(02-02 才 won)、o3(2025-12-28 won)、o6(02-03 才 won)
func knownFactSet(t *testing.T, d *sql.DB) {
	t.Helper()
	funnelSeedContact(t, d, "tnt_1", "cA", "13900000001", "form", "", "2026-01-05T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cA2", "13900000001", "form", "", "2026-01-06T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cB", "13900000002", "touch_campaign", "", "2025-12-20T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cB2", "13900000002", "touch_campaign", "", "2026-01-07T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cC", "", "manual", "", "2026-01-08T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cD", "+86 139-0000-0003", "form", "", "2026-01-09T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cD2", "13900000003", "form", "", "2026-01-10T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cE", "13900000004", "form", "", "2026-02-05T10:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cF", "13900000005", "form", "", "2026-01-12T10:00:00Z", true)

	funnelSeedFollowUp(t, d, "tnt_1", "f1", "cA", "2026-01-15T09:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "f2", "cA", "2026-01-20T09:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "f3", "cB", "2026-01-16T09:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "f4", "cE", "2026-01-17T09:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "f5", "cC", "2025-12-31T09:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "f6", "cF", "2026-01-18T09:00:00Z")

	funnelSeedOpp(t, d, "tnt_1", "o7", "cA", "open", "", "2026-01-01T00:00:00Z") // 恰在 start:含
	funnelSeedOpp(t, d, "tnt_1", "o1", "cA", "open", "", "2026-01-18T09:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "o2", "cB", "open", "", "2026-01-19T09:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "o3", "cE", "open", "", "2025-12-15T09:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "o4", "cC", "open", "", "2026-02-01T00:00:00Z") // 恰在 end:不含
	funnelSeedOpp(t, d, "tnt_1", "o5", "cF", "open", "", "2026-01-22T09:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "o6", "cA", "open", "", "2026-01-23T09:00:00Z")

	funnelSeedStageEvent(t, d, "tnt_1", "h1", "o1", "open", "won", "2026-01-25T09:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h2", "o2", "open", "won", "2026-01-26T09:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h3", "o2", "won", "open", "2026-01-27T09:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h4", "o2", "open", "won", "2026-01-28T09:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h5", "o4", "open", "won", "2026-02-02T09:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h6", "o3", "open", "won", "2025-12-28T09:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h7", "o6", "open", "won", "2026-02-03T09:00:00Z")
}

// 全场景:已知事实集 -> 手工独立复算逐项一致;含窗口边界、去重、重开不重复计。
func TestFunnelKnownFactSet(t *testing.T) {
	d := funnelTestDB(t)
	knownFactSet(t, d)

	r, err := New(d).FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	if len(r.Stages) != 6 {
		t.Fatalf("stages = %d, want 6 (曝光/留资/建档/跟进/商机/成交)", len(r.Stages))
	}

	// 独立 SQL 复算(与实现不同的写法):窗口内新建档案行数(未去重参照)。
	var liveRows int
	if err := d.QueryRow(
		`SELECT COUNT(1) FROM contacts WHERE tenant_id='tnt_1' AND deleted_at IS NULL
		 AND created_at>='2026-01-01' AND created_at<'2026-02-01'`).Scan(&liveRows); err != nil {
		t.Fatal(err)
	}
	if liveRows != 6 { // cA,cA2,cB2,cC,cD,cD2(去重后为 3 个身份)
		t.Fatalf("sanity: live in-window contact rows = %d, want 6", liveRows)
	}

	// UNKNOWN 触点阶段:available=false、count=null、中文 reason 引用票号。
	for _, key := range []string{"exposure", "lead_form"} {
		st := stageOf(t, r, key)
		if st.Available {
			t.Fatalf("stage %s must be unavailable (touch 域原生事实不在本仓)", key)
		}
		if st.Count != nil {
			t.Fatalf("stage %s count = %d, want null(绝不置 0)", key, *st.Count)
		}
		if st.Reason == "" {
			t.Fatalf("stage %s must carry a non-empty reason", key)
		}
	}
	if want := "HUI-1677"; !strings.Contains(stageOf(t, r, "exposure").Reason, want) {
		t.Fatalf("exposure reason must cite %s: %s", want, stageOf(t, r, "exposure").Reason)
	}
	if want := "HUI-1680"; !strings.Contains(stageOf(t, r, "lead_form").Reason, want) {
		t.Fatalf("lead_form reason must cite %s: %s", want, stageOf(t, r, "lead_form").Reason)
	}

	wantCount(t, r, "profile_created", 3)
	wantCount(t, r, "followed_up", 3)
	wantCount(t, r, "opportunity_created", 4)
	wantCount(t, r, "won", 2)

	wantNilRate(t, r, "profile_created") // 链首无上一可用阶段
	wantRate(t, r, "followed_up", 1.0)
	wantRate(t, r, "opportunity_created", 4.0/3.0)
	wantRate(t, r, "won", 2.0/4.0)

	// 日粒度序列:每阶段首次事件日期分布,series 求和 == count。
	wantSeries := map[string]map[string]int{
		"profile_created":     {"2026-01-05": 1, "2026-01-08": 1, "2026-01-09": 1},
		"followed_up":         {"2026-01-15": 1, "2026-01-16": 1, "2026-01-17": 1},
		"opportunity_created": {"2026-01-01": 1, "2026-01-18": 1, "2026-01-19": 1, "2026-01-23": 1},
		"won":                 {"2026-01-25": 1, "2026-01-26": 1},
	}
	for key, days := range wantSeries {
		st := stageOf(t, r, key)
		if len(st.Series) != len(days) {
			t.Fatalf("stage %s series = %v, want %v", key, st.Series, days)
		}
		sum := 0
		for _, p := range st.Series {
			if days[p.Date] != p.Count {
				t.Fatalf("stage %s series point %s = %d, want %d", key, p.Date, p.Count, days[p.Date])
			}
			sum += p.Count
		}
		if sum != *st.Count {
			t.Fatalf("stage %s series sum = %d, count = %d (series 必须可加和到总数)", key, sum, *st.Count)
		}
	}

	// 每个可用阶段都有完整定义披露(事件来源/去重键/分母/事件时间字段/窗口)。
	for _, key := range []string{"profile_created", "followed_up", "opportunity_created", "won"} {
		def := stageOf(t, r, key).Definition
		if def == nil || def.EventSource == "" || def.DedupKey == "" || def.Denominator == "" ||
			def.EventTimeField == "" || def.Window == "" {
			t.Fatalf("stage %s definition incomplete: %+v", key, def)
		}
	}
}

// 空租户:全部可用阶段为 0;上一阶段为 0 时 rate=null;无 series 点。
func TestFunnelEmptyTenant(t *testing.T) {
	d := funnelTestDB(t)
	r, err := New(d).FunnelAnalysis(funnelQuery("tnt_2"))
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	for _, key := range []string{"profile_created", "followed_up", "opportunity_created", "won"} {
		wantCount(t, r, key, 0)
		wantNilRate(t, r, key) // 上一可用阶段为 0 -> rate=null(链式传导)
		if st := stageOf(t, r, key); len(st.Series) != 0 {
			t.Fatalf("stage %s series = %v, want empty", key, st.Series)
		}
	}
}

// 同窗口重算幂等:纯计算无状态,两次输出逐字段一致。
func TestFunnelRecomputeIdempotent(t *testing.T) {
	d := funnelTestDB(t)
	knownFactSet(t, d)
	s := New(d)
	first, err := s.FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel 1: %v", err)
	}
	second, err := s.FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel 2: %v", err)
	}
	if got, want := marshalForTest(t, second), marshalForTest(t, first); got != want {
		t.Fatalf("recompute differs:\nfirst  %s\nsecond %s", want, got)
	}
}

// 晚到事件:窗口结果 = f(当前事实, 窗口)。事件时间在窗口内、入库晚于窗口期,
// 重算时按事件时间计入,且再次重算稳定(幂等、不推算、不置 0)。
func TestFunnelLateArrivingEvent(t *testing.T) {
	d := funnelTestDB(t)
	knownFactSet(t, d)
	s := New(d)
	before, err := s.FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel before: %v", err)
	}
	wantCount(t, before, "followed_up", 3)

	// 晚到入库:事件时间落在窗口内(2026-01-10),入库发生在"现在"。
	funnelSeedFollowUp(t, d, "tnt_1", "f-late", "cC", "2026-01-10T09:00:00Z")

	after, err := s.FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel after: %v", err)
	}
	wantCount(t, after, "followed_up", 4) // cC 因晚到跟进入窗
	again, err := s.FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel again: %v", err)
	}
	if marshalForTest(t, again) != marshalForTest(t, after) {
		t.Fatal("late-event recompute must be stable")
	}
}

// 成交的确定语义:同一商机窗口内 won->open->won 只计 1(按商机去重);
// won 后重开不回溯改写窗口结果;closed_lost 后转 won 正常计入。
func TestFunnelWonDedupAndReopen(t *testing.T) {
	d := funnelTestDB(t)
	funnelSeedContact(t, d, "tnt_1", "cA", "13900000006", "form", "", "2026-01-02T00:00:00Z", false)
	funnelSeedOpp(t, d, "tnt_1", "oA", "cA", "open", "", "2026-01-03T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "oB", "cA", "open", "", "2026-01-04T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "ha1", "oA", "open", "closed_lost", "2026-01-05T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "ha2", "oA", "closed_lost", "won", "2026-01-06T00:00:00Z")
	// oB:窗口内两次 won(01-07、01-09),中间重开 —— 一个商机只计一次。
	funnelSeedStageEvent(t, d, "tnt_1", "hb1", "oB", "open", "won", "2026-01-07T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "hb2", "oB", "won", "open", "2026-01-08T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "hb3", "oB", "open", "won", "2026-01-09T00:00:00Z")

	r, err := New(d).FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	wantCount(t, r, "won", 2) // {oA, oB},按唯一商机去重
	st := stageOf(t, r, "won")
	if len(st.Series) != 2 || st.Series[0].Date != "2026-01-06" || st.Series[1].Date != "2026-01-07" {
		t.Fatalf("won series = %v, want first-won dates 01-06/01-07", st.Series)
	}
}

// 来源下钻:source_type 过滤作用在联系人人群上,四阶段随人群一致收窄;
// 非法来源由 HTTP 层拒绝,store 层空串表示不过滤。
func TestFunnelSourceTypeDrillDown(t *testing.T) {
	d := funnelTestDB(t)
	funnelSeedContact(t, d, "tnt_1", "cT", "13900000007", "touch_campaign", "", "2026-01-05T00:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cM", "13900000008", "manual", "", "2026-01-06T00:00:00Z", false)
	funnelSeedFollowUp(t, d, "tnt_1", "fT", "cT", "2026-01-08T00:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "fM", "cM", "2026-01-08T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "oT", "cT", "open", "", "2026-01-09T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "oM", "cM", "open", "", "2026-01-09T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "hT", "oT", "open", "won", "2026-01-10T00:00:00Z")

	q := funnelQuery("tnt_1")
	q.SourceType = "touch_campaign"
	r, err := New(d).FunnelAnalysis(q)
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	wantCount(t, r, "profile_created", 1)
	wantCount(t, r, "followed_up", 1)
	wantCount(t, r, "opportunity_created", 1)
	wantCount(t, r, "won", 1)
	if r.SourceType != "touch_campaign" {
		t.Fatalf("report source_type = %q, want echo of the drill-down", r.SourceType)
	}
}

// 成员作用域(非 owner):只统计自己被指派的人群,复用既有记录级作用域推导。
func TestFunnelAssigneeScope(t *testing.T) {
	d := funnelTestDB(t)
	m1, m2 := funnelMembers(t, d)
	funnelSeedContact(t, d, "tnt_1", "c1", "13900000009", "form", m1, "2026-01-05T00:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "c2", "13900000010", "form", m2, "2026-01-06T00:00:00Z", false)
	funnelSeedOpp(t, d, "tnt_1", "o1", "c1", "open", m1, "2026-01-07T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "o2", "c2", "open", m2, "2026-01-07T00:00:00Z")

	q := funnelQuery("tnt_1")
	q.AssigneeMemberID = m1
	r, err := New(d).FunnelAnalysis(q)
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	wantCount(t, r, "profile_created", 1)
	wantCount(t, r, "opportunity_created", 1)
	if r.Scope != "assigned_to_me" {
		t.Fatalf("scope = %q, want assigned_to_me", r.Scope)
	}

	// owner(空指派)= 租户全量。
	q.AssigneeMemberID = ""
	all, err := New(d).FunnelAnalysis(q)
	if err != nil {
		t.Fatalf("funnel owner: %v", err)
	}
	wantCount(t, all, "profile_created", 2)
	if all.Scope != "tenant" {
		t.Fatalf("owner scope = %q, want tenant", all.Scope)
	}
}

// 跨租户零串行:两个租户的事实互不可见,查询结果只含本租户窗口事实。
func TestFunnelCrossTenantIsolation(t *testing.T) {
	d := funnelTestDB(t)
	knownFactSet(t, d) // tnt_1 全量事实
	funnelSeedContact(t, d, "tnt_2", "z1", "13900000011", "form", "", "2026-01-05T00:00:00Z", false)

	r1, err := New(d).FunnelAnalysis(funnelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("funnel t1: %v", err)
	}
	wantCount(t, r1, "profile_created", 3) // tnt_2 的 z1 不串入

	r2, err := New(d).FunnelAnalysis(funnelQuery("tnt_2"))
	if err != nil {
		t.Fatalf("funnel t2: %v", err)
	}
	wantCount(t, r2, "profile_created", 1) // 只有本租户的 z1
	wantCount(t, r2, "followed_up", 0)     // tnt_1 的跟进事实零串行
	wantCount(t, r2, "won", 0)
}

// granularity=none:不产出日序列,总数不变。
func TestFunnelNoSeriesGranularity(t *testing.T) {
	d := funnelTestDB(t)
	knownFactSet(t, d)
	q := funnelQuery("tnt_1")
	q.WithSeries = false
	r, err := New(d).FunnelAnalysis(q)
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	wantCount(t, r, "profile_created", 3)
	if st := stageOf(t, r, "profile_created"); st.Series != nil {
		t.Fatalf("series must be omitted with granularity=none, got %v", st.Series)
	}
}
