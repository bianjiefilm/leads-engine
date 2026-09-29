package sopreach

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type hit struct {
	Method string
	Body   []byte
}

type fixtureBox struct {
	mu     sync.Mutex
	status int
	hits   []hit
}

func (f *fixtureBox) snapshot() []hit {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hit, len(f.hits))
	copy(out, f.hits)
	return out
}

func startFixture(t *testing.T, status int) (*fixtureBox, string) {
	t.Helper()
	box := &fixtureBox{status: status}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		box.mu.Lock()
		box.hits = append(box.hits, hit{Method: r.Method, Body: body})
		box.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(box.status)
		_, _ = w.Write([]byte(`{"accepted":true,"receipt":"delivered","status":"已送达"}`))
	}))
	t.Cleanup(srv.Close)
	return box, srv.URL
}

func baseReach(body string) Reach {
	return Reach{
		Input:             bound(),
		ContactID:         "ct_1",
		CampaignID:        "camp_a",
		ConsentCampaignID: "camp_a",
		Body:              body,
	}
}

func autoReach(body string) Reach {
	r := baseReach(body)
	r.Level = LevelUnattended
	r.ExplicitUnattended = true
	r.LiveChannel = true
	r.BalanceCents = 8000
	r.AIScore = 99
	return r
}

func stageDraft(t *testing.T, s *Sandbox, body string) Reach {
	t.Helper()
	r := baseReach(body)
	rem := s.Remind(r)
	if !rem.OK || rem.ID == "" {
		t.Fatalf("remind: %+v", rem)
	}
	r.ReminderID = rem.ID
	dr := s.Draft(r)
	if !dr.OK || dr.ID == "" {
		t.Fatalf("draft: %+v", dr)
	}
	r.DraftID = dr.ID
	r.Role = "authorized_test"
	return r
}

func assertEvidence(t *testing.T, ev Evidence) {
	t.Helper()
	if ev.ServiceProvider != "NOT_VERIFIED" || ev.Billing != "NOT_VERIFIED" || ev.Production != "NOT_AUTHORIZED" || ev.HumanAdoption != "UNKNOWN" {
		t.Fatalf("labels: %+v", ev)
	}
	if ev.ChannelStamp != "正式渠道未验证" || ev.ChargeWrites != 0 || ev.BillingPassed || ev.ProviderCalls != 0 {
		t.Fatalf("stamp or counters: %+v", ev)
	}
}

func assertNoClaim(t *testing.T, got Outcome) {
	t.Helper()
	if got.Delivered || got.Replied {
		t.Fatalf("claimed delivery or reply: %+v", got)
	}
	blob := strings.Join([]string{got.Stamp, got.Delivery, got.Submission, got.Status, got.Refusal, got.SentBody, got.Kind}, "\n")
	for _, rec := range got.Records {
		blob += "\n" + rec.Kind + " " + rec.Status
	}
	for _, bad := range []string{"已送达", "已回复", "自动触达已成功"} {
		if strings.Contains(blob, bad) {
			t.Fatalf("outcome contains %s: %+v", bad, got)
		}
	}
	assertEvidence(t, got.Evidence)
}

func TestDefaultAutomaticDoesNotSend(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	r := autoReach("不该自动发出")
	premise := r.Input
	premise.Op = OpAuto
	if d := Decide(premise); !d.OK || d.Delivered || d.LiveCharge != 0 {
		t.Fatalf("premise decide: %+v", d)
	}
	got := s.QueueAutomatic(r)
	if got.OK || got.CreatedSend || got.AutomaticSent || got.Refusal != "automatic_send_off" || got.SentBody != "" {
		t.Fatalf("queue: %+v", got)
	}
	if len(box.snapshot()) != 0 {
		t.Fatalf("fixture posts: %+v", box.snapshot())
	}
	assertNoClaim(t, got)
}

func TestConfirmPostsFixtureAndDoesNotTreatHTTP200AsDelivery(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	r := stageDraft(t, s, "确认这版草稿")
	if len(box.snapshot()) != 0 {
		t.Fatalf("remind or draft posted: %+v", box.snapshot())
	}
	got := s.Confirm(r)
	if !got.OK || got.CreatedSend != true || got.Submission != "submission_accepted" || got.Delivery != "unknown" || got.Stamp != "正式渠道未验证" {
		t.Fatalf("confirm: %+v", got)
	}
	if got.SentBody != "确认这版草稿" || got.Kind != "channel_submission" || got.Status != "submission_accepted" {
		t.Fatalf("submission shape: %+v", got)
	}
	if len(got.Records) != 2 || got.Records[0].Kind != "channel_submission" || got.Records[0].Status != "submission_accepted" || got.Records[1].Kind != "channel_delivery" || got.Records[1].Status != "unknown" {
		t.Fatalf("records: %+v", got.Records)
	}
	assertNoClaim(t, got)
	hits := box.snapshot()
	if len(hits) != 1 || hits[0].Method != http.MethodPost {
		t.Fatalf("hits: %+v", hits)
	}
	var payload map[string]any
	if err := json.Unmarshal(hits[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["receipt"]; ok {
		t.Fatalf("payload invented a receipt: %s", hits[0].Body)
	}
	if payload["body"] != "确认这版草稿" || payload["tenant_id"] != "ten_a" || payload["contact_id"] != "ct_1" || payload["campaign_id"] != "camp_a" || payload["channel"] != "sms" || payload["purpose"] != "follow_up" || payload["content_version"] != float64(3) {
		t.Fatalf("payload: %s", hits[0].Body)
	}
	again := s.Confirm(r)
	if again.CreatedSend || again.Delivered || again.Delivery != "unknown" || len(box.snapshot()) != 1 {
		t.Fatalf("second confirm: %+v posts=%d", again, len(box.snapshot()))
	}
	assertNoClaim(t, again)
}

func TestNon200IsNotAccepted(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusInternalServerError} {
		box, url := startFixture(t, status)
		s := NewSandbox(url)
		r := stageDraft(t, s, "确认这版草稿")
		got := s.Confirm(r)
		if got.OK || got.CreatedSend || got.Submission == "submission_accepted" || got.Refusal != "fixture_not_accepted" || got.Delivery != "unknown" {
			t.Fatalf("status %d: %+v", status, got)
		}
		assertNoClaim(t, got)
		again := s.Confirm(r)
		if again.CreatedSend || len(box.snapshot()) != 1 {
			t.Fatalf("status %d retried: %+v posts=%d", status, again, len(box.snapshot()))
		}
		miss := s.Callback(Callback{CallbackID: "cb-miss", MessageID: got.ID, TenantID: "ten_a", ActionTenantID: "ten_a", HTTPStatus: http.StatusOK})
		if miss.FoundOriginal || miss.CreatedSend || len(box.snapshot()) != 1 {
			t.Fatalf("status %d stored a message: %+v", status, miss)
		}
	}
}

func TestAuthorizedTestRoleRequired(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	r := stageDraft(t, s, "确认这版草稿")
	r.Role = "owner"
	got := s.Confirm(r)
	if got.OK || got.CreatedSend || got.Refusal != "test_role_required" || len(box.snapshot()) != 0 {
		t.Fatalf("role: %+v posts=%d", got, len(box.snapshot()))
	}
	assertNoClaim(t, got)
}

func TestConfirmRequiresReminderThenDraft(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	r := baseReach("确认这版草稿")
	r.Role = "authorized_test"
	if got := s.Confirm(r); got.OK || got.Refusal != "confirm_chain_required" || len(box.snapshot()) != 0 {
		t.Fatalf("no draft: %+v", got)
	}
	rem := s.Remind(r)
	if !rem.OK {
		t.Fatal(rem)
	}
	if got := s.Draft(r); got.OK || got.Refusal != "confirm_chain_required" {
		t.Fatalf("draft without reminder: %+v", got)
	}
	if len(box.snapshot()) != 0 {
		t.Fatalf("posts: %+v", box.snapshot())
	}
}

func TestConfirmRechecksStopGates(t *testing.T) {
	cases := []struct {
		name   string
		refuse string
		mutate func(*Reach)
	}{
		{name: "unsubscribe", refuse: "unsubscribed", mutate: func(r *Reach) { r.Unsubscribed = true }},
		{name: "revoke", refuse: "revoked", mutate: func(r *Reach) { r.Revoked = true }},
		{name: "quiet hours", refuse: "outside_window", mutate: func(r *Reach) { r.InsideWindow = false }},
		{name: "frequency cap", refuse: "rate_limited", mutate: func(r *Reach) { r.RateLimited = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			box, url := startFixture(t, http.StatusOK)
			s := NewSandbox(url)
			r := stageDraft(t, s, "确认这版草稿")
			tc.mutate(&r)
			got := s.Confirm(r)
			if got.OK || got.CreatedSend || got.Refusal != tc.refuse || got.SentBody != "" || len(box.snapshot()) != 0 {
				t.Fatalf("%s: %+v posts=%d", tc.name, got, len(box.snapshot()))
			}
			assertNoClaim(t, got)
		})
	}
}

func TestStopConditionsCancelTheQueuedAutomaticAction(t *testing.T) {
	cases := []struct {
		name   string
		refuse string
		mutate func(*Reach)
	}{
		{name: "human takeover", refuse: "human_takeover", mutate: func(r *Reach) { r.HumanTakeover = true }},
		{name: "unsubscribe", refuse: "unsubscribed", mutate: func(r *Reach) { r.Unsubscribed = true }},
		{name: "revoke", refuse: "revoked", mutate: func(r *Reach) { r.Revoked = true }},
		{name: "quiet hours", refuse: "outside_window", mutate: func(r *Reach) { r.InsideWindow = false }},
		{name: "frequency cap", refuse: "rate_limited", mutate: func(r *Reach) { r.RateLimited = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			box, url := startFixture(t, http.StatusOK)
			s := NewSandbox(url)
			s.AllowAutomatic(true)
			queued := s.QueueAutomatic(autoReach("旧的自动回复"))
			if !queued.OK || queued.CreatedSend || queued.ID == "" || len(box.snapshot()) != 0 {
				t.Fatalf("queue: %+v", queued)
			}
			now := autoReach("新的回复")
			tc.mutate(&now)
			got := s.Release(queued.ID, now)
			if got.OK || got.CreatedSend || got.AutomaticSent || got.SentBody != "" || got.Refusal != tc.refuse {
				t.Fatalf("release: %+v", got)
			}
			assertNoClaim(t, got)
			again := s.Release(queued.ID, autoReach("新的回复"))
			if again.CreatedSend || again.AutomaticSent || again.SentBody != "" || len(box.snapshot()) != 0 {
				t.Fatalf("second release: %+v posts=%d", again, len(box.snapshot()))
			}
			for _, item := range box.snapshot() {
				text := string(item.Body)
				if strings.Contains(text, "旧的自动回复") || strings.Contains(text, "新的回复") {
					t.Fatalf("stale body posted: %s", text)
				}
			}
		})
	}
}

func TestDisablingAutomaticDropsQueuedSend(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	s.AllowAutomatic(true)
	queued := s.QueueAutomatic(autoReach("旧的自动回复"))
	if !queued.OK || queued.ID == "" {
		t.Fatal(queued)
	}
	s.AllowAutomatic(false)
	got := s.Release(queued.ID, autoReach("新的回复"))
	if got.CreatedSend || got.Refusal != "automatic_send_off" || len(box.snapshot()) != 0 {
		t.Fatalf("disabled: %+v", got)
	}
	s.AllowAutomatic(true)
	again := s.Release(queued.ID, autoReach("新的回复"))
	if again.CreatedSend || again.SentBody != "" || len(box.snapshot()) != 0 {
		t.Fatalf("reenabled stale send: %+v posts=%d", again, len(box.snapshot()))
	}
}

func TestExplicitAutomaticStillDoesNotClaimDelivery(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	s.AllowAutomatic(true)
	queued := s.QueueAutomatic(autoReach("排队中的自动回复"))
	if !queued.OK || queued.CreatedSend {
		t.Fatal(queued)
	}
	got := s.Release(queued.ID, autoReach("排队中的自动回复"))
	if !got.OK || !got.CreatedSend || got.Submission != "submission_accepted" || got.Delivery != "unknown" || got.SentBody != "排队中的自动回复" || got.Delivered {
		t.Fatalf("release: %+v", got)
	}
	assertNoClaim(t, got)
	hits := box.snapshot()
	if len(hits) != 1 || !strings.Contains(string(hits[0].Body), "排队中的自动回复") {
		t.Fatalf("hits: %+v", hits)
	}
	if strings.Contains(string(hits[0].Body), "receipt") {
		t.Fatalf("payload has receipt: %s", hits[0].Body)
	}
	assertEvidence(t, s.Evidence())
}

func TestManualConfirmDuringTakeoverDoesNotSendStaleAuto(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	s.AllowAutomatic(true)
	queued := s.QueueAutomatic(autoReach("旧的自动回复"))
	if !queued.OK {
		t.Fatal(queued)
	}
	stopped := autoReach("新的回复")
	stopped.HumanTakeover = true
	if got := s.Release(queued.ID, stopped); got.CreatedSend || got.Refusal != "human_takeover" {
		t.Fatalf("auto: %+v", got)
	}
	r := stageDraft(t, s, "确认这版草稿")
	r.HumanTakeover = true
	got := s.Confirm(r)
	if !got.OK || got.SentBody != "确认这版草稿" || got.Delivery != "unknown" || got.Delivered {
		t.Fatalf("confirm: %+v", got)
	}
	assertNoClaim(t, got)
	hits := box.snapshot()
	if len(hits) != 1 || !strings.Contains(string(hits[0].Body), "确认这版草稿") || strings.Contains(string(hits[0].Body), "旧的自动回复") {
		t.Fatalf("hits: %+v", hits)
	}
}

func TestEnrollmentInAnotherCampaignIsNotMarketingPermission(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	marketing := baseReach("只同意了活动甲")
	marketing.Purpose = PurposeMarketing
	marketing.ConsentPurpose = PurposeMarketing
	marketing.MarketingAllowed = true
	marketing.ConsultationOnly = false
	marketing.ConsentChannels = []string{ChannelSMS}
	marketing.CampaignID = "camp_b"
	marketing.ConsentCampaignID = "camp_a"
	marketing.EnrolledCampaignID = "camp_b"
	rem := s.Remind(marketing)
	marketing.ReminderID = rem.ID
	dr := s.Draft(marketing)
	marketing.DraftID = dr.ID
	marketing.Role = "authorized_test"
	if !rem.OK || !dr.OK {
		t.Fatalf("stage: %+v %+v", rem, dr)
	}
	denied := s.Confirm(marketing)
	if denied.OK || denied.CreatedSend || denied.Refusal != "campaign_not_permission" || len(box.snapshot()) != 0 {
		t.Fatalf("other campaign: %+v", denied)
	}
	assertNoClaim(t, denied)

	follow := baseReach("跟进不是营销")
	follow.Purpose = PurposeMarketing
	follow.Role = "authorized_test"
	followRem := s.Remind(follow)
	follow.ReminderID = followRem.ID
	followDr := s.Draft(follow)
	follow.DraftID = followDr.ID
	if got := s.Confirm(follow); got.OK || got.Refusal != "consent_scope" || len(box.snapshot()) != 0 {
		t.Fatalf("follow-up consent: %+v", got)
	}

	allowed := baseReach("活动甲的营销")
	allowed.Purpose = PurposeMarketing
	allowed.ConsentPurpose = PurposeMarketing
	allowed.MarketingAllowed = true
	allowed.ConsultationOnly = false
	allowed.ConsentChannels = []string{ChannelSMS}
	allowed.CampaignID = "camp_a"
	allowed.ConsentCampaignID = "camp_a"
	allowed.EnrolledCampaignID = "camp_b"
	allowedRem := s.Remind(allowed)
	allowed.ReminderID = allowedRem.ID
	allowedDr := s.Draft(allowed)
	allowed.DraftID = allowedDr.ID
	allowed.Role = "authorized_test"
	got := s.Confirm(allowed)
	if !got.OK || got.Submission != "submission_accepted" || got.Delivery != "unknown" || got.SentBody != "活动甲的营销" || len(box.snapshot()) != 1 {
		t.Fatalf("same campaign: %+v posts=%d", got, len(box.snapshot()))
	}
	assertNoClaim(t, got)
	var payload map[string]any
	if err := json.Unmarshal(box.snapshot()[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["campaign_id"] != "camp_a" || payload["purpose"] != "marketing" {
		t.Fatalf("payload: %s", box.snapshot()[0].Body)
	}
}

func TestCrossTenantConfirmAndCallbackRejected(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	s.AllowAutomatic(true)
	queued := s.QueueAutomatic(autoReach("租户甲的自动回复"))
	if !queued.OK {
		t.Fatal(queued)
	}
	foreign := autoReach("新的回复")
	foreign.TenantID = "ten_b"
	foreign.ActionTenantID = "ten_b"
	if got := s.Release(queued.ID, foreign); got.CreatedSend || got.Refusal != "tenant_mismatch" || len(box.snapshot()) != 0 {
		t.Fatalf("foreign release: %+v", got)
	}
	owner := s.Release(queued.ID, autoReach("租户甲的自动回复"))
	if !owner.OK || owner.SentBody != "租户甲的自动回复" || owner.Delivery != "unknown" || len(box.snapshot()) != 1 {
		t.Fatalf("owner release: %+v", owner)
	}

	r := stageDraft(t, s, "确认这版草稿")
	sneak := r
	sneak.TenantID = "ten_b"
	sneak.ActionTenantID = "ten_b"
	if got := s.Confirm(sneak); got.OK || got.CreatedSend || got.Refusal != "tenant_mismatch" || len(box.snapshot()) != 1 {
		t.Fatalf("foreign confirm: %+v", got)
	}
	okConfirm := s.Confirm(r)
	if !okConfirm.OK || okConfirm.Delivery != "unknown" || len(box.snapshot()) != 2 {
		t.Fatalf("owner confirm: %+v", okConfirm)
	}
	cb := s.Callback(Callback{
		CallbackID: "cb-cross", MessageID: okConfirm.ID, TenantID: "ten_b", ActionTenantID: "ten_b", HTTPStatus: http.StatusOK,
	})
	if cb.FoundOriginal || cb.CreatedSend || cb.Refusal != "tenant_mismatch" || len(box.snapshot()) != 2 {
		t.Fatalf("foreign callback: %+v", cb)
	}
	assertNoClaim(t, cb)
	assertEvidence(t, s.Evidence())
}

func TestUnknownCallbackDoesNotCreateASecondSend(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	first := s.Callback(Callback{CallbackID: "cb-1", MessageID: "msg-missing", TenantID: "ten_a", ActionTenantID: "ten_a", HTTPStatus: http.StatusOK})
	if first.OK || first.FoundOriginal || first.CreatedSend || first.Refusal != "original_missing" || first.Delivery != "unknown" {
		t.Fatalf("first: %+v", first)
	}
	assertNoClaim(t, first)
	second := s.Callback(Callback{CallbackID: "cb-2", MessageID: "msg-missing", TenantID: "ten_a", ActionTenantID: "ten_a", HTTPStatus: http.StatusOK})
	if second.FoundOriginal || second.CreatedSend || len(box.snapshot()) != 0 {
		t.Fatalf("second: %+v posts=%d", second, len(box.snapshot()))
	}
	repeat := s.Callback(Callback{CallbackID: "cb-1", MessageID: "msg-missing", TenantID: "ten_a", ActionTenantID: "ten_a"})
	if !repeat.Duplicate || repeat.CreatedSend || repeat.FoundOriginal || len(box.snapshot()) != 0 {
		t.Fatalf("repeat unknown: %+v", repeat)
	}
	assertEvidence(t, s.Evidence())
}

func TestRepeatCallbackDoesNotSendOrCharge(t *testing.T) {
	box, url := startFixture(t, http.StatusOK)
	s := NewSandbox(url)
	r := stageDraft(t, s, "确认这版草稿")
	sent := s.Confirm(r)
	if !sent.OK || sent.ID == "" || len(box.snapshot()) != 1 {
		t.Fatalf("confirm: %+v", sent)
	}
	first := s.Callback(Callback{CallbackID: "cb-a", MessageID: sent.ID, TenantID: "ten_a", ActionTenantID: "ten_a", HTTPStatus: http.StatusOK})
	if !first.FoundOriginal || first.CreatedSend || first.Duplicate || first.Delivery != "unknown" || first.Submission != "submission_accepted" || len(box.snapshot()) != 1 {
		t.Fatalf("first callback: %+v", first)
	}
	assertNoClaim(t, first)
	second := s.Callback(Callback{CallbackID: "cb-b", MessageID: sent.ID, TenantID: "ten_a", ActionTenantID: "ten_a", HTTPStatus: http.StatusOK})
	if !second.FoundOriginal || !second.Duplicate || second.CreatedSend || second.Delivery != "unknown" || len(box.snapshot()) != 1 {
		t.Fatalf("second callback: %+v", second)
	}
	same := s.Callback(Callback{CallbackID: "cb-a", MessageID: sent.ID, TenantID: "ten_a", ActionTenantID: "ten_a", HTTPStatus: http.StatusOK})
	if !same.Duplicate || same.CreatedSend || len(box.snapshot()) != 1 {
		t.Fatalf("same callback id: %+v", same)
	}
	assertEvidence(t, s.Evidence())
}
