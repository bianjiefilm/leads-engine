// Package workbench is the pure decision core for the sales desk (HUI-1893).
//
// It never places a call, sends a message, creates an order, or charges a
// fee. Callers pass facts they already stored; this package only classifies
// them. A manual next action always beats a suggestion, including any AI score.
package workbench

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// ModelAdviceMissing is the only intent line this desk may show.
	// There is no model call behind it.
	ModelAdviceMissing = "真实模型未完成"

	// JointChainIncomplete is the only chain line this slice may show.
	// A local fixture is not a Touch delivery, and a missing Notify receipt
	// is not a completed follow-up.
	JointChainIncomplete = "联合经营链未完成"

	BucketUnprocessed         = "unprocessed"
	BucketDueToday            = "due_today"
	BucketWaitingReply        = "waiting_reply"
	BucketStale               = "stale"
	BucketAssignmentException = "assignment_exception"
	BucketNeedsSchedule       = "needs_schedule"
	BucketHumanTakeover       = "human_takeover"

	// Today groups are the one homepage. HUI-2626 restyles this same
	// projection; it must not grow a second desk or a second state machine.
	TodayNewInquiry = "new_inquiry"
	TodayDue        = "due_today"
	TodayWaiting    = "waiting_customer"
	TodayOverdue    = "overdue"
	TodayAIDraft    = "ai_draft"
	TodayHuman      = "human_takeover"
	TodayInfo       = "info_or_permission"
	TodayScheduled  = "scheduled_next"

	staleAfterDays = 7
	relNone        = 0
	relBefore      = 1
	relToday       = 2
	relAfter       = 3

	// DraftBodyMaxRunes matches reception messages. There is no tenant
	// timezone column; today and overdue use Asia/Shanghai, the same clock
	// as the SOP window.
	DraftBodyMaxRunes = 2000
)

// Scope is the caller's existing record scope. Owner sees the tenant.
// Sales and agents see only rows assigned to themselves.
type Scope struct {
	Role     string
	MemberID string
}

// LeadView is one lead plus the facts the desk is allowed to judge.
type LeadView struct {
	ID                   string
	TenantID             string
	ContactID            string
	Status               string
	FilterReason         string
	Assignee             string
	Phone                string
	ChannelIdentity      string
	ChannelReplyAllowed  bool
	MarketingSMSOrPhone  bool
	Purpose              string
	SourceForm           string
	SourceActivity       string
	SourceSubmission     string
	SourceChannel        string
	SourceAt             string
	CreatedAt            string
	UpdatedAt            string
	SubmittedAt          string
	HasOpenFollowUp      bool
	HasCompletedFollowUp bool
	ManualNextAt         string
	ManualNextKind       string
	AIScore              int
	OwnerLabel           string
	CRMReceiptAt         string
	ContactName          string
	BusinessCategory     string
	SourceType           string
	ConsentStatus        string
	LastInteraction      string
	LastInteractionAt    string
	WaitingCustomer      bool
}

// OpportunityView is one native opportunity. AmountKind keeps the three
// money facts apart: customer_deal, painuo_service, platform_tool.
type OpportunityView struct {
	ID            string
	TenantID      string
	ContactID     string
	Stage         string
	Category      string
	Assignee      string
	AmountCents   *int64
	AmountKind    string
	UpdatedAt     string
	HasManualNext bool
}

// ReceptionView is one fact already produced by the reception core.
// OwnerLabel is a display name. Assignee stays the member id used only for scope.
type ReceptionView struct {
	SessionID     string
	TenantID      string
	LeadID        string
	Assignee      string
	OwnerLabel    string
	Mode          string
	Epoch         int
	Version       int
	PendingReason string
	WaitingReply  bool
	HumanTodo     bool
	Purpose       string
}

// DraftView is one generated reply still waiting for a person.
// Confirming or ignoring it never sends.
type DraftView struct {
	ID         string
	TenantID   string
	SessionID  string
	LeadID     string
	Assignee   string
	OwnerLabel string
	Kind       string
	Status     string
	Body       string
}

// ConfirmedContext is what this customer already established.
// Ask lists gaps only. A later change is asked by itself.
type ConfirmedContext struct {
	ContactID string   `json:"contact_id,omitempty"`
	Customer  string   `json:"customer,omitempty"`
	Business  string   `json:"business,omitempty"`
	Facts     []string `json:"facts,omitempty"`
	Ask       []string `json:"ask,omitempty"`
}

// Interaction is the latest stored moment, without a phone number.
type Interaction struct {
	At      string `json:"at,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// DraftDecision is the human choice on a generated draft. Sent stays false
// unless the reply was already sent before this call.
type DraftDecision struct {
	Status      string
	Sent        bool
	AutoCall    bool
	AutoMessage bool
}

// ReceptionFact is the only reception line this desk may show.
// It names the current holder without a member id, a phone, or knowledge text.
type ReceptionFact struct {
	Takeover   bool   `json:"takeover"`
	OwnerLabel string `json:"owner_label,omitempty"`
	Label      string `json:"label"`
	Epoch      int    `json:"epoch"`
	Version    int    `json:"version"`
}

// NextAction is advisory unless Source is manual. The three side-effect
// flags stay false: this desk does not dial, message, or order.
type NextAction struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	At          string `json:"at,omitempty"`
	Label       string `json:"label"`
	AutoCall    bool   `json:"auto_call"`
	AutoMessage bool   `json:"auto_message"`
	CreateOrder bool   `json:"create_order"`
}

// SourceLine is the provenance a seller can read without leaving the record.
type SourceLine struct {
	Form     string `json:"form,omitempty"`
	Activity string `json:"activity,omitempty"`
	Channel  string `json:"channel,omitempty"`
	At       string `json:"at,omitempty"`
}

// StatusFacts are copied from stored facts. Paid is true only when the
// caller passes a payment fact; a won stage is not a payment.
type StatusFacts struct {
	Submitted bool `json:"submitted"`
	Received  bool `json:"received"`
	Assigned  bool `json:"assigned"`
	Followed  bool `json:"followed"`
	Won       bool `json:"won"`
	Paid      bool `json:"paid"`
}

// Money keeps three ledgers side by side. There is no combined total.
type Money struct {
	CustomerDealCents       *int64 `json:"customer_deal_cents"`
	PainuoServiceOrderCents *int64 `json:"painuo_service_order_cents"`
	PlatformToolSpendCents  *int64 `json:"platform_tool_spend_cents"`
}

// Billing reports the charge for ordinary CRM work. It is always zero.
type Billing struct {
	OrdinaryCRMChargeCents int64 `json:"ordinary_crm_charge_cents"`
}

// Automation is the side-effect switch the desk refuses to flip.
type Automation struct {
	AutoCall    bool `json:"auto_call"`
	AutoMessage bool `json:"auto_message"`
	CreateOrder bool `json:"create_order"`
}

// Item is one row in a desk bucket.
type Item struct {
	ID               string           `json:"id"`
	TenantID         string           `json:"tenant_id,omitempty"`
	Bucket           string           `json:"bucket"`
	Kind             string           `json:"kind"`
	LeadID           string           `json:"lead_id,omitempty"`
	OpportunityID    string           `json:"opportunity_id,omitempty"`
	SessionID        string           `json:"session_id,omitempty"`
	Assignee         string           `json:"assignee,omitempty"`
	Reason           string           `json:"reason,omitempty"`
	Next             NextAction       `json:"next"`
	Source           SourceLine       `json:"source"`
	Statuses         StatusFacts      `json:"statuses"`
	ForceOpportunity bool             `json:"force_opportunity"`
	ShowServiceDraft bool             `json:"show_service_draft"`
	FilterReason     string           `json:"filter_reason,omitempty"`
	OwnerLabel       string           `json:"owner_label,omitempty"`
	AssignmentReason string           `json:"assignment_reason,omitempty"`
	AllowedContacts  []string         `json:"allowed_contacts,omitempty"`
	Sync             SyncFact         `json:"sync"`
	OutreachNotice   string           `json:"outreach_notice,omitempty"`
	ModelAdvice      string           `json:"model_advice,omitempty"`
	ServiceDraft     ServiceDraftDesk `json:"service_draft"`
	Reception        *ReceptionFact   `json:"reception,omitempty"`
	Context          ConfirmedContext `json:"context"`
	LastInteraction  Interaction      `json:"last_interaction"`
	Basis            string           `json:"basis,omitempty"`
	TodayGroup       string           `json:"today_group,omitempty"`
	Ask              []string         `json:"ask,omitempty"`
	DraftBody        string           `json:"draft_body,omitempty"`
}

// SyncFact is the external receipt. It has no count. Unknown is not zero
// and it is not a follow-up.
type SyncFact struct {
	CRM string `json:"crm"`
}

// ServiceDraftDesk is the workbench control. Present is false for ordinary
// sales until category, permission, and human confirmation all hold.
// Enabled never means this process handed a draft to another engine.
type ServiceDraftDesk struct {
	Present bool   `json:"present"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// TimelineEvent is one authoritative moment. ContactPlaintext is filled
// only for a caller who may read that business record.
type TimelineEvent struct {
	At               string `json:"at"`
	Kind             string `json:"kind"`
	Summary          string `json:"summary"`
	ContactPlaintext string `json:"contact_plaintext,omitempty"`
}

// JointChain says whether Touch delivery and Notify receipt were proven
// for this desk. This slice never proves them.
type JointChain struct {
	Status         string `json:"status"`
	Label          string `json:"label"`
	TouchDelivered bool   `json:"touch_delivered"`
}

// Result is the whole desk for one caller.
type Result struct {
	Scope             string            `json:"scope"`
	Buckets           map[string][]Item `json:"buckets"`
	Money             Money             `json:"money"`
	Billing           Billing           `json:"billing"`
	Automation        Automation        `json:"automation"`
	JointChain        JointChain        `json:"joint_chain"`
	OutreachSubmitted bool              `json:"outreach_submitted"`
	Today             map[string][]Item `json:"today"`
}

// OrdinaryCRMChargeCents is the fee for storing, opening, or hand-writing
// a follow-up. Ordinary work is not metered per row.
func OrdinaryCRMChargeCents(action string) int64 {
	switch action {
	case "ingest", "view", "manual_follow_up":
		return 0
	default:
		return 0
	}
}

// ShowServiceDraft is the HUI-1749 affordance. Store-consumer opportunities
// do not grow a creative-service handoff button.
func ShowServiceDraft(category string) bool {
	return category == "creative_service"
}

// RefuseSalesPush reports purposes that must not be marched into a sales opportunity.
func RefuseSalesPush(purpose string) bool {
	switch purpose {
	case "marketing_refused", "after_sales", "consult":
		return true
	default:
		return false
	}
}

// DisplayFilterReason drops a false invalid_phone mark when the lead has no
// phone but does have an authorized channel identity.
func DisplayFilterReason(lead LeadView) string {
	if strings.TrimSpace(lead.Phone) == "" && strings.TrimSpace(lead.ChannelIdentity) != "" && lead.FilterReason == "invalid_phone" {
		return ""
	}
	return lead.FilterReason
}

// MarketingChannels lists sms/phone only when that permit is itself a stored
// fact. An in-channel reply flag does not add either channel.
func MarketingChannels(lead LeadView) []string {
	if lead.MarketingSMSOrPhone {
		return []string{"sms", "phone"}
	}
	return nil
}

// ResolveNext picks the next step. A stored manual time wins. Suggestions
// never arm a call, a message, or an order.
func ResolveNext(lead LeadView, now time.Time) NextAction {
	_ = now
	if lead.ManualNextAt != "" {
		kind := lead.ManualNextKind
		if kind == "" {
			if channelEligible(lead) {
				kind = "channel_follow_up"
			} else {
				kind = "manual_follow_up"
			}
		}
		return safeAction(kind, "manual", lead.ManualNextAt)
	}
	if lead.WaitingCustomer {
		return safeAction("wait_customer", "manual", "")
	}
	if channelEligible(lead) {
		return safeAction("channel_follow_up", "suggestion", "")
	}
	if lead.Status == "new" || lead.Status == "in_progress" {
		return safeAction("suggest_schedule", "suggestion", "")
	}
	return safeAction("none", "suggestion", "")
}

// ApplyAIScore ignores the score. The returned action cannot dial, message, or order.
func ApplyAIScore(action NextAction, score int) NextAction {
	_ = score
	action.AutoCall = false
	action.AutoMessage = false
	action.CreateOrder = false
	return action
}

func safeAction(kind, source, at string) NextAction {
	return NextAction{Kind: kind, Source: source, At: at, Label: actionLabel(kind)}
}

func actionLabel(kind string) string {
	switch kind {
	case "manual_follow_up":
		return "按手工安排跟进"
	case "channel_follow_up":
		return "在授权渠道内跟进"
	case "suggest_schedule":
		return "安排下一次跟进"
	case "wait_customer":
		return "等待客户回复"
	case "confirm_draft":
		return "确认或修改草稿"
	default:
		return "无系统动作"
	}
}

func channelEligible(lead LeadView) bool {
	return strings.TrimSpace(lead.Phone) == "" && strings.TrimSpace(lead.ChannelIdentity) != "" && lead.ChannelReplyAllowed
}

// FollowThroughKey identifies one saved follow-up. The same normalized
// facts always hash to the same key; a different note does not.
func FollowThroughKey(leadID, note, nextAt, channel string, complete bool, createdBy, disposition string) string {
	bit := "0"
	if complete {
		bit = "1"
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		leadID, note, nextAt, channel, bit, createdBy, disposition,
	}, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// ProjectSync reads a receipt timestamp. Empty means unknown, never a count.
func ProjectSync(receiptAt string) SyncFact {
	if strings.TrimSpace(receiptAt) == "" {
		return SyncFact{CRM: "unknown"}
	}
	return SyncFact{CRM: "received"}
}

// DisplayOwner is the seller's name. A bare member id is never the label.
func DisplayOwner(lead LeadView) string {
	label := strings.TrimSpace(lead.OwnerLabel)
	if lead.Assignee != "" && label == "" {
		return "已分配"
	}
	return label
}

// DisplayAssignmentReason explains an empty assignee. Assigned rows stay blank.
func DisplayAssignmentReason(lead LeadView) string {
	if strings.TrimSpace(lead.Assignee) == "" {
		return "待分配"
	}
	return ""
}

// AllowedContacts lists stored permits only. A phone number on the contact
// is not itself a permit, and the raw identity is not returned.
func AllowedContacts(lead LeadView) []string {
	out := []string{}
	if lead.MarketingSMSOrPhone {
		out = append(out, "sms", "phone")
	}
	if strings.TrimSpace(lead.ChannelIdentity) != "" && lead.ChannelReplyAllowed {
		out = append(out, "channel")
	}
	return out
}

// OutreachLabel is the success sentence. It exists only when a marketing
// permit and a send receipt are both already stored.
func OutreachLabel(marketingAllowed, sendReceipt bool) string {
	if marketingAllowed && sendReceipt {
		return "自动触达已成功"
	}
	return ""
}

// OutreachNotice explains the gap without using the success sentence.
// A permit plus a receipt still does not mean this desk submitted outreach.
func OutreachNotice(marketingAllowed, sendReceipt bool) string {
	if marketingAllowed && sendReceipt {
		return ""
	}
	if !marketingAllowed {
		return "没有营销许可"
	}
	return "没有触达回执"
}

// DeskSubmittedOutreach stays false. Saving a draft or reading a fixture
// is not a submitted outreach.
func DeskSubmittedOutreach() bool {
	return false
}

// ProjectJointChain reports the joint Touch → Notify chain. This process
// does not observe that delivery, so the chain stays incomplete.
func ProjectJointChain() JointChain {
	return JointChain{Status: "incomplete", Label: JointChainIncomplete, TouchDelivered: false}
}

// ServiceDraftDesk decides whether the draft control is on screen.
// Ordinary sales do not see it until every gate holds. This round never
// treats Enabled as a handoff that already happened.
func DecideServiceDraft(role, category string, canUpdate, humanConfirmed bool) ServiceDraftDesk {
	if category != "creative_service" {
		return ServiceDraftDesk{Reason: "只有创意服务类别才有这个动作"}
	}
	if (role == "sales" || role == "agent") && (!canUpdate || !humanConfirmed) {
		return ServiceDraftDesk{Reason: "门店销售需类别、权限和人工确认同时成立"}
	}
	if !canUpdate {
		return ServiceDraftDesk{Present: true, Reason: "没有更新权限"}
	}
	if !humanConfirmed {
		return ServiceDraftDesk{Present: true, Enabled: false, Reason: "需要人工确认，本轮不会把草稿交给接单应用"}
	}
	return ServiceDraftDesk{Present: true, Enabled: true, Reason: "本轮不提交草稿"}
}

// ProjectStatus reads submitted/received/assigned/followed/won/paid off the
// facts given to it. Won does not imply paid.
func ProjectStatus(lead LeadView, opps []OpportunityView, paymentFact bool) StatusFacts {
	won := false
	for _, opp := range opps {
		if opp.Stage == "won" {
			won = true
		}
	}
	return StatusFacts{
		Submitted: lead.SubmittedAt != "",
		Received:  lead.ID != "",
		Assigned:  lead.Assignee != "",
		Followed:  lead.HasCompletedFollowUp,
		Won:       won,
		Paid:      paymentFact,
	}
}

// SeparateMoney sums each kind on its own. A missing amount stays unknown
// for that kind instead of becoming zero, and the kinds are never added together.
func SeparateMoney(opps []OpportunityView) Money {
	return Money{
		CustomerDealCents:       sumKind(opps, "customer_deal"),
		PainuoServiceOrderCents: sumKind(opps, "painuo_service"),
		PlatformToolSpendCents:  sumKind(opps, "platform_tool"),
	}
}

func sumKind(opps []OpportunityView, kind string) *int64 {
	var total int64
	saw := false
	for _, opp := range opps {
		if opp.Stage != "won" || opp.AmountKind != kind {
			continue
		}
		if opp.AmountCents == nil {
			return nil
		}
		total += *opp.AmountCents
		saw = true
	}
	if !saw {
		return nil
	}
	return &total
}

// Build classifies the desk. Sales and agents only receive their own rows.
func Build(scope Scope, now time.Time, leads []LeadView, opps []OpportunityView, desk []ReceptionView) Result {
	return buildDesk(scope, now, leads, opps, desk, nil)
}

// BuildWithDrafts is Build plus generated replies waiting for a person.
func BuildWithDrafts(scope Scope, now time.Time, leads []LeadView, opps []OpportunityView, desk []ReceptionView, drafts []DraftView) Result {
	return buildDesk(scope, now, leads, opps, desk, drafts)
}

func buildDesk(scope Scope, now time.Time, leads []LeadView, opps []OpportunityView, desk []ReceptionView, drafts []DraftView) Result {
	buckets := map[string][]Item{}
	for _, key := range []string{
		BucketUnprocessed, BucketDueToday, BucketWaitingReply, BucketStale,
		BucketAssignmentException, BucketNeedsSchedule, BucketHumanTakeover,
	} {
		buckets[key] = []Item{}
	}
	visibleOpps := make([]OpportunityView, 0, len(opps))
	for _, opp := range opps {
		if InScope(scope, opp.Assignee) {
			visibleOpps = append(visibleOpps, opp)
		}
	}
	for _, lead := range leads {
		if !InScope(scope, lead.Assignee) {
			continue
		}
		item := leadItem(lead, opps)
		reason := DisplayFilterReason(lead)
		if lead.Status == "new" && reason == "" && lead.Status != "filtered" {
			buckets[BucketUnprocessed] = append(buckets[BucketUnprocessed], item)
		}
		if lead.HasOpenFollowUp && dueOnOrBeforeToday(lead.ManualNextAt, now) {
			buckets[BucketDueToday] = append(buckets[BucketDueToday], item)
		}
		if lead.Status == "in_progress" && !futureNext(lead.ManualNextAt, now) {
			buckets[BucketNeedsSchedule] = append(buckets[BucketNeedsSchedule], item)
			if olderThan(lead.UpdatedAt, now, staleAfterDays) {
				buckets[BucketStale] = append(buckets[BucketStale], item)
			}
		}
		if scope.Role == "owner" && lead.Assignee == "" && (lead.Status == "new" || lead.Status == "in_progress") && reason == "" {
			exception := item
			exception.Bucket = BucketAssignmentException
			exception.Reason = "待分配"
			buckets[BucketAssignmentException] = append(buckets[BucketAssignmentException], exception)
		}
	}
	for _, opp := range visibleOpps {
		if !activeStage(opp.Stage) || opp.HasManualNext {
			continue
		}
		canUpdate := scope.Role == "owner" || (opp.Assignee != "" && opp.Assignee == scope.MemberID)
		item := Item{
			ID: opp.ID, TenantID: opp.TenantID, Bucket: BucketNeedsSchedule, Kind: "opportunity",
			OpportunityID: opp.ID, Assignee: opp.Assignee,
			ShowServiceDraft: ShowServiceDraft(opp.Category),
			ServiceDraft:     DecideServiceDraft(scope.Role, opp.Category, canUpdate, false),
			ForceOpportunity: false,
			Next:             safeAction("suggest_schedule", "suggestion", ""),
		}
		buckets[BucketNeedsSchedule] = append(buckets[BucketNeedsSchedule], item)
		if olderThan(opp.UpdatedAt, now, staleAfterDays) {
			stale := item
			stale.Bucket = BucketStale
			buckets[BucketStale] = append(buckets[BucketStale], stale)
		}
	}
	for _, session := range desk {
		if !InScope(scope, session.Assignee) {
			continue
		}
		if session.WaitingReply {
			buckets[BucketWaitingReply] = append(buckets[BucketWaitingReply], sessionItem(session, BucketWaitingReply))
		}
		if session.HumanTodo {
			buckets[BucketHumanTakeover] = append(buckets[BucketHumanTakeover], sessionItem(session, BucketHumanTakeover))
		}
	}
	name := "own"
	if scope.Role == "owner" {
		name = "tenant"
	}
	result := Result{
		Scope:             name,
		Buckets:           buckets,
		Money:             SeparateMoney(visibleOpps),
		Billing:           Billing{OrdinaryCRMChargeCents: OrdinaryCRMChargeCents("view")},
		Automation:        Automation{},
		JointChain:        ProjectJointChain(),
		OutreachSubmitted: DeskSubmittedOutreach(),
	}
	result.Today = projectToday(scope, now, leads, opps, desk, drafts)
	return result
}

// InScope applies the existing owner / assignee split.
func InScope(scope Scope, assignee string) bool {
	if scope.Role == "owner" {
		return true
	}
	return assignee != "" && assignee == scope.MemberID
}

func leadItem(lead LeadView, opps []OpportunityView) Item {
	item := Item{
		ID: lead.ID, TenantID: lead.TenantID, Kind: "lead", LeadID: lead.ID, Assignee: lead.Assignee,
		Next:             ResolveNext(lead, time.Time{}),
		Source:           SourceLine{Form: lead.SourceForm, Activity: lead.SourceActivity, Channel: lead.SourceChannel, At: lead.SourceAt},
		Statuses:         ProjectStatus(lead, opps, false),
		FilterReason:     DisplayFilterReason(lead),
		OwnerLabel:       DisplayOwner(lead),
		AssignmentReason: DisplayAssignmentReason(lead),
		AllowedContacts:  AllowedContacts(lead),
		Sync:             ProjectSync(lead.CRMReceiptAt),
		OutreachNotice:   OutreachNotice(lead.MarketingSMSOrPhone, false),
		ModelAdvice:      ModelAdviceMissing,
		Context:          ProjectContext(lead),
		LastInteraction:  Interaction{At: lead.LastInteractionAt, Summary: lead.LastInteraction},
	}
	item.Ask = item.Context.Ask
	item.Basis = NextBasis(item.Next, lead.AIScore)
	if RefuseSalesPush(lead.Purpose) {
		item.Reason = "不推进销售商机"
	}
	return item
}

// ProjectReceptionFact reports the current holder. Pending reason alone is
// not a takeover, and a bare member id is never the label.
func ProjectReceptionFact(session ReceptionView) ReceptionFact {
	fact := ReceptionFact{Label: "尚未接管", Epoch: session.Epoch, Version: session.Version}
	holder := strings.TrimSpace(session.Assignee)
	if session.Mode == "human" && holder != "" {
		fact.Takeover = true
		fact.Label = "人工接管"
		label := strings.TrimSpace(session.OwnerLabel)
		if label == "" || label == holder {
			label = "已接管"
		}
		fact.OwnerLabel = label
	}
	return fact
}

func sessionItem(session ReceptionView, bucket string) Item {
	fact := ProjectReceptionFact(session)
	return Item{
		ID: session.SessionID, TenantID: session.TenantID, Bucket: bucket, Kind: "reception",
		SessionID: session.SessionID, LeadID: session.LeadID, Assignee: session.Assignee,
		Reason: session.PendingReason, ForceOpportunity: false,
		Next:      safeAction("none", "suggestion", ""),
		Reception: &fact,
	}
}

func activeStage(stage string) bool {
	switch stage {
	case "open", "qualified", "proposal", "negotiation":
		return true
	default:
		return false
	}
}

func futureNext(at string, now time.Time) bool {
	ts, ok := parseTime(at)
	return ok && ts.After(now)
}

func dueOnOrBeforeToday(at string, now time.Time) bool {
	ts, ok := parseTime(at)
	if !ok {
		return false
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.UTC)
	return !ts.After(end)
}

func olderThan(at string, now time.Time, days int) bool {
	ts, ok := parseTime(at)
	if !ok {
		return false
	}
	return ts.Before(now.AddDate(0, 0, -days))
}

func parseTime(at string) (time.Time, bool) {
	at = strings.TrimSpace(at)
	if at == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, at); err == nil {
		return ts, true
	}
	ts, err := time.Parse(time.RFC3339Nano, at)
	return ts, err == nil
}

// AssembleTimeline orders events and strips customer plaintext unless the
// caller is allowed to see that business record.
func AssembleTimeline(events []TimelineEvent, reveal bool) []TimelineEvent {
	out := append([]TimelineEvent(nil), events...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At == out[j].At {
			return out[i].Kind < out[j].Kind
		}
		return out[i].At < out[j].At
	})
	if !reveal {
		for i := range out {
			out[i].ContactPlaintext = ""
		}
	}
	return out
}

var errDraftNotEditable = errors.New("draft is not editable")

// ErrDraftTooLong is the reception message cap applied to a draft edit.
var ErrDraftTooLong = errors.New("draft body is too long")

// IsTouchSource reports a lead that arrived from Touch.
func IsTouchSource(lead LeadView) bool {
	if lead.SourceType == "touch_campaign" {
		return true
	}
	channel := strings.TrimSpace(lead.SourceChannel)
	return channel == "碰一碰" || strings.EqualFold(channel, "touch")
}

// AllowCreativeHandoff is true only for a creative-service job.
// After-sales and ordinary consults never grow that handoff.
func AllowCreativeHandoff(purpose, category string) bool {
	if RefuseSalesPush(purpose) {
		return false
	}
	return category == "creative_service"
}

// NextBasis explains the next step without claiming a send or a call.
func NextBasis(action NextAction, score int) string {
	_ = score
	if action.Source == "manual" {
		return "手工 Next Action 优先于 AI 建议，不会自动外呼或发消息"
	}
	return "AI 建议可修改或忽略，分数不会自动外呼或发消息"
}

// PreferManual keeps a handwritten time ahead of any suggestion.
func PreferManual(suggested NextAction, manualAt, manualKind string) NextAction {
	if strings.TrimSpace(manualAt) == "" && strings.TrimSpace(manualKind) == "" {
		suggested.AutoCall = false
		suggested.AutoMessage = false
		suggested.CreateOrder = false
		return suggested
	}
	kind := strings.TrimSpace(manualKind)
	if kind == "" {
		kind = "manual_follow_up"
	}
	return safeAction(kind, "manual", strings.TrimSpace(manualAt))
}

// IgnoreSuggestion records a human dismissal. A manual action is left alone.
func IgnoreSuggestion(action NextAction) NextAction {
	action.AutoCall = false
	action.AutoMessage = false
	action.CreateOrder = false
	if action.Source == "manual" {
		return action
	}
	return safeAction("none", "manual", "")
}

// IgnoreDraft drops a generated reply. An already sent reply stays sent.
func IgnoreDraft(status string) DraftDecision {
	if status == "sent" {
		return DraftDecision{Status: "sent", Sent: true}
	}
	return DraftDecision{Status: "superseded", Sent: false}
}

// ReviseDraft edits a generated body and never sends it.
func ReviseDraft(status, body string) (DraftDecision, string, error) {
	body = strings.TrimSpace(body)
	if utf8.RuneCountInString(body) > DraftBodyMaxRunes {
		return DraftDecision{Status: status, Sent: status == "sent"}, body, ErrDraftTooLong
	}
	if status != "generated" || body == "" {
		return DraftDecision{Status: status, Sent: status == "sent"}, body, errDraftNotEditable
	}
	return DraftDecision{Status: "generated", Sent: false}, body, nil
}

// ProjectContext reuses confirmed customer facts and lists only the gaps.
func ProjectContext(lead LeadView) ConfirmedContext {
	ctx := ConfirmedContext{ContactID: lead.ContactID, Facts: []string{}, Ask: []string{}}
	if name := strings.TrimSpace(lead.ContactName); name != "" {
		ctx.Customer = name
		ctx.Facts = append(ctx.Facts, "客户姓名")
	} else {
		ctx.Ask = append(ctx.Ask, "客户姓名")
	}
	if lead.BusinessCategory != "" {
		ctx.Business = businessLabel(lead.BusinessCategory)
		ctx.Facts = append(ctx.Facts, "业务类别")
	} else {
		ctx.Ask = append(ctx.Ask, "业务类别")
	}
	if lead.SourceChannel != "" || lead.SourceActivity != "" || lead.SourceForm != "" {
		ctx.Facts = append(ctx.Facts, "来源")
	}
	if strings.TrimSpace(lead.Assignee) != "" && strings.TrimSpace(DisplayOwner(lead)) != "" {
		ctx.Facts = append(ctx.Facts, "负责人")
	} else if strings.TrimSpace(lead.Assignee) == "" {
		ctx.Ask = append(ctx.Ask, "负责人")
	}
	if len(AllowedContacts(lead)) > 0 {
		ctx.Facts = append(ctx.Facts, "允许的联系方式")
	}
	switch strings.TrimSpace(lead.ConsentStatus) {
	case "pending", "denied":
		ctx.Ask = append(ctx.Ask, "联系许可")
	}
	return ctx
}

func businessLabel(category string) string {
	switch category {
	case "creative_service":
		return "创意服务"
	case "merchant_customer":
		return "门店经营"
	default:
		return category
	}
}

// ChangedAsks returns the changed keys that this customer already knows.
func ChangedAsks(known, changed []string) []string {
	allow := map[string]bool{}
	for _, item := range known {
		allow[item] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range changed {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] || !allow[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

// SameCustomerAsks reuses confirmed facts. A change asks only that change.
func SameCustomerAsks(confirmed, gaps, changed []string) []string {
	if len(changed) > 0 {
		known := append(append([]string{}, confirmed...), gaps...)
		return ChangedAsks(known, changed)
	}
	return append([]string{}, gaps...)
}

// PlaceToday picks one homepage group from the same next-action facts.
// scheduled_next is a later appointment, not one of the seven today groups.
func PlaceToday(lead LeadView, now time.Time) string {
	if infoException(lead) {
		return TodayInfo
	}
	rel := relNone
	if lead.HasOpenFollowUp {
		rel = nextRelation(lead.ManualNextAt, now)
	}
	switch rel {
	case relBefore:
		return TodayOverdue
	case relToday:
		return TodayDue
	}
	if lead.WaitingCustomer {
		return TodayWaiting
	}
	if rel == relAfter {
		return TodayScheduled
	}
	if lead.Status == "new" && !lead.HasCompletedFollowUp && !lead.HasOpenFollowUp {
		return TodayNewInquiry
	}
	if (lead.Status == "in_progress" || lead.HasCompletedFollowUp) && !futureNext(lead.ManualNextAt, now) {
		if olderThan(lead.UpdatedAt, now, staleAfterDays) && !lead.HasOpenFollowUp && !lead.WaitingCustomer {
			return TodayOverdue
		}
		return TodayDue
	}
	return ""
}

func infoException(lead LeadView) bool {
	if strings.TrimSpace(lead.Assignee) == "" && (lead.Status == "new" || lead.Status == "in_progress") {
		return true
	}
	if DisplayFilterReason(lead) != "" {
		return true
	}
	return strings.TrimSpace(lead.ConsentStatus) == "denied"
}

func deskLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func nextRelation(at string, now time.Time) int {
	ts, ok := parseTime(at)
	if !ok {
		return relNone
	}
	loc := deskLocation()
	localNow := now.In(loc)
	localAt := ts.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	if localAt.Before(start) {
		return relBefore
	}
	if !localAt.Before(start.AddDate(0, 0, 1)) {
		return relAfter
	}
	return relToday
}

func projectToday(scope Scope, now time.Time, leads []LeadView, opps []OpportunityView, desk []ReceptionView, drafts []DraftView) map[string][]Item {
	today := map[string][]Item{
		TodayNewInquiry: {},
		TodayDue:        {},
		TodayWaiting:    {},
		TodayOverdue:    {},
		TodayAIDraft:    {},
		TodayHuman:      {},
		TodayInfo:       {},
	}
	for _, lead := range leads {
		if !InScope(scope, lead.Assignee) {
			continue
		}
		group := PlaceToday(lead, now)
		if _, ok := today[group]; !ok {
			continue
		}
		item := leadItem(lead, opps)
		item.Bucket = group
		item.TodayGroup = group
		today[group] = append(today[group], item)
	}
	for _, opp := range opps {
		if !InScope(scope, opp.Assignee) || !activeStage(opp.Stage) || opp.HasManualNext {
			continue
		}
		lead, ok := soleLead(leads, opp.ContactID, scope)
		if !ok {
			continue
		}
		canUpdate := scope.Role == "owner" || (opp.Assignee != "" && opp.Assignee == scope.MemberID)
		item := Item{
			ID: opp.ID, TenantID: opp.TenantID, Bucket: TodayDue, TodayGroup: TodayDue, Kind: "opportunity",
			OpportunityID: opp.ID, Assignee: opp.Assignee,
			ShowServiceDraft: ShowServiceDraft(opp.Category) && AllowCreativeHandoff(lead.Purpose, opp.Category),
			ServiceDraft:     DecideServiceDraft(scope.Role, opp.Category, canUpdate, false),
			ForceOpportunity: false,
			Next:             safeAction("suggest_schedule", "suggestion", ""),
		}
		item = copyLeadFacts(item, lead, opps)
		if !AllowCreativeHandoff(lead.Purpose, opp.Category) {
			item.ShowServiceDraft = false
			item.ServiceDraft = ServiceDraftDesk{}
		}
		if item.Basis == "" {
			item.Basis = NextBasis(item.Next, 0)
		}
		today[TodayDue] = append(today[TodayDue], item)
	}
	for _, session := range desk {
		if !InScope(scope, session.Assignee) {
			continue
		}
		if session.HumanTodo {
			item := sessionItem(session, TodayHuman)
			item.TodayGroup = TodayHuman
			if lead, ok := findLead(leads, session.LeadID); ok && InScope(scope, lead.Assignee) {
				item = copyLeadFacts(item, lead, opps)
			}
			today[TodayHuman] = append(today[TodayHuman], item)
			continue
		}
		if session.WaitingReply {
			item := sessionItem(session, TodayWaiting)
			item.TodayGroup = TodayWaiting
			if lead, ok := findLead(leads, session.LeadID); ok && InScope(scope, lead.Assignee) {
				item = copyLeadFacts(item, lead, opps)
			}
			today[TodayWaiting] = append(today[TodayWaiting], item)
		}
	}
	for _, draft := range drafts {
		if draft.Status != "generated" || (draft.Kind != "ai" && draft.Kind != "draft") {
			continue
		}
		if !InScope(scope, draft.Assignee) {
			continue
		}
		item := draftItem(draft)
		if lead, ok := findLead(leads, draft.LeadID); ok && InScope(scope, lead.Assignee) {
			item = copyLeadFacts(item, lead, nil)
		}
		today[TodayAIDraft] = append(today[TodayAIDraft], item)
	}
	return today
}

func findLead(leads []LeadView, id string) (LeadView, bool) {
	if id == "" {
		return LeadView{}, false
	}
	for _, lead := range leads {
		if lead.ID == id {
			return lead, true
		}
	}
	return LeadView{}, false
}

func soleLead(leads []LeadView, contactID string, scope Scope) (LeadView, bool) {
	if strings.TrimSpace(contactID) == "" {
		return LeadView{}, false
	}
	var found LeadView
	n := 0
	for _, lead := range leads {
		if lead.ContactID != contactID || !InScope(scope, lead.Assignee) {
			continue
		}
		n++
		found = lead
	}
	if n != 1 {
		return LeadView{}, false
	}
	return found, true
}

func copyLeadFacts(item Item, lead LeadView, opps []OpportunityView) Item {
	filled := leadItem(lead, opps)
	item.LeadID = lead.ID
	item.Source = filled.Source
	item.Context = filled.Context
	item.Ask = filled.Ask
	item.OwnerLabel = filled.OwnerLabel
	item.AllowedContacts = filled.AllowedContacts
	item.LastInteraction = filled.LastInteraction
	item.Statuses = filled.Statuses
	item.Sync = filled.Sync
	item.OutreachNotice = filled.OutreachNotice
	item.AssignmentReason = filled.AssignmentReason
	if item.Next.Kind == "" || item.Next.Kind == "none" {
		item.Next = filled.Next
		item.Basis = filled.Basis
	} else if item.Basis == "" {
		item.Basis = filled.Basis
	}
	return item
}

func draftItem(d DraftView) Item {
	item := Item{
		ID: d.ID, TenantID: d.TenantID, Bucket: TodayAIDraft, TodayGroup: TodayAIDraft, Kind: "ai_draft",
		SessionID: d.SessionID, LeadID: d.LeadID, Assignee: d.Assignee, OwnerLabel: strings.TrimSpace(d.OwnerLabel),
		DraftBody: d.Body, ForceOpportunity: false,
		Next:        safeAction("confirm_draft", "suggestion", ""),
		ModelAdvice: ModelAdviceMissing,
	}
	item.Basis = "AI 草稿待确认，可修改或忽略，不会自动发送"
	return item
}
