package outbound

import (
	"strings"
	"testing"
)

func openIsolation() Input {
	return Input{
		Op: OpDial, Mode: ModeIsolation, Simulation: true,
		ExplicitConsent: true, MarketingAllowed: true,
		BudgetBound: true, InsideWindow: true,
		TaskKey: "task-1", CampaignID: "camp-a",
	}
}

func TestProductionAutoStaysClosed(t *testing.T) {
	in := openIsolation()
	in.Op = OpAuto
	in.Mode = ModeProduction
	in.ProductionAuto = false
	in.RealLine = true
	in.AIScore = 99
	in.PhoneValid = true
	in.PublicSource = true
	got := Decide(in)
	if got.OK || got.Refusal != RefusalAutoClosed || got.Dialed || got.DialSucceeded || got.RealConnected || got.LiveCharge != 0 || got.CostKnown {
		t.Fatalf("auto closed: %+v", got)
	}
}

func TestExplicitAutoWithoutLineCannotDial(t *testing.T) {
	in := openIsolation()
	in.Op = OpAuto
	in.Mode = ModeProduction
	in.ProductionAuto = true
	in.RealLine = false
	got := Decide(in)
	if got.OK || got.Refusal != RefusalNoLine || got.Dialed || got.DialSucceeded || got.CostKnown || got.LiveCharge != 0 {
		t.Fatalf("no line: %+v", got)
	}
}

func TestPublicSourceAndScoreAreNotConsent(t *testing.T) {
	in := openIsolation()
	in.Mode = ModeProduction
	in.ProductionAuto = true
	in.RealLine = true
	in.ExplicitConsent = false
	in.MarketingAllowed = false
	in.PhoneValid = true
	in.PublicSource = true
	in.AIScore = 100
	got := Decide(in)
	if got.OK || got.Refusal != RefusalConsent || got.Dialed || got.DialSucceeded {
		t.Fatalf("not consent: %+v", got)
	}
}

func TestRevokedConsentBlocksDial(t *testing.T) {
	in := openIsolation()
	in.Revoked = true
	got := Decide(in)
	if got.Refusal != RefusalConsent || got.Dialed {
		t.Fatalf("revoked: %+v", got)
	}
}

func TestEachPreDialStop(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Input)
		want string
	}{
		{"budget", func(in *Input) { in.BudgetBound = false }, RefusalBudget},
		{"unsubscribe", func(in *Input) { in.Unsubscribed = true }, RefusalUnsubscribed},
		{"suppression", func(in *Input) { in.Suppressed = true }, RefusalSuppressed},
		{"window", func(in *Input) { in.InsideWindow = false }, RefusalWindow},
		{"global stop", func(in *Input) { in.GlobalStop = true }, RefusalGlobalStop},
		{"cancel", func(in *Input) { in.Cancelled = true }, RefusalCancelled},
		{"human", func(in *Input) { in.HumanTransfer = true }, RefusalHuman},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := openIsolation()
			tc.edit(&in)
			got := Decide(in)
			if got.OK || got.Refusal != tc.want || got.Dialed || got.DialSucceeded || got.RealConnected {
				t.Fatalf("%s: %+v", tc.name, got)
			}
		})
	}
}

func TestRejectedSubjectSurvivesCampaignSwitch(t *testing.T) {
	in := openIsolation()
	in.Rejected = true
	in.CampaignID = "camp-b"
	got := Decide(in)
	if got.OK || got.Refusal != RefusalRejected || got.Dialed {
		t.Fatalf("campaign switch: %+v", got)
	}
}

func TestIsolationMustBeLabeledSimulation(t *testing.T) {
	in := openIsolation()
	in.Simulation = false
	got := Decide(in)
	if got.OK || got.Refusal != RefusalSimulation || got.RealConnected || got.DialSucceeded {
		t.Fatalf("unlabeled: %+v", got)
	}
}

func TestSimulatedConnectIsNotReal(t *testing.T) {
	got := Decide(openIsolation())
	if !got.OK || !got.Simulation || got.Dialed || got.DialSucceeded || got.RealConnected || got.CostKnown || got.LiveCharge != 0 {
		t.Fatalf("simulation dial: %+v", got)
	}
	if len(got.Receipts) != 1 || got.Receipts[0].Kind != KindSubmission || !got.Receipts[0].Simulation || got.Receipts[0].RealConnected {
		t.Fatalf("submission receipt: %+v", got.Receipts)
	}
	if !strings.Contains(got.PublicEvent, "simulation") {
		t.Fatalf("public event: %s", got.PublicEvent)
	}
}

func TestHTTP200IsNotDialOrEmptySuccess(t *testing.T) {
	in := openIsolation()
	in.Op = OpReceipt
	in.ReceiptKind = KindConnected
	in.ProviderHTTP = 200
	in.ClaimsDialSuccess = true
	in.ClaimsEmptyNumber = true
	in.ClaimsConnected = true
	got := Decide(in)
	if !got.OK || got.DialSucceeded || got.EmptyNumberDetected || got.RealConnected || !got.Simulation || got.CostKnown || got.LiveCharge != 0 {
		t.Fatalf("http 200: %+v", got)
	}
	if len(got.Receipts) != 1 || got.Receipts[0].Kind != KindConnected || got.Receipts[0].RealConnected {
		t.Fatalf("connected receipt: %+v", got.Receipts)
	}
	if got.Receipts[0].Status == "success" || got.Receipts[0].Status == "dial_succeeded" || got.Receipts[0].Status == "empty_number" {
		t.Fatalf("status promoted provider ack: %+v", got.Receipts[0])
	}
}

func TestReceiptKindsStaySeparate(t *testing.T) {
	for _, kind := range []string{KindSubmission, KindRinging, KindConnected, KindCompleted, KindIntent} {
		in := openIsolation()
		in.Op = OpReceipt
		in.ReceiptKind = kind
		got := Decide(in)
		if !got.OK || len(got.Receipts) != 1 || got.Receipts[0].Kind != kind || got.RealConnected {
			t.Fatalf("kind %s: %+v", kind, got)
		}
	}
}

func TestDuplicateTaskDoesNotDialAgain(t *testing.T) {
	in := openIsolation()
	in.DuplicateTask = true
	got := Decide(in)
	if !got.OK || !got.Replay || got.Dialed || len(got.Receipts) != 0 {
		t.Fatalf("replay: %+v", got)
	}
}

func TestUnknownResultLooksUpOriginal(t *testing.T) {
	in := openIsolation()
	in.Op = OpRetry
	in.ResultUnknown = true
	in.OriginalLookedUp = false
	got := Decide(in)
	if got.OK || got.Refusal != RefusalLookup || got.Dialed || len(got.Receipts) != 0 {
		t.Fatalf("lookup: %+v", got)
	}
	in.OriginalLookedUp = true
	in.OriginalReconciled = false
	got = Decide(in)
	if got.OK || got.Refusal != RefusalUnreconciled || got.Dialed || len(got.Receipts) != 0 {
		t.Fatalf("unreconciled: %+v", got)
	}
}

func TestStopBlocksLaterProgressReceipt(t *testing.T) {
	in := openIsolation()
	in.Op = OpReceipt
	in.ReceiptKind = KindConnected
	in.ClaimsConnected = true
	in.GlobalStop = true
	got := Decide(in)
	if got.OK || got.Refusal != RefusalGlobalStop || got.RealConnected || got.DialSucceeded {
		t.Fatalf("stop receipt: %+v", got)
	}
}

func TestCancelAndTransferRecordWithoutDialing(t *testing.T) {
	cancel := Decide(Input{Op: OpCancel, Mode: ModeIsolation, Simulation: true, TaskKey: "task-1"})
	if !cancel.OK || cancel.Dialed || len(cancel.Receipts) != 1 || cancel.Receipts[0].Kind != KindCancel {
		t.Fatalf("cancel: %+v", cancel)
	}
	transfer := Decide(Input{Op: OpTransfer, Mode: ModeIsolation, Simulation: true, TaskKey: "task-1"})
	if !transfer.OK || transfer.Dialed || len(transfer.Receipts) != 1 || transfer.Receipts[0].Kind != KindTransfer {
		t.Fatalf("transfer: %+v", transfer)
	}
}

func TestPublicEventOmitsRecordingAndIdentity(t *testing.T) {
	in := openIsolation()
	in.Phone = "13800138000"
	in.Recording = "rec-secret-audio"
	in.Transcript = "客户说了预算八十万"
	got := Decide(in)
	for _, secret := range []string{"13800138000", "rec-secret-audio", "预算八十万", "138"} {
		if strings.Contains(got.PublicEvent, secret) {
			t.Fatalf("public event leaked %q: %s", secret, got.PublicEvent)
		}
	}
}

func TestUsageStaysUnknownWithoutRealLine(t *testing.T) {
	in := openIsolation()
	charge := 880
	in.UsageCents = &charge
	got := Decide(in)
	if got.CostKnown || got.CostCents != nil || got.LiveCharge != 0 {
		t.Fatalf("unknown cost: %+v", got)
	}
}

func TestRealLineUsageIsKnownButHTTPAckIsNot(t *testing.T) {
	in := openIsolation()
	in.Mode = ModeProduction
	in.RealLine = true
	in.Op = OpReceipt
	in.ReceiptKind = KindCompleted
	in.ProviderHTTP = 200
	charge := 880
	in.UsageCents = &charge
	got := Decide(in)
	if !got.CostKnown || got.CostCents == nil || *got.CostCents != 880 || got.LiveCharge != 0 || got.DialSucceeded || got.EmptyNumberDetected {
		t.Fatalf("line usage: %+v", got)
	}
}
