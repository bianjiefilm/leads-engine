// HUI-1690 / FEAT-0191 客户画像标签体系·派生标签层(纯只读按需重算,零迁移、
// 零落库、零状态):每个标签都从仓内既有可信事实确定性推导,同事实同 as_of
// 重算结果恒一致(纯函数:f(当前事实, as_of))。四族:
//
//	生命周期 lifecycle:opportunities 状态 + opportunity_stage_history 审计链。
//	  在途(stage NOT IN won/closed_lost)> 已成交(当前 stage='won' 或审计链存在
//	  to_stage='won',审计链只追加、重开不回溯)> 已流失(stage='closed_lost');
//	  生命周期是联系人当前状态:三态取唯一优先态,无商机事实不产出该族标签。
//	活跃度 activity:最近 intake(lead_intake_events 账本)/最近 follow_up
//	  (follow_ups)距 as_of 的天数窗,阈值=常量 ActivityRecentDays=30,随响应
//	  披露(机器可断言);按时间值比较不做天数取整:最近事实 ≥ as_of−30天即命中
//	  (恰在窗界含、窗外一秒不含);无事实不命中、不推算。
//	来源渠道 source:contacts.source_type 既有域原样映射(FEAT-0196 同域);
//	  空/未知值不产出任何来源标签(不发明新维度)。
//	跟进状态 followup:follow_ups.completed_at 完结语义(HUI-1692/FEAT-0193,
//	  非空=已完结);待跟进(存在未完结)> 已完结(全部完结且至少一条);
//	  无跟进记录不产出该族标签。
//
// 定义披露范式沿 FEAT-0195 funnel.go:每枚派生标签输出 definition 机器披露
// (事实来源 + 判定规则 + 阈值),随目录/联系人画像/分群响应原文输出。
//
// AI 画像评分不在本票(外部依赖,deferred):接口不出现任何评分字段。
//
// 作用域:联系人记录级作用域(与联系人列表同款推导,owner 全量 / 非 owner
// 只见 assigned_member_id 人群);商机/跟进/intake 事实按联系人归属聚合,
// 不再按商机/跟进自身指派二次收窄 —— 标签是联系人画像,人群边界即隐私边界。
package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// ActivityRecentDays is the constant activity-window cutoff in days (机器可
// 断言,随目录/画像/分群响应披露,绝不静默).
const ActivityRecentDays = 30

// Derived tag families (stable machine identifiers).
const (
	TagFamilyLifecycle = "lifecycle"
	TagFamilyActivity  = "activity"
	TagFamilySource    = "source"
	TagFamilyFollowUp  = "followup"
	TagFamilyManual    = "manual" // 人工标签族:值为 contact_tag_defs.id,不参与派生
)

// Derived tag keys (stable machine identifiers, unique across families).
const (
	TagLifecycleInFlight = "opp_active" // 在途商机
	TagLifecycleWon      = "opp_won"    // 已成交
	TagLifecycleLost     = "opp_lost"   // 已流失
	TagActivityIntake    = "recent_intake"
	TagActivityFollowUp  = "recent_follow_up"
	TagSourceManual      = "src_manual"
	TagSourceForm        = "src_form"
	TagSourceCampaign    = "src_touch_campaign"
	TagFollowUpPending   = "followup_pending" // 待跟进
	TagFollowUpClosed    = "followup_closed"  // 已完结
)

// ErrSegmentBadGroup marks an invalid segment group (unknown family, unknown
// key, empty values, unknown manual tag id) — HTTP 层显式 400,绝不静默忽略。
var ErrSegmentBadGroup = errors.New("contact tags: segment group is invalid")

// activityThresholdDisclosure is the constant-threshold disclosure shared by
// both activity tags (定义披露:阈值必须随响应可见).
const activityThresholdDisclosure = "activity_recent_days=30(常量,随响应披露)"

// DerivedTagDef is the machine-readable definition disclosure of one derived
// tag (事实来源 + 判定规则 + 阈值),沿 FEAT-0195 的定义披露范式.
type DerivedTagDef struct {
	Key        string `json:"key"`
	Family     string `json:"family"`
	Label      string `json:"label"`
	FactSource string `json:"fact_source"`
	Rule       string `json:"rule"`
	// Threshold discloses the constant threshold when the family is
	// threshold-based (activity); empty otherwise.
	Threshold string `json:"threshold,omitempty"`
}

const (
	lifecycleFactSource = "opportunities(HUI-1693/FEAT-0194 商机域;经在册 contacts JOIN,墓碑档案排除)"
	wonFactSource       = "opportunities.stage 与 opportunity_stage_history 审计链(to_stage='won';审计链只追加,重开不回溯成交事实;won=人工标记成交,绝不表示已支付/已收款)"
	sourceFactSource    = "contacts.source_type(仓内既有来源事实域,与 FEAT-0196 渠道分析同域)"
	followUpFactSource  = "follow_ups.completed_at(HUI-1692/FEAT-0193 完结语义:非空=已完结,完结/重开走显式端点)"
	lifecyclePrecedence = "生命周期是联系人当前状态:三态按 在途>已成交>已流失 取唯一优先态;无任何商机事实不产出该族标签"
)

// DerivedTagCatalog returns every derived tag with its full definition
// disclosure in deterministic (family, key) order. Pure function; callers
// embed the catalog in responses so the rules travel with the data.
func DerivedTagCatalog() []DerivedTagDef {
	return []DerivedTagDef{
		{
			Key: TagLifecycleInFlight, Family: TagFamilyLifecycle, Label: "在途商机",
			FactSource: lifecycleFactSource,
			Rule:       "联系人名下存在 stage NOT IN ('won','closed_lost') 的商机即在途;" + lifecyclePrecedence,
		},
		{
			Key: TagLifecycleWon, Family: TagFamilyLifecycle, Label: "已成交",
			FactSource: wonFactSource,
			Rule: "名下任一商机满足 stage='won' 或审计链存在 to_stage='won' 行即已成交" +
				"(成交事实一经发生不因重开消失);" + lifecyclePrecedence,
		},
		{
			Key: TagLifecycleLost, Family: TagFamilyLifecycle, Label: "已流失",
			FactSource: lifecycleFactSource,
			Rule:       "名下存在 stage='closed_lost' 的商机即已流失;" + lifecyclePrecedence,
		},
		{
			Key: TagActivityIntake, Family: TagFamilyActivity, Label: "近期有咨询",
			FactSource: "lead_intake_events(HUI-1683 intake 事件账本,任一分类的最新 created_at;经在册 contacts JOIN,墓碑档案排除)",
			Rule:       "最近一次 intake 事件时间 ≥ as_of − 30 天即命中(按时间值比较,不做天数取整;恰在窗界含);无 intake 事实不命中",
			Threshold:  activityThresholdDisclosure,
		},
		{
			Key: TagActivityFollowUp, Family: TagFamilyActivity, Label: "近期有跟进",
			FactSource: "follow_ups(HUI-1692/FEAT-0193 销售跟进记录的最新 created_at;经在册 contacts JOIN,墓碑档案排除)",
			Rule:       "最近一次跟进记录时间 ≥ as_of − 30 天即命中(按时间值比较,不做天数取整;恰在窗界含);无跟进事实不命中",
			Threshold:  activityThresholdDisclosure,
		},
		{
			Key: TagSourceManual, Family: TagFamilySource, Label: "人工录入来源",
			FactSource: sourceFactSource,
			Rule:       "contacts.source_type='manual' 的在册联系人原样映射;空/未知值不产出任何来源标签(不推算、不发明新维度)",
		},
		{
			Key: TagSourceForm, Family: TagFamilySource, Label: "表单留资来源",
			FactSource: sourceFactSource,
			Rule:       "contacts.source_type='form' 的在册联系人原样映射;空/未知值不产出任何来源标签(不推算、不发明新维度)",
		},
		{
			Key: TagSourceCampaign, Family: TagFamilySource, Label: "触点活动来源",
			FactSource: sourceFactSource,
			Rule:       "contacts.source_type='touch_campaign' 的在册联系人原样映射;空/未知值不产出任何来源标签(不推算、不发明新维度)",
		},
		{
			Key: TagFollowUpPending, Family: TagFamilyFollowUp, Label: "待跟进",
			FactSource: followUpFactSource,
			Rule:       "存在 completed_at 为空的跟进记录即待跟进;跟进状态是联系人当前状态:二态按 待跟进>已完结 取唯一优先态;无跟进记录不产出该族标签",
		},
		{
			Key: TagFollowUpClosed, Family: TagFamilyFollowUp, Label: "已完结",
			FactSource: followUpFactSource,
			Rule:       "存在非空 completed_at 的跟进且无未完结记录即已完结;二态优先级 待跟进>已完结;无跟进记录不产出该族标签",
		},
	}
}

// SegmentCombineRule is the combination-semantics disclosure carried by every
// segment response (组合语义机器可断言).
const SegmentCombineRule = "组合语义:组间 AND(联系人必须满足每一个出现的标签组),组内 OR(满足组内任一标签即命中该组);" +
	"结果只返回联系人引用 contact_id,零联系方式/姓名等 PII;作用域=记录级(与联系人列表一致:" +
	"owner 租户全量,非 owner 只见 assigned_member_id 人群);派生标签按需重算不落库"

// ---- pure rule functions (真/假两侧 + 边界全部矩阵化测试) ------------------------

// LifecycleKeys derives the single lifecycle state by precedence 在途>已成交>已流失.
func LifecycleKeys(hasActive, hasWon, hasLost bool) []string {
	switch {
	case hasActive:
		return []string{TagLifecycleInFlight}
	case hasWon:
		return []string{TagLifecycleWon}
	case hasLost:
		return []string{TagLifecycleLost}
	}
	return nil
}

// ActivityKeys derives the activity tags: a fact counts when its latest time
// is at or after asOf−30d (boundary inclusive; a later-than-asOf fact still
// satisfies the comparison). No facts invent no tags.
func ActivityKeys(lastIntake, lastFollowUp *time.Time, asOf time.Time) []string {
	cutoff := asOf.Add(-ActivityRecentDays * 24 * time.Hour)
	var keys []string
	if lastIntake != nil && !lastIntake.Before(cutoff) {
		keys = append(keys, TagActivityIntake)
	}
	if lastFollowUp != nil && !lastFollowUp.Before(cutoff) {
		keys = append(keys, TagActivityFollowUp)
	}
	return keys
}

// SourceKeys maps the existing contacts.source_type domain verbatim; empty or
// unknown values produce no tag (不推算、不发明新维度).
func SourceKeys(sourceType string) []string {
	switch sourceType {
	case "manual":
		return []string{TagSourceManual}
	case "form":
		return []string{TagSourceForm}
	case "touch_campaign":
		return []string{TagSourceCampaign}
	}
	return nil
}

// FollowUpStatusKeys derives the single follow-up state by precedence
// 待跟进>已完结; no follow-ups produce no tag.
func FollowUpStatusKeys(openCount, doneCount int) []string {
	switch {
	case openCount > 0:
		return []string{TagFollowUpPending}
	case doneCount > 0:
		return []string{TagFollowUpClosed}
	}
	return nil
}

// ---- derived computation --------------------------------------------------------

// DerivedTagsQuery is a fully resolved read-only derived-tag computation.
type DerivedTagsQuery struct {
	TenantID string
	// AssigneeMemberID: "" = tenant-wide (owner); else the member's own
	// assignment population (record-level scope reuse, 与联系人列表同款).
	AssigneeMemberID string
	// ContactIDs optionally narrows the population (per-contact view);
	// unknown ids simply have no entry (existence is the caller's check).
	ContactIDs []string
	// AsOf is the deterministic "now" for the activity window (UTC).
	AsOf time.Time
}

// contactFacts is one contact's raw fact snapshot for derivation.
type contactFacts struct {
	source                              string
	hasActiveOpp, hasWonOpp, hasLostOpp bool
	lastIntakeAt, lastFollowUpAt        *time.Time
	openFollowUps, doneFollowUps        int
}

// tagPopulation resolves the scoped live-contact population (id -> source_type).
func (s *Store) tagPopulation(tenantID, assignee string, ids []string) (map[string]string, error) {
	q := `SELECT id, source_type FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`
	args := []any{tenantID}
	if assignee != "" {
		q += ` AND assigned_member_id=?`
		args = append(args, assignee)
	}
	if len(ids) > 0 {
		q += ` AND id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pop := map[string]string{}
	for rows.Next() {
		var id, src string
		if err := rows.Scan(&id, &src); err != nil {
			return nil, err
		}
		pop[id] = src
	}
	return pop, rows.Err()
}

// ComputeDerivedTags computes, for every live contact in scope, the sorted
// derived tag keys. Read-only: nothing is persisted, recompute is stable.
func (s *Store) ComputeDerivedTags(q DerivedTagsQuery) (map[string][]string, error) {
	if q.TenantID == "" {
		return nil, errors.New("contact tags: tenant required")
	}
	if q.AsOf.IsZero() {
		return nil, errors.New("contact tags: as_of required")
	}
	pop, err := s.tagPopulation(q.TenantID, q.AssigneeMemberID, q.ContactIDs)
	if err != nil {
		return nil, err
	}
	facts := map[string]*contactFacts{}
	for id, src := range pop {
		facts[id] = &contactFacts{source: src}
	}

	// 生命周期:商机状态按联系人归属聚合(在途/已流失看当前 stage)。
	rows, err := s.DB.Query(
		`SELECT o.contact_id,
		        SUM(CASE WHEN o.stage NOT IN ('won','closed_lost') THEN 1 ELSE 0 END),
		        SUM(CASE WHEN o.stage = 'closed_lost' THEN 1 ELSE 0 END)
		 FROM opportunities o
		 JOIN contacts c ON c.id = o.contact_id AND c.deleted_at IS NULL
		 WHERE o.tenant_id=? GROUP BY o.contact_id`, q.TenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var contactID string
		var active, lost int
		if err := rows.Scan(&contactID, &active, &lost); err != nil {
			rows.Close()
			return nil, err
		}
		if f, ok := facts[contactID]; ok { // 人群外(墓碑/作用域外):事实不算别人的画像
			f.hasActiveOpp, f.hasLostOpp = active > 0, lost > 0
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 已成交:当前 stage 或只追加审计链里的 to_stage='won'(重开不回溯)。
	rows, err = s.DB.Query(
		`SELECT o.contact_id FROM opportunities o
		 JOIN contacts c ON c.id = o.contact_id AND c.deleted_at IS NULL
		 WHERE o.tenant_id=? AND (o.stage='won' OR EXISTS(
		     SELECT 1 FROM opportunity_stage_history h
		     WHERE h.opportunity_id=o.id AND h.to_stage='won'))
		 GROUP BY o.contact_id`, q.TenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var contactID string
		if err := rows.Scan(&contactID); err != nil {
			rows.Close()
			return nil, err
		}
		if f, ok := facts[contactID]; ok {
			f.hasWonOpp = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 活跃度(intake 账本):每联系人最近一次事件时间。
	rows, err = s.DB.Query(
		`SELECT e.contact_id, MAX(e.created_at) FROM lead_intake_events e
		 JOIN contacts c ON c.id = e.contact_id AND c.deleted_at IS NULL
		 WHERE e.tenant_id=? GROUP BY e.contact_id`, q.TenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var contactID, at string
		if err := rows.Scan(&contactID, &at); err != nil {
			rows.Close()
			return nil, err
		}
		if f, ok := facts[contactID]; ok {
			if t, ok := parseFunnelTime(at); ok {
				f.lastIntakeAt = &t // 无法定位时间的行不推算(防御)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 活跃度+跟进状态(follow_ups):最近时间与完结计数一次取回。
	rows, err = s.DB.Query(
		`SELECT f.contact_id, MAX(f.created_at),
		        SUM(CASE WHEN f.completed_at IS NULL THEN 1 ELSE 0 END),
		        SUM(CASE WHEN f.completed_at IS NOT NULL THEN 1 ELSE 0 END)
		 FROM follow_ups f
		 JOIN contacts c ON c.id = f.contact_id AND c.deleted_at IS NULL
		 WHERE f.tenant_id=? GROUP BY f.contact_id`, q.TenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var contactID, at string
		var open, done int
		if err := rows.Scan(&contactID, &at, &open, &done); err != nil {
			rows.Close()
			return nil, err
		}
		if f, ok := facts[contactID]; ok {
			if t, ok := parseFunnelTime(at); ok {
				f.lastFollowUpAt = &t
			}
			f.openFollowUps, f.doneFollowUps = open, done
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := map[string][]string{}
	for id, f := range facts {
		keys := LifecycleKeys(f.hasActiveOpp, f.hasWonOpp, f.hasLostOpp)
		keys = append(keys, ActivityKeys(f.lastIntakeAt, f.lastFollowUpAt, q.AsOf)...)
		keys = append(keys, SourceKeys(f.source)...)
		keys = append(keys, FollowUpStatusKeys(f.openFollowUps, f.doneFollowUps)...)
		if len(keys) == 0 {
			continue // 无任何派生事实的联系人不出现在结果里(无标签不发明)
		}
		sort.Strings(keys) // 输出确定性:升序
		out[id] = keys
	}
	return out, nil
}

// ---- segment query ----------------------------------------------------------------

// SegmentGroup is one label group: family + values (OR within the group).
type SegmentGroup struct {
	Family string   `json:"family"`
	Values []string `json:"values"`
}

// SegmentQuery is a fully resolved read-only segment query.
type SegmentQuery struct {
	TenantID         string
	AssigneeMemberID string
	AsOf             time.Time
	Groups           []SegmentGroup
}

// ContactSegment answers the segment: live contacts in scope matching EVERY
// group (AND across groups, OR within a group's values). Results are sorted
// contact references — 分群结果零 PII,规则见 SegmentCombineRule.
func (s *Store) ContactSegment(q SegmentQuery) ([]string, error) {
	if q.TenantID == "" {
		return nil, errors.New("contact tags: tenant required")
	}
	if q.AsOf.IsZero() {
		return nil, errors.New("contact tags: as_of required")
	}
	if len(q.Groups) == 0 {
		return nil, ErrSegmentBadGroup // 必须显式给组:空查询绝不静默等于全量
	}

	// 组校验:家族封闭、派生键必须在所属族、人工值必须是本租户在册标签。
	valid := map[string]map[string]bool{}
	for _, def := range DerivedTagCatalog() {
		if valid[def.Family] == nil {
			valid[def.Family] = map[string]bool{}
		}
		valid[def.Family][def.Key] = true
	}
	for _, g := range q.Groups {
		if len(g.Values) == 0 {
			return nil, ErrSegmentBadGroup
		}
		if g.Family == TagFamilyManual {
			for _, v := range g.Values {
				if strings.TrimSpace(v) == "" {
					return nil, ErrSegmentBadGroup
				}
				if _, err := s.GetContactTagDef(v, q.TenantID); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return nil, ErrSegmentBadGroup
					}
					return nil, err
				}
			}
			continue
		}
		keys, ok := valid[g.Family]
		if !ok {
			return nil, ErrSegmentBadGroup
		}
		for _, v := range g.Values {
			if !keys[v] {
				return nil, ErrSegmentBadGroup
			}
		}
	}

	pop, err := s.tagPopulation(q.TenantID, q.AssigneeMemberID, nil)
	if err != nil {
		return nil, err
	}

	// 派生键集合(仅当存在派生组时计算)。
	derived := map[string][]string{}
	hasDerived := false
	for _, g := range q.Groups {
		if g.Family != TagFamilyManual {
			hasDerived = true
			break
		}
	}
	if hasDerived {
		derived, err = s.ComputeDerivedTags(DerivedTagsQuery{
			TenantID: q.TenantID, AssigneeMemberID: q.AssigneeMemberID, AsOf: q.AsOf})
		if err != nil {
			return nil, err
		}
	}

	// 人工标签挂载集合(仅当存在人工组时取回;人群作用域一致)。
	manual := map[string]map[string]bool{} // contact_id -> set(tag_id)
	for _, g := range q.Groups {
		if g.Family != TagFamilyManual {
			continue
		}
		qry := `SELECT l.contact_id, l.tag_id FROM contact_tag_links l
		 JOIN contacts c ON c.id = l.contact_id AND c.deleted_at IS NULL
		 WHERE l.tenant_id=?`
		args := []any{q.TenantID}
		if q.AssigneeMemberID != "" {
			qry += ` AND c.assigned_member_id=?`
			args = append(args, q.AssigneeMemberID)
		}
		rows, err := s.DB.Query(qry, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var contactID, tagID string
			if err := rows.Scan(&contactID, &tagID); err != nil {
				rows.Close()
				return nil, err
			}
			if manual[contactID] == nil {
				manual[contactID] = map[string]bool{}
			}
			manual[contactID][tagID] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// 组间 AND、组内 OR;只输出人群内的联系人引用,升序确定性。
	out := []string{}
	for id := range pop {
		match := true
	groups:
		for _, g := range q.Groups {
			if g.Family == TagFamilyManual {
				for _, v := range g.Values {
					if manual[id][v] {
						continue groups
					}
				}
				match = false
				break
			}
			for _, v := range g.Values {
				if contains(derived[id], v) {
					continue groups
				}
			}
			match = false
			break
		}
		if match {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
