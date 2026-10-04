package workbench

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTodayTouchLeadIsHandledInPlace(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	lead := LeadView{
		ID: "touch1", TenantID: "tnt_a", ContactID: "c_touch", Status: "new", Assignee: "sales1",
		ContactName: "碰一碰客户", BusinessCategory: "merchant_customer", SourceType: "touch_campaign",
		SourceChannel: "碰一碰", SourceActivity: "camp-1", SourceAt: "2026-10-04T01:00:00Z",
		OwnerLabel: "Sales A1", ConsentStatus: "granted", AIScore: 99,
		LastInteraction: "活动 camp-1 · 碰一碰", LastInteractionAt: "2026-10-04T01:00:00Z",
	}
	if !IsTouchSource(lead) {
		t.Fatal("touch campaign was not recognized")
	}
	res := Build(Scope{Role: "sales", MemberID: "sales1"}, now, []LeadView{lead}, nil, nil)
	item, ok := todayItem(res, TodayNewInquiry, "touch1")
	if !ok {
		t.Fatalf("new inquiry = %#v", res.Today)
	}
	if item.Context.Customer != "碰一碰客户" || item.Context.ContactID != "c_touch" {
		t.Fatalf("context = %+v", item.Context)
	}
	if containsString(item.Context.Ask, "客户姓名") || containsString(item.Context.Ask, "业务类别") {
		t.Fatalf("confirmed facts were asked again: %+v", item.Context)
	}
	if item.Source.Activity != "camp-1" || item.Source.Channel != "碰一碰" {
		t.Fatalf("source = %+v", item.Source)
	}
	if item.OwnerLabel != "Sales A1" || len(item.AllowedContacts) != 0 {
		t.Fatalf("owner/contacts = %q %v", item.OwnerLabel, item.AllowedContacts)
	}
	if item.LastInteraction.Summary == "" || item.Next.AutoCall || item.Next.AutoMessage || item.Next.CreateOrder {
		t.Fatalf("interaction/next = %+v %+v", item.LastInteraction, item.Next)
	}
	if item.OutreachNotice != "没有营销许可" || strings.Contains(item.Basis, "自动触达已成功") || strings.Contains(item.Basis, "自动外呼") && item.Next.AutoCall {
		t.Fatalf("basis/outreach = %q %q", item.Basis, item.OutreachNotice)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "自动触达已成功") || bytesContainPhone(raw) {
		t.Fatalf("desk invented success or a phone: %s", raw)
	}
	if item.ForceOpportunity || item.ShowServiceDraft {
		t.Fatal("ordinary touch inquiry grew an opportunity or a creative handoff")
	}

	waiting := lead
	waiting.HasCompletedFollowUp = true
	waiting.WaitingCustomer = true
	if got := PlaceToday(waiting, now); got != TodayWaiting {
		t.Fatalf("after waiting = %s", got)
	}
	scheduled := lead
	scheduled.HasOpenFollowUp = true
	scheduled.ManualNextAt = "2026-10-04T15:00:00Z"
	scheduled.ManualNextKind = "manual_follow_up"
	if got := PlaceToday(scheduled, now); got != TodayDue {
		t.Fatalf("same-day next = %s", got)
	}
	later := scheduled
	later.ManualNextAt = "2026-10-06T15:00:00Z"
	if got := PlaceToday(later, now); got != TodayScheduled {
		t.Fatalf("future next = %s", got)
	}
	overdue := scheduled
	overdue.ManualNextAt = "2026-10-02T15:00:00Z"
	if got := PlaceToday(overdue, now); got != TodayOverdue {
		t.Fatalf("overdue = %s", got)
	}
	action := ApplyAIScore(ResolveNext(scheduled, now), 99)
	if action.Source != "manual" || action.AutoCall || action.AutoMessage || action.CreateOrder {
		t.Fatalf("manual next lost to score: %+v", action)
	}
}

func TestSameCustomerAsksOnlyChanges(t *testing.T) {
	first := ProjectContext(LeadView{
		ContactID: "c1", ContactName: "甲商家", BusinessCategory: "merchant_customer",
		SourceChannel: "碰一碰", Assignee: "sales1", OwnerLabel: "Sales A1", ConsentStatus: "granted",
	})
	second := ProjectContext(LeadView{
		ContactID: "c1", ContactName: "甲商家", BusinessCategory: "merchant_customer",
		SourceChannel: "碰一碰", Assignee: "sales1", OwnerLabel: "Sales A1", ConsentStatus: "granted",
	})
	if strings.Join(first.Facts, ",") != strings.Join(second.Facts, ",") {
		t.Fatalf("same customer changed facts: %v vs %v", first.Facts, second.Facts)
	}
	if got := SameCustomerAsks(first.Facts, second.Ask, nil); len(got) != 0 {
		t.Fatalf("unchanged customer was questioned: %v", got)
	}
	got := SameCustomerAsks(first.Facts, second.Ask, []string{"客户姓名", "客户姓名", "不存在的字段"})
	if len(got) != 1 || got[0] != "客户姓名" {
		t.Fatalf("changed asks = %v", got)
	}
}

func TestTodayKeepsRefusalsDraftsAndScope(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	after := LeadView{
		ID: "as1", Status: "new", Assignee: "sales1", Purpose: "after_sales",
		ContactName: "售后客户", BusinessCategory: "merchant_customer", ConsentStatus: "granted",
	}
	consult := after
	consult.ID = "cs1"
	consult.Purpose = "consult"
	consult.ContactName = "咨询客户"
	denied := LeadView{
		ID: "deny1", Status: "new", Assignee: "sales1", ContactName: "无许可", ConsentStatus: "denied",
	}
	pool := LeadView{ID: "pool", Status: "new", Assignee: "", ContactName: "未分配"}
	draft := DraftView{
		ID: "dr1", TenantID: "tnt_a", SessionID: "s1", Assignee: "sales1", Kind: "draft", Status: "generated", Body: "请确认这段回复",
	}
	otherDraft := draft
	otherDraft.ID = "dr2"
	otherDraft.Assignee = "sales2"
	desk := []ReceptionView{{
		SessionID: "take1", TenantID: "tnt_a", Assignee: "sales1", OwnerLabel: "Sales A1",
		Mode: "human", Epoch: 2, Version: 3, HumanTodo: true, PendingReason: "human_takeover",
	}}
	res := BuildWithDrafts(Scope{Role: "owner", MemberID: "owner"}, now,
		[]LeadView{after, consult, denied, pool}, nil, desk, []DraftView{draft, otherDraft})
	for _, id := range []string{"as1", "cs1"} {
		item, ok := todayItem(res, TodayNewInquiry, id)
		if !ok || item.ForceOpportunity || item.ShowServiceDraft {
			t.Fatalf("%s today item = %+v ok=%v", id, item, ok)
		}
	}
	if _, ok := todayItem(res, TodayInfo, "deny1"); !ok {
		t.Fatal("denied consent left the permission group")
	}
	if _, ok := todayItem(res, TodayNewInquiry, "deny1"); ok {
		t.Fatal("denied consent stayed in new inquiries")
	}
	if _, ok := todayItem(res, TodayInfo, "pool"); !ok {
		t.Fatal("unassigned lead left the info group")
	}
	if !todayHas(res, TodayAIDraft, "dr1") || !todayHas(res, TodayAIDraft, "dr2") {
		t.Fatalf("owner drafts = %+v", res.Today[TodayAIDraft])
	}
	if _, ok := todayItem(res, TodayHuman, "take1"); !ok {
		t.Fatal("human takeover missing from today")
	}
	sales := BuildWithDrafts(Scope{Role: "sales", MemberID: "sales1"}, now, []LeadView{pool, denied}, nil, desk, []DraftView{draft, otherDraft})
	if todayHas(sales, TodayInfo, "pool") || todayHas(sales, TodayAIDraft, "dr2") {
		t.Fatalf("sales saw another queue: %#v", sales.Today)
	}
	if !todayHas(sales, TodayHuman, "take1") || !todayHas(sales, TodayAIDraft, "dr1") {
		t.Fatalf("sales missed own takeover or draft: %#v", sales.Today)
	}
	ignored := IgnoreDraft("generated")
	if ignored.Status != "superseded" || ignored.Sent || ignored.AutoCall || ignored.AutoMessage {
		t.Fatalf("ignore = %+v", ignored)
	}
	kept := IgnoreDraft("sent")
	if !kept.Sent || kept.Status != "sent" {
		t.Fatalf("ignore rewrote a sent reply: %+v", kept)
	}
	revised, body, err := ReviseDraft("generated", "改成人工措辞")
	if err != nil || revised.Sent || revised.Status != "generated" || body != "改成人工措辞" {
		t.Fatalf("revise = %+v %q %v", revised, body, err)
	}
	if _, _, err := ReviseDraft("sent", "不行"); err == nil {
		t.Fatal("sent draft was editable")
	}
	if AllowCreativeHandoff("after_sales", "creative_service") || AllowCreativeHandoff("consult", "creative_service") {
		t.Fatal("after-sales or consult grew a creative handoff")
	}
	if !AllowCreativeHandoff("sales", "creative_service") || AllowCreativeHandoff("sales", "merchant_customer") {
		t.Fatal("creative handoff gate drifted")
	}
	manual := PreferManual(ResolveNext(LeadView{Status: "new", AIScore: 80}, now), "2026-10-05T01:00:00Z", "")
	if manual.Source != "manual" || manual.AutoCall || manual.AutoMessage {
		t.Fatalf("prefer manual = %+v", manual)
	}
	ignoredSuggestion := IgnoreSuggestion(ResolveNext(LeadView{Status: "new", AIScore: 80}, now))
	if ignoredSuggestion.Source != "manual" || ignoredSuggestion.Kind != "none" || ignoredSuggestion.AutoCall {
		t.Fatalf("ignore suggestion = %+v", ignoredSuggestion)
	}
}

func TestTodayUsesShanghaiCalendarBeforeEight(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	// 01:30 in Shanghai is still the previous UTC date. 09:00 the same local
	// morning is the next UTC date, so a UTC day boundary hides it from today.
	now := time.Date(2026, 10, 4, 1, 30, 0, 0, loc).UTC()
	appointment := time.Date(2026, 10, 4, 9, 0, 0, 0, loc).UTC().Format(time.RFC3339)
	lead := LeadView{
		ID: "morning", Status: "in_progress", Assignee: "sales1",
		HasOpenFollowUp: true, ManualNextAt: appointment, ManualNextKind: "manual_follow_up",
	}
	if got := PlaceToday(lead, now); got != TodayDue {
		t.Fatalf("local morning appointment = %s, want %s", got, TodayDue)
	}
	evening := time.Date(2026, 10, 4, 20, 0, 0, 0, loc).UTC()
	dawn := time.Date(2026, 10, 5, 1, 0, 0, 0, loc).UTC().Format(time.RFC3339)
	nextDawn := lead
	nextDawn.ManualNextAt = dawn
	if got := PlaceToday(nextDawn, evening); got != TodayScheduled {
		t.Fatalf("next local dawn = %s, want %s", got, TodayScheduled)
	}
}

func TestTodayDraftAndReceptionWithLeadAreNotEmpty(t *testing.T) {
	now := time.Date(2026, 10, 4, 1, 30, 0, 0, time.UTC)
	const phone = "13800138000"
	const knowledge = "知识库不应进今天"
	lead := LeadView{
		ID: "lead1", TenantID: "tnt_a", ContactID: "c1", Status: "new", Assignee: "mem_sales1",
		ContactName: "甲商家", BusinessCategory: "merchant_customer", SourceType: "touch_campaign",
		SourceChannel: "碰一碰", SourceActivity: "camp-1", SourceAt: "2026-10-04T01:00:00Z",
		OwnerLabel: "Sales A1", ConsentStatus: "granted", Phone: phone,
		ChannelIdentity: "wx_openid_1", ChannelReplyAllowed: true,
		LastInteraction: "活动 camp-1 · 碰一碰", LastInteractionAt: "2026-10-04T01:00:00Z",
	}
	other := lead
	other.ID = "lead2"
	other.ContactID = "c2"
	third := lead
	third.ID = "lead3"
	third.ContactID = "c2"
	draft := DraftView{
		ID: "dr1", TenantID: "tnt_a", SessionID: "s1", LeadID: "lead1", Assignee: "mem_sales1",
		OwnerLabel: "Sales A1", Kind: "draft", Status: "generated", Body: "请确认这段回复",
	}
	session := ReceptionView{
		SessionID: "rs1", TenantID: "tnt_a", LeadID: "lead1", Assignee: "mem_sales1", OwnerLabel: "Sales A1",
		Mode: "human", Epoch: 2, Version: 3, HumanTodo: true, PendingReason: "human_takeover",
	}
	opps := []OpportunityView{
		{ID: "opp_tied", TenantID: "tnt_a", ContactID: "c1", Stage: "open", Category: "merchant_customer", Assignee: "mem_sales1"},
		{ID: "opp_orphan", TenantID: "tnt_a", ContactID: "nobody", Stage: "open", Category: "merchant_customer", Assignee: "mem_sales1"},
		{ID: "opp_many", TenantID: "tnt_a", ContactID: "c2", Stage: "proposal", Category: "merchant_customer", Assignee: "mem_sales1"},
	}
	res := BuildWithDrafts(Scope{Role: "sales", MemberID: "mem_sales1"}, now, []LeadView{lead, other, third}, opps, []ReceptionView{session}, []DraftView{draft})
	for _, spec := range []struct{ group, id string }{{TodayAIDraft, "dr1"}, {TodayHuman, "rs1"}} {
		item, ok := todayItem(res, spec.group, spec.id)
		if !ok {
			t.Fatalf("%s missing from %s", spec.id, spec.group)
		}
		if item.LeadID != "lead1" || item.Source.Channel == "" || item.Source.Activity == "" || item.Context.Customer == "" || item.OwnerLabel == "" || item.OwnerLabel == "mem_sales1" {
			t.Fatalf("%s facts = %+v", spec.id, item)
		}
		if len(item.AllowedContacts) == 0 || item.LastInteraction.Summary == "" || item.Next.Label == "" || item.Basis == "" {
			t.Fatalf("%s next/contact = %+v", spec.id, item)
		}
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), phone) || strings.Contains(string(raw), knowledge) || strings.Contains(item.OwnerLabel, "mem_") {
			t.Fatalf("%s leaked a phone, member id, or knowledge: %s", spec.id, raw)
		}
	}
	tied, ok := todayItem(res, TodayDue, "opp_tied")
	if !ok || tied.LeadID != "lead1" || tied.Context.Customer != "甲商家" || tied.Source.Channel == "" {
		t.Fatalf("tied opportunity = %+v ok=%v", tied, ok)
	}
	for _, group := range []string{TodayNewInquiry, TodayDue, TodayWaiting, TodayOverdue, TodayAIDraft, TodayHuman, TodayInfo} {
		if todayHas(res, group, "opp_orphan") || todayHas(res, group, "opp_many") {
			t.Fatalf("opportunity without one lead stayed in %s", group)
		}
	}
}

func TestReviseDraftUsesTheReceptionLimit(t *testing.T) {
	okBody := strings.Repeat("字", 2000)
	revised, body, err := ReviseDraft("generated", okBody)
	if err != nil || revised.Sent || revised.Status != "generated" || body != okBody {
		t.Fatalf("2000 runes = %+v %d %v", revised, len([]rune(body)), err)
	}
	if _, _, err := ReviseDraft("generated", strings.Repeat("字", 2001)); err == nil {
		t.Fatal("2001 runes was accepted")
	}
}

func todayItem(res Result, group, id string) (Item, bool) {
	for _, item := range res.Today[group] {
		if item.ID == id || item.LeadID == id || item.SessionID == id {
			return item, true
		}
	}
	return Item{}, false
}

func todayHas(res Result, group, id string) bool {
	_, ok := todayItem(res, group, id)
	return ok
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func bytesContainPhone(raw []byte) bool {
	text := string(raw)
	return strings.Contains(text, "138") || strings.Contains(text, "137")
}
