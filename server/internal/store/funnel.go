// HUI-1694 / FEAT-0195 全漏斗分析存储层(纯只读计算,零迁移、零状态):
//
//	v1 漏斗 = leads 域可信事实链,四阶段:
//	  建档   profile_created     窗口内首次建档的唯一联系人身份
//	                           (去重键 = NormalizePhone 归一化手机号,与 intake
//	                           查重同域;无手机号档案各自独立身份;同号多档案
//	                           —— ambiguous 孪生 —— 合并计 1;窗口前已建档的
//	                           身份不重复计入;事件来源含 touch/form intake
//	                           与人工建档路径)
//	  跟进   followed_up         窗口内存在 follow_ups(FEAT-0193)记录的唯一联系人
//	  商机   opportunity_created 窗口内创建的商机数
//	  成交   won                 窗口内发生过 to_stage='won' 审计事件的唯一商机
//	                           (won=人工标记成交,绝不表示已支付/已收款;
//	                           won 后重开不回溯改写窗口结果;leads 的
//	                           converted 状态无事件时间字段——leads 无阶段
//	                           审计链——v1 不以 updated_at 推断,不计入)
//	另有两个触点阶段(曝光/留资)只有 touch 域原生事实(HUI-1677/HUI-1680),
//	本仓没有可信本地事实:按 UNKNOWN 诚实降级输出 available=false + 中文
//	reason,count 恒为 null —— 绝不推算、绝不置 0。
//
// 窗口语义:[window_start, window_end) 半开区间,按各阶段事件时间字段
// (UTC RFC3339)过滤。纯函数:同窗口重算结果恒一致(无任何游标/状态)。
// 晚到事件按其事件时间计入对应窗口,重算可见 —— 窗口结果 = f(当前事实, 窗口)。
//
// 时间比较纪律:created_at/changed_at 以 RFC3339(可含小数秒)入库,字符串
// 比较在秒边界不可靠(小数点与 Z 的字典序陷阱),故窗口过滤在 Go 侧按时间
// 值比较;SQL 只做租户/指派/来源过滤(全部走既有索引列)。租户规模为 SMB
// 单写 sqlite,与 intake 查重的租户内全扫先例同量级。
//
// 作用域:复用既有 authz 推导 —— owner(空指派)租户全量;sales/agent 只见
// 自己被指派人群(联系人 assigned_member_id;跟进取挂靠 lead 优先/contact
// 的 COALESCE 单点作用域,与到期面同款;商机 assigned_member_id)。
// 下钻:source_type(contacts 既有来源域 manual/form/touch_campaign)作用于
// 人群,四阶段随人群一致收窄;source_app/source_ns 只存在于 intake 事件接缝,
// 不贯穿 followups/opportunities,故不发明跨阶段新维度。
package store

import (
	"errors"
	"sort"
	"time"
)

// Funnel stage keys (stable machine identifiers in the API response).
const (
	FunnelStageExposure           = "exposure"
	FunnelStageLeadForm           = "lead_form"
	FunnelStageProfileCreated     = "profile_created"
	FunnelStageFollowedUp         = "followed_up"
	FunnelStageOpportunityCreated = "opportunity_created"
	FunnelStageWon                = "won"
)

// Funnel scopes (echoed in the response so callers see the applied slice).
const (
	FunnelScopeTenant   = "tenant"
	FunnelScopeAssigned = "assigned_to_me"
)

// FunnelQuery is a fully resolved read-only funnel query. WindowStart is
// inclusive, WindowEnd exclusive; both must be UTC-normalized by the caller.
type FunnelQuery struct {
	TenantID string
	// SourceType drill-down: "" = all; else one of manual/form/touch_campaign
	// (the existing contacts.source_type domain — no invented dimensions).
	SourceType string
	// AssigneeMemberID: "" = tenant-wide (owner); else the member's own
	// assignment population (record-level scope reuse).
	AssigneeMemberID string
	// WithSeries selects granularity=day (per-day series) vs none (totals).
	WithSeries bool
	// WindowStart/WindowEnd are the half-open analysis window.
	WindowStart time.Time
	WindowEnd   time.Time
}

// FunnelDayPoint is one granularity=day bucket (units whose first in-window
// event falls on that UTC date; series sums to the stage count).
type FunnelDayPoint struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// FunnelDefinition is the per-stage definition disclosure (定义披露): every
// stage states where its events come from, what one unit is, and how the
// window applies — the numbers are only as honest as this disclosure.
type FunnelDefinition struct {
	EventSource    string `json:"event_source"`
	DedupKey       string `json:"dedup_key"`
	Denominator    string `json:"denominator"`
	EventTimeField string `json:"event_time_field"`
	Window         string `json:"window"`
}

// FunnelStage is one stage of the funnel report. Unavailable stages (touch
// touchpoints without trusted local facts) carry Count=nil and a Chinese
// Reason — never zero, never an estimate (UNKNOWN 诚实降级).
type FunnelStage struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	// Reason is set only when Available=false (机器可断言的中文披露).
	Reason string `json:"reason,omitempty"`
	// Count is nil for unavailable stages; JSON null, never 0.
	Count *int `json:"count"`
	// Unit names what one unit is ("unique_contact" / "opportunity").
	Unit string `json:"unit,omitempty"`
	// RateFromPrevious = count ÷ previous AVAILABLE stage count; null when
	// there is no previous available stage or its count is 0.
	RateFromPrevious *float64          `json:"rate_from_previous"`
	Series           []FunnelDayPoint  `json:"series,omitempty"`
	Definition       *FunnelDefinition `json:"definition,omitempty"`
}

// FunnelReport is the full response payload of one funnel computation.
type FunnelReport struct {
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	Granularity string `json:"granularity"`
	Scope       string `json:"scope"`
	// SourceType echoes the drill-down when applied.
	SourceType string        `json:"source_type,omitempty"`
	RateRule   string        `json:"rate_rule"`
	Stages     []FunnelStage `json:"stages"`
}

// funnelRateRule is the single derivation rule, disclosed in every response.
const funnelRateRule = "rate_from_previous = 本阶段 count ÷ 上一可用阶段 count;" +
	"无上一可用阶段或其 count=0 时为 null;available=false 的触点阶段不参与推导"

// UNKNOWN 诚实降级:touch 触点原生事实不在本仓(HUI-1677 曝光 / HUI-1680
// 收件箱留资),输出不可用与原因,绝不推算、绝不置 0。
const (
	funnelExposureReason = "曝光(触点展示)的原生事实在 touch 域(HUI-1677)," +
		"本仓没有任何可信本地事实;按 UNKNOWN 诚实降级输出不可用,绝不推算、绝不置 0。"
	funnelLeadFormReason = "渠道留资表单的原生事实在 touch 域(HUI-1680 收件箱侧)," +
		"本仓没有该触点的可信全量事实;按 UNKNOWN 诚实降级输出不可用,绝不推算、绝不置 0。" +
		"(注:本仓 HUI-1679/FEAT-0180 表单域只有自管表单的提交流水,v1 不以局部事实冒充渠道触点全量。)"
)

// FunnelAnalysis computes the read-only funnel for one query. It is a pure
// function of (current facts, window): no cursors, no state, no writes.
func (s *Store) FunnelAnalysis(q FunnelQuery) (*FunnelReport, error) {
	if q.TenantID == "" {
		return nil, errors.New("funnel: tenant required")
	}
	if !q.WindowEnd.After(q.WindowStart) {
		return nil, errors.New("funnel: window_end must be after window_start")
	}
	switch q.SourceType {
	case "", "manual", "form", "touch_campaign":
	default:
		return nil, errors.New("funnel: source_type must be manual, form or touch_campaign")
	}

	r := &FunnelReport{
		WindowStart: q.WindowStart.UTC().Format(time.RFC3339),
		WindowEnd:   q.WindowEnd.UTC().Format(time.RFC3339),
		Granularity: "none",
		Scope:       FunnelScopeTenant,
		SourceType:  q.SourceType,
		RateRule:    funnelRateRule,
	}
	if q.WithSeries {
		r.Granularity = "day"
	}
	if q.AssigneeMemberID != "" {
		r.Scope = FunnelScopeAssigned
	}

	defs := funnelDefinitions(r.WindowStart, r.WindowEnd)

	// ---- UNKNOWN touchpoint stages (no trusted local facts) ---------------
	r.Stages = append(r.Stages,
		FunnelStage{Key: FunnelStageExposure, Label: "曝光", Reason: funnelExposureReason},
		FunnelStage{Key: FunnelStageLeadForm, Label: "留资", Reason: funnelLeadFormReason},
	)

	// ---- the leads-domain trusted fact chain ------------------------------
	profiles, err := s.funnelProfiles(q)
	if err != nil {
		return nil, err
	}
	r.Stages = append(r.Stages, funnelCountStage(FunnelStageProfileCreated, "建档",
		"unique_contact", profiles, defs[FunnelStageProfileCreated], q.WithSeries))

	followups, err := s.funnelFollowUps(q)
	if err != nil {
		return nil, err
	}
	r.Stages = append(r.Stages, funnelCountStage(FunnelStageFollowedUp, "跟进",
		"unique_contact", followups, defs[FunnelStageFollowedUp], q.WithSeries))

	opps, err := s.funnelOpportunities(q)
	if err != nil {
		return nil, err
	}
	r.Stages = append(r.Stages, funnelCountStage(FunnelStageOpportunityCreated, "商机",
		"opportunity", opps, defs[FunnelStageOpportunityCreated], q.WithSeries))

	won, err := s.funnelWon(q)
	if err != nil {
		return nil, err
	}
	r.Stages = append(r.Stages, funnelCountStage(FunnelStageWon, "成交",
		"opportunity", won, defs[FunnelStageWon], q.WithSeries))

	// ---- adjacent conversion rates between AVAILABLE stages only ----------
	var prev *int
	for i := range r.Stages {
		st := &r.Stages[i]
		if !st.Available || st.Count == nil {
			continue
		}
		if prev != nil && *prev > 0 {
			rate := float64(*st.Count) / float64(*prev)
			st.RateFromPrevious = &rate
		}
		prev = st.Count
	}
	return r, nil
}

// funnelCountStage materializes one available stage from first-event days.
// firstEvent maps each unit to its earliest event time inside the window.
func funnelCountStage(key, label, unit string, firstEvent map[string]time.Time,
	def FunnelDefinition, withSeries bool) FunnelStage {
	count := len(firstEvent)
	st := FunnelStage{Key: key, Label: label, Available: true, Count: &count, Unit: unit, Definition: &def}
	if withSeries && count > 0 {
		days := map[string]int{}
		for _, t := range firstEvent {
			days[t.UTC().Format("2006-01-02")]++
		}
		dates := make([]string, 0, len(days))
		for d := range days {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		for _, d := range dates {
			st.Series = append(st.Series, FunnelDayPoint{Date: d, Count: days[d]})
		}
	}
	return st
}

// funnelDefinitions returns the per-stage definition disclosure texts with the
// concrete window bounds baked in (定义披露原文随响应输出)。
func funnelDefinitions(start, end string) map[string]FunnelDefinition {
	window := "[" + start + ", " + end + ") 半开区间,按事件时间(UTC)过滤"
	return map[string]FunnelDefinition{
		FunnelStageProfileCreated: {
			EventSource: "contacts(本租户在册档案,deleted_at IS NULL;来源含 intake 建档" +
				"——touch 触达/public_form 表单——与人工建档路径)",
			DedupKey: "NormalizePhone(手机号) 归一化身份(与 intake 查重同域);无手机号档案各自独立身份",
			Denominator: "窗口内首次建档的唯一联系人身份:同号多档案(ambiguous 孪生)合并计 1;" +
				"该身份在窗口开始前已建档的不重复计入",
			EventTimeField: "contacts.created_at",
			Window:         window,
		},
		FunnelStageFollowedUp: {
			EventSource: "follow_ups(HUI-1692/FEAT-0193 销售跟进记录;JOIN 在册 contacts," +
				"墓碑档案排除;HUI-1691 只追加审计时间线 contact_followups 不计入)",
			DedupKey:       "follow_ups.contact_id(唯一联系人)",
			Denominator:    "窗口内存在至少一条跟进记录的唯一联系人(同联系人多条记录只计 1)",
			EventTimeField: "follow_ups.created_at",
			Window:         window,
		},
		FunnelStageOpportunityCreated: {
			EventSource:    "opportunities(JOIN 在册 contacts,墓碑档案排除)",
			DedupKey:       "opportunities.id(唯一商机)",
			Denominator:    "窗口内创建的商机数(每商机只创建一次)",
			EventTimeField: "opportunities.created_at",
			Window:         window,
		},
		FunnelStageWon: {
			EventSource: "opportunity_stage_history(to_stage='won' 审计链行;JOIN opportunities " +
				"与在册 contacts)。won=人工标记成交,绝不表示已支付/已收款(FEAT-0194 语义红线);" +
				"leads.status='converted' 无事件时间字段(leads 无阶段审计链),v1 不以 updated_at " +
				"推断成交时间,故不计入本阶段",
			DedupKey: "opportunity_id(唯一商机;窗口内多次 won 只计 1)",
			Denominator: "窗口内发生过 won 审计事件的唯一商机数;won 后重开不回溯改写窗口结果" +
				"(审计链只追加,如实反映窗口内发生过成交)",
			EventTimeField: "opportunity_stage_history.changed_at",
			Window:         window,
		},
	}
}

// ---- stage computations ------------------------------------------------------

// funnelProfiles counts unique contact identities whose FIRST live contact row
// was created inside the window. Identities are normalized-phone equivalence
// classes (phoneless rows are each their own identity).
func (s *Store) funnelProfiles(q FunnelQuery) (map[string]time.Time, error) {
	query := `SELECT id, phone, created_at FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`
	args := []any{q.TenantID}
	if q.AssigneeMemberID != "" {
		query += ` AND assigned_member_id=?`
		args = append(args, q.AssigneeMemberID)
	}
	if q.SourceType != "" {
		query += ` AND source_type=?`
		args = append(args, q.SourceType)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 全历史首次建档时间(窗口判定必须对照全历史,而非仅窗口内)。
	firstEver := map[string]time.Time{}
	for rows.Next() {
		var id, phone, createdAt string
		if err := rows.Scan(&id, &phone, &createdAt); err != nil {
			return nil, err
		}
		t, ok := parseFunnelTime(createdAt)
		if !ok {
			continue // 无法定位时间的行不进任何窗口(防御,不推算)
		}
		key := funnelIdentityKey(phone, id)
		if cur, seen := firstEver[key]; !seen || t.Before(cur) {
			firstEver[key] = t
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return inWindowFirsts(firstEver, q.WindowStart, q.WindowEnd), nil
}

// funnelIdentityKey collapses normalized-equal phones into one identity;
// phoneless contacts are each their own identity (no signal is invented).
func funnelIdentityKey(phone, id string) string {
	if norm := NormalizePhone(phone); norm != "" {
		return "p:" + norm
	}
	return "id:" + id
}

// funnelFollowUps counts unique live contacts with at least one follow_ups
// record inside the window (record-level scope = COALESCE lead assignee,
// contact assignee — the same single-point derivation as the due surface).
func (s *Store) funnelFollowUps(q FunnelQuery) (map[string]time.Time, error) {
	query := `SELECT f.contact_id, f.created_at
		FROM follow_ups f
		JOIN contacts c ON c.id = f.contact_id AND c.deleted_at IS NULL
		LEFT JOIN leads l ON l.id = f.lead_id
		WHERE f.tenant_id=?`
	args := []any{q.TenantID}
	if q.AssigneeMemberID != "" {
		query += ` AND COALESCE(l.assigned_member_id, c.assigned_member_id, '')=?`
		args = append(args, q.AssigneeMemberID)
	}
	if q.SourceType != "" {
		query += ` AND c.source_type=?`
		args = append(args, q.SourceType)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFirstInWindow(rows, q.WindowStart, q.WindowEnd)
}

// funnelOpportunities counts opportunities created inside the window.
func (s *Store) funnelOpportunities(q FunnelQuery) (map[string]time.Time, error) {
	query := `SELECT o.id, o.created_at
		FROM opportunities o
		JOIN contacts c ON c.id = o.contact_id AND c.deleted_at IS NULL
		WHERE o.tenant_id=?`
	args := []any{q.TenantID}
	if q.AssigneeMemberID != "" {
		query += ` AND o.assigned_member_id=?`
		args = append(args, q.AssigneeMemberID)
	}
	if q.SourceType != "" {
		query += ` AND c.source_type=?`
		args = append(args, q.SourceType)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFirstInWindow(rows, q.WindowStart, q.WindowEnd)
}

// funnelWon counts unique opportunities with at least one to_stage='won'
// audit event inside the window. Multiple wins in the window collapse to one;
// a reopen never retracts a window result (the chain is append-only).
func (s *Store) funnelWon(q FunnelQuery) (map[string]time.Time, error) {
	query := `SELECT h.opportunity_id, h.changed_at
		FROM opportunity_stage_history h
		JOIN opportunities o ON o.id = h.opportunity_id
		JOIN contacts c ON c.id = o.contact_id AND c.deleted_at IS NULL
		WHERE h.tenant_id=? AND h.to_stage='won'`
	args := []any{q.TenantID}
	if q.AssigneeMemberID != "" {
		query += ` AND o.assigned_member_id=?`
		args = append(args, q.AssigneeMemberID)
	}
	if q.SourceType != "" {
		query += ` AND c.source_type=?`
		args = append(args, q.SourceType)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFirstInWindow(rows, q.WindowStart, q.WindowEnd)
}

// ---- shared helpers ------------------------------------------------------------

// collectFirstInWindow reduces (unit, time) rows to units with at least one
// in-window event, mapped to their EARLIEST in-window time (series semantics:
// 首次事件日期分布,series 求和 == count).
func collectFirstInWindow(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
},
	start, end time.Time) (map[string]time.Time, error) {
	first := map[string]time.Time{}
	for rows.Next() {
		var unit, at string
		if err := rows.Scan(&unit, &at); err != nil {
			return nil, err
		}
		t, ok := parseFunnelTime(at)
		if !ok || !funnelInWindow(t, start, end) {
			continue
		}
		if cur, seen := first[unit]; !seen || t.Before(cur) {
			first[unit] = t
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return first, nil
}

// inWindowFirsts filters a first-ever map down to identities whose first event
// falls inside the window.
func inWindowFirsts(firstEver map[string]time.Time, start, end time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for key, t := range firstEver {
		if funnelInWindow(t, start, end) {
			out[key] = t
		}
	}
	return out
}

// funnelInWindow is the half-open [start, end) membership test.
func funnelInWindow(t, start, end time.Time) bool {
	return !t.Before(start) && t.Before(end)
}

// parseFunnelTime parses stored RFC3339 timestamps (fractional seconds
// included) into comparable time values.
func parseFunnelTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
