package sopreach

import "testing"

func bound() Input {
	return Input{
		Channel: ChannelSMS, Capability: true, Recipient: "13800000000",
		Purpose: PurposeFollowUp, ConsentPurpose: PurposeFollowUp, ConsentChannels: []string{ChannelSMS},
		TenantID: "ten_a", ActionTenantID: "ten_a", ContentVersion: 3,
		OperatorID: "mem_1", BudgetBound: true, InsideWindow: true, MaxAttempts: 1,
	}
}

func TestBalanceAndScoreDoNotOpenUnattended(t *testing.T) {
	in := bound()
	in.Op = OpAuto
	in.BalanceCents = 50000
	in.AIScore = 99
	got := Decide(in)
	if got.Unattended || got.OK || got.Refusal != RefusalUnattendedClosed || got.Delivered || got.LiveCharge != 0 {
		t.Fatalf("balance or score opened automation: %+v", got)
	}
}

func TestExplicitUnattendedStillCannotClaimDelivery(t *testing.T) {
	in := bound()
	in.Op = OpAuto
	in.Level = LevelUnattended
	in.ExplicitUnattended = true
	in.LiveChannel = true
	in.BalanceCents = 1
	in.AIScore = 100
	got := Decide(in)
	if !got.Unattended || !got.OK || got.Delivered || got.LiveCharge != 0 {
		t.Fatalf("explicit run invented delivery: %+v", got)
	}
	if status(got, KindDelivery) != StatusUndelivered || status(got, KindSubmission) != StatusPendingSend {
		t.Fatalf("records: %+v", got.Records)
	}
	if hasStatus(got, StatusDelivered) {
		t.Fatalf("wrote delivered: %+v", got.Records)
	}
}

func TestReminderDraftAndHumanConfirmStayUndelivered(t *testing.T) {
	remind := bound()
	remind.Op = OpRemind
	draft := bound()
	draft.Op = OpDraft
	confirm := bound()
	confirm.Op = OpConfirm
	confirm.LiveChannel = true
	steps := []Result{Decide(remind), Decide(draft), Decide(confirm)}
	wantKind := []string{KindReminder, KindDraft, KindSubmission}
	for i, got := range steps {
		if !got.OK || got.Delivered || got.LiveCharge != 0 || status(got, wantKind[i]) == "" {
			t.Fatalf("step %d: %+v", i, got)
		}
		if hasStatus(got, StatusDelivered) {
			t.Fatalf("step %d delivered: %+v", i, got.Records)
		}
	}
	if status(steps[2], KindDelivery) != StatusUndelivered {
		t.Fatalf("confirm delivery: %+v", steps[2].Records)
	}
	if status(steps[0], KindReminder) != StatusRecorded || status(steps[1], KindDraft) != StatusRecorded {
		t.Fatalf("reminder/draft mixed with send: %+v %+v", steps[0].Records, steps[1].Records)
	}
}

func TestUnsubscribeAndRejectionBlockSend(t *testing.T) {
	unsub := bound()
	unsub.Op = OpConfirm
	unsub.Unsubscribed = true
	if got := Decide(unsub); got.OK || got.Refusal != RefusalUnsubscribed || got.Delivered {
		t.Fatalf("unsubscribe: %+v", got)
	}
	rejected := bound()
	rejected.Op = OpConfirm
	rejected.Rejected = true
	if got := Decide(rejected); got.OK || got.Refusal != RefusalRejected || got.Delivered {
		t.Fatalf("rejected schedule: %+v", got)
	}
}

func TestRetryReconcilesOnceAndDoesNotInventDelivery(t *testing.T) {
	early := bound()
	early.Op = OpRetry
	early.TimedOut = true
	early.Attempt = 1
	if got := Decide(early); got.OK || got.Refusal != RefusalUnreconciled || got.Delivered {
		t.Fatalf("retry before reconcile: %+v", got)
	}
	once := early
	once.OriginalReconciled = true
	got := Decide(once)
	if !got.OK || got.Delivered || status(got, KindDelivery) != StatusUndelivered {
		t.Fatalf("reconciled retry: %+v", got)
	}
	again := once
	again.Attempt = 2
	if got := Decide(again); got.OK || got.Refusal != RefusalAttempts || got.Delivered {
		t.Fatalf("second retry: %+v", got)
	}
}

func TestConsultationIsNotMarketingConsent(t *testing.T) {
	in := bound()
	in.Op = OpConfirm
	in.Purpose = PurposeMarketing
	in.ConsultationOnly = true
	in.MarketingAllowed = false
	in.ConsentPurpose = PurposeFollowUp
	if got := Decide(in); got.OK || got.Refusal != RefusalConsent || got.Delivered {
		t.Fatalf("consultation marketed: %+v", got)
	}
	other := bound()
	other.Op = OpConfirm
	other.Channel = ChannelEmail
	if got := Decide(other); got.OK || got.Refusal != RefusalConsent {
		t.Fatalf("sms consent sent email: %+v", got)
	}
}

func TestStopsAndHumanTakeoverBlockAutomation(t *testing.T) {
	for _, mutate := range []func(*Input){
		func(in *Input) { in.GlobalStop = true },
		func(in *Input) { in.CustomerStop = true },
		func(in *Input) { in.Revoked = true },
		func(in *Input) { in.RateLimited = true },
		func(in *Input) { in.InsideWindow = false },
	} {
		in := bound()
		in.Op = OpConfirm
		mutate(&in)
		if got := Decide(in); got.OK || got.Delivered || got.Refusal == "" {
			t.Fatalf("gate missed: %+v", got)
		}
	}
	auto := bound()
	auto.Op = OpAuto
	auto.Level = LevelUnattended
	auto.ExplicitUnattended = true
	auto.HumanTakeover = true
	if got := Decide(auto); got.OK || got.Refusal != RefusalHumanTakeover || got.Delivered {
		t.Fatalf("takeover still auto-replied: %+v", got)
	}
	manual := auto
	manual.Op = OpConfirm
	got := Decide(manual)
	if !got.OK || got.Delivered || status(got, KindDelivery) != StatusUndelivered {
		t.Fatalf("human confirm after takeover: %+v", got)
	}
}

func TestUserReplyIsItsOwnRecord(t *testing.T) {
	in := bound()
	in.Op = OpUserReply
	in.HumanTakeover = true
	got := Decide(in)
	if !got.OK || got.Delivered || status(got, KindUserReply) != StatusRecorded || len(got.Records) != 1 {
		t.Fatalf("user reply: %+v", got)
	}
}

func status(got Result, kind string) string {
	for _, rec := range got.Records {
		if rec.Kind == kind {
			return rec.Status
		}
	}
	return ""
}

func hasStatus(got Result, status string) bool {
	for _, rec := range got.Records {
		if rec.Status == status {
			return true
		}
	}
	return false
}
