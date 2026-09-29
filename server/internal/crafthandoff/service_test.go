package crafthandoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSaveDraftKeepsOnlySelectedAuthorizedFacts(t *testing.T) {
	svc := New()
	got, err := svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a",
		LeadID:   "lead-1",
		Version:  3,
		Facts: []Fact{
			{Key: "scene", Value: "想看门店方案", Selected: true, Authorized: true},
			{Key: "question", Value: "在问价", Selected: true, Authorized: true},
			{Key: "offer", Value: "工作日到店", Selected: true, Authorized: true},
			{Key: "next_step", Value: "先约周四", Selected: true, Authorized: true},
			{Key: "phone", Value: "13800000000", Selected: false, Authorized: true},
			{Key: "note", Value: "旁白不进草稿", Selected: false, Authorized: true},
			{Key: "portrait", Value: "face-secret", Selected: true, Authorized: true},
		},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	const wantBody = "想看门店方案\n在问价\n工作日到店\n先约周四"
	if got.Body != wantBody {
		t.Fatalf("body = %q", got.Body)
	}
	if strings.Contains(got.Body, "13800000000") || strings.Contains(got.Body, "旁白不进草稿") || strings.Contains(got.Body, "face-secret") {
		t.Fatalf("draft leaked unselected or portrait text: %q", got.Body)
	}
	if len(got.Facts) != 4 {
		t.Fatalf("facts = %+v", got.Facts)
	}
	wantKeys := []string{"scene", "question", "offer", "next_step"}
	for i, key := range wantKeys {
		if got.Facts[i].Key != key || got.Facts[i].Value == "" || !got.Facts[i].Selected || !got.Facts[i].Authorized {
			t.Fatalf("fact %d = %+v", i, got.Facts[i])
		}
	}
	if svc.Projects() != 0 || svc.ReplyBotsStarted() != 0 {
		t.Fatalf("save started work projects=%d bots=%d", svc.Projects(), svc.ReplyBotsStarted())
	}
}

func TestSaveDraftRejectsUnauthorizedForbiddenAndPortrait(t *testing.T) {
	svc := New()
	kept, err := svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 1,
		Facts: []Fact{{Key: "scene", Value: "先留下的草稿", Selected: true, Authorized: true}},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	cases := []struct {
		name string
		fact Fact
		want error
	}{
		{name: "unselected only", fact: Fact{Key: "scene", Value: "没选", Selected: false, Authorized: true}, want: ErrNothingSelected},
		{name: "not authorized", fact: Fact{Key: "scene", Value: "未授权", Selected: true, Authorized: false}, want: ErrUnauthorizedFact},
		{name: "unknown key", fact: Fact{Key: "crm_dump", Value: "整份画像", Selected: true, Authorized: true}, want: ErrUnauthorizedFact},
		{name: "phone", fact: Fact{Key: "phone", Value: "13800000000", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "email", fact: Fact{Key: "email", Value: "a@b.c", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "profile", fact: Fact{Key: "crm_profile", Value: "全量画像", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "profile alias", fact: Fact{Key: "profile", Value: "全量画像", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "sales note", fact: Fact{Key: "sales_note", Value: "内部：先压价", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "internal note", fact: Fact{Key: "internal_note", Value: "内部：先压价", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "sales note long", fact: Fact{Key: "internal_sales_note", Value: "内部：先压价", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "phone in scene", fact: Fact{Key: "scene", Value: "电话13800000000", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "email in offer", fact: Fact{Key: "offer", Value: "写信 a@b.c", Selected: true, Authorized: true}, want: ErrForbiddenFact},
		{name: "portrait", fact: Fact{Key: "portrait", Value: "face-secret", Selected: true, Authorized: false}, want: ErrUnauthorizedPortrait},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := []Fact{
				{Key: "scene", Value: "这句不该替换旧草稿", Selected: true, Authorized: true},
				tc.fact,
			}
			if tc.name == "unselected only" || tc.name == "not authorized" || tc.name == "unknown key" || tc.name == "phone in scene" {
				facts = []Fact{tc.fact}
			}
			_, err := svc.SaveDraft(SaveRequest{TenantID: "tenant-a", LeadID: "lead-1", Version: 2, Facts: facts})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			got, getErr := svc.Draft("tenant-a", "lead-1")
			if getErr != nil || got.Body != kept.Body {
				t.Fatalf("draft changed: %v %+v", getErr, got)
			}
		})
	}
	_, err = svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a", LeadID: "lead-2", Version: 1,
		Facts: []Fact{{Key: "portrait", Value: "face-secret", Selected: true, Authorized: true}},
	})
	if !errors.Is(err, ErrNothingSelected) {
		t.Fatalf("portrait-only err = %v", err)
	}
	if _, err := svc.Draft("tenant-a", "lead-2"); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("portrait-only stored a draft: %v", err)
	}
}

func TestSaveDraftIsNotSentAndDoesNotStartReplyBot(t *testing.T) {
	svc := New()
	got, err := svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 1,
		Facts: []Fact{{Key: "scene", Value: "只是草稿", Selected: true, Authorized: true}},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got.Sent || got.Published || got.MarketingLicense || got.ReplyBotStarted {
		t.Fatalf("save marked the draft done: %+v", got)
	}
	if got.ModelStamp != "真实模型未完成" {
		t.Fatalf("stamp = %q", got.ModelStamp)
	}
	if svc.ReplyBotsStarted() != 0 || svc.ProviderCalls() != 0 || svc.ChargeWrites() != 0 || svc.Projects() != 0 {
		t.Fatalf("save started side effects bots=%d calls=%d charges=%d projects=%d",
			svc.ReplyBotsStarted(), svc.ProviderCalls(), svc.ChargeWrites(), svc.Projects())
	}
	dump := fmt.Sprintf("%+v", got)
	for _, bad := range []string{"已发送", "已发布", "自动触达已成功", "白标经营链完成"} {
		if strings.Contains(dump, bad) {
			t.Fatalf("draft claims %s: %s", bad, dump)
		}
	}
}

func TestPayloadCarriesLeadVersionAndPayerOnly(t *testing.T) {
	svc := New()
	got, err := svc.Handoff(Input{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 3, Target: "goboost", PayerRef: "payer-a",
		Portrait: "face-secret", PortraitAuthorized: true,
	})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	raw, err := json.Marshal(got.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var payload struct {
		SourceLeadID string `json:"source_lead_id"`
		Version      int    `json:"version"`
		PayerRef     string `json:"payer_ref"`
	}
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("payload = %s (%v)", raw, err)
	}
	if payload.SourceLeadID != "lead-1" || payload.Version != 3 || payload.PayerRef != "payer-a" {
		t.Fatalf("payload = %+v", payload)
	}
	if strings.Contains(string(raw), "face-secret") {
		t.Fatalf("portrait entered payload: %s", raw)
	}
}

func TestForbiddenMaterialNeverEntersPayload(t *testing.T) {
	cases := []Input{
		{Phone: "13800000000"},
		{Email: "a@b.c"},
		{CRMProfile: "全量画像"},
		{SalesNotes: "内部：先压价"},
		{Portrait: "face-secret", PortraitAuthorized: false},
		{PayerRef: "13800000000"},
		{PayerRef: "a@b.c"},
	}
	for _, extra := range cases {
		svc := New()
		in := Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "avatar", PayerRef: "payer-a"}
		if extra.Phone != "" {
			in.Phone = extra.Phone
		}
		if extra.Email != "" {
			in.Email = extra.Email
		}
		if extra.CRMProfile != "" {
			in.CRMProfile = extra.CRMProfile
		}
		if extra.SalesNotes != "" {
			in.SalesNotes = extra.SalesNotes
		}
		if extra.Portrait != "" {
			in.Portrait = extra.Portrait
			in.PortraitAuthorized = extra.PortraitAuthorized
		}
		if extra.PayerRef != "" {
			in.PayerRef = extra.PayerRef
		}
		want := ErrForbiddenFact
		if extra.Portrait != "" && !extra.PortraitAuthorized {
			want = ErrUnauthorizedPortrait
		}
		if _, err := svc.Handoff(in); !errors.Is(err, want) {
			t.Fatalf("input %+v err = %v, want %v", extra, err, want)
		}
		if svc.Projects() != 0 || svc.ChargeWrites() != 0 || svc.ProviderCalls() != 0 {
			t.Fatalf("rejected handoff was stored: projects=%d", svc.Projects())
		}
	}
}

func TestSameTupleReturnsSameHandoff(t *testing.T) {
	svc := New()
	first, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 4, Target: "aicut", PayerRef: "payer-a"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 4, Target: "aicut", PayerRef: "payer-a"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID == "" || first.ID != second.ID || first.FixtureRef == "" || first.FixtureRef != second.FixtureRef {
		t.Fatalf("identity changed: %+v %+v", first, second)
	}
	otherPayer, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 4, Target: "aicut", PayerRef: "payer-b"})
	if err != nil {
		t.Fatalf("other payer: %v", err)
	}
	if otherPayer.ID != first.ID || otherPayer.Payload.PayerRef != "payer-a" {
		t.Fatalf("payer fork = %+v", otherPayer)
	}
	otherVersion, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 5, Target: "aicut", PayerRef: "payer-a"})
	if err != nil {
		t.Fatalf("other version: %v", err)
	}
	if otherVersion.ID == first.ID {
		t.Fatalf("version reused %s", otherVersion.ID)
	}
	if svc.Projects() != 2 {
		t.Fatalf("projects = %d", svc.Projects())
	}
}

func TestRepeatClickDoesNotCreateSecondProjectOrCharge(t *testing.T) {
	svc := New()
	in := Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "product-image", PayerRef: "payer-a"}
	first, err := svc.Handoff(in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := 0; i < len(ids); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			again := in
			again.TargetFailed = true
			got, err := svc.Handoff(again)
			if err != nil {
				ids[i] = err.Error()
				return
			}
			ids[i] = got.ID
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != first.ID {
			t.Fatalf("repeat id = %q, want %s", id, first.ID)
		}
	}
	if svc.Projects() != 1 || svc.ChargeWrites() != 0 || svc.ProviderCalls() != 0 {
		t.Fatalf("projects=%d charges=%d calls=%d", svc.Projects(), svc.ChargeWrites(), svc.ProviderCalls())
	}
	if first.ProjectCount != 1 || first.ChargeWrites != 0 || first.ProviderCalls != 0 || first.BillingPassed {
		t.Fatalf("first result counted a charge: %+v", first)
	}
}

func TestDifferentTenantIsRejected(t *testing.T) {
	svc := New()
	saved, err := svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 1,
		Facts: []Fact{{Key: "scene", Value: "甲的草稿", Selected: true, Authorized: true}},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "goboost", PayerRef: "payer-a"}); err != nil {
		t.Fatalf("owner handoff: %v", err)
	}
	if _, err := svc.Draft("tenant-b", "lead-1"); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("read err = %v", err)
	}
	if _, err := svc.EditByHand("tenant-b", "lead-1", "乙改掉"); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("edit err = %v", err)
	}
	if err := svc.Revoke("tenant-b", "lead-1"); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("revoke err = %v", err)
	}
	if _, err := svc.Handoff(Input{TenantID: "tenant-b", LeadID: "lead-1", Version: 1, Target: "goboost", PayerRef: "payer-b"}); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("handoff err = %v", err)
	}
	got, err := svc.Draft("tenant-a", "lead-1")
	if err != nil || got.Body != saved.Body || got.Body != "甲的草稿" {
		t.Fatalf("owner draft = %+v %v", got, err)
	}
	if svc.Projects() != 1 {
		t.Fatalf("projects = %d", svc.Projects())
	}
}

func TestRevokedPermissionRejectsNewHandoffAndKeepsDraft(t *testing.T) {
	svc := New()
	saved, err := svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 2,
		Facts: []Fact{{Key: "scene", Value: "撤销前的草稿", Selected: true, Authorized: true}},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 2, Target: "avatar", PayerRef: "payer-a"}); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if err := svc.Revoke("tenant-a", "lead-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 9, Target: "goboost", PayerRef: "payer-a"}); !errors.Is(err, ErrPermissionRevoked) {
		t.Fatalf("new handoff err = %v", err)
	}
	got, err := svc.Draft("tenant-a", "lead-1")
	if err != nil || got.Body != saved.Body {
		t.Fatalf("draft after revoke = %+v %v", got, err)
	}
	if svc.Projects() != 1 {
		t.Fatalf("projects = %d, want the original fixture only", svc.Projects())
	}
}

func TestTargetFailureLeavesDraftEditableByHand(t *testing.T) {
	svc := New()
	if _, err := svc.SaveDraft(SaveRequest{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 1,
		Facts: []Fact{{Key: "scene", Value: "失败前的草稿", Selected: true, Authorized: true}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := svc.Handoff(Input{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "goboost", PayerRef: "payer-a", TargetFailed: true,
	})
	if !errors.Is(err, ErrTargetFailed) {
		t.Fatalf("err = %v", err)
	}
	if svc.Projects() != 0 || svc.ChargeWrites() != 0 || svc.ProviderCalls() != 0 {
		t.Fatalf("failed target was stored projects=%d", svc.Projects())
	}
	edited, err := svc.EditByHand("tenant-a", "lead-1", "  手改后的短回复  ")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.Body != "手改后的短回复" || edited.Sent || edited.Published || edited.MarketingLicense || edited.ReplyBotStarted {
		t.Fatalf("edit = %+v", edited)
	}
	got, err := svc.Draft("tenant-a", "lead-1")
	if err != nil || got.Body != "手改后的短回复" {
		t.Fatalf("stored edit = %+v %v", got, err)
	}
	if svc.ReplyBotsStarted() != 0 {
		t.Fatalf("edit started a reply bot")
	}
}

func TestGenerationSuccessIsNotSentPublishedOrLicensed(t *testing.T) {
	svc := New()
	got, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "goboost", PayerRef: "payer-a"})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if !got.GenerationOK || got.Sent || got.Published || got.MarketingLicense || got.LiveSession {
		t.Fatalf("generation flags = %+v", got)
	}
	if got.ModelStamp != "真实模型未完成" {
		t.Fatalf("stamp = %q", got.ModelStamp)
	}
	if got.ServiceProvider != "NOT_VERIFIED" || got.Billing != "NOT_VERIFIED" || got.Production != "NOT_AUTHORIZED" || got.HumanAdoption != "UNKNOWN" {
		t.Fatalf("labels = %+v", got)
	}
	if got.BillingPassed || got.ProviderCalls != 0 || got.ChargeWrites != 0 {
		t.Fatalf("counters = %+v", got)
	}
	dump := fmt.Sprintf("%+v", got)
	for _, bad := range []string{"已发送", "已发布", "营销许可", "自动触达已成功", "白标经营链完成"} {
		if strings.Contains(dump, bad) {
			t.Fatalf("result claims %s: %s", bad, dump)
		}
	}
}

func TestUnknownCostStaysUnknownAndKnownZeroDoesNotCharge(t *testing.T) {
	svc := New()
	unknown, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "goboost", PayerRef: "payer-a"})
	if err != nil {
		t.Fatalf("unknown: %v", err)
	}
	if unknown.Cost.Known || unknown.Cost.Label() != "unknown" {
		t.Fatalf("unknown cost = %+v label %q", unknown.Cost, unknown.Cost.Label())
	}
	raw, err := json.Marshal(unknown.Cost)
	if err != nil || string(raw) != "null" {
		t.Fatalf("unknown cost json = %s (%v)", raw, err)
	}
	zero := int64(0)
	known, err := svc.Handoff(Input{
		TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: "aicut", PayerRef: "payer-a", CostMinor: &zero,
	})
	if err != nil {
		t.Fatalf("known zero: %v", err)
	}
	if !known.Cost.Known || known.Cost.Minor != 0 || known.Cost.Label() != "0" {
		t.Fatalf("known zero = %+v label %q", known.Cost, known.Cost.Label())
	}
	if known.ChargeWrites != 0 || known.BillingPassed || svc.ChargeWrites() != 0 || svc.ProviderCalls() != 0 {
		t.Fatalf("zero cost charged: %+v service charges %d", known, svc.ChargeWrites())
	}
}

func TestFixtureIsNotALiveSession(t *testing.T) {
	svc := New()
	targets := []string{"goboost", "product-image", "avatar", "aicut"}
	for _, target := range targets {
		got, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 2, Target: target, PayerRef: "payer-a"})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if got.LiveSession || !got.Fixture || !strings.HasPrefix(got.FixtureRef, "fixture:"+target+":") || strings.Contains(got.FixtureRef, "://") {
			t.Fatalf("%s session = %+v", target, got)
		}
		if got.GenerationOK && (got.Sent || got.Published || got.MarketingLicense) {
			t.Fatalf("%s generation was delivery: %+v", target, got)
		}
	}
	if svc.Projects() != len(targets) || svc.ProviderCalls() != 0 {
		t.Fatalf("projects=%d calls=%d", svc.Projects(), svc.ProviderCalls())
	}
}

func TestUnknownTargetDoesNotOpenASession(t *testing.T) {
	for _, target := range []string{"", "product_image", "digital_human", "goboost,aicut", "avatar "} {
		svc := New()
		if _, err := svc.SaveDraft(SaveRequest{
			TenantID: "tenant-a", LeadID: "lead-1", Version: 1,
			Facts: []Fact{{Key: "scene", Value: "仍可手改", Selected: true, Authorized: true}},
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
		_, err := svc.Handoff(Input{TenantID: "tenant-a", LeadID: "lead-1", Version: 1, Target: target, PayerRef: "payer-a"})
		if !errors.Is(err, ErrUnknownTarget) {
			t.Fatalf("target %q err = %v", target, err)
		}
		if svc.Projects() != 0 {
			t.Fatalf("target %q stored a project", target)
		}
		if _, err := svc.EditByHand("tenant-a", "lead-1", "目标无效后手改"); err != nil {
			t.Fatalf("edit after %q: %v", target, err)
		}
	}
}
