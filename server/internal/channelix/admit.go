// Package channelix admits one authorized comment or direct message.
//
// 发布授权不包含私信读取。评论和消息先是互动；只有明确的购买意向才建立线索候选。
// 没有真实渠道凭证时，验证状态保持 unverified，不把任何平台写成已支持。
// 本包不访问网络，也不代替接待核心发送回复。
package channelix

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	CapCommentRead = "comment.read"
	CapMessageRead = "message.read"
	CapReply       = "reply"
	CapPublish     = "publish"

	KindComment = "comment"
	KindMessage = "direct_message"

	PurposeSalesInquiry = "sales_inquiry"
	PurposeSupport      = "support"
	PurposeGeneralQA    = "general_qa"
	PurposeAftersales   = "aftersales"

	SpeakerLeads  = "leads_bot"
	SpeakerMatrix = "matrix_bot"
	SpeakerHuman  = "human"

	RefusalCapability   = "capability_missing"
	RefusalForgedTenant = "forged_tenant"
	RefusalForgedSource = "forged_source"
	RefusalSpeaker      = "speaker_conflict"
	RefusalRevoked      = "permission_revoked"
)

// Grant is the authorized connection snapshot. CredentialClaim is ignored:
// this build cannot verify a live channel credential.
type Grant struct {
	TenantID, Provider, AccountID, AppID string
	SubjectNS, MessageNS, PostNS         string
	Capabilities                         []string
	Revoked                              bool
	CredentialClaim                      string
}

// Event is one connector delivery. Target tenant and source fields must match
// the grant; the caller does not get to aim the event at another tenant.
type Event struct {
	TargetTenantID, Provider, AccountID, AppID string
	SubjectNS, MessageNS, PostNS               string
	EventID, Kind, SubjectID, Nickname, Phone  string
	Text                                       string
	ExplicitIntent                             bool
	Purpose                                    string
	Retracted                                  bool
	Score                                      int
	PhoneMarketingConsent                      bool
	SMSMarketingConsent                        bool
}

// Stored is the row already kept for this dedup key.
type Stored struct {
	InteractionID string
	CandidateID   string
	TextSHA256    string
	Retracted     bool
}

// Input is everything Admit may see.
type Input struct {
	Grant          Grant
	Event          Event
	Prior          *Stored
	Floor          string
	WantReply      bool
	HumanProcessed bool
}

// SubjectRef is the channel subject. Nickname and phone are not identity.
type SubjectRef struct {
	TenantID, Provider, AccountID, SubjectID, Nickname, Phone string
}

// Result is the admission decision. AutoReach and Delivered stay false:
// a score or a reply draft does not send anything.
type Result struct {
	OK                bool
	Refusal           string
	DedupKey          string
	InteractionID     string
	CandidateID       string
	TextSHA256        string
	CreateInteraction bool
	Update            bool
	CreateCandidate   bool
	Idempotent        bool
	Retract           bool
	WithdrawCandidate bool
	ChannelContact    bool
	PhoneMarketing    bool
	SMSMarketing      bool
	AutoReach         bool
	ReplyAllowed      bool
	ReplyRefusal      string
	Delivered         bool
	HumanProcessed    bool
	Verification      string
	LiveProviders     []string
}

// CapView is the public capability statement for this build.
type CapView struct {
	Verification              string
	LiveProviders             []string
	PublishImpliesMessageRead bool
}

// Admit decides what to keep. It never grants phone or SMS marketing from a
// missing number, and it never treats a high score as permission to reach out.
func Admit(in Input) Result {
	res := Result{
		DedupKey:      dedupKey(in.Grant.TenantID, in.Grant.Provider, in.Grant.AccountID, in.Event.EventID),
		TextSHA256:    hashText(in.Event.Text),
		Verification:  "unverified",
		LiveProviders: nil,
	}
	if in.Grant.Revoked {
		res.Refusal = RefusalRevoked
		return res
	}
	if strings.TrimSpace(in.Event.TargetTenantID) == "" || in.Event.TargetTenantID != in.Grant.TenantID {
		res.Refusal = RefusalForgedTenant
		return res
	}
	if in.Event.Provider != in.Grant.Provider || in.Event.AccountID != in.Grant.AccountID || in.Event.AppID != in.Grant.AppID ||
		in.Event.SubjectNS != in.Grant.SubjectNS || in.Event.MessageNS != in.Grant.MessageNS || in.Event.PostNS != in.Grant.PostNS {
		res.Refusal = RefusalForgedSource
		return res
	}
	need := capForKind(in.Event.Kind)
	if need == "" || !hasCap(in.Grant, need) {
		res.Refusal = RefusalCapability
		return res
	}
	res.OK = true
	res.ChannelContact = true
	res.PhoneMarketing = strings.TrimSpace(in.Event.Phone) != "" && in.Event.PhoneMarketingConsent
	res.SMSMarketing = strings.TrimSpace(in.Event.Phone) != "" && in.Event.SMSMarketingConsent
	res.HumanProcessed = in.HumanProcessed
	res.ReplyAllowed, res.ReplyRefusal = decideReply(in)

	if in.Event.Retracted {
		res.Retract = true
		if in.Prior != nil {
			res.InteractionID = in.Prior.InteractionID
			res.CandidateID = in.Prior.CandidateID
			res.WithdrawCandidate = in.Prior.CandidateID != "" && !in.Prior.Retracted
			res.Idempotent = in.Prior.Retracted
			return res
		}
		res.CreateInteraction = true
		return res
	}
	if in.Prior != nil {
		res.InteractionID = in.Prior.InteractionID
		res.CandidateID = in.Prior.CandidateID
		if in.Prior.TextSHA256 == res.TextSHA256 {
			res.Idempotent = true
			return res
		}
		res.Update = true
		if res.CandidateID == "" && explicitSales(in.Event) {
			res.CreateCandidate = true
		}
		return res
	}
	res.CreateInteraction = true
	res.CreateCandidate = explicitSales(in.Event)
	return res
}

// SharesIdentity is true only for the same tenant, provider, account, and subject.
func SharesIdentity(a, b SubjectRef) bool {
	return a.TenantID != "" && a.TenantID == b.TenantID &&
		a.Provider == b.Provider && a.AccountID == b.AccountID &&
		a.SubjectID != "" && a.SubjectID == b.SubjectID
}

// AuditLine is safe for logs. The customer text argument is never copied.
func AuditLine(tenant, provider, account, eventID, text string) string {
	_ = text
	return "tenant=" + tenant + " provider=" + provider + " account=" + account + " event=" + eventID + " text=redacted"
}

// InteractionPath returns an id-only path. Customer text is rejected.
func InteractionPath(id, text string) (string, error) {
	if text != "" {
		return "", errors.New("customer text is not a path")
	}
	if !token(id, 128) {
		return "", errors.New("interaction id")
	}
	return "/channel-interactions/" + id, nil
}

// Capability reports that no live provider is verified in this build.
func Capability() CapView {
	return CapView{Verification: "unverified"}
}

func decideReply(in Input) (bool, string) {
	if !in.WantReply {
		return false, ""
	}
	if !hasCap(in.Grant, CapReply) {
		return false, RefusalCapability
	}
	if in.Floor != "" && in.Floor != SpeakerLeads {
		return false, RefusalSpeaker
	}
	return true, ""
}

func explicitSales(ev Event) bool {
	return ev.ExplicitIntent && ev.Purpose == PurposeSalesInquiry
}

func capForKind(kind string) string {
	switch kind {
	case KindComment:
		return CapCommentRead
	case KindMessage:
		return CapMessageRead
	default:
		return ""
	}
}

func hasCap(g Grant, cap string) bool {
	for _, item := range g.Capabilities {
		if item == cap {
			return true
		}
	}
	return false
}

func dedupKey(tenant, provider, account, eventID string) string {
	return tenant + "/" + provider + "/" + account + "/" + eventID
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func token(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
