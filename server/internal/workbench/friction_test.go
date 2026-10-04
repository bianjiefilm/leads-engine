package workbench

import (
	"strings"
	"testing"
	"time"
)

func TestFrictionRecordKeepsUnmeasuredUnknown(t *testing.T) {
	got := TodayFriction()
	if got.Task != "HUI-2591" {
		t.Fatalf("task = %q", got.Task)
	}
	wantSteps := []string{
		"看到今天的待办",
		"在同一屏写下跟进",
		"选择等待客户或下一步时间",
		"提交",
	}
	gotSteps := strings.Split(got.CurrentSteps, "\n")
	if len(gotSteps) != len(wantSteps) {
		t.Fatalf("current_steps = %q", got.CurrentSteps)
	}
	for i := range wantSteps {
		if gotSteps[i] != wantSteps[i] {
			t.Fatalf("step %d = %q", i, gotSteps[i])
		}
	}
	if got.BaselineSteps != "not_measured" {
		t.Fatalf("baseline_steps = %q", got.BaselineSteps)
	}
	if got.Reduction != "not_claimed" {
		t.Fatalf("reduction = %q", got.Reduction)
	}
	if got.ActiveTimeMS != "not_measured" || got.HumanActiveTime != "not_measured" {
		t.Fatalf("active time = %q %q", got.ActiveTimeMS, got.HumanActiveTime)
	}
	if got.RepeatInput != "只问变化项，不重复已确认事实" {
		t.Fatalf("repeat_input = %q", got.RepeatInput)
	}
	if got.FirstJudgable != "待办行上已有来源、上下文和下一步，提交前就能判断" {
		t.Fatalf("first_judgable = %q", got.FirstJudgable)
	}
	if got.Recovery != "离场草稿按租户记住；未计时" {
		t.Fatalf("recovery = %q", got.Recovery)
	}
	for _, field := range []string{got.BaselineSteps, got.Reduction, got.ActiveTimeMS, got.HumanActiveTime} {
		if field == "0" || field == "pass" || field == "ok" || field == "success" || field == "done" {
			t.Fatalf("unknown written as success: %q", field)
		}
	}
}

func TestFrictionPathStillPlacesWaitingOrNextWithoutAutoCall(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	waiting := LeadView{
		ID: "wait1", Status: "in_progress", Assignee: "sales1",
		HasCompletedFollowUp: true, WaitingCustomer: true,
	}
	if got := PlaceToday(waiting, now); got != TodayWaiting {
		t.Fatalf("waiting customer = %s", got)
	}
	scheduled := LeadView{
		ID: "next1", Status: "in_progress", Assignee: "sales1",
		HasOpenFollowUp: true, ManualNextAt: "2026-10-04T15:00:00Z", ManualNextKind: "manual_follow_up",
		AIScore: 99,
	}
	if got := PlaceToday(scheduled, now); got != TodayDue {
		t.Fatalf("same-day next = %s", got)
	}
	action := ApplyAIScore(ResolveNext(scheduled, now), 99)
	if action.AutoCall || action.AutoMessage || action.CreateOrder {
		t.Fatalf("score armed outreach: %+v", action)
	}
	if action.Source != "manual" {
		t.Fatalf("manual next = %+v", action)
	}
}
