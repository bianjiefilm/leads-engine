// HUI-1695 / FEAT-0196 渠道效果分析存储层(纯只读 group-by,零迁移、零状态):
//
//	渠道效果 = 按渠道分组的线索质量漏斗对比。阶段定义/披露范式/窗口语义/作用域
//	推导全部复用 FEAT-0195(HUI-1694,server/internal/store/funnel.go),本文件
//	只做「渠道 group-by」,不重造阶段、不发明新事实与新语义。
//
//	渠道维度落定 = contacts.source_type(manual/form/touch_campaign,仓内既有
//	来源事实域):它记录在联系人行上,五阶段全部经 contacts JOIN 贯穿,天然支撑
//	分渠道漏斗。intake 事件接缝的 source_app/source_ns 只存在于
//	lead_intake_events,不贯穿 follow_ups/opportunities —— 与 FEAT-0195 同款
//	纪律,不作渠道维度,也不发明「按首投事件归因渠道」之类的新语义。
//
//	样本量纪律:每渠道输出 sample_size(该渠道窗口内建档身份计数,即该渠道漏斗
//	的人群基数)与 low_sample = sample_size < ChannelLowSampleThreshold(常量
//	10,随响应披露)。low_sample=true 是机器标注:小样本渠道的转化率(如
//	1/1=100%)不具统计参考性,防止小样本误导渠道对比。
//
//	ROI 诚实降级:广告投放花费等费用事实不在本仓(费用归因属 HUI-1696),
//	spend/roi/cac 恒为 JSON null + available=false + 中文 reason —— 绝不推算、
//	绝不置 0、无演示数据。
//
//	渠道枚举披露:只列窗口内实际出现过事实的渠道(任一计算阶段计数>0),伴随
//	样本数;确定性排序(sample_size 降序,同量按渠道名字典序)。同窗口重算恒
//	一致(纯函数:结果 = f(当前事实, 窗口),无游标、无状态)。
package store

import (
	"errors"
	"sort"
	"time"
)

// 渠道枚举 = contacts.source_type 既有域(仓内唯一贯穿全漏斗的渠道事实)。
const (
	ChannelManual        = "manual"
	ChannelForm          = "form"
	ChannelTouchCampaign = "touch_campaign"
)

// ChannelLowSampleThreshold is the constant low-sample cutoff (机器可断言,
// 随响应披露,绝不静默):渠道建档基数低于该值时 low_sample=true。
const ChannelLowSampleThreshold = 10

// channelFunnelLabels 是渠道枚举值的展示名(既有来源域的中性标注,非新语义)。
var channelFunnelLabels = map[string]string{
	ChannelManual:        "人工录入",
	ChannelForm:          "表单留资",
	ChannelTouchCampaign: "触点活动",
}

// channelDimensionRule 披露渠道维度为何落定在 contacts.source_type(维度选择
// 的诚实披露:为什么不按 intake 事件接缝的 source_app/source_ns 分组)。
const channelDimensionRule = "渠道 = contacts.source_type(manual/form/touch_campaign," +
	"仓内既有来源事实域,记录在联系人上、贯穿五阶段);lead_intake_events 的 " +
	"source_app/source_ns 只存在于 intake 事件接缝、不贯穿 follow_ups/opportunities," +
	"不作渠道维度、不发明归因新语义(FEAT-0195 同款纪律)"

// channelLowSampleRule 披露 low_sample 的确定判定规则(常量阈值,机器可断言)。
const channelLowSampleRule = "low_sample = sample_size < low_sample_threshold;" +
	"sample_size = 该渠道窗口内建档身份计数(profile_created,该渠道漏斗的人群基数);" +
	"low_sample=true 表示小样本渠道,其转化率不具统计参考性,防止小样本误导"

// channelRoiUnavailableReason 是 ROI 诚实降级的中文披露(机器可断言引 HUI-1696)。
const channelRoiUnavailableReason = "广告投放花费等费用事实不在本仓,费用归因属 " +
	"HUI-1696(费用侧接入票);本端点没有任何可信费用本地事实,spend/roi/cac 恒为 " +
	"null,绝不推算、绝不置 0、无演示数据。"

// ChannelRoiDisclosure is the ROI honest-degradation disclosure: the spend
// side has no trusted local facts in this repo, so every money field is JSON
// null forever (never zero, never estimated, no demo data).
type ChannelRoiDisclosure struct {
	Available bool     `json:"available"`
	Reason    string   `json:"reason"`
	Spend     *float64 `json:"spend"`
	Roi       *float64 `json:"roi"`
	Cac       *float64 `json:"cac"`
}

// ChannelFunnelEntry is one channel's funnel: the exact FEAT-0195 stage list
// (identical order/definitions/units) computed over that channel's population,
// plus the sample-size discipline fields.
type ChannelFunnelEntry struct {
	Channel string `json:"channel"`
	Label   string `json:"label"`
	// SampleSize is the channel's funnel population base (in-window
	// profile_created identities); LowSample is the machine flag against
	// small-sample rate misleading.
	SampleSize int           `json:"sample_size"`
	LowSample  bool          `json:"low_sample"`
	Stages     []FunnelStage `json:"stages"`
}

// ChannelFunnelReport is the full response payload of the channel group-by.
type ChannelFunnelReport struct {
	WindowStart        string               `json:"window_start"`
	WindowEnd          string               `json:"window_end"`
	Scope              string               `json:"scope"`
	ChannelRule        string               `json:"channel_rule"`
	RateRule           string               `json:"rate_rule"`
	LowSampleRule      string               `json:"low_sample_rule"`
	LowSampleThreshold int                  `json:"low_sample_threshold"`
	Roi                ChannelRoiDisclosure `json:"roi"`
	Channels           []ChannelFunnelEntry `json:"channels"`
}

// ChannelFunnelAnalysis computes the read-only per-channel funnel for one
// query. It is a pure function of (current facts, window): no cursors, no
// state, no writes. Each channel's stages are exactly FunnelAnalysis's stage
// computations narrowed by source_type (the existing FEAT-0195 drill-down
// semantics), so per-channel numbers always reconcile with the single funnel.
func (s *Store) ChannelFunnelAnalysis(q FunnelQuery) (*ChannelFunnelReport, error) {
	if q.TenantID == "" {
		return nil, errors.New("channel funnel: tenant required")
	}
	if !q.WindowEnd.After(q.WindowStart) {
		return nil, errors.New("channel funnel: window_end must be after window_start")
	}
	// 渠道 group-by 与 source_type 下钻互斥:同时传入是自相矛盾的查询,显式拒绝。
	if q.SourceType != "" {
		return nil, errors.New("channel funnel: source_type drill-down contradicts the channel group-by (leave source_type empty)")
	}

	r := &ChannelFunnelReport{
		WindowStart:        q.WindowStart.UTC().Format(time.RFC3339),
		WindowEnd:          q.WindowEnd.UTC().Format(time.RFC3339),
		Scope:              FunnelScopeTenant,
		ChannelRule:        channelDimensionRule,
		RateRule:           funnelRateRule,
		LowSampleRule:      channelLowSampleRule,
		LowSampleThreshold: ChannelLowSampleThreshold,
		Roi:                ChannelRoiDisclosure{Available: false, Reason: channelRoiUnavailableReason},
		Channels:           []ChannelFunnelEntry{},
	}
	if q.AssigneeMemberID != "" {
		r.Scope = FunnelScopeAssigned
	}

	defs := funnelDefinitions(r.WindowStart, r.WindowEnd)
	for _, channel := range []string{ChannelManual, ChannelForm, ChannelTouchCampaign} {
		entry, err := s.channelFunnelEntry(q, channel, defs)
		if err != nil {
			return nil, err
		}
		// 渠道枚举披露:窗口内零事实的渠道不出现(避免零行噪音)。
		if !channelAppeared(entry) {
			continue
		}
		r.Channels = append(r.Channels, entry)
	}
	// 确定性排序:样本量降序,同量按渠道名字典序 —— 对比阅读顺序稳定。
	sort.SliceStable(r.Channels, func(i, j int) bool {
		if r.Channels[i].SampleSize != r.Channels[j].SampleSize {
			return r.Channels[i].SampleSize > r.Channels[j].SampleSize
		}
		return r.Channels[i].Channel < r.Channels[j].Channel
	})
	return r, nil
}

// channelFunnelEntry computes one channel's funnel: the exact FEAT-0195 stage
// computations narrowed by source_type, exposure UNKNOWN degradation included,
// with in-channel conversion rates chained by the same single rule.
func (s *Store) channelFunnelEntry(q FunnelQuery, channel string, defs map[string]FunnelDefinition) (ChannelFunnelEntry, error) {
	qc := q
	qc.SourceType = channel
	entry := ChannelFunnelEntry{Channel: channel, Label: channelFunnelLabels[channel], Stages: []FunnelStage{}}

	// 触点阶段(曝光)在各渠道同样无可信本地事实:UNKNOWN 诚实降级照常
	// (available=false + 中文 reason,count=null,绝不置 0)。
	entry.Stages = append(entry.Stages,
		FunnelStage{Key: FunnelStageExposure, Label: "曝光", Reason: funnelExposureReason})

	forms, err := s.funnelFormSubmissions(qc)
	if err != nil {
		return entry, err
	}
	entry.Stages = append(entry.Stages, funnelCountStage(FunnelStageLeadForm, "留资",
		"unique_contact", forms, defs[FunnelStageLeadForm], q.WithSeries))

	profiles, err := s.funnelProfiles(qc)
	if err != nil {
		return entry, err
	}
	entry.Stages = append(entry.Stages, funnelCountStage(FunnelStageProfileCreated, "建档",
		"unique_contact", profiles, defs[FunnelStageProfileCreated], q.WithSeries))

	follows, err := s.funnelFollowUps(qc)
	if err != nil {
		return entry, err
	}
	entry.Stages = append(entry.Stages, funnelCountStage(FunnelStageFollowedUp, "跟进",
		"unique_contact", follows, defs[FunnelStageFollowedUp], q.WithSeries))

	opps, err := s.funnelOpportunities(qc)
	if err != nil {
		return entry, err
	}
	entry.Stages = append(entry.Stages, funnelCountStage(FunnelStageOpportunityCreated, "商机",
		"opportunity", opps, defs[FunnelStageOpportunityCreated], q.WithSeries))

	won, err := s.funnelWon(qc)
	if err != nil {
		return entry, err
	}
	entry.Stages = append(entry.Stages, funnelCountStage(FunnelStageWon, "成交",
		"opportunity", won, defs[FunnelStageWon], q.WithSeries))

	// 样本量 = 该渠道窗口内建档身份计数(漏斗人群基数);常量阈值机器判定。
	entry.SampleSize = len(profiles)
	entry.LowSample = entry.SampleSize < ChannelLowSampleThreshold

	// 渠道内相邻可用阶段转化率:与 FunnelAnalysis 同一条推导规则(funnelRateRule)。
	var prev *int
	for i := range entry.Stages {
		st := &entry.Stages[i]
		if !st.Available || st.Count == nil {
			continue
		}
		if prev != nil && *prev > 0 {
			rate := float64(*st.Count) / float64(*prev)
			st.RateFromPrevious = &rate
		}
		prev = st.Count
	}
	return entry, nil
}

// channelAppeared reports whether the channel has at least one in-window fact
// (any computed stage count > 0) — the enumeration-disclosure predicate.
func channelAppeared(e ChannelFunnelEntry) bool {
	for _, st := range e.Stages {
		if st.Available && st.Count != nil && *st.Count > 0 {
			return true
		}
	}
	return false
}
