// Package workbench is the pure decision core for the sales desk (HUI-1893).
//
// It never places a call, sends a message, creates an order, or charges a
// fee. Callers pass facts they already stored; this package only classifies
// them. A manual next action always beats a suggestion, including any AI score.
package workbench

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

const (
	// ModelAdviceMissing is the only intent line this desk may show.
	// There is no model call behind it.
	ModelAdviceMissing = "真实模型未完成"

	BucketUnprocessed         = "unprocessed"
	BucketDueToday            = "due_today"
	BucketWaitingReply        = "waiting_reply"
	BucketStale               = "stale"
	BucketAssignmentException = "assignment_exception"
	BucketNeedsSchedule       = "needs_schedule"
	BucketHumanTakeover       = "human_takeover"
	staleAfterDays            = 7
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
type ReceptionView struct {
	SessionID     string
	TenantID      string
	LeadID        string
	Assignee      string
	PendingReason string
	WaitingReply  bool
	HumanTodo     bool
	Purpose       string
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

// Result is the whole desk for one caller.
type Result struct {
	Scope      string            `json:"scope"`
	Buckets    map[string][]Item `json:"buckets"`
	Money      Money             `json:"money"`
	Billing    Billing           `json:"billing"`
	Automation Automation        `json:"automation"`
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
	default:
		return "无系统动作"
	}
}

func channelEligible(lead LeadView) bool {
	return strings.TrimSpace(lead.Phone) == "" && strings.TrimSpace(lead.ChannelIdentity) != "" && lead.ChannelReplyAllowed
}

// FollowThroughKey identifies one saved follow-up. The same normalized
// facts always hash to the same key; a different note does not.
func FollowThroughKey(leadID, note, nextAt, channel string, complete bool, createdBy string) string {
	bit := "0"
	if complete {
		bit = "1"
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		leadID, note, nextAt, channel, bit, createdBy,
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
func OutreachNotice(marketingAllowed, sendReceipt bool) string {
	if marketingAllowed && sendReceipt {
		return OutreachLabel(true, true)
	}
	if !marketingAllowed {
		return "没有营销许可"
	}
	return "没有触达回执"
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
	return Result{
		Scope:      name,
		Buckets:    buckets,
		Money:      SeparateMoney(visibleOpps),
		Billing:    Billing{OrdinaryCRMChargeCents: OrdinaryCRMChargeCents("view")},
		Automation: Automation{},
	}
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
	}
	if RefuseSalesPush(lead.Purpose) {
		item.Reason = "不推进销售商机"
	}
	return item
}

func sessionItem(session ReceptionView, bucket string) Item {
	return Item{
		ID: session.SessionID, TenantID: session.TenantID, Bucket: bucket, Kind: "reception",
		SessionID: session.SessionID, LeadID: session.LeadID, Assignee: session.Assignee,
		Reason: session.PendingReason, ForceOpportunity: false,
		Next: safeAction("none", "suggestion", ""),
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
