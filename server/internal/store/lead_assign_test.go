// HUI-1685 / FEAT-0186 线索自动分配(确定性加权轮询)store 层单测:
//   - 池 CRUD 与校验:每租户每成员至多一行;weight 域 1..1000;成员必须本租户、
//     在册、在职、role=sales(复用既有角色模型,不发明新角色);
//   - 平滑加权轮询:同权重退化为纯轮询(1:1:1 → m1,m2,m3 循环);
//     权重 1:2 → 精确平滑序列且任意连续 3 次里重权者恰好 2 次;
//   - 地域/行业精确匹配优先:线索携带的每个非空维度都必须与池条目标签完全相等;
//     无匹配 → 全池平滑加权轮询;
//   - intake 接缝:只分配首投(重放短路)、开关 off 逐字节旧行为、池空不阻塞
//     建档、人工改派不被覆盖、跨租户池隔离、filtered 线索照常分配(正交);
//   - 成员停用/复职动态进出轮询(有效池 = JOIN members 在册在职 sales)。
package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// assignMember seeds a members row and returns its id.
func assignMember(t *testing.T, s *Store, tenant, principal, role string, enabled bool) string {
	t.Helper()
	m, err := s.CreateMember(tenant, principal, role, principal, "test", enabled)
	if err != nil {
		t.Fatalf("seed member %s: %v", principal, err)
	}
	return m.ID
}

// seedPoolRow inserts a lead_assign_pool row with explicit created_at so the
// rotation order (created_at, id) is fully deterministic in tests.
func seedPoolRow(t *testing.T, d *sql.DB, tenant, id, memberID string, weight, current int, region, industry, createdAt string) {
	t.Helper()
	_, err := d.Exec(
		`INSERT INTO lead_assign_pool(id,tenant_id,member_id,weight,current_weight,region,industry,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, tenant, memberID, weight, current, region, industry, memberID, createdAt, createdAt)
	if err != nil {
		t.Fatalf("seed pool row %s: %v", id, err)
	}
}

// assignedOf reads the lead's assigned_member_id.
func assignedOf(t *testing.T, d *sql.DB, leadID string) string {
	t.Helper()
	var a sql.NullString
	if err := d.QueryRow(`SELECT assigned_member_id FROM leads WHERE id=?`, leadID).Scan(&a); err != nil {
		t.Fatalf("lead row %s: %v", leadID, err)
	}
	return a.String
}

// intakeAssign runs one intake in its own committed tx.
func intakeAssign(t *testing.T, d *sql.DB, in IntakeInput) IntakeResult {
	t.Helper()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := IntakeLeadInTx(tx, in)
	if err != nil {
		t.Fatalf("intake %s/%s/%s: %v", in.SourceApp, in.SourceNS, in.EventID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAssignPoolEntryCRUDAndValidation(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	admin := assignMember(t, s, "tnt_1", "usr_admin", "owner", true) // created_by 外键主体
	sales := assignMember(t, s, "tnt_1", "usr_s1", "sales", true)

	e, err := s.CreateAssignPoolEntry("tnt_1", sales, 2, "华东", "餐饮", admin)
	if err != nil {
		t.Fatalf("create pool entry: %v", err)
	}
	if e.Weight != 2 || e.Region != "华东" || e.Industry != "餐饮" || e.MemberID != sales {
		t.Fatalf("entry = %+v", e)
	}
	// 同成员重复入池:显式拒绝(每成员至多一行)。
	if _, err := s.CreateAssignPoolEntry("tnt_1", sales, 1, "", "", admin); !errors.Is(err, ErrAssignPoolDuplicate) {
		t.Fatalf("duplicate err = %v, want ErrAssignPoolDuplicate", err)
	}
	// weight 域 1..1000。
	second := assignMember(t, s, "tnt_1", "usr_s2", "sales", true)
	for _, w := range []int{0, -1, 1001} {
		if _, err := s.CreateAssignPoolEntry("tnt_1", second, w, "", "", admin); err == nil ||
			!strings.Contains(err.Error(), "weight") {
			t.Fatalf("weight %d err = %v, want weight validation error", w, err)
		}
	}
	// role 必须含销售语义(owner 不入池);成员必须在职。
	owner := assignMember(t, s, "tnt_1", "usr_owner", "owner", true)
	if _, err := s.CreateAssignPoolEntry("tnt_1", owner, 1, "", "", admin); err == nil || !strings.Contains(err.Error(), "sales") {
		t.Fatalf("owner-role err = %v, want sales-role validation error", err)
	}
	disabled := assignMember(t, s, "tnt_1", "usr_dis", "sales", false)
	if _, err := s.CreateAssignPoolEntry("tnt_1", disabled, 1, "", "", admin); err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("disabled member err = %v, want enabled validation error", err)
	}
	// 跨租户成员与不存在的成员都拒绝。
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_2','B','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	other := assignMember(t, s, "tnt_2", "usr_other_t", "sales", true)
	if _, err := s.CreateAssignPoolEntry("tnt_1", other, 1, "", "", admin); err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("cross-tenant member err = %v, want tenant validation error", err)
	}
	if _, err := s.CreateAssignPoolEntry("tnt_1", "mem_missing", 1, "", "", admin); err == nil {
		t.Fatal("missing member must be rejected")
	}
	// 标签长度上限 64 runes(CJK 按字符计)。
	third := assignMember(t, s, "tnt_1", "usr_s3", "sales", true)
	long := strings.Repeat("东", 65)
	if _, err := s.CreateAssignPoolEntry("tnt_1", third, 1, long, "", admin); err == nil ||
		!strings.Contains(err.Error(), "region") {
		t.Fatalf("long region err = %v, want region cap error", err)
	}

	// 更新:改权重/清标签(显式空串=清空,nil=不变);未知 id → ErrAssignPoolNotFound。
	w := 5
	up, err := s.UpdateAssignPoolEntry(e.ID, "tnt_1", &w, strPtr(""), nil)
	if err != nil {
		t.Fatalf("update pool entry: %v", err)
	}
	if up.Weight != 5 || up.Region != "" || up.Industry != "餐饮" {
		t.Fatalf("updated entry = %+v, want weight 5 / region cleared / industry kept", up)
	}
	if _, err := s.UpdateAssignPoolEntry("mem_nope", "tnt_1", &w, nil, nil); !errors.Is(err, ErrAssignPoolNotFound) {
		t.Fatalf("update unknown err = %v, want ErrAssignPoolNotFound", err)
	}
	items, err := s.ListAssignPoolEntries("tnt_1")
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %d entries err=%v, want 1 (其余创建尝试都被校验拒绝)", len(items), err)
	}
	if err := s.DeleteAssignPoolEntry(e.ID, "tnt_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteAssignPoolEntry(e.ID, "tnt_1"); !errors.Is(err, ErrAssignPoolNotFound) {
		t.Fatalf("re-delete err = %v, want ErrAssignPoolNotFound", err)
	}
	// 删除后可重新入池。
	if _, err := s.CreateAssignPoolEntry("tnt_1", sales, 1, "", "", admin); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

// 同权重退化为纯轮询:1:1:1 → m1,m2,m3,m1,m2,m3(确定性顺序,created_at 显式)。
func TestSmoothWRRUniformRotation(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	m1 := assignMember(t, s, "tnt_1", "usr_r1", "sales", true)
	m2 := assignMember(t, s, "tnt_1", "usr_r2", "sales", true)
	m3 := assignMember(t, s, "tnt_1", "usr_r3", "sales", true)
	seedPoolRow(t, d, "tnt_1", "p1", m1, 1, 0, "", "", "2026-01-01T00:00:01Z")
	seedPoolRow(t, d, "tnt_1", "p2", m2, 1, 0, "", "", "2026-01-01T00:00:02Z")
	seedPoolRow(t, d, "tnt_1", "p3", m3, 1, 0, "", "", "2026-01-01T00:00:03Z")

	want := []string{m1, m2, m3, m1, m2, m3}
	for i, w := range want {
		res := intakeAssign(t, d, assignIn("touch", "wx", "e"+string(rune('a'+i))))
		if res.AssignedTo != w {
			t.Fatalf("pick %d assigned %s, want %s (纯轮询序列)", i+1, res.AssignedTo, w)
		}
		if got := assignedOf(t, d, res.LeadID); got != w {
			t.Fatalf("pick %d lead assignee = %s, want %s", i+1, got, w)
		}
	}
	// 一个整周期后游标归零,状态自洽。
	var cursors int
	if err := d.QueryRow(`SELECT COUNT(1) FROM lead_assign_pool WHERE current_weight<>0`).Scan(&cursors); err != nil || cursors != 0 {
		t.Fatalf("non-zero cursors = %d err=%v, want 0 after full cycles", cursors, err)
	}
}

// 权重 1:2:A(轻)B(重) → 精确平滑序列 B,A,B,B,A,B:任意连续 3 次里
// B 恰好 2 次、A 恰好 1 次(平滑 = 无突发连发超配)。
func TestSmoothWRRWeightRatio(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	a := assignMember(t, s, "tnt_1", "usr_wa", "sales", true)
	b := assignMember(t, s, "tnt_1", "usr_wb", "sales", true)
	seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "", "2026-01-01T00:00:01Z")
	seedPoolRow(t, d, "tnt_1", "pb", b, 2, 0, "", "", "2026-01-01T00:00:02Z")

	var countA, countB int
	seq := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		res := intakeAssign(t, d, assignIn("touch", "wx", "w-e"+string(rune('a'+i))))
		switch res.AssignedTo {
		case a:
			countA++
		case b:
			countB++
		default:
			t.Fatalf("pick %d assigned %q, want a or b", i+1, res.AssignedTo)
		}
		seq = append(seq, res.AssignedTo)
	}
	wantSeq := []string{b, a, b, b, a, b}
	for i := range wantSeq {
		if seq[i] != wantSeq[i] {
			t.Fatalf("weight sequence = %v, want %v (平滑加权轮询)", seq, wantSeq)
		}
	}
	if countA != 2 || countB != 4 {
		t.Fatalf("counts A=%d B=%d, want 2:4 (1:2 比例)", countA, countB)
	}
	for i := 0; i+3 <= len(seq); i++ {
		win := map[string]int{}
		for _, p := range seq[i : i+3] {
			win[p]++
		}
		if win[a] != 1 || win[b] != 2 {
			t.Fatalf("window %v = %v, want exactly one A and two B", i, seq[i:i+3])
		}
	}
}

// 地域/行业精确匹配优先;无匹配 → 全池平滑加权轮询。
func TestAssignRegionIndustryMatchAndFallback(t *testing.T) {
	t.Run("region tag routes to the tagged seller only", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_ra", "sales", true)
		b := assignMember(t, s, "tnt_1", "usr_rb", "sales", true)
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "华东", "", "2026-01-01T00:00:01Z")
		seedPoolRow(t, d, "tnt_1", "pb", b, 1, 0, "", "", "2026-01-01T00:00:02Z")
		in := assignIn("touch", "wx", "re-1")
		in.Region = "华东"
		if res := intakeAssign(t, d, in); res.AssignedTo != a {
			t.Fatalf("region 华东 assigned %q, want the tagged seller %q", res.AssignedTo, a)
		}
	})
	t.Run("industry tag routes to the tagged seller only", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_ia", "sales", true)
		b := assignMember(t, s, "tnt_1", "usr_ib", "sales", true)
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "餐饮", "2026-01-01T00:00:01Z")
		seedPoolRow(t, d, "tnt_1", "pb", b, 1, 0, "", "", "2026-01-01T00:00:02Z")
		in := assignIn("touch", "wx", "in-1")
		in.Industry = "餐饮"
		if res := intakeAssign(t, d, in); res.AssignedTo != a {
			t.Fatalf("industry 餐饮 assigned %q, want %q", res.AssignedTo, a)
		}
	})
	t.Run("both dimensions must match exactly", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_ba", "sales", true) // 华东 + 餐饮
		b := assignMember(t, s, "tnt_1", "usr_bb", "sales", true) // 仅华东
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "华东", "餐饮", "2026-01-01T00:00:01Z")
		seedPoolRow(t, d, "tnt_1", "pb", b, 1, 0, "华东", "", "2026-01-01T00:00:02Z")
		in := assignIn("touch", "wx", "bd-1")
		in.Region, in.Industry = "华东", "餐饮"
		if res := intakeAssign(t, d, in); res.AssignedTo != a {
			t.Fatalf("华东+餐饮 assigned %q, want the fully-tagged seller %q", res.AssignedTo, a)
		}
	})
	t.Run("no match falls back to the full pool", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_fa", "sales", true) // 只有华南标签
		b := assignMember(t, s, "tnt_1", "usr_fb", "sales", true) // 无标签,权重 2
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "华南", "", "2026-01-01T00:00:01Z")
		seedPoolRow(t, d, "tnt_1", "pb", b, 2, 0, "", "", "2026-01-01T00:00:02Z")
		in := assignIn("touch", "wx", "fb-1")
		in.Region = "华东" // 无华东标签 → 全池:平滑 WRR 首轮 cw=[1,2] 选 b(证明未按标签缩小)
		if res := intakeAssign(t, d, in); res.AssignedTo != b {
			t.Fatalf("no-match fallback assigned %q, want full-pool pick %q", res.AssignedTo, b)
		}
	})
}

// intake 接缝:开关 off = 逐字节旧行为(有池也不分配);重放短路不重复分配;
// 池空照常建档;人工改派不被自动分配覆盖。
func TestAssignIntakeSeamGating(t *testing.T) {
	t.Run("flag off keeps byte-identical legacy behavior", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_ga", "sales", true)
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "", "2026-01-01T00:00:01Z")
		res := intakeAssign(t, d, intakeIn("touch", "wx", "g-off", "13900000088")) // AssignEnabled 未设
		if res.AssignedTo != "" {
			t.Fatalf("flag-off AssignedTo = %q, want empty", res.AssignedTo)
		}
		if got := assignedOf(t, d, res.LeadID); got != "" {
			t.Fatalf("flag-off lead assignee = %q, want empty (off = 与既往一致)", got)
		}
	})
	t.Run("pool empty still creates the lead", func(t *testing.T) {
		d := intakeTestDB(t)
		res := intakeAssign(t, d, assignIn("touch", "wx", "g-empty"))
		if res.AssignedTo != "" {
			t.Fatalf("empty-pool AssignedTo = %q, want empty", res.AssignedTo)
		}
		var n int
		if err := d.QueryRow(`SELECT COUNT(1) FROM leads WHERE id=?`, res.LeadID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("lead rows = %d err=%v, want 1 (池空不阻塞建档)", n, err)
		}
	})
	t.Run("replay short-circuits and manual reassignment survives", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_sa", "sales", true)
		b := assignMember(t, s, "tnt_1", "usr_sb", "sales", true)
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "", "2026-01-01T00:00:01Z")
		seedPoolRow(t, d, "tnt_1", "pb", b, 1, 0, "", "", "2026-01-01T00:00:02Z")

		first := intakeAssign(t, d, assignIn("touch", "wx", "g-1"))
		if first.AssignedTo != a {
			t.Fatalf("first pick = %q, want %q", first.AssignedTo, a)
		}
		// 人工改派(普通更新路径)。
		if _, err := s.UpdateLead(first.LeadID, "tnt_1", LeadPatch{AssignedTo: &b}); err != nil {
			t.Fatalf("manual reassign: %v", err)
		}
		// 同键重放:幂等返回,绝不改写人工改派。
		replay := intakeAssign(t, d, assignIn("touch", "wx", "g-1"))
		if !replay.Duplicate || replay.AssignedTo != "" {
			t.Fatalf("replay = %+v, want duplicate with empty AssignedTo (重放短路)", replay)
		}
		if got := assignedOf(t, d, first.LeadID); got != b {
			t.Fatalf("lead assignee after replay = %q, want manual %q", got, b)
		}
	})
	t.Run("cross-tenant pool never serves another tenant", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_2','B','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		foreign := assignMember(t, s, "tnt_2", "usr_foreign", "sales", true)
		seedPoolRow(t, d, "tnt_2", "px", foreign, 1, 0, "", "", "2026-01-01T00:00:01Z")
		res := intakeAssign(t, d, assignIn("touch", "wx", "x-1")) // tnt_1 无池
		if res.AssignedTo != "" {
			t.Fatalf("cross-tenant leakage: tnt_1 lead assigned %q", res.AssignedTo)
		}
		if got := assignedOf(t, d, res.LeadID); got != "" {
			t.Fatalf("tnt_1 lead assignee = %q, want empty", got)
		}
	})
	t.Run("filtered lead is still assigned (FEAT-0187 orthogonal)", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_filt", "sales", true)
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "", "2026-01-01T00:00:01Z")
		in := intakeIn("touch", "wx", "f-1", "12345678901")
		in.FilterEnabled = true
		in.AssignEnabled = true
		res := intakeAssign(t, d, in)
		if res.FilterReason != FilterReasonInvalidPhone {
			t.Fatalf("filter reason = %q, want invalid_phone", res.FilterReason)
		}
		if res.AssignedTo != a {
			t.Fatalf("filtered lead AssignedTo = %q, want %q (filtered 正交,照常分配)", res.AssignedTo, a)
		}
		var status string
		if err := d.QueryRow(`SELECT status FROM leads WHERE id=?`, res.LeadID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != LeadStatusFiltered {
			t.Fatalf("status = %q, want filtered", status)
		}
	})
	t.Run("disabled member leaves the rotation dynamically", func(t *testing.T) {
		d := intakeTestDB(t)
		s := New(d)
		a := assignMember(t, s, "tnt_1", "usr_da", "sales", true)
		b := assignMember(t, s, "tnt_1", "usr_db", "sales", true)
		seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "", "2026-01-01T00:00:01Z")
		seedPoolRow(t, d, "tnt_1", "pb", b, 1, 0, "", "", "2026-01-01T00:00:02Z")

		if res := intakeAssign(t, d, assignIn("touch", "wx", "d-1")); res.AssignedTo != a {
			t.Fatalf("pick1 = %q, want %q", res.AssignedTo, a)
		}
		// b 停用 → 退出轮询;后续全归 a。
		if _, err := s.UpdateMember(b, nil, boolPtr(false), nil); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if res := intakeAssign(t, d, assignIn("touch", "wx", "d-"+string(rune('a'+i)))); res.AssignedTo != a {
				t.Fatalf("pick while b disabled = %q, want %q (在职纪律)", res.AssignedTo, a)
			}
		}
		// 复职 → 回到轮询。
		if _, err := s.UpdateMember(b, nil, boolPtr(true), nil); err != nil {
			t.Fatal(err)
		}
		if res := intakeAssign(t, d, assignIn("touch", "wx", "d-back")); res.AssignedTo != b {
			t.Fatalf("pick after re-enable = %q, want %q", res.AssignedTo, b)
		}
	})
}

// 公共表单通道同样经 intake 接缝分配(AssignEnabled 穿透 SubmitFormInTx);
// 面向提交者的公开结果不携带销售路由信息。
func TestSubmitFormAutoAssign(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")
	a := assignMember(t, s, "tnt_1", "usr_form", "sales", true)
	seedPoolRow(t, d, "tnt_1", "pa", a, 1, 0, "", "", "2026-01-01T00:00:01Z")

	res := mustSubmit(t, d, f.ID, SubmitFormInput{
		FormID:        f.ID,
		Payload:       submitPayload("王五", "13900000007", "wx_post", "ref-assign-1", true),
		Content:       []byte(`{"name":"王五"}`),
		Pepper:        "pepper",
		AssignEnabled: true,
	})
	if got := assignedOf(t, d, res.LeadID); got != a {
		t.Fatalf("form lead assignee = %q, want %q", got, a)
	}
}

// ---- helpers -------------------------------------------------------------------

func boolPtr(b bool) *bool { return &b }

// assignIn is an intake input with auto-assignment enabled.
func assignIn(app, ns, event string) IntakeInput {
	in := intakeIn(app, ns, event, "13900000099")
	in.AssignEnabled = true
	return in
}
