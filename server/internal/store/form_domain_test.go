// HUI-1679 / FEAT-0180 表单域存储层单测:fields 白名单、版本递增、发布不可变、
// 公共提交的幂等/短窗/intake 三分类 —— 直接驱动真实 sqlite(事务由测试持有)。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/db"
)

func formTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','A','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_2','B','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant2: %v", err)
	}
	return d
}

func publishableForm(t *testing.T, s *Store, tenantID string) Form {
	t.Helper()
	f, err := s.CreateFormDraft(tenantID, "mem_owner", FormDraftInput{
		NoticeVersion: strPtr("v1"),
		Purpose:       strPtr("collection"),
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	f, err = s.PublishForm(f.ID, tenantID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return f
}

func strPtr(v string) *string { return &v }

func submitPayload(name, phone, source, ref string, marketing bool) SubmissionPayload {
	return SubmissionPayload{Name: name, Phone: phone, Source: source, SourceRef: ref, MarketingAllowed: marketing}
}

func mustSubmit(t *testing.T, d *sql.DB, formID string, in SubmitFormInput) SubmitFormResult {
	t.Helper()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := SubmitFormInTx(tx, in)
	if err != nil {
		t.Fatalf("SubmitFormInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_ = formID
	return res
}

// ---- fields JSON 白名单 -------------------------------------------------------

func TestValidateFormFieldsJSON(t *testing.T) {
	d := formTestDB(t)
	s := New(d)

	t.Run("default becomes name+phone required", func(t *testing.T) {
		def, canonical, err := ValidateFormFieldsJSON("")
		if err != nil {
			t.Fatalf("empty fields: %v", err)
		}
		if len(def.Fields) != 2 || !def.Fields[0].Required || !def.Fields[1].Required {
			t.Fatalf("default fields wrong: %+v", def)
		}
		if def.SchemaVersion != 1 {
			t.Fatalf("schema_version = %d, want 1", def.SchemaVersion)
		}
		if !json.Valid([]byte(canonical)) {
			t.Fatalf("canonical not valid JSON: %s", canonical)
		}
	})

	t.Run("foreign field names rejected (无自由字段)", func(t *testing.T) {
		if _, _, err := ValidateFormFieldsJSON(`{"schema_version":1,"fields":[{"name":"address","required":true}]}`); err == nil {
			t.Fatal("address must be rejected")
		}
	})

	t.Run("duplicate names rejected", func(t *testing.T) {
		if _, _, err := ValidateFormFieldsJSON(`{"schema_version":1,"fields":[{"name":"phone"},{"name":"phone"}]}`); err == nil {
			t.Fatal("duplicate phone must be rejected")
		}
	})

	t.Run("name and phone are forced required (首版拍板)", func(t *testing.T) {
		def, _, err := ValidateFormFieldsJSON(`{"schema_version":1,"fields":[{"name":"name","required":false},{"name":"phone","required":false},{"name":"wechat"}]}`)
		if err != nil {
			t.Fatalf("valid config rejected: %v", err)
		}
		for _, f := range def.Fields {
			if (f.Name == FormFieldName || f.Name == FormFieldPhone) && !f.Required {
				t.Fatalf("%s must be forced required", f.Name)
			}
		}
	})

	t.Run("unknown schema version rejected", func(t *testing.T) {
		if _, _, err := ValidateFormFieldsJSON(`{"schema_version":2,"fields":[{"name":"name"},{"name":"phone"}]}`); err == nil {
			t.Fatal("schema_version 2 must be rejected")
		}
	})

	_ = s
}

// ---- 草稿 / 发布 / 版本 -------------------------------------------------------

func TestFormDraftPublishVersionLifecycle(t *testing.T) {
	d := formTestDB(t)
	s := New(d)

	v1, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{FormKey: strPtr("main")})
	if err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if v1.Status != FormStatusDraft || v1.Version != 1 {
		t.Fatalf("v1 wrong: %+v", v1)
	}

	// 发布校验:notice_version/purpose 非空。
	if _, err := s.PublishForm(v1.ID, "tnt_1"); !errors.Is(err, ErrFormIncomplete) {
		t.Fatalf("publish without notice/purpose must fail incomplete, got %v", err)
	}
	if _, err := s.UpdateFormDraft(v1.ID, "tnt_1", FormDraftInput{
		NoticeVersion: strPtr("v1"), Purpose: strPtr("collection"),
	}); err != nil {
		t.Fatalf("patch draft: %v", err)
	}
	pub, err := s.PublishForm(v1.ID, "tnt_1")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.Status != FormStatusPublished || pub.PublishedAt == "" {
		t.Fatalf("published form wrong: %+v", pub)
	}

	// 发布后不可变:改配置必须走新版本行。
	if _, err := s.UpdateFormDraft(v1.ID, "tnt_1", FormDraftInput{Purpose: strPtr("hacked")}); !errors.Is(err, ErrFormNotDraft) {
		t.Fatalf("published form must not be editable, got %v", err)
	}
	v2, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{
		FormKey: strPtr("main"), NoticeVersion: strPtr("v2"), Purpose: strPtr("collection"),
	})
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("version must increment within the family, got %d", v2.Version)
	}

	// 同租户不同 form_key 版本独立;跨租户不可见。
	other, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{FormKey: strPtr("qr")})
	if err != nil {
		t.Fatalf("create qr: %v", err)
	}
	if other.Version != 1 {
		t.Fatalf("separate family must start at 1, got %d", other.Version)
	}
	if _, err := s.GetForm(v1.ID, "tnt_2"); !errors.Is(err, ErrFormNotFound) {
		t.Fatalf("cross-tenant form read must be 404-equivalent, got %v", err)
	}

	// 停用 + 重复停用幂等。
	dis, err := s.DisableForm(v1.ID, "tnt_1")
	if err != nil || dis.Status != FormStatusDisabled {
		t.Fatalf("disable: %v %+v", err, dis)
	}
	if _, err := s.DisableForm(v1.ID, "tnt_1"); err != nil {
		t.Fatalf("disable must be idempotent: %v", err)
	}

	// form_key 字符纪律(intake 幂等键要求 ASCII 标识符)。
	if _, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{FormKey: strPtr("bad key!")}); err == nil {
		t.Fatal("form_key with spaces must be rejected")
	}
}

func TestFormExpiryFunctional(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	f, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{
		NoticeVersion: strPtr("v1"), Purpose: strPtr("c"), ExpiresAt: strPtr(past),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.PublishForm(f.ID, "tnt_1"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, err := s.GetFormAnyTenant(f.ID)
	if err != nil {
		t.Fatalf("GetFormAnyTenant: %v", err)
	}
	if !FormExpiredAt(got, time.Now()) {
		t.Fatal("past expires_at must read as functionally expired")
	}
}

// ---- 公共提交 -----------------------------------------------------------------

func TestSubmitFormHappyPathAtomic(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")

	res := mustSubmit(t, d, f.ID, SubmitFormInput{
		FormID: f.ID, Payload: submitPayload("张三", "139 0000 0001", "wx_post", "camp-1", true),
		Content: []byte(`{"name":"张三"}`), Pepper: "pepper",
		ResubmitWindow: 10 * time.Minute,
	})
	if res.Class != IntakeClassNew || res.Duplicate {
		t.Fatalf("first submit class=%s dup=%v", res.Class, res.Duplicate)
	}
	// contact + lead + consent + submission 全部落库且互相指向一致。
	var contactTenant, contactPhone string
	if err := d.QueryRow(`SELECT tenant_id,phone FROM contacts WHERE id=?`, res.ContactID).
		Scan(&contactTenant, &contactPhone); err != nil {
		t.Fatalf("contact missing: %v", err)
	}
	if contactTenant != "tnt_1" || contactPhone != "139 0000 0001" {
		t.Fatalf("contact wrong: tenant=%s phone=%q", contactTenant, contactPhone)
	}
	var leadTenant, leadContact string
	if err := d.QueryRow(`SELECT tenant_id,contact_id FROM leads WHERE id=?`, res.LeadID).
		Scan(&leadTenant, &leadContact); err != nil {
		t.Fatalf("lead missing: %v", err)
	}
	if leadTenant != "tnt_1" || leadContact != res.ContactID {
		t.Fatalf("lead wrong: %+v", res)
	}
	var marketing int
	var notice, purpose, channel, subRef, consentID string
	if err := d.QueryRow(
		`SELECT marketing_allowed,notice_version,purpose,source_channel,source_submission_ref,id FROM contact_consents WHERE id=?`,
		res.ConsentID).Scan(&marketing, &notice, &purpose, &channel, &subRef, &consentID); err != nil {
		t.Fatalf("consent missing: %v", err)
	}
	if marketing != 1 || notice != "v1" || purpose != "collection" || channel != "public_form" || subRef != res.SubmissionID {
		t.Fatalf("consent wrong: mk=%d notice=%q purpose=%q channel=%s subRef=%s",
			marketing, notice, purpose, channel, subRef)
	}
	var subMarketing int
	var subForm, subTenant, subSource, subRefCol, sha string
	if err := d.QueryRow(
		`SELECT form_id,tenant_id,marketing_allowed,source,source_ref,payload_sha256 FROM form_submissions WHERE id=?`,
		res.SubmissionID).Scan(&subForm, &subTenant, &subMarketing, &subSource, &subRefCol, &sha); err != nil {
		t.Fatalf("submission missing: %v", err)
	}
	if subForm != f.ID || subTenant != "tnt_1" || subMarketing != 1 || subSource != "wx_post" || subRefCol != "camp-1" || sha == "" {
		t.Fatalf("submission row wrong: form=%s tenant=%s mk=%d", subForm, subTenant, subMarketing)
	}
	// 来源留痕(source_refs)已建且 lead 引用。
	var srcCount int
	if err := d.QueryRow(`SELECT COUNT(1) FROM source_refs WHERE tenant_id='tnt_1' AND source_app='wx_post' AND source_ref='camp-1'`).Scan(&srcCount); err != nil || srcCount != 1 {
		t.Fatalf("source_refs row missing: %v %d", err, srcCount)
	}
}

func TestSubmitFormIdempotentReplay(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")
	in := SubmitFormInput{
		FormID: f.ID, Payload: submitPayload("李四", "13900000002", "wx_post", "ref-1", true),
		Content: []byte("a"), Pepper: "pepper", ResubmitWindow: 10 * time.Minute,
	}
	first := mustSubmit(t, d, f.ID, in)

	// 同键重试:即使内容不同也返回原 submission(表单幂等优先),零写入。
	retry := in
	retry.Payload.Name = "李四改"
	retry.Content = []byte("b")
	second := mustSubmit(t, d, f.ID, retry)
	if !second.Duplicate || second.DuplicateKind != "idempotent" {
		t.Fatalf("replay must be idempotent, got %+v", second)
	}
	if second.SubmissionID != first.SubmissionID || second.ContactID != first.ContactID || second.LeadID != first.LeadID {
		t.Fatalf("replay must return the original references: %+v vs %+v", first, second)
	}
	var leads, contacts, subs, consents int
	_ = d.QueryRow(`SELECT COUNT(1) FROM leads`).Scan(&leads)
	_ = d.QueryRow(`SELECT COUNT(1) FROM contacts`).Scan(&contacts)
	_ = d.QueryRow(`SELECT COUNT(1) FROM form_submissions`).Scan(&subs)
	_ = d.QueryRow(`SELECT COUNT(1) FROM contact_consents`).Scan(&consents)
	if leads != 1 || contacts != 1 || subs != 1 || consents != 1 {
		t.Fatalf("replay must write nothing: leads=%d contacts=%d subs=%d consents=%d", leads, contacts, subs, consents)
	}
}

func TestSubmitFormRecentWindowSuppression(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")
	base := SubmitFormInput{
		FormID: f.ID, Payload: submitPayload("王五", "13900000003", "wx_post", "ref-a", false),
		Content: []byte("a"), Pepper: "pepper", ResubmitWindow: 10 * time.Minute,
	}
	first := mustSubmit(t, d, f.ID, base)

	// 同 contact+form 短窗内换 source_ref 再交:幂等提示,不新建。
	again := base
	again.Payload.SourceRef = "ref-b"
	second := mustSubmit(t, d, f.ID, again)
	if !second.Duplicate || second.DuplicateKind != "recent_window" {
		t.Fatalf("window must suppress, got %+v", second)
	}
	if second.SubmissionID != first.SubmissionID || second.LeadID != first.LeadID {
		t.Fatalf("window must answer the original submission")
	}
	// 未勾选营销:consent marketing_allowed=0(没有同意不进营销池)。
	var mk int
	if err := d.QueryRow(`SELECT marketing_allowed FROM contact_consents WHERE id=?`, first.ConsentID).Scan(&mk); err != nil || mk != 0 {
		t.Fatalf("unchecked marketing must store 0, got %v %d", err, mk)
	}
}

func TestSubmitFormRepeatConsultOutsideWindow(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")
	base := SubmitFormInput{
		FormID: f.ID, Payload: submitPayload("赵六", "13900000004", "wx_post", "r1", true),
		Content: []byte("a"), Pepper: "pepper", ResubmitWindow: 0, // 窗外
	}
	first := mustSubmit(t, d, f.ID, base)
	secondIn := base
	secondIn.Payload.SourceRef = "r2"
	second := mustSubmit(t, d, f.ID, secondIn)
	if second.Duplicate {
		t.Fatalf("outside the window a genuine re-consult proceeds: %+v", second)
	}
	if second.Class != IntakeClassRepeatConsult {
		t.Fatalf("class = %s, want repeat_consult", second.Class)
	}
	if second.ContactID != first.ContactID {
		t.Fatalf("repeat consult must attach to the existing contact")
	}
	if second.LeadID == first.LeadID {
		t.Fatal("repeat consult must create a NEW lead (咨询次数不丢)")
	}
	// 新提交 = 新 consent 键(新 source_submission_ref)。
	if second.ConsentID == "" || second.ConsentID == first.ConsentID {
		t.Fatalf("each submission owns its consent row: %s vs %s", first.ConsentID, second.ConsentID)
	}
	var leads int
	_ = d.QueryRow(`SELECT COUNT(1) FROM leads`).Scan(&leads)
	if leads != 2 {
		t.Fatalf("leads = %d, want 2", leads)
	}
}

func TestSubmitFormStateGating(t *testing.T) {
	d := formTestDB(t)
	s := New(d)

	draft, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{
		NoticeVersion: strPtr("v1"), Purpose: strPtr("c"),
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	run := func(formID string) error {
		tx, err := d.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		_, err = SubmitFormInTx(tx, SubmitFormInput{
			FormID: formID, Payload: submitPayload("甲", "13900000005", "s", "r", false),
			Content: []byte("x"), Pepper: "p", ResubmitWindow: time.Minute,
		})
		return err
	}
	// 草稿:公共不可见(与不存在同答案)。
	if err := run(draft.ID); !errors.Is(err, ErrFormNotFound) {
		t.Fatalf("draft must answer not-found, got %v", err)
	}
	pub, err := s.PublishForm(draft.ID, "tnt_1")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := run(pub.ID); err != nil {
		t.Fatalf("published submit: %v", err)
	}
	if _, err := s.DisableForm(pub.ID, "tnt_1"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := run(pub.ID); !errors.Is(err, ErrFormDisabled) {
		t.Fatalf("disabled must be explicit, got %v", err)
	}

	// 过期(past expires_at)→ ErrFormExpired。
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	expF, err := s.CreateFormDraft("tnt_1", "mem_owner", FormDraftInput{
		NoticeVersion: strPtr("v1"), Purpose: strPtr("c"), ExpiresAt: strPtr(past),
	})
	if err != nil {
		t.Fatalf("exp draft: %v", err)
	}
	expPub, err := s.PublishForm(expF.ID, "tnt_1")
	if err != nil {
		t.Fatalf("exp publish: %v", err)
	}
	if err := run(expPub.ID); !errors.Is(err, ErrFormExpired) {
		t.Fatalf("expired must be explicit, got %v", err)
	}
}

func TestSubmitFormReceivingTenantIsTheFormOwner(t *testing.T) {
	d := formTestDB(t)
	s := New(d)
	f := publishableForm(t, s, "tnt_1")
	// intake 查重空间严格限定接收租户:同号码在 tnt_2 不影响 tnt_1 分类。
	tx, _ := d.Begin()
	if _, err := IntakeLeadInTx(tx, IntakeInput{
		TenantID: "tnt_2", SourceApp: "x", SourceNS: "y", EventID: "e",
		Content: []byte("z"), ContactName: "乙", Phone: "13900000006", Pepper: "p",
	}); err != nil {
		t.Fatalf("tenant2 seed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res := mustSubmit(t, d, f.ID, SubmitFormInput{
		FormID: f.ID, Payload: submitPayload("丙", "13900000006", "s", "r", false),
		Content: []byte("x"), Pepper: "p", ResubmitWindow: time.Minute,
	})
	var tenant string
	if err := d.QueryRow(`SELECT tenant_id FROM contacts WHERE id=?`, res.ContactID).Scan(&tenant); err != nil || tenant != "tnt_1" {
		t.Fatalf("receiving tenant must be the form owner: %v %s", err, tenant)
	}
	if res.Class != IntakeClassNew {
		t.Fatalf("cross-tenant same phone must not classify within tnt_1, got %s", res.Class)
	}
}

func TestValidateSubmissionPayloadItemized(t *testing.T) {
	_, fieldsJSON, err := ValidateFormFieldsJSON(`{"schema_version":1,"fields":[{"name":"name"},{"name":"phone"},{"name":"wechat"}],"consent_required":true}`)
	if err != nil {
		t.Fatalf("fields: %v", err)
	}
	_, fieldsWechatRequired, err := ValidateFormFieldsJSON(`{"schema_version":1,"fields":[{"name":"name"},{"name":"phone"},{"name":"wechat","required":true}]}`)
	if err != nil {
		t.Fatalf("wechat-required fields: %v", err)
	}
	_, fieldsNoWechat, err := ValidateFormFieldsJSON(`{"schema_version":1,"fields":[{"name":"name"},{"name":"phone"}]}`)
	if err != nil {
		t.Fatalf("no-wechat fields: %v", err)
	}

	cases := []struct {
		label   string
		fields  string
		payload SubmissionPayload
		want    map[string]bool // fields expected in details
	}{
		{"all empty", fieldsJSON, SubmissionPayload{}, map[string]bool{"name": true, "phone": true, "source": true, "source_ref": true}},
		{"bad phone", fieldsJSON, submitPayload("甲", "12345", "s", "r", false), map[string]bool{"phone": true}},
		{"long name", fieldsJSON, SubmissionPayload{Name: strings.Repeat("名", 101), Phone: "13900000007", Source: "s", SourceRef: "r"}, map[string]bool{"name": true}},
		{"required wechat empty", fieldsWechatRequired, SubmissionPayload{Name: "甲", Phone: "13900000007", Source: "s", SourceRef: "r"}, map[string]bool{"wechat": true}},
		{"wechat not collected", fieldsNoWechat, SubmissionPayload{Name: "甲", Phone: "13900000007", Wechat: "wx_ok", Source: "s", SourceRef: "r"}, map[string]bool{"wechat": true}},
		{"bad source", fieldsJSON, submitPayload("甲", "13900000007", "bad source!", "r", false), map[string]bool{"source": true}},
	}
	for _, tc := range cases {
		details := ValidateSubmissionPayload(tc.fields, tc.payload)
		got := map[string]bool{}
		for _, d := range details {
			got[d.Field] = true
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v want %v", tc.label, details, tc.want)
			continue
		}
		for f := range tc.want {
			if !got[f] {
				t.Errorf("%s: missing field %s in %v", tc.label, f, details)
			}
		}
	}
	// 合法载荷零报错(含可选项 wechat)。
	if details := ValidateSubmissionPayload(fieldsJSON, SubmissionPayload{
		Name: "甲", Phone: "13900000007", Wechat: "wx_ok", Source: "s", SourceRef: "r",
	}); len(details) != 0 {
		t.Fatalf("valid payload must pass, got %v", details)
	}
}
