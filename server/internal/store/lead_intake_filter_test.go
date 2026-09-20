// HUI-1686 / FEAT-0187 无效线索过滤(纯确定性规则)单测:
//   - 确定性:中国大陆手机号规范化后须匹配 ^1[3-9]\d{9}$;另拒全同数字等明显
//     无效模式;提供了邮箱则校验基本格式。绝不外接运营商实号/短信验证能力。
//   - 过滤不静默丢弃:filtered 线索仍写台账(lead status=filtered + 机器原因码),
//     不进入营销池;事件三分类语义不变(过滤维度记在 lead 上)。
//   - 与去重的先后:规范化 → 过滤分类 → 去重分类。同一无效号码重复提交仍按
//     去重语义(repeat_consult / exact_duplicate),绝不绕过去重多建 contact。
//   - 与 consent 正交:filtered 线索的授权记录照常落库。
//   - PII:原因码是机器码;联系方式原文不进任何新表面。
package store

import (
	"testing"
	"time"
)

func TestLeadFilterReasonPhone(t *testing.T) {
	cases := []struct {
		phone string
		want  string // "" = 通过
	}{
		// 合法大陆手机号(含 +86/86 前缀与分隔符,复用 NormalizePhone)。
		{"13900000001", ""},
		{"139 0000 0001", ""},
		{"139-0000-0001", ""},
		{"+8613900000001", ""},
		{"86 139 0000 0001", ""},
		{"（139）0000-0001", ""},
		{"19912345678", ""},
		// 未提供手机号:无号可判,不在电话规则内过滤(intake 域本就允许无手机号)。
		{"", ""},
		{"   ", ""},
		// 错格式:非 1 开头 / 第二位越界 / 位数不对 / 带字母 / 座机。
		{"12345678901", FilterReasonInvalidPhone},
		{"23900000001", FilterReasonInvalidPhone},
		{"10300000001", FilterReasonInvalidPhone},
		{"1390000000", FilterReasonInvalidPhone},
		{"139000000011", FilterReasonInvalidPhone},
		{"1390000abc1", FilterReasonInvalidPhone},
		{"010-1234567", FilterReasonInvalidPhone},
		// 全同数字等明显无效模式(能匹配号段但无意义)。
		{"13333333333", FilterReasonInvalidPhone},
		{"19999999999", FilterReasonInvalidPhone},
		// 超长。
		{"139000000012345", FilterReasonInvalidPhone},
		{"1390000000123456789012345678901234567", FilterReasonInvalidPhone},
	}
	for _, tc := range cases {
		if got := LeadFilterReason(tc.phone, ""); got != tc.want {
			t.Errorf("LeadFilterReason(%q, \"\") = %q, want %q", tc.phone, got, tc.want)
		}
	}
}

func TestLeadFilterReasonEmail(t *testing.T) {
	passPhone := "13900000001"
	cases := []struct {
		email string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"a@b.cn", ""},
		{"yi.shop+tag@example.com.cn", ""},
		{"  user@example.cn  ", ""},
		{"no-at", FilterReasonInvalidEmail},
		{"a@", FilterReasonInvalidEmail},
		{"@b.cn", FilterReasonInvalidEmail},
		{"a@b@c.cn", FilterReasonInvalidEmail},
		{"a b@c.cn", FilterReasonInvalidEmail},
		{"a@b", FilterReasonInvalidEmail},
		{"a@b.", FilterReasonInvalidEmail},
		{"a@.cn", FilterReasonInvalidEmail},
		{"a@b..cn", FilterReasonInvalidEmail},
	}
	for _, tc := range cases {
		if got := LeadFilterReason(passPhone, tc.email); got != tc.want {
			t.Errorf("LeadFilterReason(phone, %q) = %q, want %q", tc.email, got, tc.want)
		}
	}
	// 手机号与邮箱同时无效:报手机号原因(一次一个机器码,电话优先)。
	if got := LeadFilterReason("12345678901", "no-at"); got != FilterReasonInvalidPhone {
		t.Errorf("both invalid = %q, want phone-first", got)
	}
	// 手机号无效时即便邮箱合法也过滤,但原因码仍是手机号的。
	if got := LeadFilterReason("12345678901", "a@b.cn"); got != FilterReasonInvalidPhone {
		t.Errorf("invalid phone valid email = %q, want invalid_phone", got)
	}
}

// 过滤开:无效手机号线索仍写台账(contact/lead/事件三行俱全),但 lead 是
// status=filtered + 机器原因码 —— 不以 status=new 进入营销池。
func TestIntakeFilterOnFilteredLeadStillLedgered(t *testing.T) {
	d := intakeTestDB(t)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	in := intakeIn("touch", "wx", "e1", "12345678901")
	in.FilterEnabled = true
	res, err := IntakeLeadInTx(tx, in)
	if err != nil {
		t.Fatalf("filtered intake: %v", err)
	}
	if res.Class != IntakeClassNew {
		t.Fatalf("class = %q, want new (三分类语义不变)", res.Class)
	}
	if res.FilterReason != FilterReasonInvalidPhone {
		t.Fatalf("filter reason = %q, want invalid_phone", res.FilterReason)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := d.QueryRow(`SELECT status,filter_reason FROM leads WHERE id=?`, res.LeadID).
		Scan(&status, &reason); err != nil {
		t.Fatalf("lead row missing: %v", err)
	}
	if status != LeadStatusFiltered || reason != FilterReasonInvalidPhone {
		t.Fatalf("lead = status %q reason %q, want filtered/invalid_phone", status, reason)
	}
	// 台账不丢:contact 与事件行都存在。
	var contacts int
	if err := d.QueryRow(`SELECT COUNT(1) FROM contacts WHERE id=?`, res.ContactID).Scan(&contacts); err != nil || contacts != 1 {
		t.Fatalf("contact rows = %d err=%v, want 1 (台账留痕)", contacts, err)
	}
	var events int
	if err := d.QueryRow(`SELECT COUNT(1) FROM lead_intake_events WHERE lead_id=?`, res.LeadID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("event rows = %d err=%v, want 1 (台账留痕)", events, err)
	}
}

// 过滤关(默认):完全沿用现行为 —— 同样的无效手机号原样建档为 new。
func TestIntakeFilterOffKeepsLegacyBehavior(t *testing.T) {
	d := intakeTestDB(t)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	in := intakeIn("touch", "wx", "e1", "12345678901")
	in.FilterEnabled = false
	res, err := IntakeLeadInTx(tx, in)
	if err != nil {
		t.Fatalf("filter-off intake: %v", err)
	}
	if res.FilterReason != "" {
		t.Fatalf("filter reason = %q, want empty when the flag is off", res.FilterReason)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := d.QueryRow(`SELECT status,filter_reason FROM leads WHERE id=?`, res.LeadID).
		Scan(&status, &reason); err != nil {
		t.Fatalf("lead row: %v", err)
	}
	if status != "new" || reason != "" {
		t.Fatalf("lead = %q/%q, want new/\"\" (off = 与既往行为一致)", status, reason)
	}
	// off 时超长号码仍走既有 400 式硬拒。
	tx2, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	long := intakeIn("touch", "wx", "e2", "1390000000123456789012345678901234567")
	long.FilterEnabled = false
	if _, err := IntakeLeadInTx(tx2, long); err == nil {
		t.Fatal("filter off: over-long phone must keep the legacy hard rejection")
	}
	tx2.Rollback()
}

// 过滤开:合法号(含 +86 形态)不受影响;错邮箱单独触发 invalid_email;
// 超长号码不再硬拒而是分类为 filtered。
func TestIntakeFilterOnMatrix(t *testing.T) {
	d := intakeTestDB(t)

	mk := func(event, phone, email string) IntakeInput {
		in := intakeIn("touch", "wx", event, phone)
		in.Email = email
		in.FilterEnabled = true
		return in
	}

	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// +86 合法号 → 正常 new,无原因码。
	res, err := IntakeLeadInTx(tx, mk("e-ok", "+86 139-0000-0002", ""))
	if err != nil {
		t.Fatalf("valid +86 intake: %v", err)
	}
	if res.FilterReason != "" {
		t.Fatalf("valid number got reason %q, want empty", res.FilterReason)
	}
	var okStatus string
	if err := tx.QueryRow(`SELECT status FROM leads WHERE id=?`, res.LeadID).Scan(&okStatus); err != nil {
		t.Fatal(err)
	}
	if okStatus != "new" {
		t.Fatalf("valid lead status = %q, want new", okStatus)
	}
	// 错邮箱 → invalid_email。
	resMail, err := IntakeLeadInTx(tx, mk("e-mail", "13900000003", "no-at"))
	if err != nil {
		t.Fatalf("bad email intake: %v", err)
	}
	if resMail.FilterReason != FilterReasonInvalidEmail {
		t.Fatalf("bad email reason = %q, want invalid_email", resMail.FilterReason)
	}
	// 未提供手机号:过滤开也不判空号(intake 域本就允许无手机号建档)。
	resNone, err := IntakeLeadInTx(tx, mk("e-nophone", "   ", ""))
	if err != nil {
		t.Fatalf("no-phone intake: %v", err)
	}
	if resNone.FilterReason != "" {
		t.Fatalf("no-phone reason = %q, want empty", resNone.FilterReason)
	}
	// 超长:过滤开时不再硬拒,分类为 filtered/invalid_phone。
	resLong, err := IntakeLeadInTx(tx, mk("e-long", "1390000000123456789012345678901234567", ""))
	if err != nil {
		t.Fatalf("over-long phone with filter on must classify, got err %v", err)
	}
	if resLong.FilterReason != FilterReasonInvalidPhone {
		t.Fatalf("over-long reason = %q, want invalid_phone", resLong.FilterReason)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var longStatus string
	if err := d.QueryRow(`SELECT status FROM leads WHERE id=?`, resLong.LeadID).Scan(&longStatus); err != nil {
		t.Fatal(err)
	}
	if longStatus != LeadStatusFiltered {
		t.Fatalf("over-long lead status = %q, want filtered", longStatus)
	}
}

// 同一无效号码重复提交:仍按去重语义 —— 第二投 repeat_consult 关联同一 contact,
// 重放同键 exact_duplicate;绝不绕过去重多建 contact。
func TestIntakeFilterOnRepeatInvalidStillDedups(t *testing.T) {
	d := intakeTestDB(t)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, err := IntakeLeadInTx(tx, intakeFilteredIn("e1", "12345678901"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := IntakeLeadInTx(tx, intakeFilteredIn("e2", "123 4567 8901"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := IntakeLeadInTx(tx, intakeFilteredIn("e1", "12345678901"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if second.Class != IntakeClassRepeatConsult {
		t.Fatalf("second class = %q, want repeat_consult (去重语义不因过滤改变)", second.Class)
	}
	if second.ContactID != first.ContactID {
		t.Fatalf("second contact = %s, want same as first %s (无效号码也参与去重)", second.ContactID, first.ContactID)
	}
	if second.FilterReason != FilterReasonInvalidPhone {
		t.Fatalf("second filter reason = %q, want invalid_phone", second.FilterReason)
	}
	if !replay.Duplicate || replay.Class != IntakeClassExactDuplicate {
		t.Fatalf("replay = %+v, want exact_duplicate", replay)
	}
	// 三次投递(首投/同号重投/同键重放)只允许一个 contact(不得绕过去重多建)。
	var contacts int
	if err := d.QueryRow(`SELECT COUNT(1) FROM contacts WHERE tenant_id='tnt_1'`).Scan(&contacts); err != nil {
		t.Fatal(err)
	}
	if contacts != 1 {
		t.Fatalf("contacts for the invalid number = %d, want 1", contacts)
	}
	// 两条 lead 都是 filtered(每次真实投递各自留痕),但 contact 只有一条。
	var filteredLeads int
	if err := d.QueryRow(`SELECT COUNT(1) FROM leads WHERE contact_id=? AND status='filtered' AND filter_reason='invalid_phone'`, first.ContactID).Scan(&filteredLeads); err != nil || filteredLeads != 2 {
		t.Fatalf("filtered leads = %d err=%v, want 2 (两次投递各自台账留痕)", filteredLeads, err)
	}
}

// 过滤与 consent 正交:filtered 线索的授权记录照常按普通规则落库。
func TestIntakeFilterOnConsentOrthogonal(t *testing.T) {
	d := intakeTestDB(t)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	in := intakeFilteredIn("e1", "12345678901")
	in.Consent = &ConsentUpsert{
		SourceSubmissionRef: "sub-1", SourceChannel: "wx", NoticeVersion: "v1",
		Purpose: "marketing", MarketingAllowed: true,
	}
	res, err := IntakeLeadInTx(tx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if res.FilterReason != FilterReasonInvalidPhone {
		t.Fatalf("reason = %q, want invalid_phone", res.FilterReason)
	}
	var consents int
	if err := d.QueryRow(`SELECT COUNT(1) FROM contact_consents WHERE contact_id=?`, res.ContactID).Scan(&consents); err != nil || consents != 1 {
		t.Fatalf("consent rows = %d err=%v, want 1 (过滤与 consent 正交)", consents, err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM leads WHERE id=?`, res.LeadID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != LeadStatusFiltered {
		t.Fatalf("lead status = %q, want filtered", status)
	}
}

// 公共表单通道同样走服务端单点判定:FilterEnabled 经 SubmitFormInTx 传入。
func TestSubmitFormFilterOn(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")

	res := mustSubmit(t, d, f.ID, SubmitFormInput{
		FormID:  f.ID,
		Payload: submitPayload("王五", "12345678901", "wx_post", "ref-1", true),
		Content: []byte(`{"name":"王五"}`), Pepper: "pepper",
		ResubmitWindow: 10 * time.Minute, FilterEnabled: true,
	})
	if res.FilterReason != FilterReasonInvalidPhone {
		t.Fatalf("form filter reason = %q, want invalid_phone", res.FilterReason)
	}
	var status, reason string
	if err := d.QueryRow(`SELECT status,filter_reason FROM leads WHERE id=?`, res.LeadID).
		Scan(&status, &reason); err != nil {
		t.Fatalf("lead row: %v", err)
	}
	if status != LeadStatusFiltered || reason != FilterReasonInvalidPhone {
		t.Fatalf("form lead = %q/%q, want filtered/invalid_phone", status, reason)
	}
}

func intakeFilteredIn(event, phone string) IntakeInput {
	in := intakeIn("touch", "wx", event, phone)
	in.FilterEnabled = true
	return in
}
