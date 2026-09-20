// HUI-1695 / FEAT-0196 渠道效果分析 store 层单测:
// 已知事实集直接 SQL 播种(三个渠道:form/touch_campaign/manual)-> 分渠道
// 独立 SQL 复算 vs ChannelFunnelAnalysis 输出逐项一致。覆盖:渠道维度落定
// (contacts.source_type,不发明 intake 接缝新维度)、渠道枚举披露(窗口内
// 实际出现过事实的渠道 + 样本数)、low_sample 阈值两侧(10 上/下)、ROI
// UNKNOWN(available=false + 中文 reason 引 HUI-1696,spend/roi/cac 恒 null)、
// 窗口 [start,end) 半开边界与 FEAT-0195 一致、同窗口重算幂等、跨租户零串行、
// 每渠道阶段定义披露与曝光 UNKNOWN 降级照常。
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// channelQuery builds the canonical January-2026 window query (no series —
// the channel group-by is totals-only, same as the HTTP contract).
func channelQuery(tenant string) FunnelQuery {
	start, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	end, _ := time.Parse(time.RFC3339, "2026-02-01T00:00:00Z")
	return FunnelQuery{TenantID: tenant, WindowStart: start, WindowEnd: end}
}

// channelKnownFactSet seeds the deterministic three-channel scenario for
// tnt_1, window [2026-01-01, 2026-02-01):
//
//	form 渠道:
//	  建档 = 13:cf01..cf12(01-03..01-14)+ cfb(恰在 start 01-01,含);
//	         cfe(2025-12-20 窗口前建档,只用于窗外提交对照)
//	  留资 = 11:cf01..cf10(01-06..01-15)+ cfb(恰在 start,含);
//	         排除:fs_e(cfe 恰在 end 02-01,不含)、fs_pre(cf12 窗口前 12-25)
//	  跟进 = 5:cf01..cf05(01-16);排除 fu_out(cf06 窗口前 12-31)
//	  商机 = 3:ocf1(01-18)+ ocf2(01-19)+ ocs(恰在 start,含);
//	         排除 oce(恰在 end 02-01,不含)
//	  成交 = 1:ocf1 于 01-25 won
//	touch_campaign 渠道:建档 3 / 留资 1 / 跟进 2 / 商机 1 / 成交 1(全小样本)
//	manual 渠道:窗口前建档 cm01 + 窗口内一条跟进 —— 靠跟进出现,
//	  sample_size=0(low_sample=true 的 0 基数边界)
func channelKnownFactSet(t *testing.T, d *sql.DB) {
	t.Helper()
	// form 渠道:12 个窗口内建档 + start 边界 cfb + 窗口前 cfe(手机号全部相异,
	// 建档身份计数 == 在窗行数,SQL 复算得以独立成立)。
	for i := 1; i <= 12; i++ {
		funnelSeedContact(t, d, "tnt_1", fmt.Sprintf("cf%02d", i),
			fmt.Sprintf("139000000%02d", i), "form", "",
			fmt.Sprintf("2026-01-%02dT10:00:00Z", i+2), false)
	}
	funnelSeedContact(t, d, "tnt_1", "cfb", "13900000013", "form", "", "2026-01-01T00:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cfe", "13900000015", "form", "", "2025-12-20T10:00:00Z", false)

	// form 留资:cf01..cf10 窗口内 + cfb 恰在 start(含)+ cfe 恰在 end(不含)
	// + cf12 窗口前(不含)。
	for i := 1; i <= 10; i++ {
		funnelSeedFormSubmission(t, d, "tnt_1", fmt.Sprintf("fs_cf%02d", i), fmt.Sprintf("cf%02d", i),
			fmt.Sprintf("2026-01-%02dT09:00:00Z", i+5))
	}
	funnelSeedFormSubmission(t, d, "tnt_1", "fs_b", "cfb", "2026-01-01T00:00:00Z")
	funnelSeedFormSubmission(t, d, "tnt_1", "fs_e", "cfe", "2026-02-01T00:00:00Z")
	funnelSeedFormSubmission(t, d, "tnt_1", "fs_pre", "cf12", "2025-12-25T09:00:00Z")

	// form 跟进:cf01..cf05 窗口内;cf06 窗口前排除。
	for i := 1; i <= 5; i++ {
		funnelSeedFollowUp(t, d, "tnt_1", fmt.Sprintf("fu_cf%02d", i), fmt.Sprintf("cf%02d", i), "2026-01-16T09:00:00Z")
	}
	funnelSeedFollowUp(t, d, "tnt_1", "fu_out", "cf06", "2025-12-31T09:00:00Z")

	// form 商机:两个窗口内 + start 边界含 + end 边界不含;form 成交 1。
	funnelSeedOpp(t, d, "tnt_1", "ocf1", "cf01", "open", "", "2026-01-18T09:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "ocf2", "cf02", "open", "", "2026-01-19T09:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "ocs", "cf03", "open", "", "2026-01-01T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "oce", "cf04", "open", "", "2026-02-01T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h_ocf1", "ocf1", "open", "won", "2026-01-25T09:00:00Z")

	// touch_campaign 渠道:全链路小样本(3/1/2/1/1)。
	funnelSeedContact(t, d, "tnt_1", "ct01", "13900000021", "touch_campaign", "", "2026-01-05T11:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "ct02", "13900000022", "touch_campaign", "", "2026-01-06T11:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "ct03", "13900000023", "touch_campaign", "", "2026-01-07T11:00:00Z", false)
	funnelSeedFormSubmission(t, d, "tnt_1", "fs_ct01", "ct01", "2026-01-08T11:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "fu_ct01", "ct01", "2026-01-10T11:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "fu_ct02", "ct02", "2026-01-10T11:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "oct1", "ct01", "open", "", "2026-01-12T11:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "h_oct1", "oct1", "open", "won", "2026-01-15T11:00:00Z")

	// manual 渠道:窗口前建档 + 窗口内一条跟进(靠跟进出现,sample_size=0)。
	funnelSeedContact(t, d, "tnt_1", "cm01", "13900000031", "manual", "", "2025-12-15T10:00:00Z", false)
	funnelSeedFollowUp(t, d, "tnt_1", "fu_cm01", "cm01", "2026-01-09T10:00:00Z")
}

// channelSQLCounts recomputes one channel's five stage counts with a
// formulation independent of the implementation (direct COUNT with the
// source_type filter), for row-by-row reconciliation. The profiles count is a
// plain row count because the fixture phones are distinct within each channel
// (rows == identities there).
func channelSQLCounts(t *testing.T, d *sql.DB, tenant, channel string) (forms, profiles, followups, opps, won int) {
	t.Helper()
	if err := d.QueryRow(
		`SELECT COUNT(DISTINCT s.contact_id) FROM form_submissions s
		 JOIN contacts c ON c.id=s.contact_id AND c.deleted_at IS NULL
		 WHERE s.tenant_id=? AND c.source_type=? AND s.created_at>='2026-01-01' AND s.created_at<'2026-02-01'`,
		tenant, channel).Scan(&forms); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(
		`SELECT COUNT(1) FROM contacts WHERE tenant_id=? AND source_type=? AND deleted_at IS NULL
		 AND created_at>='2026-01-01' AND created_at<'2026-02-01'`,
		tenant, channel).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(
		`SELECT COUNT(DISTINCT f.contact_id) FROM follow_ups f
		 JOIN contacts c ON c.id=f.contact_id AND c.deleted_at IS NULL
		 WHERE f.tenant_id=? AND c.source_type=? AND f.created_at>='2026-01-01T00:00:00+00:00' AND f.created_at<'2026-02-01T00:00:00+00:00'`,
		tenant, channel).Scan(&followups); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(
		`SELECT COUNT(1) FROM opportunities o
		 JOIN contacts c ON c.id=o.contact_id AND c.deleted_at IS NULL
		 WHERE o.tenant_id=? AND c.source_type=? AND o.created_at>='2026-01-01' AND o.created_at<'2026-02-01'`,
		tenant, channel).Scan(&opps); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(
		`SELECT COUNT(DISTINCT h.opportunity_id) FROM opportunity_stage_history h
		 JOIN opportunities o ON o.id=h.opportunity_id
		 JOIN contacts c ON c.id=o.contact_id AND c.deleted_at IS NULL
		 WHERE h.tenant_id=? AND c.source_type=? AND h.to_stage='won' AND h.changed_at>='2026-01-01' AND h.changed_at<'2026-02-01'`,
		tenant, channel).Scan(&won); err != nil {
		t.Fatal(err)
	}
	return forms, profiles, followups, opps, won
}

// ---- per-entry assertion helpers (channel-scoped variants of the funnel ones)

func channelEntryOf(t *testing.T, r *ChannelFunnelReport, channel string) ChannelFunnelEntry {
	t.Helper()
	for _, e := range r.Channels {
		if e.Channel == channel {
			return e
		}
	}
	t.Fatalf("channel %q missing from report %+v", channel, r.Channels)
	return ChannelFunnelEntry{}
}

func entryStageOf(t *testing.T, e ChannelFunnelEntry, key string) FunnelStage {
	t.Helper()
	for _, st := range e.Stages {
		if st.Key == key {
			return st
		}
	}
	t.Fatalf("stage %q missing from channel %s: %+v", key, e.Channel, e.Stages)
	return FunnelStage{}
}

func wantEntryCount(t *testing.T, e ChannelFunnelEntry, key string, want int) {
	t.Helper()
	st := entryStageOf(t, e, key)
	if st.Count == nil {
		t.Fatalf("channel %s stage %s count = null, want %d", e.Channel, key, want)
	}
	if *st.Count != want {
		t.Fatalf("channel %s stage %s count = %d, want %d", e.Channel, key, *st.Count, want)
	}
}

func wantEntryRate(t *testing.T, e ChannelFunnelEntry, key string, want float64) {
	t.Helper()
	st := entryStageOf(t, e, key)
	if st.RateFromPrevious == nil {
		t.Fatalf("channel %s stage %s rate = null, want %v", e.Channel, key, want)
	}
	diff := *st.RateFromPrevious - want
	if diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("channel %s stage %s rate = %v, want %v", e.Channel, key, *st.RateFromPrevious, want)
	}
}

func wantEntryNilRate(t *testing.T, e ChannelFunnelEntry, key string) {
	t.Helper()
	if st := entryStageOf(t, e, key); st.RateFromPrevious != nil {
		t.Fatalf("channel %s stage %s rate = %v, want null", e.Channel, key, *st.RateFromPrevious)
	}
}

// 全场景:三渠道已知事实 -> 分渠道独立 SQL 复算逐项一致;渠道枚举披露、
// 排序、low_sample、ROI 降级、每渠道阶段定义与曝光 UNKNOWN、窗口边界。
func TestChannelFunnelKnownFactSet(t *testing.T) {
	d := funnelTestDB(t)
	channelKnownFactSet(t, d)

	r, err := New(d).ChannelFunnelAnalysis(channelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("channel funnel: %v", err)
	}

	// 披露:窗口回显、作用域、规则文本、常量阈值、ROI 降级。
	if r.WindowStart != "2026-01-01T00:00:00Z" || r.WindowEnd != "2026-02-01T00:00:00Z" {
		t.Fatalf("window echo = [%s, %s), want [2026-01-01T00:00:00Z, 2026-02-01T00:00:00Z)", r.WindowStart, r.WindowEnd)
	}
	if r.Scope != FunnelScopeTenant {
		t.Fatalf("scope = %q, want tenant", r.Scope)
	}
	if !strings.Contains(r.ChannelRule, "source_type") || !strings.Contains(r.ChannelRule, "source_app") {
		t.Fatalf("channel_rule must disclose the dimension choice: %s", r.ChannelRule)
	}
	if r.RateRule != funnelRateRule {
		t.Fatalf("rate_rule must reuse the FEAT-0195 rule, got %q", r.RateRule)
	}
	if r.LowSampleRule == "" || r.LowSampleThreshold != 10 {
		t.Fatalf("low_sample disclosure missing: rule=%q threshold=%d", r.LowSampleRule, r.LowSampleThreshold)
	}
	// ROI 诚实降级:available=false + 中文 reason 引 HUI-1696,金额字段恒 null。
	if r.Roi.Available {
		t.Fatal("roi must be available=false (费用事实不在本仓)")
	}
	if !strings.Contains(r.Roi.Reason, "HUI-1696") {
		t.Fatalf("roi reason must cite HUI-1696: %s", r.Roi.Reason)
	}
	if r.Roi.Spend != nil || r.Roi.Roi != nil || r.Roi.Cac != nil {
		t.Fatalf("roi spend/roi/cac must be null, got %+v", r.Roi)
	}

	// 渠道枚举披露:只有窗口内出现过事实的渠道;按 sample_size 降序稳定排序。
	// manual 靠窗口内跟进出现(建档基数 0 也出现)。
	if len(r.Channels) != 3 {
		t.Fatalf("channels = %d, want 3 (form/touch_campaign/manual)", len(r.Channels))
	}
	wantOrder := []string{ChannelForm, ChannelTouchCampaign, ChannelManual}
	for i, w := range wantOrder {
		if r.Channels[i].Channel != w {
			t.Fatalf("channels[%d] = %s, want %s (sample_size 降序)", i, r.Channels[i].Channel, w)
		}
	}

	// 分渠道独立 SQL 复算 vs 输出逐项一致。
	for _, e := range r.Channels {
		wantForms, wantProfiles, wantFollow, wantOpps, wantWon := channelSQLCounts(t, d, "tnt_1", e.Channel)
		if e.SampleSize != wantProfiles {
			t.Fatalf("channel %s sample_size = %d, SQL recompute = %d", e.Channel, e.SampleSize, wantProfiles)
		}
		if len(e.Stages) != 6 {
			t.Fatalf("channel %s stages = %d, want 6 (曝光/留资/建档/跟进/商机/成交)", e.Channel, len(e.Stages))
		}
		wantKeys := []string{FunnelStageExposure, FunnelStageLeadForm, FunnelStageProfileCreated,
			FunnelStageFollowedUp, FunnelStageOpportunityCreated, FunnelStageWon}
		for i, k := range wantKeys {
			if e.Stages[i].Key != k {
				t.Fatalf("channel %s stages[%d] = %s, want %s (FEAT-0195 阶段顺序)", e.Channel, i, e.Stages[i].Key, k)
			}
		}
		wantEntryCount(t, e, FunnelStageLeadForm, wantForms)
		wantEntryCount(t, e, FunnelStageProfileCreated, wantProfiles)
		wantEntryCount(t, e, FunnelStageFollowedUp, wantFollow)
		wantEntryCount(t, e, FunnelStageOpportunityCreated, wantOpps)
		wantEntryCount(t, e, FunnelStageWon, wantWon)

		// 曝光 UNKNOWN 在每个渠道照常降级;五个计算阶段定义披露齐备;无序列。
		exp := entryStageOf(t, e, FunnelStageExposure)
		if exp.Available || exp.Count != nil || !strings.Contains(exp.Reason, "HUI-1677") {
			t.Fatalf("channel %s exposure must be UNKNOWN (available=false + null count + HUI-1677 reason), got %+v", e.Channel, exp)
		}
		for _, k := range []string{FunnelStageLeadForm, FunnelStageProfileCreated, FunnelStageFollowedUp,
			FunnelStageOpportunityCreated, FunnelStageWon} {
			st := entryStageOf(t, e, k)
			if !st.Available || st.Count == nil {
				t.Fatalf("channel %s stage %s must be computed", e.Channel, k)
			}
			def := st.Definition
			if def == nil || def.EventSource == "" || def.DedupKey == "" || def.Denominator == "" ||
				def.EventTimeField == "" || def.Window == "" {
				t.Fatalf("channel %s stage %s definition incomplete: %+v", e.Channel, k, def)
			}
			if st.Series != nil {
				t.Fatalf("channel %s stage %s must omit series (totals-only group-by)", e.Channel, k)
			}
		}
	}

	// 人工推演的精确期望(form 渠道,含窗口边界与渠道内转化率)。
	form := channelEntryOf(t, r, ChannelForm)
	if form.LowSample || form.SampleSize != 13 {
		t.Fatalf("form sample_size=%d low_sample=%v, want 13/false(阈上)", form.SampleSize, form.LowSample)
	}
	wantEntryCount(t, form, FunnelStageLeadForm, 11) // cfb 恰在 start 含;fs_e 恰在 end、fs_pre 窗前排除
	wantEntryRate(t, form, FunnelStageProfileCreated, 13.0/11.0)
	wantEntryRate(t, form, FunnelStageFollowedUp, 5.0/13.0)
	wantEntryRate(t, form, FunnelStageOpportunityCreated, 3.0/5.0)
	wantEntryRate(t, form, FunnelStageWon, 1.0/3.0)
	wantEntryNilRate(t, form, FunnelStageLeadForm) // 渠道内链首无上一可用阶段

	// touch_campaign:小样本全链路,转化率在渠道内推导。
	touch := channelEntryOf(t, r, ChannelTouchCampaign)
	if !touch.LowSample || touch.SampleSize != 3 {
		t.Fatalf("touch sample_size=%d low_sample=%v, want 3/true(阈下)", touch.SampleSize, touch.LowSample)
	}
	wantEntryRate(t, touch, FunnelStageProfileCreated, 3.0)
	wantEntryRate(t, touch, FunnelStageFollowedUp, 2.0/3.0)
	wantEntryRate(t, touch, FunnelStageOpportunityCreated, 1.0/2.0)
	wantEntryRate(t, touch, FunnelStageWon, 1.0)

	// manual:靠窗口内跟进出现;建档基数 0 -> low_sample=true;链上 0 分母传 null。
	manual := channelEntryOf(t, r, ChannelManual)
	if !manual.LowSample || manual.SampleSize != 0 {
		t.Fatalf("manual sample_size=%d low_sample=%v, want 0/true", manual.SampleSize, manual.LowSample)
	}
	wantEntryCount(t, manual, FunnelStageFollowedUp, 1)
	wantEntryNilRate(t, manual, FunnelStageProfileCreated)
	wantEntryNilRate(t, manual, FunnelStageFollowedUp)
	wantEntryRate(t, manual, FunnelStageOpportunityCreated, 0.0) // 0/1:上一可用阶段为 1
	wantEntryNilRate(t, manual, FunnelStageWon)
}

// low_sample 阈值两侧:恰好 10 个建档身份 -> false;9 个 -> true(常量阈值披露)。
func TestChannelFunnelLowSampleThreshold(t *testing.T) {
	d := funnelTestDB(t)
	for i := 0; i < 10; i++ {
		funnelSeedContact(t, d, "tnt_1", fmt.Sprintf("bf%02d", i),
			fmt.Sprintf("139000000%02d", 41+i), "form", "",
			fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1), false)
	}
	for i := 0; i < 9; i++ {
		funnelSeedContact(t, d, "tnt_1", fmt.Sprintf("bm%02d", i),
			fmt.Sprintf("139000000%02d", 51+i), "manual", "",
			fmt.Sprintf("2026-01-%02dT00:00:00Z", i+5), false)
	}

	r, err := New(d).ChannelFunnelAnalysis(channelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("channel funnel: %v", err)
	}
	if r.LowSampleThreshold != 10 {
		t.Fatalf("threshold = %d, want 10", r.LowSampleThreshold)
	}
	if len(r.Channels) != 2 {
		t.Fatalf("channels = %d, want 2 (form/manual)", len(r.Channels))
	}
	form := channelEntryOf(t, r, ChannelForm)
	if form.SampleSize != 10 || form.LowSample {
		t.Fatalf("form sample=%d low_sample=%v, want 10/false(恰在阈上)", form.SampleSize, form.LowSample)
	}
	manual := channelEntryOf(t, r, ChannelManual)
	if manual.SampleSize != 9 || !manual.LowSample {
		t.Fatalf("manual sample=%d low_sample=%v, want 9/true(阈下)", manual.SampleSize, manual.LowSample)
	}
}

// 跨租户零串行 + 渠道枚举按租户各自披露。
func TestChannelFunnelCrossTenantIsolation(t *testing.T) {
	d := funnelTestDB(t)
	channelKnownFactSet(t, d)
	// tnt_2 只有一个窗口内 touch 事实。
	funnelSeedContact(t, d, "tnt_2", "z1", "13900000081", "touch_campaign", "", "2026-01-05T00:00:00Z", false)
	funnelSeedFormSubmission(t, d, "tnt_2", "zs1", "z1", "2026-01-06T00:00:00Z")

	r2, err := New(d).ChannelFunnelAnalysis(channelQuery("tnt_2"))
	if err != nil {
		t.Fatalf("channel funnel t2: %v", err)
	}
	if len(r2.Channels) != 1 || r2.Channels[0].Channel != ChannelTouchCampaign {
		t.Fatalf("tnt_2 channels = %+v, want only touch_campaign(tnt_1 事实零串入)", r2.Channels)
	}
	wantEntryCount(t, r2.Channels[0], FunnelStageLeadForm, 1)
	wantEntryCount(t, r2.Channels[0], FunnelStageProfileCreated, 1)
	wantEntryCount(t, r2.Channels[0], FunnelStageFollowedUp, 0)
	wantEntryCount(t, r2.Channels[0], FunnelStageWon, 0)

	// 反向:tnt_1 的输出不因 tnt_2 的事实改变(manual 仍只有自己一条跟进)。
	r1, err := New(d).ChannelFunnelAnalysis(channelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("channel funnel t1: %v", err)
	}
	wantEntryCount(t, channelEntryOf(t, r1, ChannelManual), FunnelStageFollowedUp, 1)
	wantEntryCount(t, channelEntryOf(t, r1, ChannelForm), FunnelStageLeadForm, 11)
}

// 同窗口重算幂等:纯计算无状态,两次输出逐字段一致。
func TestChannelFunnelRecomputeIdempotent(t *testing.T) {
	d := funnelTestDB(t)
	channelKnownFactSet(t, d)
	s := New(d)
	first, err := s.ChannelFunnelAnalysis(channelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("channel funnel 1: %v", err)
	}
	second, err := s.ChannelFunnelAnalysis(channelQuery("tnt_1"))
	if err != nil {
		t.Fatalf("channel funnel 2: %v", err)
	}
	if got, want := marshalForTest(t, second), marshalForTest(t, first); got != want {
		t.Fatalf("recompute differs:\nfirst  %s\nsecond %s", want, got)
	}
}

// 校验:租户必填;[start,end) 空窗/倒窗拒绝;source_type 下钻与渠道 group-by
// 互斥(自相矛盾的查询显式报错,不静默)。
func TestChannelFunnelValidation(t *testing.T) {
	d := funnelTestDB(t)
	channelKnownFactSet(t, d)
	s := New(d)

	q := channelQuery("tnt_1")
	q.TenantID = ""
	if _, err := s.ChannelFunnelAnalysis(q); err == nil {
		t.Fatal("empty tenant must be rejected")
	}
	q = channelQuery("tnt_1")
	q.WindowEnd = q.WindowStart
	if _, err := s.ChannelFunnelAnalysis(q); err == nil {
		t.Fatal("empty window must be rejected")
	}
	q = channelQuery("tnt_1")
	q.WindowStart, q.WindowEnd = q.WindowEnd, q.WindowStart
	if _, err := s.ChannelFunnelAnalysis(q); err == nil {
		t.Fatal("inverted window must be rejected")
	}
	q = channelQuery("tnt_1")
	q.SourceType = ChannelForm
	if _, err := s.ChannelFunnelAnalysis(q); err == nil {
		t.Fatal("source_type drill-down contradicts the channel group-by and must be rejected")
	}
}
