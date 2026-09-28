// Package sopreach decides the first slice of multi-channel follow-up.
//
// 首版只产生内部提醒、待发送草稿和人工确认。无人值守默认关闭，余额和
// AI 分数都不能把它打开。本包不访问网络、不发送短信/邮件/企微、不扣费。
// 没有渠道回执时，确认结果停在待发送和未送达，不能写成已送达。
package sopreach

const (
	LevelRemind     = "remind_draft_confirm"
	LevelUnattended = "unattended"

	ChannelSMS   = "sms"
	ChannelEmail = "email"
	ChannelWecom = "wecom"

	PurposeFollowUp  = "follow_up"
	PurposeMarketing = "marketing"

	OpRemind    = "remind"
	OpDraft     = "draft"
	OpConfirm   = "confirm"
	OpRetry     = "retry"
	OpAuto      = "auto"
	OpUserReply = "user_reply"

	KindReminder   = "internal_reminder"
	KindDraft      = "pending_draft"
	KindSubmission = "channel_submission"
	KindDelivery   = "channel_delivery"
	KindUserReply  = "user_reply"

	StatusRecorded    = "recorded"
	StatusPendingSend = "pending_send"
	StatusUndelivered = "undelivered"
	StatusBlocked     = "blocked"
	StatusDelivered   = "delivered"

	RefusalUnattendedClosed = "unattended_closed"
	RefusalHumanTakeover    = "human_takeover"
	RefusalUnsubscribed     = "unsubscribed"
	RefusalRejected         = "rejected_schedule"
	RefusalUnreconciled     = "original_unreconciled"
	RefusalAttempts         = "attempt_exhausted"
	RefusalConsent          = "consent_scope"
	RefusalBinding          = "incomplete_binding"
	RefusalRevoked          = "revoked"
	RefusalRate             = "rate_limited"
	RefusalWindow           = "outside_window"
	RefusalGlobalStop       = "global_stop"
	RefusalCustomerStop     = "customer_stop"
	RefusalCapability       = "capability_missing"
	RefusalTenant           = "tenant_mismatch"
)

// Input is the snapshot rechecked at decision time. LiveChannel and a high
// score are not a provider receipt.
type Input struct {
	Op                 string
	Level              string
	ExplicitUnattended bool
	BalanceCents       int
	AIScore            int
	Channel            string
	Capability         bool
	LiveChannel        bool
	Recipient          string
	Purpose            string
	ConsentPurpose     string
	ConsentChannels    []string
	ConsultationOnly   bool
	MarketingAllowed   bool
	Revoked            bool
	TenantID           string
	ActionTenantID     string
	ContentVersion     int
	OperatorID         string
	BudgetBound        bool
	Unsubscribed       bool
	RateLimited        bool
	InsideWindow       bool
	HumanTakeover      bool
	GlobalStop         bool
	CustomerStop       bool
	Rejected           bool
	Attempt            int
	MaxAttempts        int
	TimedOut           bool
	OriginalReconciled bool
}

// Record is one fact. Kinds are not collapsed into a single send status.
type Record struct {
	Kind   string
	Status string
}

// Result is the decision. Delivered stays false without a provider receipt,
// which this package never invents. LiveCharge stays 0.
type Result struct {
	OK         bool
	Refusal    string
	Unattended bool
	Delivered  bool
	LiveCharge int
	Records    []Record
}

// Decide applies the first-version gate. It does not send or charge.
func Decide(in Input) Result {
	_, unattended := openLevel(in)
	out := Result{Unattended: unattended, Delivered: false, LiveCharge: 0}
	if in.Op == OpAuto && !unattended {
		out.Refusal = RefusalUnattendedClosed
		return out
	}
	if in.Op == OpAuto && in.HumanTakeover {
		out.Refusal = RefusalHumanTakeover
		return out
	}
	switch in.Op {
	case OpRemind:
		if refusal := bind(in, false); refusal != "" {
			out.Refusal = refusal
			return out
		}
		out.OK = true
		out.Records = []Record{{Kind: KindReminder, Status: StatusRecorded}}
	case OpDraft:
		if refusal := bind(in, false); refusal != "" {
			out.Refusal = refusal
			return out
		}
		out.OK = true
		out.Records = []Record{{Kind: KindDraft, Status: StatusRecorded}}
	case OpUserReply:
		if in.TenantID == "" || in.TenantID != in.ActionTenantID || in.OperatorID == "" {
			out.Refusal = RefusalBinding
			return out
		}
		out.OK = true
		out.Records = []Record{{Kind: KindUserReply, Status: StatusRecorded}}
	case OpConfirm, OpRetry, OpAuto:
		if refusal := gate(in); refusal != "" {
			out.Refusal = refusal
			out.Records = []Record{{Kind: KindSubmission, Status: StatusBlocked}}
			return out
		}
		out.OK = true
		out.Records = []Record{
			{Kind: KindSubmission, Status: StatusPendingSend},
			{Kind: KindDelivery, Status: StatusUndelivered},
		}
	default:
		out.Refusal = RefusalBinding
	}
	return out
}

func openLevel(in Input) (string, bool) {
	if in.ExplicitUnattended && in.Level == LevelUnattended {
		return LevelUnattended, true
	}
	return LevelRemind, false
}

func bind(in Input, sending bool) string {
	if in.TenantID == "" || in.TenantID != in.ActionTenantID {
		return RefusalTenant
	}
	if in.OperatorID == "" || in.Recipient == "" || in.ContentVersion < 1 || !in.BudgetBound {
		return RefusalBinding
	}
	if in.Channel != ChannelSMS && in.Channel != ChannelEmail && in.Channel != ChannelWecom {
		return RefusalBinding
	}
	if in.Purpose != PurposeFollowUp && in.Purpose != PurposeMarketing {
		return RefusalBinding
	}
	if sending && !in.Capability {
		return RefusalCapability
	}
	return ""
}

func gate(in Input) string {
	if refusal := bind(in, true); refusal != "" {
		return refusal
	}
	if in.GlobalStop {
		return RefusalGlobalStop
	}
	if in.CustomerStop {
		return RefusalCustomerStop
	}
	if in.Revoked {
		return RefusalRevoked
	}
	if in.Unsubscribed {
		return RefusalUnsubscribed
	}
	if in.Rejected {
		return RefusalRejected
	}
	if in.RateLimited {
		return RefusalRate
	}
	if !in.InsideWindow {
		return RefusalWindow
	}
	if !consentCovers(in) {
		return RefusalConsent
	}
	if in.Op == OpRetry {
		if in.TimedOut && !in.OriginalReconciled {
			return RefusalUnreconciled
		}
		max := in.MaxAttempts
		if max < 1 {
			max = 1
		}
		if in.Attempt > max {
			return RefusalAttempts
		}
	}
	return ""
}

func consentCovers(in Input) bool {
	if in.Revoked || in.ConsentPurpose != in.Purpose {
		return false
	}
	if in.Purpose == PurposeMarketing && (!in.MarketingAllowed || in.ConsultationOnly) {
		return false
	}
	if in.ConsultationOnly && in.Purpose == PurposeMarketing {
		return false
	}
	for _, ch := range in.ConsentChannels {
		if ch == in.Channel {
			return true
		}
	}
	return false
}
