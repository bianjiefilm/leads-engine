// HUI-1683 / FEAT-0184 intake 领域函数单测:归一化、指纹、幂等键纪律与
// 同键异内容冲突 —— 直接驱动 IntakeLeadInTx(真实 sqlite,事务由测试持有)。
package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/db"
)

func intakeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return d
}

func intakeIn(app, ns, event, phone string) IntakeInput {
	return IntakeInput{
		TenantID: "tnt_1", SourceApp: app, SourceNS: ns, EventID: event,
		Content: []byte("payload-" + event), ContactName: "某商家", Phone: phone,
		Pepper: "unit-pepper",
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"13900000001", "13900000001"},
		{"139 0000 0001", "13900000001"},
		{"139-0000-0001", "13900000001"},
		{"+8613900000001", "13900000001"},
		{"86 139 0000 0001", "13900000001"},
		{"（139）0000-0001", "13900000001"},
	}
	for _, tc := range cases {
		if got := NormalizePhone(tc.in); got != tc.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPhoneFingerprint(t *testing.T) {
	a := PhoneFingerprint("pepper-x", "139 0000 0001")
	if a == "" || len(a) != 64 {
		t.Fatalf("fingerprint shape wrong: %q", a)
	}
	// 归一化后等价的号码同指纹。
	if b := PhoneFingerprint("pepper-x", "+8613900000001"); b != a {
		t.Fatalf("equivalent numbers must share the fingerprint: %q vs %q", a, b)
	}
	// 换 pepper 必换指纹;空手机号无指纹。
	if c := PhoneFingerprint("pepper-y", "13900000001"); c == a {
		t.Fatal("different peppers must produce different fingerprints")
	}
	if d := PhoneFingerprint("pepper-x", ""); d != "" {
		t.Fatalf("empty phone must have no fingerprint, got %q", d)
	}
}

func TestIntakeLeadInTxClassifyAndConflict(t *testing.T) {
	d := intakeTestDB(t)

	// 零命中 → new;同键同内容 → exact_duplicate 引用原行;同键异内容 → 冲突。
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, err := IntakeLeadInTx(tx, intakeIn("touch", "wx", "e1", "13900000001"))
	if err != nil {
		t.Fatalf("first intake: %v", err)
	}
	if first.Class != IntakeClassNew || first.Duplicate {
		t.Fatalf("first = %+v, want new", first)
	}
	replay, err := IntakeLeadInTx(tx, intakeIn("touch", "wx", "e1", "13900000001"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Class != IntakeClassExactDuplicate || !replay.Duplicate ||
		replay.LeadID != first.LeadID || replay.ContactID != first.ContactID {
		t.Fatalf("replay = %+v, want original reference", replay)
	}
	if _, err := IntakeLeadInTx(tx, IntakeInput{
		TenantID: "tnt_1", SourceApp: "touch", SourceNS: "wx", EventID: "e1",
		Content: []byte("TAMPERED"), ContactName: "某商家", Pepper: "unit-pepper",
	}); !errors.Is(err, ErrEventContentConflict) {
		t.Fatalf("conflicting content err = %v, want ErrEventContentConflict", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM lead_intake_events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ledger rows = %d err=%v, want 1 (重放与冲突零写入)", n, err)
	}
	var fpr string
	if err := d.QueryRow(`SELECT phone_fpr FROM lead_intake_events`).Scan(&fpr); err != nil || len(fpr) != 64 {
		t.Fatalf("stored fingerprint = %q err=%v, want 64-hex (索引不落明文)", fpr, err)
	}
	if strings.Contains(fpr, "13900000001") {
		t.Fatal("fingerprint must not contain the raw phone")
	}
}

func TestIntakeLeadInTxGuards(t *testing.T) {
	d := intakeTestDB(t)

	// 非法幂等键字符集。
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IntakeLeadInTx(tx, intakeIn("touch", "wx oa", "e1", "")); err == nil {
		t.Fatal("key charset violation must be rejected")
	}
	// 缺租户/缺名字。
	if _, err := IntakeLeadInTx(tx, IntakeInput{SourceApp: "a", SourceNS: "n", EventID: "e", ContactName: "x"}); err == nil {
		t.Fatal("missing tenant must be rejected")
	}
	noName := intakeIn("touch", "wx", "e2", "1")
	noName.ContactName = "   "
	if _, err := IntakeLeadInTx(tx, noName); err == nil {
		t.Fatal("missing contact name must be rejected")
	}
	// 事务回滚:什么都没留下。
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM lead_intake_events`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ledger rows after rollback = %d err=%v, want 0", n, err)
	}

	// ambiguous:两个既有 contact 同号 → 新独立 contact + 候选行,不静默合并。
	st := New(d)
	c1, err := st.CreateContact(Contact{TenantID: "tnt_1", Name: "甲", Phone: "13700001111",
		BusinessCategory: "merchant_customer", SourceType: "manual", ConsentStatus: "pending"}, "cli", "")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := st.CreateContact(Contact{TenantID: "tnt_1", Name: "乙", Phone: "137-0000-1111",
		BusinessCategory: "merchant_customer", SourceType: "manual", ConsentStatus: "pending"}, "cli", "")
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err := IntakeLeadInTx(tx2, intakeIn("touch", "wx", "e3", "13700001111"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Class != IntakeClassAmbiguous {
		t.Fatalf("class = %q, want ambiguous", res.Class)
	}
	if res.ContactID == c1.ID || res.ContactID == c2.ID {
		t.Fatal("ambiguous must create an independent contact")
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	var cands int
	if err := d.QueryRow(`SELECT COUNT(1) FROM merge_candidates WHERE status='pending' AND reason='shared_phone'`).Scan(&cands); err != nil || cands != 3 {
		t.Fatalf("pending candidates = %d err=%v, want 3 (两两成对)", cands, err)
	}
	var merged int
	if err := d.QueryRow(
		`SELECT COUNT(1) FROM contacts WHERE deleted_at IS NULL AND id IN (?, ?, ?)`,
		c1.ID, c2.ID, res.ContactID).Scan(&merged); err != nil || merged != 3 {
		t.Fatalf("live contacts in the ambiguous set = %d err=%v, want 3 (绝不静默合并)", merged, err)
	}
}
