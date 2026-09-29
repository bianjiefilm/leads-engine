package sopreach

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	refusalAutomaticOff    = "automatic_send_off"
	refusalTestRole        = "test_role_required"
	refusalChain           = "confirm_chain_required"
	refusalCampaign        = "campaign_not_permission"
	refusalFixture         = "fixture_not_accepted"
	refusalOriginalMissing = "original_missing"

	statusSubmissionAccepted = "submission_accepted"
	statusDeliveryUnknown    = "unknown"
	statusQueued             = "queued"

	roleAuthorizedTest = "authorized_test"
	stampFormalChannel = "正式渠道未验证"
)

// Sandbox schedules a reminder, a draft, and an authorized test confirmation.
// Automatic send stays off until AllowAutomatic(true). A post goes only to the
// fixture URL. HTTP 200 means the submission was accepted. Delivery stays
// unknown: the response body is discarded, and this scheduler does not read a
// receipt. A fixture post is not a provider call and not a charge.
type Sandbox struct {
	mu            sync.Mutex
	fixtureURL    string
	client        *http.Client
	automatic     bool
	n             int
	chargeWrites  int
	billingPassed bool
	providerCalls int
	reminders     map[string]*storedReach
	drafts        map[string]*storedDraft
	actions       map[string]*storedAuto
	messages      map[string]*originalMessage
	callbacks     map[string]Outcome
}

// Reach is the decision snapshot plus the campaign the action is for.
// EnrolledCampaignID never grants marketing permission.
type Reach struct {
	Input
	ContactID          string
	CampaignID         string
	ConsentCampaignID  string
	EnrolledCampaignID string
	Body               string
	Role               string
	ReminderID         string
	DraftID            string
}

// Evidence is the channel claim this scheduler is allowed to make.
type Evidence struct {
	ServiceProvider string
	Billing         string
	Production      string
	HumanAdoption   string
	ChannelStamp    string
	ChargeWrites    int
	BillingPassed   bool
	ProviderCalls   int
}

// Outcome is one scheduler step. Delivered and Replied stay false.
type Outcome struct {
	OK            bool
	Refusal       string
	ID            string
	Kind          string
	Status        string
	Submission    string
	Delivery      string
	Delivered     bool
	Replied       bool
	AutomaticSent bool
	CreatedSend   bool
	Duplicate     bool
	FoundOriginal bool
	Stopped       bool
	SentBody      string
	Stamp         string
	Records       []Record
	Evidence      Evidence
}

// Callback looks up an original message. It does not carry a receipt.
type Callback struct {
	CallbackID     string
	MessageID      string
	TenantID       string
	ActionTenantID string
	HTTPStatus     int
}

type storedReach struct {
	ID    string
	Reach Reach
}

type storedDraft struct {
	ID         string
	ReminderID string
	Reach      Reach
	Attempted  bool
	Result     Outcome
}

type storedAuto struct {
	ID         string
	Reach      Reach
	Stopped    bool
	StopReason string
	Attempted  bool
	Result     Outcome
}

type originalMessage struct {
	ID         string
	TenantID   string
	Submission string
	Delivery   string
	Callbacks  int
}

type fixtureMessage struct {
	MessageID      string `json:"message_id"`
	TenantID       string `json:"tenant_id"`
	ContactID      string `json:"contact_id"`
	CampaignID     string `json:"campaign_id"`
	Channel        string `json:"channel"`
	Purpose        string `json:"purpose"`
	ContentVersion int    `json:"content_version"`
	Body           string `json:"body"`
}

// NewSandbox posts only to fixtureURL. Automatic send is off.
func NewSandbox(fixtureURL string) *Sandbox {
	return &Sandbox{
		fixtureURL: fixtureURL,
		client:     &http.Client{Timeout: 3 * time.Second},
		reminders:  map[string]*storedReach{},
		drafts:     map[string]*storedDraft{},
		actions:    map[string]*storedAuto{},
		messages:   map[string]*originalMessage{},
		callbacks:  map[string]Outcome{},
	}
}

// AllowAutomatic is the explicit in-process switch. Balance and score do not call it.
func (s *Sandbox) AllowAutomatic(on bool) {
	s.mu.Lock()
	s.automatic = on
	s.mu.Unlock()
}

// Evidence returns the fixed unverified claim and the charge counters.
func (s *Sandbox) Evidence() Evidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

func (s *Sandbox) snapshot() Evidence {
	return Evidence{
		ServiceProvider: "NOT_VERIFIED",
		Billing:         "NOT_VERIFIED",
		Production:      "NOT_AUTHORIZED",
		HumanAdoption:   "UNKNOWN",
		ChannelStamp:    stampFormalChannel,
		ChargeWrites:    s.chargeWrites,
		BillingPassed:   s.billingPassed,
		ProviderCalls:   s.providerCalls,
	}
}

func (s *Sandbox) finish(out Outcome) Outcome {
	out.Evidence = s.snapshot()
	out.Stamp = stampFormalChannel
	out.Delivered = false
	out.Replied = false
	return out
}

func (s *Sandbox) next(prefix string) string {
	s.n++
	return prefix + strconv.Itoa(s.n)
}

// Remind records an internal reminder. It does not post.
func (s *Sandbox) Remind(r Reach) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if refusal := boundReach(r); refusal != "" {
		return s.finish(Outcome{Refusal: refusal})
	}
	r.Op = OpRemind
	decision := Decide(r.Input)
	if !decision.OK {
		return s.finish(refused(decision))
	}
	id := s.next("rem_")
	s.reminders[id] = &storedReach{ID: id, Reach: r}
	return s.finish(Outcome{OK: true, ID: id, Kind: KindReminder, Status: StatusRecorded, Records: decision.Records})
}

// Draft records a pending draft after its reminder. It does not post.
func (s *Sandbox) Draft(r Reach) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if refusal := boundReach(r); refusal != "" {
		return s.finish(Outcome{Refusal: refusal})
	}
	rem, ok := s.reminders[r.ReminderID]
	if !ok || !sameAction(rem.Reach, r) {
		return s.finish(Outcome{Refusal: refusalChain})
	}
	r.Op = OpDraft
	decision := Decide(r.Input)
	if !decision.OK {
		return s.finish(refused(decision))
	}
	id := s.next("drf_")
	s.drafts[id] = &storedDraft{ID: id, ReminderID: r.ReminderID, Reach: r}
	return s.finish(Outcome{OK: true, ID: id, Kind: KindDraft, Status: StatusRecorded, Records: decision.Records})
}

// Confirm posts the stored draft only for an authorized test role.
func (s *Sandbox) Confirm(r Reach) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.drafts[r.DraftID]
	if !ok || draft.ReminderID == "" {
		return s.finish(Outcome{Refusal: refusalChain})
	}
	if _, ok := s.reminders[draft.ReminderID]; !ok {
		return s.finish(Outcome{Refusal: refusalChain})
	}
	if r.TenantID != draft.Reach.TenantID || r.ActionTenantID != draft.Reach.TenantID {
		return s.finish(Outcome{Refusal: RefusalTenant})
	}
	if !sameAction(draft.Reach, r) {
		return s.finish(Outcome{Refusal: refusalChain})
	}
	if r.Role != roleAuthorizedTest {
		return s.finish(Outcome{Refusal: refusalTestRole})
	}
	if draft.Attempted {
		prev := draft.Result
		prev.CreatedSend = false
		prev.AutomaticSent = false
		prev.Duplicate = true
		return s.finish(prev)
	}
	if refusal := marketingCampaignRefusal(r); refusal != "" {
		return s.finish(Outcome{Refusal: refusal})
	}
	r.Op = OpConfirm
	decision := Decide(r.Input)
	if !decision.OK {
		return s.finish(refused(decision))
	}
	return s.finish(s.submit(draft.Reach.Body, r, &draft.Attempted, &draft.Result))
}

// QueueAutomatic stores one explicit automatic action and does not post.
func (s *Sandbox) QueueAutomatic(r Reach) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.automatic {
		return s.finish(Outcome{Refusal: refusalAutomaticOff})
	}
	if refusal := boundReach(r); refusal != "" {
		return s.finish(Outcome{Refusal: refusal})
	}
	r.Op = OpAuto
	decision := Decide(r.Input)
	if !decision.OK {
		return s.finish(refused(decision))
	}
	if refusal := marketingCampaignRefusal(r); refusal != "" {
		return s.finish(Outcome{Refusal: refusal})
	}
	id := s.next("act_")
	s.actions[id] = &storedAuto{ID: id, Reach: r}
	return s.finish(Outcome{OK: true, ID: id, Kind: KindSubmission, Status: statusQueued})
}

// Release rechecks the queued action. A stop, or automatic send being off, cancels it.
func (s *Sandbox) Release(actionID string, now Reach) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	act, ok := s.actions[actionID]
	if !ok {
		return s.finish(Outcome{Refusal: RefusalBinding})
	}
	if now.TenantID != act.Reach.TenantID || now.ActionTenantID != act.Reach.TenantID {
		return s.finish(Outcome{Refusal: RefusalTenant})
	}
	if act.Stopped {
		return s.finish(Outcome{Refusal: act.StopReason, Stopped: true})
	}
	if act.Attempted {
		prev := act.Result
		prev.CreatedSend = false
		prev.AutomaticSent = false
		prev.Duplicate = true
		return s.finish(prev)
	}
	if !s.automatic {
		act.Stopped = true
		act.StopReason = refusalAutomaticOff
		return s.finish(Outcome{Refusal: refusalAutomaticOff, Stopped: true})
	}
	merged := act.Reach
	merged.HumanTakeover = now.HumanTakeover
	merged.Unsubscribed = now.Unsubscribed
	merged.Revoked = now.Revoked
	merged.InsideWindow = now.InsideWindow
	merged.RateLimited = now.RateLimited
	merged.GlobalStop = now.GlobalStop
	merged.CustomerStop = now.CustomerStop
	merged.Rejected = now.Rejected
	merged.Op = OpAuto
	decision := Decide(merged.Input)
	if !decision.OK {
		if stickyStop(decision.Refusal) {
			act.Stopped = true
			act.StopReason = decision.Refusal
		}
		out := refused(decision)
		out.Stopped = act.Stopped
		return s.finish(out)
	}
	if refusal := marketingCampaignRefusal(merged); refusal != "" {
		return s.finish(Outcome{Refusal: refusal})
	}
	out := s.submit(act.Reach.Body, merged, &act.Attempted, &act.Result)
	if out.CreatedSend {
		out.AutomaticSent = true
		act.Result.AutomaticSent = true
	}
	return s.finish(out)
}

// Callback looks up the original message for this tenant. It never posts or charges.
func (s *Sandbox) Callback(c Callback) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.TenantID == "" || c.TenantID != c.ActionTenantID {
		return s.finish(Outcome{Refusal: RefusalTenant, Delivery: statusDeliveryUnknown})
	}
	if c.CallbackID != "" {
		if prev, ok := s.callbacks[c.CallbackID]; ok {
			prev.Duplicate = true
			prev.CreatedSend = false
			prev.AutomaticSent = false
			return s.finish(prev)
		}
	}
	out := Outcome{Delivery: statusDeliveryUnknown}
	msg, ok := s.messages[c.MessageID]
	if c.MessageID == "" || !ok {
		out.Refusal = refusalOriginalMissing
		s.rememberCallback(c.CallbackID, out)
		return s.finish(out)
	}
	if msg.TenantID != c.TenantID {
		out.Refusal = RefusalTenant
		s.rememberCallback(c.CallbackID, out)
		return s.finish(out)
	}
	msg.Callbacks++
	out.OK = true
	out.FoundOriginal = true
	out.ID = msg.ID
	out.Submission = msg.Submission
	out.Delivery = statusDeliveryUnknown
	out.Duplicate = msg.Callbacks > 1
	s.rememberCallback(c.CallbackID, out)
	return s.finish(out)
}

func (s *Sandbox) rememberCallback(id string, out Outcome) {
	if id == "" {
		return
	}
	s.callbacks[id] = out
}

func (s *Sandbox) submit(body string, r Reach, attempted *bool, dest *Outcome) Outcome {
	*attempted = true
	msgID := s.next("msg_")
	status, err := s.post(fixtureMessage{
		MessageID:      msgID,
		TenantID:       r.TenantID,
		ContactID:      r.ContactID,
		CampaignID:     r.CampaignID,
		Channel:        r.Channel,
		Purpose:        r.Purpose,
		ContentVersion: r.ContentVersion,
		Body:           body,
	})
	out := Outcome{Delivery: statusDeliveryUnknown}
	if err != nil || status != http.StatusOK {
		out.Refusal = refusalFixture
		*dest = out
		return out
	}
	s.messages[msgID] = &originalMessage{
		ID: msgID, TenantID: r.TenantID, Submission: statusSubmissionAccepted, Delivery: statusDeliveryUnknown,
	}
	out = Outcome{
		OK:          true,
		ID:          msgID,
		Kind:        KindSubmission,
		Status:      statusSubmissionAccepted,
		Submission:  statusSubmissionAccepted,
		Delivery:    statusDeliveryUnknown,
		CreatedSend: true,
		SentBody:    body,
		Records: []Record{
			{Kind: KindSubmission, Status: statusSubmissionAccepted},
			{Kind: KindDelivery, Status: statusDeliveryUnknown},
		},
	}
	*dest = out
	return out
}

func (s *Sandbox) post(msg fixtureMessage) (int, error) {
	if s.fixtureURL == "" || s.client == nil {
		return 0, errors.New("fixture url missing")
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, s.fixtureURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func boundReach(r Reach) string {
	if r.ContactID == "" || r.CampaignID == "" {
		return RefusalBinding
	}
	if r.TenantID == "" || r.TenantID != r.ActionTenantID {
		return RefusalTenant
	}
	return ""
}

func sameAction(saved, now Reach) bool {
	return saved.TenantID == now.TenantID &&
		saved.ContactID == now.ContactID &&
		saved.CampaignID == now.CampaignID &&
		saved.Channel == now.Channel &&
		saved.Purpose == now.Purpose &&
		saved.ContentVersion == now.ContentVersion &&
		saved.Recipient == now.Recipient
}

func marketingCampaignRefusal(r Reach) string {
	if r.Purpose != PurposeMarketing {
		return ""
	}
	if r.CampaignID == "" || r.ConsentCampaignID != r.CampaignID {
		return refusalCampaign
	}
	return ""
}

func stickyStop(refusal string) bool {
	switch refusal {
	case RefusalHumanTakeover, RefusalUnsubscribed, RefusalRevoked, RefusalWindow, RefusalRate:
		return true
	default:
		return false
	}
}

func refused(decision Result) Outcome {
	out := Outcome{Refusal: decision.Refusal, Records: decision.Records}
	if len(decision.Records) > 0 {
		out.Kind = decision.Records[0].Kind
		out.Status = decision.Records[0].Status
	}
	return out
}
