// Package outbound decides whether an AI call may be attempted.
//
// 生产自动外呼默认关闭。没有真实线路和客户明确许可时不能拨出。
// 公开来源、号码格式和 AI 分数都不是联系授权。本包不拨号、不扣费。
// 隔离演练必须标明模拟。接口 200 不是拨打成功，也不是空号检测成功。
package outbound

const (
	ModeProduction = "production"
	ModeIsolation  = "isolation"

	OpDial     = "dial"
	OpAuto     = "auto"
	OpRetry    = "retry"
	OpCancel   = "cancel"
	OpTransfer = "transfer"
	OpReceipt  = "receipt"

	KindSubmission = "dial_submission"
	KindRinging    = "ringing"
	KindConnected  = "connected"
	KindCompleted  = "call_completed"
	KindIntent     = "intent_suggestion"
	KindCancel     = "cancel"
	KindTransfer   = "human_transfer"

	StatusBlocked     = "blocked"
	StatusSimulated   = "simulated"
	StatusProviderAck = "provider_ack"
	StatusRecorded    = "recorded"

	RefusalAutoClosed   = "production_auto_closed"
	RefusalNoLine       = "no_real_line"
	RefusalConsent      = "consent_missing"
	RefusalBudget       = "budget_unbound"
	RefusalUnsubscribed = "unsubscribed"
	RefusalSuppressed   = "suppressed"
	RefusalWindow       = "outside_window"
	RefusalGlobalStop   = "global_stop"
	RefusalRejected     = "rejected_subject"
	RefusalUnreconciled = "original_unreconciled"
	RefusalLookup       = "lookup_required"
	RefusalCancelled    = "cancelled"
	RefusalHuman        = "human_transfer"
	RefusalSimulation   = "simulation_unlabeled"
	RefusalBinding      = "incomplete_binding"
)

// Input is the snapshot rechecked before a dial. Phone, recording, and
// transcript never belong in a public event.
type Input struct {
	Op                 string
	Mode               string
	ProductionAuto     bool
	RealLine           bool
	Simulation         bool
	ExplicitConsent    bool
	MarketingAllowed   bool
	Revoked            bool
	PublicSource       bool
	PhoneValid         bool
	AIScore            int
	BudgetBound        bool
	Unsubscribed       bool
	Suppressed         bool
	InsideWindow       bool
	GlobalStop         bool
	Rejected           bool
	CampaignID         string
	TaskKey            string
	DuplicateTask      bool
	Cancelled          bool
	HumanTransfer      bool
	OriginalLookedUp   bool
	OriginalReconciled bool
	ResultUnknown      bool
	ReceiptKind        string
	ProviderHTTP       int
	ClaimsDialSuccess  bool
	ClaimsEmptyNumber  bool
	ClaimsConnected    bool
	Phone              string
	Recording          string
	Transcript         string
	UsageCents         *int
}

// Receipt is one call fact. Kinds are not collapsed into a success flag.
type Receipt struct {
	Kind          string
	Status        string
	Simulation    bool
	RealConnected bool
}

// Result is the decision. DialSucceeded and RealConnected stay false when
// the only evidence is an HTTP status. LiveCharge stays 0.
type Result struct {
	OK                  bool
	Refusal             string
	Dialed              bool
	DialSucceeded       bool
	RealConnected       bool
	EmptyNumberDetected bool
	Simulation          bool
	LiveCharge          int
	CostKnown           bool
	CostCents           *int
	Replay              bool
	Receipts            []Receipt
	PublicEvent         string
}

// Decide applies the outbound gate. It does not place a call.
func Decide(in Input) Result {
	out := Result{LiveCharge: 0, Dialed: false, DialSucceeded: false, RealConnected: false, EmptyNumberDetected: false}
	out.Simulation = in.Mode == ModeIsolation || in.Simulation
	if in.TaskKey == "" || (in.Mode != ModeProduction && in.Mode != ModeIsolation) {
		out.Refusal = RefusalBinding
		out.PublicEvent = publicEvent(in, "", StatusBlocked, out.Simulation)
		return out
	}
	switch in.Op {
	case OpCancel:
		out.OK = true
		out.Receipts = []Receipt{{Kind: KindCancel, Status: StatusRecorded, Simulation: out.Simulation}}
	case OpTransfer:
		out.OK = true
		out.Receipts = []Receipt{{Kind: KindTransfer, Status: StatusRecorded, Simulation: out.Simulation}}
	case OpReceipt:
		out = decideReceipt(in, out)
	case OpDial, OpAuto, OpRetry:
		out = decideDial(in, out)
	default:
		out.Refusal = RefusalBinding
	}
	kind, status := "", StatusBlocked
	if len(out.Receipts) == 1 {
		kind, status = out.Receipts[0].Kind, out.Receipts[0].Status
	}
	out.PublicEvent = publicEvent(in, kind, status, out.Simulation)
	applyUsage(&out, in)
	return out
}

func decideDial(in Input, out Result) Result {
	if in.DuplicateTask {
		out.OK = true
		out.Replay = true
		out.Receipts = nil
		return out
	}
	if refusal := blocked(in, in.Op == OpAuto); refusal != "" {
		out.Refusal = refusal
		return out
	}
	if in.Op == OpRetry && in.ResultUnknown {
		if !in.OriginalLookedUp {
			out.Refusal = RefusalLookup
			return out
		}
		if !in.OriginalReconciled {
			out.Refusal = RefusalUnreconciled
			return out
		}
	}
	if in.Mode == ModeIsolation && !in.Simulation {
		out.Refusal = RefusalSimulation
		return out
	}
	if in.Mode == ModeProduction && !in.RealLine {
		out.Refusal = RefusalNoLine
		return out
	}
	out.OK = true
	out.Receipts = []Receipt{{
		Kind: KindSubmission, Status: receiptStatus(in, out.Simulation), Simulation: out.Simulation,
	}}
	return out
}

func decideReceipt(in Input, out Result) Result {
	if !knownReceipt(in.ReceiptKind) {
		out.Refusal = RefusalBinding
		return out
	}
	if refusal := blocked(in, false); refusal != "" {
		out.Refusal = refusal
		out.Receipts = []Receipt{{Kind: in.ReceiptKind, Status: StatusBlocked, Simulation: out.Simulation}}
		return out
	}
	if in.Mode == ModeIsolation && !in.Simulation {
		out.Refusal = RefusalSimulation
		return out
	}
	out.OK = true
	out.Receipts = []Receipt{{
		Kind: in.ReceiptKind, Status: receiptStatus(in, out.Simulation), Simulation: out.Simulation,
	}}
	return out
}

func blocked(in Input, auto bool) string {
	if auto && !in.ProductionAuto {
		return RefusalAutoClosed
	}
	if in.Cancelled {
		return RefusalCancelled
	}
	if in.HumanTransfer {
		return RefusalHuman
	}
	if in.GlobalStop {
		return RefusalGlobalStop
	}
	if in.Unsubscribed {
		return RefusalUnsubscribed
	}
	if in.Suppressed {
		return RefusalSuppressed
	}
	if in.Rejected {
		return RefusalRejected
	}
	if !in.InsideWindow {
		return RefusalWindow
	}
	if !in.BudgetBound {
		return RefusalBudget
	}
	if !consentCovers(in) {
		return RefusalConsent
	}
	return ""
}

func consentCovers(in Input) bool {
	if in.Revoked || !in.ExplicitConsent || !in.MarketingAllowed {
		return false
	}
	return true
}

func knownReceipt(kind string) bool {
	switch kind {
	case KindSubmission, KindRinging, KindConnected, KindCompleted, KindIntent:
		return true
	default:
		return false
	}
}

func receiptStatus(in Input, simulation bool) string {
	if simulation {
		return StatusSimulated
	}
	if in.ProviderHTTP > 0 || in.ClaimsDialSuccess || in.ClaimsEmptyNumber || in.ClaimsConnected {
		return StatusProviderAck
	}
	return StatusRecorded
}

func applyUsage(out *Result, in Input) {
	out.LiveCharge = 0
	out.CostKnown = false
	out.CostCents = nil
	if in.RealLine && in.UsageCents != nil {
		cents := *in.UsageCents
		out.CostKnown = true
		out.CostCents = &cents
	}
}

func publicEvent(in Input, kind, status string, simulation bool) string {
	mode := "production"
	if simulation {
		mode = "simulation"
	}
	if kind == "" {
		kind = "none"
	}
	if status == "" {
		status = "none"
	}
	return "outbound task=" + in.TaskKey + " kind=" + kind + " status=" + status + " mode=" + mode
}
