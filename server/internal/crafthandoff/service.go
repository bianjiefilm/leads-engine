// Package crafthandoff is an in-memory handoff from the current lead scene
// to one professional-tool fixture.
//
// 短回复只能由调用方标成已选且已授权的事实组成。保存不等于发送，也不会启动回复机器人。
// 复杂制作只返回 goboost、product-image、avatar 或 aicut 的夹具引用。
// 载荷只有来源线索、版本和付款方引用。
// lightcopy 仍负责场景内短文案的失败关闭。本包不调用模型，也不打开在线制作会话。
// 盖「真实模型未完成」。未给出的费用保持未知。
package crafthandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
)

const (
	StampModelIncomplete = "真实模型未完成"

	ServiceNotVerified      = "NOT_VERIFIED"
	BillingNotVerified      = "NOT_VERIFIED"
	ProductionNotAuthorized = "NOT_AUTHORIZED"
	HumanUnknown            = "UNKNOWN"

	StatusDraftSaved      = "draft_saved"
	StatusFixtureRecorded = "fixture_recorded"
)

const (
	TargetGoBoost      = "goboost"
	TargetProductImage = "product-image"
	TargetAvatar       = "avatar"
	TargetAiCut        = "aicut"
)

var (
	ErrInvalid              = errors.New("crafthandoff: invalid")
	ErrNothingSelected      = errors.New("crafthandoff: nothing_selected")
	ErrUnauthorizedFact     = errors.New("crafthandoff: unauthorized_fact")
	ErrForbiddenFact        = errors.New("crafthandoff: forbidden_fact")
	ErrUnauthorizedPortrait = errors.New("crafthandoff: unauthorized_portrait")
	ErrCrossTenant          = errors.New("crafthandoff: cross_tenant")
	ErrUnknownTarget        = errors.New("crafthandoff: unknown_target")
	ErrPayerRequired        = errors.New("crafthandoff: payer_required")
	ErrPermissionRevoked    = errors.New("crafthandoff: permission_revoked")
	ErrTargetFailed         = errors.New("crafthandoff: target_failed")
	ErrEmptyDraft           = errors.New("crafthandoff: empty_draft")
	ErrDraftNotFound        = errors.New("crafthandoff: draft_not_found")
)

var allowedKey = map[string]bool{
	"scene":     true,
	"question":  true,
	"offer":     true,
	"next_step": true,
}

var forbiddenKey = map[string]bool{
	"phone":               true,
	"email":               true,
	"crm_profile":         true,
	"profile":             true,
	"sales_note":          true,
	"internal_note":       true,
	"internal_sales_note": true,
}

var knownTarget = map[string]bool{
	TargetGoBoost:      true,
	TargetProductImage: true,
	TargetAvatar:       true,
	TargetAiCut:        true,
}

// Fact is one caller-marked statement. Unselected facts are ignored.
type Fact struct {
	Key        string
	Value      string
	Selected   bool
	Authorized bool
}

// SaveRequest asks to store a short reply from selected, authorized facts.
type SaveRequest struct {
	TenantID string
	LeadID   string
	Version  int
	Facts    []Fact
}

// Draft is the saved short reply. It is not a send.
type Draft struct {
	TenantID         string
	LeadID           string
	Version          int
	Body             string
	Facts            []Fact
	Sent             bool
	Published        bool
	MarketingLicense bool
	ReplyBotStarted  bool
	EditableByHand   bool
	ModelStamp       string
	Status           string
}

// Input is one attempt to open a professional-tool fixture.
// Phone, email, CRM profile, sales notes, and an unauthorized portrait are
// rejected. An authorized portrait is not copied into the payload.
type Input struct {
	TenantID           string
	LeadID             string
	Version            int
	Target             string
	PayerRef           string
	Phone              string
	Email              string
	CRMProfile         string
	SalesNotes         string
	Portrait           string
	PortraitAuthorized bool
	CostMinor          *int64
	TargetFailed       bool
}

// Payload is the only document a fixture may carry.
type Payload struct {
	SourceLeadID string `json:"source_lead_id"`
	Version      int    `json:"version"`
	PayerRef     string `json:"payer_ref"`
}

// Cost is unknown when Known is false. Unknown is not zero.
type Cost struct {
	Known bool
	Minor int64
}

// Label renders unknown costs as unknown, never as 0.
func (c Cost) Label() string {
	if !c.Known {
		return "unknown"
	}
	return strconv.FormatInt(c.Minor, 10)
}

// MarshalJSON writes null for an unknown cost and a number for a known one.
func (c Cost) MarshalJSON() ([]byte, error) {
	if !c.Known {
		return []byte("null"), nil
	}
	return json.Marshal(c.Minor)
}

// Result is one fixture handoff. GenerationOK does not mean the reply was sent,
// published, or given a marketing license.
type Result struct {
	ID               string
	TenantID         string
	LeadID           string
	Version          int
	Target           string
	Payload          Payload
	FixtureRef       string
	Fixture          bool
	LiveSession      bool
	ProjectCount     int
	GenerationOK     bool
	Sent             bool
	Published        bool
	MarketingLicense bool
	ModelStamp       string
	ProviderCalls    int
	ChargeWrites     int
	BillingPassed    bool
	Cost             Cost
	ServiceProvider  string
	Billing          string
	Production       string
	HumanAdoption    string
	Status           string
}

// Service stores drafts and fixture handoffs in memory. It does not call a provider.
type Service struct {
	mu            sync.Mutex
	leads         map[string]string
	drafts        map[string]Draft
	revoked       map[string]bool
	handoffs      map[string]Result
	providerCalls int
	chargeWrites  int
	replyBots     int
}

// New returns an empty in-memory service.
func New() *Service {
	return &Service{
		leads:    map[string]string{},
		drafts:   map[string]Draft{},
		revoked:  map[string]bool{},
		handoffs: map[string]Result{},
	}
}

// Projects is the number of stored fixture handoffs, not the number of clicks.
func (s *Service) Projects() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.handoffs)
}

// ProviderCalls stays 0. A fixture reference is not a provider call.
func (s *Service) ProviderCalls() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerCalls
}

// ChargeWrites stays 0. Repeating a handoff does not write a charge.
func (s *Service) ChargeWrites() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chargeWrites
}

// ReplyBotsStarted stays 0. Saving a draft does not start a reply bot.
func (s *Service) ReplyBotsStarted() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replyBots
}

// SaveDraft stores a short reply made only from selected, authorized facts.
func (s *Service) SaveDraft(req SaveRequest) (Draft, error) {
	if s == nil || !canonicalID(req.TenantID) || !canonicalID(req.LeadID) || req.Version < 1 {
		return Draft{}, ErrInvalid
	}
	facts, body, err := selectedCopy(req.Facts)
	if err != nil {
		return Draft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.bind(req.TenantID, req.LeadID); err != nil {
		return Draft{}, err
	}
	draft := sealDraft(Draft{
		TenantID: req.TenantID,
		LeadID:   req.LeadID,
		Version:  req.Version,
		Body:     body,
		Facts:    facts,
	})
	s.drafts[req.LeadID] = draft
	return copyDraft(draft), nil
}

// Draft returns the saved short reply. Another tenant is rejected.
func (s *Service) Draft(tenantID, leadID string) (Draft, error) {
	if s == nil || !canonicalID(tenantID) || !canonicalID(leadID) {
		return Draft{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.leads[leadID]; ok && prev != tenantID {
		return Draft{}, ErrCrossTenant
	}
	draft, ok := s.drafts[leadID]
	if !ok {
		return Draft{}, ErrDraftNotFound
	}
	if draft.TenantID != tenantID {
		return Draft{}, ErrCrossTenant
	}
	return copyDraft(draft), nil
}

// EditByHand replaces the draft body. It does not send or start a reply bot.
func (s *Service) EditByHand(tenantID, leadID, body string) (Draft, error) {
	if s == nil || !canonicalID(tenantID) || !canonicalID(leadID) {
		return Draft{}, ErrInvalid
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Draft{}, ErrEmptyDraft
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.leads[leadID]; ok && prev != tenantID {
		return Draft{}, ErrCrossTenant
	}
	draft, ok := s.drafts[leadID]
	if !ok {
		return Draft{}, ErrDraftNotFound
	}
	if draft.TenantID != tenantID {
		return Draft{}, ErrCrossTenant
	}
	draft.Body = body
	draft = sealDraft(draft)
	s.drafts[leadID] = draft
	return copyDraft(draft), nil
}

// Revoke rejects later handoffs for this lead and does not delete the draft.
func (s *Service) Revoke(tenantID, leadID string) error {
	if s == nil || !canonicalID(tenantID) || !canonicalID(leadID) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.bind(tenantID, leadID); err != nil {
		return err
	}
	s.revoked[leadID] = true
	return nil
}

// Handoff returns the fixture for one target. The same tenant, lead, version,
// and target returns the stored handoff instead of a second project.
func (s *Service) Handoff(in Input) (Result, error) {
	if s == nil || !canonicalID(in.TenantID) || !canonicalID(in.LeadID) || in.Version < 1 {
		return Result{}, ErrInvalid
	}
	if !knownTarget[in.Target] {
		return Result{}, ErrUnknownTarget
	}
	if strings.TrimSpace(in.PayerRef) == "" {
		return Result{}, ErrPayerRequired
	}
	if looksLikeContact(in.PayerRef) || contactMaterial(in) {
		return Result{}, ErrForbiddenFact
	}
	if !canonicalID(in.PayerRef) {
		return Result{}, ErrInvalid
	}
	if strings.TrimSpace(in.Portrait) != "" && !in.PortraitAuthorized {
		return Result{}, ErrUnauthorizedPortrait
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.bind(in.TenantID, in.LeadID); err != nil {
		return Result{}, err
	}
	if s.revoked[in.LeadID] {
		return Result{}, ErrPermissionRevoked
	}
	key := handoffKey(in.TenantID, in.LeadID, in.Version, in.Target)
	if existing, ok := s.handoffs[key]; ok {
		return seal(existing), nil
	}
	if in.TargetFailed {
		return Result{}, ErrTargetFailed
	}
	id := handoffID(in.TenantID, in.LeadID, in.Version, in.Target)
	result := seal(Result{
		ID:       id,
		TenantID: in.TenantID,
		LeadID:   in.LeadID,
		Version:  in.Version,
		Target:   in.Target,
		Payload: Payload{
			SourceLeadID: in.LeadID,
			Version:      in.Version,
			PayerRef:     in.PayerRef,
		},
		Cost: costFrom(in.CostMinor),
	})
	s.handoffs[key] = result
	return result, nil
}

func (s *Service) bind(tenantID, leadID string) error {
	if prev, ok := s.leads[leadID]; ok && prev != tenantID {
		return ErrCrossTenant
	}
	s.leads[leadID] = tenantID
	return nil
}

func selectedCopy(facts []Fact) ([]Fact, string, error) {
	selected := 0
	kept := make([]Fact, 0)
	for _, fact := range facts {
		if !fact.Selected {
			continue
		}
		selected++
		if fact.Key == "portrait" {
			if !fact.Authorized {
				return nil, "", ErrUnauthorizedPortrait
			}
			continue
		}
		if forbiddenKey[fact.Key] {
			return nil, "", ErrForbiddenFact
		}
		if !allowedKey[fact.Key] || !fact.Authorized {
			return nil, "", ErrUnauthorizedFact
		}
		value := strings.TrimSpace(fact.Value)
		if value == "" {
			return nil, "", ErrInvalid
		}
		if looksLikeContact(value) {
			return nil, "", ErrForbiddenFact
		}
		kept = append(kept, Fact{Key: fact.Key, Value: value, Selected: true, Authorized: true})
	}
	if selected == 0 || len(kept) == 0 {
		return nil, "", ErrNothingSelected
	}
	parts := make([]string, len(kept))
	for i, fact := range kept {
		parts[i] = fact.Value
	}
	return kept, strings.Join(parts, "\n"), nil
}

func contactMaterial(in Input) bool {
	return strings.TrimSpace(in.Phone) != "" ||
		strings.TrimSpace(in.Email) != "" ||
		strings.TrimSpace(in.CRMProfile) != "" ||
		strings.TrimSpace(in.SalesNotes) != ""
}

func looksLikeContact(value string) bool {
	if strings.Contains(value, "@") {
		return true
	}
	run := 0
	for _, r := range value {
		if r >= '0' && r <= '9' {
			run++
			if run >= 8 {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

func costFrom(minor *int64) Cost {
	if minor == nil {
		return Cost{}
	}
	return Cost{Known: true, Minor: *minor}
}

func sealDraft(draft Draft) Draft {
	draft.Sent = false
	draft.Published = false
	draft.MarketingLicense = false
	draft.ReplyBotStarted = false
	draft.EditableByHand = true
	draft.ModelStamp = StampModelIncomplete
	draft.Status = StatusDraftSaved
	draft.Facts = cloneFacts(draft.Facts)
	return draft
}

func copyDraft(draft Draft) Draft {
	draft.Facts = cloneFacts(draft.Facts)
	return draft
}

func cloneFacts(facts []Fact) []Fact {
	if len(facts) == 0 {
		return nil
	}
	out := make([]Fact, len(facts))
	copy(out, facts)
	return out
}

func seal(result Result) Result {
	result.Payload = Payload{
		SourceLeadID: result.Payload.SourceLeadID,
		Version:      result.Payload.Version,
		PayerRef:     result.Payload.PayerRef,
	}
	result.FixtureRef = "fixture:" + result.Target + ":" + result.ID
	result.Fixture = true
	result.LiveSession = false
	result.ProjectCount = 1
	result.GenerationOK = true
	result.Sent = false
	result.Published = false
	result.MarketingLicense = false
	result.ModelStamp = StampModelIncomplete
	result.ProviderCalls = 0
	result.ChargeWrites = 0
	result.BillingPassed = false
	result.ServiceProvider = ServiceNotVerified
	result.Billing = BillingNotVerified
	result.Production = ProductionNotAuthorized
	result.HumanAdoption = HumanUnknown
	result.Status = StatusFixtureRecorded
	return result
}

func handoffKey(tenantID, leadID string, version int, target string) string {
	return tenantID + "\x00" + leadID + "\x00" + strconv.Itoa(version) + "\x00" + target
}

func handoffID(tenantID, leadID string, version int, target string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"craft-handoff/v1", tenantID, leadID, strconv.Itoa(version), target,
	}, "\n")))
	return "crh_" + hex.EncodeToString(sum[:16])
}

func canonicalID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == ':' || r == '-':
		default:
			return false
		}
	}
	return true
}
