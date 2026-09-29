package workbench

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func cents(n int64) *int64 { return &n }

func TestManualNextBeatsSuggestionAndIgnoresAIScore(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	lead := LeadView{
		ID: "lead_1", Status: "new", Assignee: "sales1",
		ManualNextAt: "2026-09-28T02:00:00Z", ManualNextKind: "manual_follow_up",
		HasOpenFollowUp: true, AIScore: 99,
	}
	got := ApplyAIScore(ResolveNext(lead, now), 99)
	if got.Source != "manual" || got.Kind != "manual_follow_up" || got.At != "2026-09-28T02:00:00Z" {
		t.Fatalf("manual next lost: %+v", got)
	}
	if got.AutoCall || got.AutoMessage || got.CreateOrder {
		t.Fatalf("AI score armed a side effect: %+v", got)
	}
	plain := ResolveNext(LeadView{ID: "lead_1", Status: "new", AIScore: 99}, now)
	if plain.Kind != "suggest_schedule" || plain.Source != "suggestion" || plain.AutoCall || plain.AutoMessage || plain.CreateOrder {
		t.Fatalf("suggestion must stay advisory: %+v", plain)
	}
}

func TestChannelFollowUpWithoutPhoneIsNotInvalidAndNotSMS(t *testing.T) {
	lead := LeadView{
		ID: "lead_ch", Status: "new", Phone: "  ",
		ChannelIdentity: "wx_openid_1", ChannelReplyAllowed: true,
		FilterReason: "invalid_phone",
	}
	if DisplayFilterReason(lead) != "" {
		t.Fatalf("filter shown as %q, want empty", DisplayFilterReason(lead))
	}
	got := ResolveNext(lead, time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC))
	if got.Kind != "channel_follow_up" || got.AutoCall || got.AutoMessage {
		t.Fatalf("channel next: %+v", got)
	}
	if channels := MarketingChannels(lead); len(channels) != 0 {
		t.Fatalf("channel reply expanded to %v", channels)
	}
	permitted := lead
	permitted.MarketingSMSOrPhone = true
	if channels := MarketingChannels(permitted); len(channels) != 2 || channels[0] != "sms" || channels[1] != "phone" {
		t.Fatalf("explicit sms/phone permit = %v", channels)
	}
}

func TestRefusedMarketingAndAfterSalesAreNotForced(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	for _, purpose := range []string{"marketing_refused", "after_sales", "consult"} {
		if !RefuseSalesPush(purpose) {
			t.Fatalf("%s should refuse a sales push", purpose)
		}
		res := Build(Scope{Role: "owner", MemberID: "owner"}, now, []LeadView{{
			ID: "lead_" + purpose, Status: "new", Assignee: "sales1", Purpose: purpose,
		}}, nil, []ReceptionView{{
			SessionID: "sess_" + purpose, Assignee: "sales1", WaitingReply: true, Purpose: purpose,
		}})
		for _, bucket := range res.Buckets {
			for _, item := range bucket {
				if item.ForceOpportunity {
					t.Fatalf("%s item forced an opportunity: %+v", purpose, item)
				}
			}
		}
	}
	if RefuseSalesPush("sales") {
		t.Fatal("ordinary sales purpose must stay eligible")
	}
}

func TestQueuesScopeMoneyAndAuthoritativeStatus(t *testing.T) {
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	leads := []LeadView{
		{ID: "mine", Status: "new", Assignee: "sales1", CreatedAt: now.Format(time.RFC3339), SourceChannel: "手工录入", SourceAt: now.Format(time.RFC3339)},
		{ID: "theirs", Status: "new", Assignee: "sales2"},
		{ID: "pool", Status: "new", Assignee: ""},
		{
			ID: "due", Status: "in_progress", Assignee: "sales1", HasOpenFollowUp: true,
			ManualNextAt: "2026-09-28T09:00:00Z", UpdatedAt: now.Format(time.RFC3339),
		},
		{
			ID: "later", Status: "in_progress", Assignee: "sales1", HasOpenFollowUp: true,
			ManualNextAt: "2026-09-29T09:00:00Z", UpdatedAt: now.Format(time.RFC3339),
		},
		{
			ID: "stale", Status: "in_progress", Assignee: "sales1",
			UpdatedAt: now.AddDate(0, 0, -8).Format(time.RFC3339),
		},
		{
			ID: "fresh", Status: "in_progress", Assignee: "sales1",
			UpdatedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{ID: "done", Status: "new", Assignee: "sales1", SubmittedAt: now.Format(time.RFC3339), HasCompletedFollowUp: true},
	}
	opps := []OpportunityView{
		{ID: "opp_open", ContactID: "c1", Stage: "open", Category: "merchant_customer", Assignee: "sales1", UpdatedAt: now.AddDate(0, 0, -8).Format(time.RFC3339)},
		{ID: "opp_won", Stage: "won", Category: "merchant_customer", Assignee: "sales1", AmountCents: cents(1500), AmountKind: "customer_deal"},
		{ID: "opp_creative", Stage: "proposal", Category: "creative_service", Assignee: "sales1", HasManualNext: true, UpdatedAt: now.Format(time.RFC3339)},
		{ID: "svc", Stage: "won", AmountKind: "painuo_service", AmountCents: cents(9000), Assignee: "sales1"},
		{ID: "tool", Stage: "won", AmountKind: "platform_tool", AmountCents: cents(50), Assignee: "sales1"},
	}
	desk := []ReceptionView{{
		SessionID: "wait1", LeadID: "mine", Assignee: "sales1", WaitingReply: true, Purpose: "sales",
	}, {
		SessionID: "take1", Assignee: "sales1", HumanTodo: true, PendingReason: "human_takeover",
	}}

	sales := Build(Scope{Role: "sales", MemberID: "sales1"}, now, leads, opps, desk)
	if sales.Scope != "own" {
		t.Fatalf("sales scope = %s", sales.Scope)
	}
	if !hasID(sales, BucketUnprocessed, "mine") || hasID(sales, BucketUnprocessed, "theirs") || hasID(sales, BucketUnprocessed, "pool") {
		t.Fatalf("unprocessed = %v", ids(sales, BucketUnprocessed))
	}
	if !hasID(sales, BucketDueToday, "due") || hasID(sales, BucketDueToday, "later") {
		t.Fatalf("due = %v", ids(sales, BucketDueToday))
	}
	if !hasID(sales, BucketWaitingReply, "wait1") {
		t.Fatal("missing waiting reply")
	}
	if !hasID(sales, BucketHumanTakeover, "take1") {
		t.Fatal("missing human takeover")
	}
	if !hasID(sales, BucketStale, "stale") || hasID(sales, BucketStale, "fresh") || hasID(sales, BucketStale, "due") {
		t.Fatalf("stale = %v", ids(sales, BucketStale))
	}
	if !hasID(sales, BucketNeedsSchedule, "opp_open") || !hasID(sales, BucketNeedsSchedule, "stale") || !hasID(sales, BucketNeedsSchedule, "fresh") {
		t.Fatalf("needs schedule = %v", ids(sales, BucketNeedsSchedule))
	}
	if hasID(sales, BucketNeedsSchedule, "opp_creative") || hasID(sales, BucketNeedsSchedule, "opp_won") {
		t.Fatalf("scheduled or closed opp leaked into needs schedule: %v", ids(sales, BucketNeedsSchedule))
	}
	if len(sales.Buckets[BucketAssignmentException]) != 0 {
		t.Fatalf("sales saw assignment exceptions: %+v", sales.Buckets[BucketAssignmentException])
	}
	for _, item := range sales.Buckets[BucketNeedsSchedule] {
		if item.OpportunityID == "opp_open" && item.ShowServiceDraft {
			t.Fatal("merchant opportunity exposed a service draft")
		}
	}
	if !ShowServiceDraft("creative_service") || ShowServiceDraft("merchant_customer") {
		t.Fatal("service draft visibility drifted")
	}

	owner := Build(Scope{Role: "owner", MemberID: "owner"}, now, leads, opps, desk)
	if owner.Scope != "tenant" || !hasID(owner, BucketUnprocessed, "theirs") || !hasID(owner, BucketAssignmentException, "pool") {
		t.Fatalf("owner view missing tenant rows: unprocessed=%v assign=%v", ids(owner, BucketUnprocessed), ids(owner, BucketAssignmentException))
	}

	agent := Build(Scope{Role: "agent", MemberID: "sales1"}, now, leads, nil, nil)
	if hasID(agent, BucketUnprocessed, "theirs") || !hasID(agent, BucketUnprocessed, "mine") {
		t.Fatalf("agent scope = %v", ids(agent, BucketUnprocessed))
	}

	if sales.Money.CustomerDealCents == nil || *sales.Money.CustomerDealCents != 1500 {
		t.Fatalf("customer deal = %v", sales.Money.CustomerDealCents)
	}
	if sales.Money.PainuoServiceOrderCents == nil || *sales.Money.PainuoServiceOrderCents != 9000 {
		t.Fatalf("service order = %v", sales.Money.PainuoServiceOrderCents)
	}
	if sales.Money.PlatformToolSpendCents == nil || *sales.Money.PlatformToolSpendCents != 50 {
		t.Fatalf("tool spend = %v", sales.Money.PlatformToolSpendCents)
	}
	raw, _ := json.Marshal(sales.Money)
	if bytes.Contains(raw, []byte("total")) || bytes.Contains(raw, []byte("10550")) {
		t.Fatalf("money was combined: %s", raw)
	}
	unknown := SeparateMoney([]OpportunityView{{Stage: "won", AmountKind: "customer_deal"}})
	if unknown.CustomerDealCents != nil {
		t.Fatal("unknown deal amount became a number")
	}

	followed := ProjectStatus(leads[7], []OpportunityView{{Stage: "won"}}, false)
	if !followed.Submitted || !followed.Received || !followed.Assigned || !followed.Followed || !followed.Won || followed.Paid {
		t.Fatalf("status facts = %+v", followed)
	}
	manual := ProjectStatus(leads[0], nil, false)
	if manual.Submitted || manual.Followed || manual.Won || manual.Paid || !manual.Received || !manual.Assigned {
		t.Fatalf("manual lead status = %+v", manual)
	}

	for _, action := range []string{"ingest", "view", "manual_follow_up"} {
		if OrdinaryCRMChargeCents(action) != 0 {
			t.Fatalf("%s was billed", action)
		}
	}
	if sales.Billing.OrdinaryCRMChargeCents != 0 || sales.Automation.AutoCall || sales.Automation.AutoMessage || sales.Automation.CreateOrder {
		t.Fatalf("billing/automation = %+v %+v", sales.Billing, sales.Automation)
	}
	src := sales.Buckets[BucketUnprocessed]
	var mine Item
	for _, item := range src {
		if item.LeadID == "mine" {
			mine = item
		}
	}
	if mine.Source.Channel != "手工录入" || mine.Source.At == "" {
		t.Fatalf("source line = %+v", mine.Source)
	}
}

func TestTimelineHidesPlaintextWithoutPermission(t *testing.T) {
	events := []TimelineEvent{
		{At: "2026-09-28T00:00:02Z", Kind: "assignment", Summary: "分配给 sales1"},
		{At: "2026-09-28T00:00:01Z", Kind: "source", Summary: "手工录入", ContactPlaintext: "13800000000"},
		{At: "2026-09-28T00:00:03Z", Kind: "follow_up", Summary: "已联系"},
	}
	hidden := AssembleTimeline(events, false)
	if hidden[0].Kind != "source" || hidden[1].Kind != "assignment" || hidden[2].Kind != "follow_up" {
		t.Fatalf("order = %+v", hidden)
	}
	for _, ev := range hidden {
		if ev.ContactPlaintext != "" {
			t.Fatalf("plaintext leaked: %+v", ev)
		}
	}
	shown := AssembleTimeline(events, true)
	if shown[0].ContactPlaintext != "13800000000" {
		t.Fatal("authorized reader lost the business-domain plaintext")
	}
}

func TestFollowThroughKeyStableAndDistinct(t *testing.T) {
	a := FollowThroughKey("lead_1", "已联系", "2026-09-29T02:00:00Z", "", false, "mem_1")
	b := FollowThroughKey("lead_1", "已联系", "2026-09-29T02:00:00Z", "", false, "mem_1")
	if a == "" || a != b {
		t.Fatalf("same follow-up changed key: %q %q", a, b)
	}
	other := FollowThroughKey("lead_1", "另一条", "2026-09-29T02:00:00Z", "", false, "mem_1")
	if other == a {
		t.Fatal("a different note reused the same fact key")
	}
}

func TestUnknownSyncIsNotZeroOrFollowed(t *testing.T) {
	sync := ProjectSync("")
	if sync.CRM != "unknown" {
		t.Fatalf("sync = %+v", sync)
	}
	raw, _ := json.Marshal(sync)
	if bytes.Contains(raw, []byte("0")) || bytes.Contains(raw, []byte("已跟进")) {
		t.Fatalf("unknown sync was numeric or followed: %s", raw)
	}
	status := ProjectStatus(LeadView{ID: "lead_1"}, nil, false)
	if status.Followed {
		t.Fatal("missing receipt was stored as followed")
	}
	got := ProjectSync("2026-09-29T00:00:00Z")
	if got.CRM != "received" {
		t.Fatalf("receipt sync = %+v", got)
	}
}

func TestNoMarketingPermitHasNoSuccessClaim(t *testing.T) {
	if OutreachLabel(false, true) != "" || OutreachLabel(true, false) != "" {
		t.Fatal("success claim without both a permit and a send receipt")
	}
	if OutreachLabel(true, true) != "自动触达已成功" {
		t.Fatal("label drifted")
	}
	notice := OutreachNotice(false, false)
	if notice == "" || strings.Contains(notice, "自动触达已成功") {
		t.Fatalf("notice = %q", notice)
	}
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	desk := Build(Scope{Role: "sales", MemberID: "sales1"}, now, []LeadView{{
		ID: "lead_m", Status: "new", Assignee: "sales1", MarketingSMSOrPhone: true,
	}}, nil, nil)
	raw, _ := json.Marshal(desk)
	if bytes.Contains(raw, []byte("自动触达已成功")) {
		t.Fatalf("desk claimed success: %s", raw)
	}
}

func TestOrdinarySalesHideUnconfirmedServiceDraft(t *testing.T) {
	sales := DecideServiceDraft("sales", "creative_service", true, false)
	if sales.Present || sales.Enabled {
		t.Fatalf("sales saw the draft button: %+v", sales)
	}
	if !strings.Contains(sales.Reason, "人工确认") {
		t.Fatalf("reason = %q", sales.Reason)
	}
	store := DecideServiceDraft("sales", "merchant_customer", true, true)
	if store.Present || store.Enabled {
		t.Fatal("store sales grew a service draft")
	}
	owner := DecideServiceDraft("owner", "creative_service", true, false)
	if !owner.Present || owner.Enabled || owner.Reason == "" {
		t.Fatalf("owner affordance = %+v", owner)
	}
	if strings.Contains(owner.Reason, "自动触达已成功") {
		t.Fatal("draft reason claimed outreach")
	}
}

func TestDeskRowShowsOwnerPermissionAndNext(t *testing.T) {
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	res := Build(Scope{Role: "owner", MemberID: "owner"}, now, []LeadView{
		{
			ID: "mine", Status: "new", Assignee: "sales1", OwnerLabel: "Sales A1",
			SourceChannel: "手工录入", SourceAt: now.Format(time.RFC3339),
		},
		{ID: "pool", Status: "new", Assignee: ""},
	}, []OpportunityView{{
		ID: "creative", Stage: "open", Category: "creative_service", Assignee: "sales1",
		UpdatedAt: now.Format(time.RFC3339),
	}}, nil)
	var mine Item
	for _, item := range res.Buckets[BucketUnprocessed] {
		if item.LeadID == "mine" {
			mine = item
		}
	}
	if mine.OwnerLabel != "Sales A1" || mine.Source.Channel != "手工录入" || mine.Next.Kind == "" {
		t.Fatalf("desk row = %+v", mine)
	}
	if len(mine.AllowedContacts) != 0 || mine.Sync.CRM != "unknown" || mine.ModelAdvice != "真实模型未完成" {
		t.Fatalf("facts = %+v", mine)
	}
	if !hasID(res, BucketAssignmentException, "pool") {
		t.Fatal("unassigned lead disappeared")
	}
	for _, item := range res.Buckets[BucketAssignmentException] {
		if item.LeadID == "pool" && item.AssignmentReason != "待分配" {
			t.Fatalf("assignment = %+v", item)
		}
	}
	var creative Item
	for _, item := range res.Buckets[BucketNeedsSchedule] {
		if item.OpportunityID == "creative" {
			creative = item
		}
	}
	if !creative.ShowServiceDraft || !creative.ServiceDraft.Present || creative.ServiceDraft.Enabled || creative.ServiceDraft.Reason == "" {
		t.Fatalf("owner creative affordance = %+v", creative.ServiceDraft)
	}
	sales := Build(Scope{Role: "sales", MemberID: "sales1"}, now, nil, []OpportunityView{{
		ID: "creative", Stage: "open", Category: "creative_service", Assignee: "sales1",
		UpdatedAt: now.Format(time.RFC3339),
	}}, nil)
	for _, item := range sales.Buckets[BucketNeedsSchedule] {
		if item.OpportunityID == "creative" && (item.ServiceDraft.Present || item.ServiceDraft.Enabled) {
			t.Fatalf("sales creative affordance = %+v", item.ServiceDraft)
		}
	}
	permitted := AllowedContacts(LeadView{MarketingSMSOrPhone: true, ChannelReplyAllowed: true, ChannelIdentity: "wechat_dm"})
	if len(permitted) != 3 || permitted[0] != "sms" || permitted[1] != "phone" || permitted[2] != "channel" {
		t.Fatalf("contacts = %v", permitted)
	}
}

func hasID(res Result, bucket, id string) bool {
	for _, item := range res.Buckets[bucket] {
		if item.ID == id || item.LeadID == id || item.OpportunityID == id || item.SessionID == id {
			return true
		}
	}
	return false
}

func ids(res Result, bucket string) []string {
	var out []string
	for _, item := range res.Buckets[bucket] {
		out = append(out, item.ID)
	}
	return out
}
