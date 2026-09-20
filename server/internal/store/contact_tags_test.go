// HUI-1690 / FEAT-0191 客户画像标签体系 store 层单测:
//   - 派生标签目录与定义披露纪律(事实来源+判定规则+阈值,沿 FEAT-0195 范式);
//   - 派生规则矩阵纯函数真假两侧 + 边界(恰在阈值窗内/窗外一秒/无事实/未来时间);
//   - 派生计算(种子事实):生命周期三态优先级、活跃度 30 天常量窗、来源映射、
//     跟进状态二态、墓碑排除、跨租户零串行;
//   - 人工标签:定义 CRUD(重名冲突/改名校验/在用拒删)、打标幂等首戳留痕、
//     去标幂等、留痕可回查、坏引用/跨租户/墓碑 fail-closed;
//   - 分群组合查询:组间 AND 组内 OR、与独立 SQL 对拍、记录级作用域、
//     非法组显式报错、跨租户 0 行。
package store

import (
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// tagAsOf is the deterministic "today" for every derived-tag assertion:
// the 30-day activity cutoff is therefore 2026-01-02T00:00:00Z.
var tagAsOf = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

// ---- seeds -----------------------------------------------------------------

// tagSeedDoneFollowUp inserts a COMPLETED follow_ups row (completed_at set =
// FEAT-0193 完结语义), distinct from funnelSeedFollowUp's open rows.
func tagSeedDoneFollowUp(t *testing.T, d *sql.DB, tenant, id, contactID, createdAt, completedAt string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO follow_ups(id,tenant_id,contact_id,lead_id,note,next_follow_up_at,completed_at,created_by,created_at,updated_at)
		 VALUES(?,?,?,NULL,'跟进','',?,?,?,?)`, id, tenant, contactID, completedAt,
		funnelActorID(t, d, tenant), createdAt, completedAt); err != nil {
		t.Fatalf("seed done follow-up %s: %v", id, err)
	}
}

// tagSeedIntakeEvent inserts one lead_intake_events ledger row (HUI-1683)
// with an explicit time; lead_id NOT NULL -> each event carries a minimal lead.
func tagSeedIntakeEvent(t *testing.T, d *sql.DB, tenant, id, contactID, createdAt string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO leads(id,tenant_id,contact_id,status,created_by,created_at,updated_at)
		 VALUES(?,?,?,'new','test',?,?)`, "lead_"+id, tenant, contactID, createdAt, createdAt); err != nil {
		t.Fatalf("seed intake lead %s: %v", id, err)
	}
	if _, err := d.Exec(
		`INSERT INTO lead_intake_events(id,tenant_id,source_app,source_ns,event_id,content_sha256,phone_fpr,class,lead_id,contact_id,created_at)
		 VALUES(?,?,'tags','store',?,'sha','','new',?,?,?)`, id, tenant, "ev_"+id, "lead_"+id, contactID, createdAt); err != nil {
		t.Fatalf("seed intake event %s: %v", id, err)
	}
}

func tagSeedDef(t *testing.T, s *Store, tenant, name string) ContactTagDef {
	t.Helper()
	def, err := s.CreateContactTagDef(tenant, name, "", "", funnelActorID(t, s.DB, tenant))
	if err != nil {
		t.Fatalf("seed tag def %s: %v", name, err)
	}
	return def
}

func keysOf(t *testing.T, m map[string][]string, contactID string) []string {
	t.Helper()
	keys, ok := m[contactID]
	if !ok {
		return nil
	}
	return keys
}

func wantKeys(t *testing.T, m map[string][]string, contactID string, want ...string) {
	t.Helper()
	got := keysOf(t, m, contactID)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Fatalf("derived keys of %s = %v, want %v", contactID, got, want)
	}
}

// ---- derived catalog disclosure discipline ----------------------------------

// 目录纪律:派生标签=四族十枚,每枚必须带完整机器披露(事实来源+判定规则),
// 活跃度族必须披露常量阈值;键全局唯一;家族枚举封闭。
func TestDerivedTagCatalogDiscipline(t *testing.T) {
	cat := DerivedTagCatalog()
	if len(cat) != 10 {
		t.Fatalf("catalog size = %d, want 10 (生命周期3+活跃度2+来源3+跟进2)", len(cat))
	}
	seen := map[string]bool{}
	for _, def := range cat {
		if def.Key == "" || def.Family == "" || def.Label == "" || def.FactSource == "" || def.Rule == "" {
			t.Fatalf("derived tag %s definition incomplete: %+v", def.Key, def)
		}
		if seen[def.Key] {
			t.Fatalf("duplicate derived key %s", def.Key)
		}
		seen[def.Key] = true
		switch def.Family {
		case TagFamilyLifecycle:
			if def.Threshold != "" {
				t.Fatalf("lifecycle tag %s must not carry a threshold: %s", def.Key, def.Threshold)
			}
		case TagFamilyActivity:
			if !strings.Contains(def.Threshold, "30") {
				t.Fatalf("activity tag %s must disclose the 30-day constant, got threshold %q", def.Key, def.Threshold)
			}
		case TagFamilySource, TagFamilyFollowUp:
		default:
			t.Fatalf("derived tag %s has unknown family %q", def.Key, def.Family)
		}
	}
	for _, key := range []string{TagLifecycleInFlight, TagLifecycleWon, TagLifecycleLost,
		TagActivityIntake, TagActivityFollowUp,
		TagSourceManual, TagSourceForm, TagSourceCampaign,
		TagFollowUpPending, TagFollowUpClosed} {
		if !seen[key] {
			t.Fatalf("catalog missing key %s", key)
		}
	}
	if ActivityRecentDays != 30 {
		t.Fatalf("ActivityRecentDays = %d, want 30 (常量随响应披露)", ActivityRecentDays)
	}
}

// ---- pure rule matrices (真假两侧 + 边界) ------------------------------------

func TestLifecycleRuleMatrix(t *testing.T) {
	cases := []struct {
		name              string
		active, won, lost bool
		want              []string
	}{
		{"在途", true, false, false, []string{TagLifecycleInFlight}},
		{"已成交", false, true, false, []string{TagLifecycleWon}},
		{"已流失", false, false, true, []string{TagLifecycleLost}},
		{"在途压过已成交/已流失", true, true, true, []string{TagLifecycleInFlight}},
		{"成交压过流失(won 审计链只追加)", false, true, true, []string{TagLifecycleWon}},
		{"无商机事实", false, false, false, nil},
	}
	for _, tc := range cases {
		if got := LifecycleKeys(tc.active, tc.won, tc.lost); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: LifecycleKeys = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestActivityRuleMatrixBoundaries(t *testing.T) {
	cutoff := tagAsOf.Add(-ActivityRecentDays * 24 * time.Hour)
	exactly := cutoff                        // 恰在窗界:含
	oneSecOut := cutoff.Add(-time.Second)    // 窗界前一秒:不含
	inside := tagAsOf.Add(-time.Hour)        // 窗内
	future := tagAsOf.Add(time.Hour)         // 晚到/时钟偏移:仍命中(>= 截止即真)
	old := tagAsOf.Add(-31 * 24 * time.Hour) // 窗外

	if got := ActivityKeys(&exactly, nil, tagAsOf); !reflect.DeepEqual(got, []string{TagActivityIntake}) {
		t.Fatalf("恰在 30 天窗界必须命中, got %v", got)
	}
	if got := ActivityKeys(&oneSecOut, nil, tagAsOf); len(got) != 0 {
		t.Fatalf("窗界前一秒必须不命中, got %v", got)
	}
	if got := ActivityKeys(nil, &inside, tagAsOf); !reflect.DeepEqual(got, []string{TagActivityFollowUp}) {
		t.Fatalf("窗内跟进必须命中, got %v", got)
	}
	if got := ActivityKeys(&old, &old, tagAsOf); len(got) != 0 {
		t.Fatalf("双事实皆窗外必须全不命中, got %v", got)
	}
	if got := ActivityKeys(&future, &future, tagAsOf); !reflect.DeepEqual(got,
		[]string{TagActivityIntake, TagActivityFollowUp}) {
		t.Fatalf("最近事实晚于 as_of 仍命中(>= 截止), got %v", got)
	}
	if got := ActivityKeys(nil, nil, tagAsOf); len(got) != 0 {
		t.Fatalf("无事实不得发明标签, got %v", got)
	}
}

func TestSourceRuleMatrix(t *testing.T) {
	if got := SourceKeys("form"); !reflect.DeepEqual(got, []string{TagSourceForm}) {
		t.Fatalf("form -> %v", got)
	}
	if got := SourceKeys("manual"); !reflect.DeepEqual(got, []string{TagSourceManual}) {
		t.Fatalf("manual -> %v", got)
	}
	if got := SourceKeys("touch_campaign"); !reflect.DeepEqual(got, []string{TagSourceCampaign}) {
		t.Fatalf("touch_campaign -> %v", got)
	}
	if got := SourceKeys(""); len(got) != 0 {
		t.Fatalf("空来源不产出任何标签(不推算), got %v", got)
	}
	if got := SourceKeys("wechat"); len(got) != 0 {
		t.Fatalf("未知来源值不产出任何标签(不发明新维度), got %v", got)
	}
}

func TestFollowUpStatusRuleMatrix(t *testing.T) {
	if got := FollowUpStatusKeys(1, 0); !reflect.DeepEqual(got, []string{TagFollowUpPending}) {
		t.Fatalf("只有未完结 -> 待跟进, got %v", got)
	}
	if got := FollowUpStatusKeys(0, 1); !reflect.DeepEqual(got, []string{TagFollowUpClosed}) {
		t.Fatalf("只有已完结 -> 已完结, got %v", got)
	}
	if got := FollowUpStatusKeys(2, 3); !reflect.DeepEqual(got, []string{TagFollowUpPending}) {
		t.Fatalf("并存时待跟进优先, got %v", got)
	}
	if got := FollowUpStatusKeys(0, 0); len(got) != 0 {
		t.Fatalf("无跟进记录不产出该族标签, got %v", got)
	}
}

// ---- seeded derived computation ----------------------------------------------
//
// 已知事实集(as_of=2026-02-01,30 天截止=2026-01-02T00:00:00Z):
//
//	活跃度:cIn 恰在截止(命中)/ cOut 截止+1s(不命中)/ cNear 窗内(命中)
//	       / cOld 窗外(不命中)/ cFut ==as_of(命中)
//	生命周期:cNear 在途 / cWon 当前 won / cLost closed_lost / cMix 在途+won
//	       (在途优先)/ cWL closed_lost 但审计链有过 won(成交优先)
//	跟进状态:cPend 未完结 / cDone 已完结 / cBoth 并存(待跟进优先)
//	来源:form/manual/touch_campaign/空各自映射
//	墓碑:cTomb 有 intake+跟进+人工标签但被排除;跨租户零串行。
func tagSeedDerivedScene(t *testing.T, d *sql.DB) {
	t.Helper()
	seed := func(id, phone, src, created string) {
		funnelSeedContact(t, d, "tnt_1", id, phone, src, "", created, false)
	}
	seed("cIn", "13900000021", "form", "2025-12-01T00:00:00Z")
	seed("cOut", "13900000022", "form", "2025-12-02T00:00:00Z")
	seed("cNear", "13900000023", "form", "2025-12-03T00:00:00Z")
	seed("cOld", "13900000024", "form", "2025-12-04T00:00:00Z")
	seed("cFut", "13900000034", "form", "2025-12-05T00:00:00Z")
	seed("cWon", "13900000025", "manual", "2025-12-06T00:00:00Z")
	seed("cLost", "13900000026", "manual", "2025-12-07T00:00:00Z")
	seed("cMix", "13900000027", "form", "2025-12-08T00:00:00Z")
	seed("cWL", "13900000028", "form", "2025-12-09T00:00:00Z")
	seed("cPend", "13900000029", "form", "2025-12-10T00:00:00Z")
	seed("cDone", "13900000030", "manual", "2025-12-11T00:00:00Z")
	seed("cBoth", "13900000031", "form", "2025-12-12T00:00:00Z")
	// source_type 有 CHECK 约束(只允许三枚举),故每个在册联系人至少有来源标签;
	// 「无任何派生键」的防御分支由 SourceKeys 纯函数覆盖。
	seed("cNone", "13900000032", "manual", "2025-12-13T00:00:00Z")
	funnelSeedContact(t, d, "tnt_1", "cTomb", "13900000033", "form", "", "2025-12-14T00:00:00Z", true)

	tagSeedIntakeEvent(t, d, "tnt_1", "ie1", "cIn", "2026-01-02T00:00:00Z")   // 恰在截止:含
	tagSeedIntakeEvent(t, d, "tnt_1", "ie2", "cOut", "2026-01-01T23:59:59Z")  // 截止前一秒:不含
	tagSeedIntakeEvent(t, d, "tnt_1", "ie3", "cNear", "2026-01-31T23:59:59Z") // 窗内
	tagSeedIntakeEvent(t, d, "tnt_1", "ie4", "cOld", "2025-12-01T00:00:00Z")  // 窗外
	tagSeedIntakeEvent(t, d, "tnt_1", "ie5", "cFut", "2026-02-01T00:00:00Z")  // == as_of:命中
	tagSeedIntakeEvent(t, d, "tnt_1", "ie6", "cMix", "2026-01-15T00:00:00Z")  // 组合对拍用
	tagSeedIntakeEvent(t, d, "tnt_1", "ie7", "cTomb", "2026-01-15T00:00:00Z") // 墓碑:排除

	funnelSeedFollowUp(t, d, "tnt_1", "tf1", "cPend", "2026-01-05T00:00:00Z")
	tagSeedDoneFollowUp(t, d, "tnt_1", "tf2", "cDone", "2026-01-06T00:00:00Z", "2026-01-07T00:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "tf3", "cBoth", "2026-01-08T00:00:00Z")
	tagSeedDoneFollowUp(t, d, "tnt_1", "tf4", "cBoth", "2026-01-09T00:00:00Z", "2026-01-10T00:00:00Z")
	funnelSeedFollowUp(t, d, "tnt_1", "tf5", "cTomb", "2026-01-11T00:00:00Z") // 墓碑:排除

	funnelSeedOpp(t, d, "tnt_1", "ocn", "cNear", "open", "", "2026-01-05T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "ow", "cWon", "won", "", "2026-01-06T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "ol", "cLost", "closed_lost", "", "2026-01-07T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "om1", "cMix", "open", "", "2026-01-08T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "om2", "cMix", "won", "", "2026-01-09T00:00:00Z")
	funnelSeedOpp(t, d, "tnt_1", "owl", "cWL", "closed_lost", "", "2026-01-10T00:00:00Z")
	funnelSeedStageEvent(t, d, "tnt_1", "oh_wl", "owl", "open", "won", "2026-01-11T00:00:00Z")
}

func TestComputeDerivedTagsKnownScene(t *testing.T) {
	d := funnelTestDB(t)
	tagSeedDerivedScene(t, d)

	m, err := New(d).ComputeDerivedTags(DerivedTagsQuery{TenantID: "tnt_1", AsOf: tagAsOf})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	// source_type 有 CHECK 约束:每个在册联系人至少带一枚来源标签。
	wantKeys(t, m, "cIn", TagActivityIntake, TagSourceForm)
	wantKeys(t, m, "cNear", TagLifecycleInFlight, TagActivityIntake, TagSourceForm)
	wantKeys(t, m, "cFut", TagActivityIntake, TagSourceForm)
	wantKeys(t, m, "cWon", TagLifecycleWon, TagSourceManual)
	wantKeys(t, m, "cLost", TagLifecycleLost, TagSourceManual)
	wantKeys(t, m, "cMix", TagLifecycleInFlight, TagActivityIntake, TagSourceForm) // 在途压过已成交
	wantKeys(t, m, "cWL", TagLifecycleWon, TagSourceForm)                          // 审计链 won 压过 closed_lost
	wantKeys(t, m, "cPend", TagFollowUpPending, TagActivityFollowUp, TagSourceForm)
	wantKeys(t, m, "cDone", TagFollowUpClosed, TagActivityFollowUp, TagSourceManual)
	wantKeys(t, m, "cBoth", TagFollowUpPending, TagActivityFollowUp, TagSourceForm)
	wantKeys(t, m, "cNone", TagSourceManual)
	// 活跃度边界两侧:恰好窗外(截止前一秒/31 天)只剩来源标签。
	wantKeys(t, m, "cOut", TagSourceForm)
	wantKeys(t, m, "cOld", TagSourceForm)
	if _, present := m["cTomb"]; present {
		t.Fatal("tombstoned contact must be excluded entirely (墓碑不进画像)")
	}
	// 键输出确定性:逐联系人升序。
	for id, keys := range m {
		if !sort.StringsAreSorted(keys) {
			t.Fatalf("contact %s keys not sorted: %v", id, keys)
		}
	}

	// ContactIDs 收窄:只算指定联系人,越界 id 静默无键(存在性由调用方判定)。
	narrow, err := New(d).ComputeDerivedTags(DerivedTagsQuery{TenantID: "tnt_1", AsOf: tagAsOf,
		ContactIDs: []string{"cWon", "con_ghost"}})
	if err != nil {
		t.Fatalf("narrow compute: %v", err)
	}
	if len(narrow) != 1 || !reflect.DeepEqual(keysOf(t, narrow, "cWon"),
		[]string{TagLifecycleWon, TagSourceManual}) {
		t.Fatalf("narrow compute = %v, want only cWon", narrow)
	}

	// 跨租户零串行:tnt_2 只见自己的事实。
	funnelSeedContact(t, d, "tnt_2", "z1", "13900000051", "form", "", "2026-01-05T00:00:00Z", false)
	tagSeedIntakeEvent(t, d, "tnt_2", "ze1", "z1", "2026-01-06T00:00:00Z")
	m2, err := New(d).ComputeDerivedTags(DerivedTagsQuery{TenantID: "tnt_2", AsOf: tagAsOf})
	if err != nil {
		t.Fatalf("compute t2: %v", err)
	}
	wantKeys(t, m2, "z1", TagActivityIntake, TagSourceForm)
	if len(m2) != 1 {
		t.Fatalf("tenant2 keys = %v, want only z1 (tnt_1 事实零串行)", m2)
	}
}

// ---- manual tag definitions CRUD ----------------------------------------------

func TestContactTagDefCRUD(t *testing.T) {
	d := funnelTestDB(t)
	s := New(d)
	actor1 := funnelActorID(t, d, "tnt_1")
	actor2 := funnelActorID(t, d, "tnt_2")

	def, err := s.CreateContactTagDef("tnt_1", "高意向", "#FF8800", "重点客户", actor1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(def.ID) <= 4 || def.ID[:4] != "ctd_" {
		t.Fatalf("tag def id = %s, want ctd_ prefix", def.ID)
	}
	if def.Name != "高意向" || def.Color != "#FF8800" || def.Description != "重点客户" ||
		def.TenantID != "tnt_1" || def.CreatedBy != actor1 || def.CreatedAt == "" || def.UpdatedAt == "" {
		t.Fatalf("created def round-trip mismatch: %+v", def)
	}

	// 重名 4xx 前提:同租户重名显式冲突。
	if _, err := s.CreateContactTagDef("tnt_1", "高意向", "", "", actor1); !errors.Is(err, ErrTagDefDuplicate) {
		t.Fatalf("duplicate name = %v, want ErrTagDefDuplicate", err)
	}
	// 跨租户同名合法(租户内唯一,不是全局唯一)。
	if _, err := s.CreateContactTagDef("tnt_2", "高意向", "", "", actor2); err != nil {
		t.Fatalf("same name in another tenant must be allowed: %v", err)
	}

	// 列表租户作用域。
	defs1, err := s.ListContactTagDefs("tnt_1")
	if err != nil || len(defs1) != 1 || defs1[0].ID != def.ID {
		t.Fatalf("list tnt_1 = %v err=%v, want exactly the one def", defs1, err)
	}

	// 更新:改名/颜色/描述。
	other := tagSeedDef(t, s, "tnt_1", "普通")
	newName, newDesc := "超级意向", ""
	upd, err := s.UpdateContactTagDef(def.ID, "tnt_1", ContactTagDefPatch{Name: &newName, Color: nil, Description: &newDesc})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "超级意向" || upd.Description != "" || upd.Color != "#FF8800" {
		t.Fatalf("patched def = %+v (nil 字段必须不变)", upd)
	}
	// 改名撞既有名:同样显式冲突。
	if _, err := s.UpdateContactTagDef(def.ID, "tnt_1", ContactTagDefPatch{Name: &other.Name}); !errors.Is(err, ErrTagDefDuplicate) {
		t.Fatalf("rename onto existing name = %v, want ErrTagDefDuplicate", err)
	}
	// 跨租户更新/删除按未找到处理(fail-closed)。
	if _, err := s.UpdateContactTagDef(def.ID, "tnt_2", ContactTagDefPatch{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant update = %v, want ErrNoRows", err)
	}

	// 在用拒删:打标后删除显式冲突;去标后可删。
	funnelSeedContact(t, d, "tnt_1", "cA", "13900000041", "form", "", "2026-01-05T00:00:00Z", false)
	if _, _, err := s.TagContact("tnt_1", "cA", def.ID, actor1); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if err := s.DeleteContactTagDef(def.ID, "tnt_1"); !errors.Is(err, ErrTagDefInUse) {
		t.Fatalf("delete in-use = %v, want ErrTagDefInUse", err)
	}
	if changed, err := s.UntagContact("tnt_1", "cA", def.ID); err != nil || !changed {
		t.Fatalf("untag = %v/%v, want changed", changed, err)
	}
	if err := s.DeleteContactTagDef(def.ID, "tnt_1"); err != nil {
		t.Fatalf("delete unused = %v, want nil", err)
	}
	if _, err := s.GetContactTagDef(def.ID, "tnt_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get deleted def = %v, want ErrNoRows", err)
	}
	if err := s.DeleteContactTagDef("ctd_ghost", "tnt_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete unknown = %v, want ErrNoRows", err)
	}
}

// ---- tagging idempotency + audit trail -----------------------------------------

func TestTagContactIdempotentAndAudit(t *testing.T) {
	d := funnelTestDB(t)
	s := New(d)
	actor := funnelActorID(t, d, "tnt_1")
	funnelSeedContact(t, d, "tnt_1", "cA", "13900000042", "form", "", "2026-01-05T00:00:00Z", false)
	funnelSeedContact(t, d, "tnt_1", "cGone", "13900000043", "form", "", "2026-01-05T00:00:00Z", true)
	defHi := tagSeedDef(t, s, "tnt_1", "高意向")
	defSvc := tagSeedDef(t, s, "tnt_1", "售后")
	defOther := tagSeedDef(t, s, "tnt_2", "别租户的标签")

	link, changed, err := s.TagContact("tnt_1", "cA", defHi.ID, actor)
	if err != nil || !changed {
		t.Fatalf("first tag = changed=%t err=%v, want created", changed, err)
	}
	if link.TagName != "高意向" || link.ContactID != "cA" || link.AppliedBy != actor || link.AppliedAt == "" {
		t.Fatalf("link audit fields incomplete: %+v", link)
	}
	// 幂等重放:零新行,首戳不改写。
	replay, changed2, err := s.TagContact("tnt_1", "cA", defHi.ID, actor)
	if err != nil || changed2 {
		t.Fatalf("replay tag must be idempotent (changed=false), got changed=%t err=%v", changed2, err)
	}
	if replay.ID != link.ID || replay.AppliedAt != link.AppliedAt || replay.AppliedBy != link.AppliedBy {
		t.Fatalf("replay must keep the FIRST stamp: %+v vs %+v", replay, link)
	}
	// 第二个标签并存。
	if _, _, err := s.TagContact("tnt_1", "cA", defSvc.ID, actor); err != nil {
		t.Fatalf("second tag: %v", err)
	}

	// 留痕可回查:谁/何时/哪标签。
	links, err := s.ListContactTags("tnt_1", "cA")
	if err != nil || len(links) != 2 {
		t.Fatalf("audit trail = %v err=%v, want 2 links", links, err)
	}
	names := map[string]bool{}
	for _, l := range links {
		names[l.TagName] = true
		if l.AppliedBy == "" || l.AppliedAt == "" {
			t.Fatalf("audit row missing who/when: %+v", l)
		}
	}
	if !names["高意向"] || !names["售后"] {
		t.Fatalf("audit trail must expose tag names: %v", names)
	}

	// 去标幂等。
	if changed, err := s.UntagContact("tnt_1", "cA", defHi.ID); err != nil || !changed {
		t.Fatalf("untag = %t/%v, want changed", changed, err)
	}
	if changed, err := s.UntagContact("tnt_1", "cA", defHi.ID); err != nil || changed {
		t.Fatalf("second untag must be idempotent (changed=false), got %t/%v", changed, err)
	}
	if links, _ := s.ListContactTags("tnt_1", "cA"); len(links) != 1 || links[0].TagID != defSvc.ID {
		t.Fatalf("after untag = %v, want only 售后", links)
	}

	// fail-closed:坏引用/墓碑/跨租户。
	if _, _, err := s.TagContact("tnt_1", "con_ghost", defHi.ID, actor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown contact = %v, want ErrNoRows", err)
	}
	if _, _, err := s.TagContact("tnt_1", "cGone", defHi.ID, actor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("tombstoned contact = %v, want ErrNoRows(墓碑不可再打标)", err)
	}
	if _, _, err := s.TagContact("tnt_1", "cA", "ctd_ghost", actor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown tag = %v, want ErrNoRows", err)
	}
	if _, _, err := s.TagContact("tnt_1", "cA", defOther.ID, actor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant tag def = %v, want ErrNoRows", err)
	}
	if changed, err := s.UntagContact("tnt_2", "cA", defSvc.ID); err != nil || changed {
		t.Fatalf("cross-tenant untag = %t/%v, want no-op", changed, err)
	}
}

// ---- segment query --------------------------------------------------------------

// 分群组合查询:组间 AND、组内 OR;与独立 SQL 对拍;记录级作用域;非法组显式报错;
// 跨租户 0 行。复用 tagSeedDerivedScene 的事实集,人工标签:cIn→高意向、
// cNear→高意向、cMix→售后、cTomb→高意向(墓碑排除)。
func seedSegmentTags(t *testing.T, s *Store) (tagHi, tagSvc ContactTagDef) {
	t.Helper()
	tagHi = tagSeedDef(t, s, "tnt_1", "高意向")
	tagSvc = tagSeedDef(t, s, "tnt_1", "售后")
	for _, pair := range [][2]string{{"cIn", tagHi.ID}, {"cNear", tagHi.ID}, {"cMix", tagSvc.ID}} {
		if _, _, err := s.TagContact("tnt_1", pair[0], pair[1], funnelActorID(t, s.DB, "tnt_1")); err != nil {
			t.Fatalf("seed link %v: %v", pair, err)
		}
	}
	return tagHi, tagSvc
}

func segIDs(t *testing.T, s *Store, q SegmentQuery) []string {
	t.Helper()
	ids, err := s.ContactSegment(q)
	if err != nil {
		t.Fatalf("segment: %v", err)
	}
	return ids
}

func TestContactSegmentCombineAndReconciliation(t *testing.T) {
	d := funnelTestDB(t)
	s := New(d)
	tagSeedDerivedScene(t, d)
	tagHi, tagSvc := seedSegmentTags(t, s)

	// 组内 OR:生命周期 ∈ {在途, 已成交}。
	got := segIDs(t, s, SegmentQuery{TenantID: "tnt_1", AsOf: tagAsOf,
		Groups: []SegmentGroup{{Family: TagFamilyLifecycle, Values: []string{TagLifecycleInFlight, TagLifecycleWon}}}})
	if want := []string{"cMix", "cNear", "cWL", "cWon"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle OR-group = %v, want %v", got, want)
	}

	// 组间 AND:在途 ∧ 人工[高意向]。
	got = segIDs(t, s, SegmentQuery{TenantID: "tnt_1", AsOf: tagAsOf,
		Groups: []SegmentGroup{
			{Family: TagFamilyLifecycle, Values: []string{TagLifecycleInFlight}},
			{Family: TagFamilyManual, Values: []string{tagHi.ID}},
		}})
	if want := []string{"cNear"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle AND manual = %v, want %v", got, want)
	}

	// 四组综合对拍:独立 SQL 重算同一分群(与实现不同写法),结果必须逐行一致。
	complex := SegmentQuery{TenantID: "tnt_1", AsOf: tagAsOf, Groups: []SegmentGroup{
		{Family: TagFamilyLifecycle, Values: []string{TagLifecycleInFlight, TagLifecycleWon}},
		{Family: TagFamilyActivity, Values: []string{TagActivityIntake}},
		{Family: TagFamilySource, Values: []string{TagSourceForm}},
		{Family: TagFamilyManual, Values: []string{tagHi.ID, tagSvc.ID}},
	}}
	got = segIDs(t, s, complex)

	var wantSQL []string
	rows2, err2 := d.Query(`SELECT c.id FROM contacts c
		WHERE c.tenant_id=? AND c.deleted_at IS NULL
		AND ( EXISTS(SELECT 1 FROM opportunities o WHERE o.contact_id=c.id AND o.stage NOT IN ('won','closed_lost'))
		   OR EXISTS(SELECT 1 FROM opportunities o2
		             JOIN opportunity_stage_history h ON h.opportunity_id=o2.id
		             WHERE o2.contact_id=c.id AND (o2.stage='won' OR h.to_stage='won')) )
		AND EXISTS(SELECT 1 FROM lead_intake_events e WHERE e.contact_id=c.id AND e.tenant_id=?
		           AND e.created_at >= '2026-01-02T00:00:00Z')
		AND c.source_type='form'
		AND ( EXISTS(SELECT 1 FROM contact_tag_links l WHERE l.contact_id=c.id AND l.tenant_id=? AND l.tag_id=?)
		   OR EXISTS(SELECT 1 FROM contact_tag_links l WHERE l.contact_id=c.id AND l.tenant_id=? AND l.tag_id=?) )
		ORDER BY c.id`,
		"tnt_1", "tnt_1", "tnt_1", tagHi.ID, "tnt_1", tagSvc.ID)
	if err2 != nil {
		t.Fatalf("reconcile SQL: %v", err2)
	}
	defer rows2.Close()
	for rows2.Next() {
		var id string
		if err := rows2.Scan(&id); err != nil {
			t.Fatal(err)
		}
		wantSQL = append(wantSQL, id)
	}
	if err := rows2.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantSQL) {
		t.Fatalf("segment = %v, independent SQL = %v (对拍必须逐行一致)", got, wantSQL)
	}

	// 记录级作用域:非 owner 只见自己被指派联系人(复用既有作用域推导)。
	m1, m2 := funnelMembers(t, d)
	if _, err := d.Exec(`UPDATE contacts SET assigned_member_id=? WHERE id='cNear'`, m1); err != nil {
		t.Fatal(err)
	}
	got = segIDs(t, s, SegmentQuery{TenantID: "tnt_1", AsOf: tagAsOf, AssigneeMemberID: m1,
		Groups: []SegmentGroup{{Family: TagFamilyLifecycle, Values: []string{TagLifecycleInFlight, TagLifecycleWon}}}})
	if want := []string{"cNear"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("m1 scope = %v, want %v", got, want)
	}
	got = segIDs(t, s, SegmentQuery{TenantID: "tnt_1", AsOf: tagAsOf, AssigneeMemberID: m2,
		Groups: []SegmentGroup{{Family: TagFamilyLifecycle, Values: []string{TagLifecycleInFlight, TagLifecycleWon}}}})
	if len(got) != 0 {
		t.Fatalf("m2 scope = %v, want empty", got)
	}

	// 跨租户 0 行:tnt_2 无任何满足事实。
	got = segIDs(t, s, SegmentQuery{TenantID: "tnt_2", AsOf: tagAsOf,
		Groups: []SegmentGroup{{Family: TagFamilySource, Values: []string{TagSourceForm}}}})
	if len(got) != 0 {
		t.Fatalf("cross-tenant segment = %v, want 0 rows", got)
	}
}

func TestContactSegmentValidation(t *testing.T) {
	d := funnelTestDB(t)
	s := New(d)
	tagSeedDerivedScene(t, d)
	tagHi, _ := seedSegmentTags(t, s)
	q := SegmentQuery{TenantID: "tnt_1", AsOf: tagAsOf}

	if _, err := s.ContactSegment(q); !errors.Is(err, ErrSegmentBadGroup) {
		t.Fatalf("no groups = %v, want ErrSegmentBadGroup(必须显式给组)", err)
	}
	if _, err := s.ContactSegment(appendQuery(q, SegmentGroup{Family: TagFamilySource, Values: nil})); !errors.Is(err, ErrSegmentBadGroup) {
		t.Fatalf("empty values = %v, want ErrSegmentBadGroup", err)
	}
	if _, err := s.ContactSegment(appendQuery(q, SegmentGroup{Family: "push_audience", Values: []string{"x"}})); !errors.Is(err, ErrSegmentBadGroup) {
		t.Fatalf("unknown family = %v, want ErrSegmentBadGroup(绝不发明触达域)", err)
	}
	if _, err := s.ContactSegment(appendQuery(q, SegmentGroup{Family: TagFamilyLifecycle, Values: []string{TagActivityIntake}})); !errors.Is(err, ErrSegmentBadGroup) {
		t.Fatalf("cross-family key = %v, want ErrSegmentBadGroup", err)
	}
	if _, err := s.ContactSegment(appendQuery(q, SegmentGroup{Family: TagFamilyLifecycle, Values: []string{"opp_zombie"}})); !errors.Is(err, ErrSegmentBadGroup) {
		t.Fatalf("unknown derived key = %v, want ErrSegmentBadGroup", err)
	}
	if _, err := s.ContactSegment(appendQuery(q, SegmentGroup{Family: TagFamilyManual, Values: []string{"ctd_ghost"}})); !errors.Is(err, ErrSegmentBadGroup) {
		t.Fatalf("unknown manual tag = %v, want ErrSegmentBadGroup", err)
	}
	// 已知标签 + 已知键:合法。
	if _, err := s.ContactSegment(appendQuery(q,
		SegmentGroup{Family: TagFamilyManual, Values: []string{tagHi.ID}})); err != nil {
		t.Fatalf("valid manual group must pass: %v", err)
	}
}

func appendQuery(q SegmentQuery, g SegmentGroup) SegmentQuery {
	q.Groups = append(q.Groups, g)
	return q
}
