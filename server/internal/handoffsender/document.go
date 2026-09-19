// Package handoffsender is the HUI-1749 constrained handoff SENDER for
// applicable (creative_service) opportunities.
//
// 红线:
//   - 文档只含用户显式确认的字段与显式选择的资产引用;联系人档案
//     (phone/email/notes/tags)与销售跟进历史在任何路径都不进入载荷;
//   - 幂等:同 opportunity+同内容指纹 → 同一 handoff_id 同字节重试;
//     新内容 = 新确认 = 新快照新版本(绝不覆盖已确认内容);
//   - 投递成功只是「接单侧已建草稿」的状态投影,绝不表示成交/已支付;
//   - 线格式 = E1 order-handoff/v1 + source-profile/v1(冻结面),发送端
//     以共享向量(source-profile-vectors.json)独立钉版。
package handoffsender

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// SchemaVersionV1 is the frozen envelope version (extension, not v2).
const SchemaVersionV1 = "order-handoff/v1"

// ProfileVersionV1 is the only source_profile version this sender emits.
const ProfileVersionV1 = "source-profile/v1"

// SourceKindStandalone is the only kind the receiver accepts for leads drafts.
const SourceKindStandalone = "standalone"

// CapabilityHandoff / ReturnTargetID are ecosystem facts read from the app
// registry (values mirror the guanlan-order receiver's expectations; see
// _reports/hui-1749-l1/REPORT.md O2 alignment table).
const (
	CapabilityHandoff = "leads.handoff"
	ReturnTargetID    = "rc-leads-engine-main"
)

// DeliveryScope is the minimal authorization scope set for a draft import.
var DeliveryScopes = []string{"project.resume", "asset.import"}

// GatingPolicyVersion mirrors the shared vectors' policy reference.
const GatingPolicyVersion = "order-gating/v1"

// Constraints prefixes (opaque references; the receiver consumes exactly the
// purpose:/category: prefixes per the O2 alignment list).
const (
	ConstraintPurpose  = "purpose:"
	ConstraintCategory = "category:"
	ConstraintBudget   = "budget_cents:"
	ConstraintDeadline = "deadline:"
)

// PurposeServiceProcurement is the only authorized purpose (对齐 O2 拒绝表:
// consumer_marketing 是 campaign 营销事件,显式不适用本链路)。
const PurposeServiceProcurement = "service_procurement"

// MaxDocBytes is the wire size cap (frozen: 1 MiB).
const MaxDocBytes = 1 << 20

// maxValidityWindow keeps issued_at < expires_at ≤ +900s (frozen). The sender
// issues a 600s window — comfortably inside the cap, long enough for one
// delivery attempt plus retries.
const maxValidityWindow = 900 * time.Second

var (
	serviceCategoryRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	hexDigestRe       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	dateRe            = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// AssetInput is one EXPLICITLY user-provided asset reference. The CRM has no
// asset store, so every field must come from the confirming user in the same
// request; anything less (missing hash/size) is an unauthorized reference and
// is rejected — nothing is ever guessed from CRM notes or followups.
type AssetInput struct {
	Ref       string `json:"asset_ref"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	MediaType string `json:"media_type"`
}

// ConfirmedInput is the set of user-confirmed facts for one handoff snapshot.
// Only these fields (plus opportunity identity/context) enter the document.
type ConfirmedInput struct {
	// TenantID is the caller's membership tenant (bookkeeping only; it is
	// NOT part of the fingerprint and NOT part of the wire document — the
	// wire carries the deployment tenant_scope instead).
	TenantID string `json:"-"`

	OpportunityID    string
	OpportunityTitle string
	BusinessCategory string
	Summary          string       `json:"summary"`
	ServiceCategory  string       `json:"service_category"`
	BudgetCents      *int64       `json:"budget_cents,omitempty"`
	Deadline         string       `json:"deadline,omitempty"`
	Assets           []AssetInput `json:"assets,omitempty"`
}

// Fingerprint returns the content fingerprint of the confirmed input: same
// fingerprint ⇒ same snapshot ⇒ same handoff_id (idempotent retry); any
// change ⇒ new snapshot. Opportunity identity and category are part of the
// fingerprint so content can never float between records.
func (in ConfirmedInput) Fingerprint() (string, error) {
	canonical := struct {
		OpportunityID    string       `json:"opportunity_id"`
		BusinessCategory string       `json:"business_category"`
		Summary          string       `json:"summary"`
		ServiceCategory  string       `json:"service_category"`
		BudgetCents      *int64       `json:"budget_cents,omitempty"`
		Deadline         string       `json:"deadline,omitempty"`
		Assets           []AssetInput `json:"assets,omitempty"`
	}{
		OpportunityID: in.OpportunityID, BusinessCategory: in.BusinessCategory,
		Summary: in.Summary, ServiceCategory: in.ServiceCategory,
		BudgetCents: in.BudgetCents, Deadline: in.Deadline, Assets: in.Assets,
	}
	raw, err := json.Marshal(&canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks the user-confirmed inputs (shape only; business meaning
// stays with the caller). All limits are stricter than or equal to the frozen
// wire limits. Absence of optional facts is a MissingFields concern, not an
// error here — the preview must be able to render 缺失 marks.
func (in ConfirmedInput) Validate() error {
	if in.OpportunityID == "" {
		return fmt.Errorf("opportunity_id is required")
	}
	if in.BusinessCategory != "creative_service" {
		return fmt.Errorf("only creative_service opportunities can become service drafts")
	}
	if runeLen(in.Summary) == 0 {
		return fmt.Errorf("summary is required (缺失字段标缺失,不由 CRM 备注猜测)")
	}
	if !serviceCategoryRe.MatchString(in.ServiceCategory) {
		return fmt.Errorf("service_category must be a lowercase slug like video or design-poster")
	}
	return in.ValidateProvided()
}

// ValidateProvided checks ONLY the fields that are actually provided (preview
// path: malformed provided facts are rejected; absent facts are marked
// missing, never rejected and never guessed from CRM notes).
func (in ConfirmedInput) ValidateProvided() error {
	if runeLen(in.Summary) > 2000 {
		return fmt.Errorf("summary must be at most 2000 characters")
	}
	if in.ServiceCategory != "" && !serviceCategoryRe.MatchString(in.ServiceCategory) {
		return fmt.Errorf("service_category must be a lowercase slug like video or design-poster")
	}
	if in.BudgetCents != nil && *in.BudgetCents <= 0 {
		return fmt.Errorf("budget_cents must be a positive amount when provided")
	}
	if in.Deadline != "" && !dateRe.MatchString(in.Deadline) {
		return fmt.Errorf("deadline must be YYYY-MM-DD when provided")
	}
	if len(in.Assets) > 100 {
		return fmt.Errorf("at most 100 asset references")
	}
	seen := map[string]bool{}
	for _, a := range in.Assets {
		if a.Ref == "" || runeLen(a.Ref) > 256 || strings.TrimSpace(a.Ref) == "" {
			return fmt.Errorf("asset_ref must be 1..256 non-blank characters")
		}
		if seen[a.Ref] {
			return fmt.Errorf("duplicate asset_ref: %s", a.Ref)
		}
		seen[a.Ref] = true
		if !hexDigestRe.MatchString(a.SHA256) {
			// 未授权/不完整资产引用:缺 hash = 无法核实 = 拒绝,绝不代填。
			return fmt.Errorf("asset %s: sha256 (64 lowercase hex) is required and user-confirmed", a.Ref)
		}
		if a.SizeBytes < 1 {
			return fmt.Errorf("asset %s: size_bytes must be a positive integer", a.Ref)
		}
		if a.MediaType == "" || runeLen(a.MediaType) > 256 {
			return fmt.Errorf("asset %s: media_type is required", a.Ref)
		}
	}
	return nil
}

// MissingFields lists the optional confirmed facts the user left empty. The
// preview marks these as 缺失; nothing fills them in from CRM notes.
func (in ConfirmedInput) MissingFields() []string {
	var missing []string
	if strings.TrimSpace(in.Summary) == "" {
		missing = append(missing, "summary")
	}
	if in.BudgetCents == nil {
		missing = append(missing, "budget_cents")
	}
	if in.Deadline == "" {
		missing = append(missing, "deadline")
	}
	if len(in.Assets) == 0 {
		missing = append(missing, "assets")
	}
	return missing
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// ---- wire document ------------------------------------------------------------

// wireDoc mirrors the frozen schema key order exactly (vectors use this
// order; byte-identical retries come from storage, determinism from here).
type wireAsset struct {
	AssetRef  string `json:"asset_ref"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	MediaType string `json:"media_type"`
}

type wireActor struct {
	Issuer  string `json:"issuer"`
	AppID   string `json:"app_id"`
	Subject string `json:"subject"`
}

type wireBinding struct {
	BindingRef  string `json:"binding_ref"`
	ProofDigest string `json:"proof_digest"`
}

type wireGating struct {
	PolicyVersion string   `json:"policy_version"`
	OpenGates     []string `json:"open_gates"`
}

type wireDelivery struct {
	MediaType   string `json:"media_type"`
	Description string `json:"description"`
}

type wireProfile struct {
	ProfileVersion string   `json:"profile_version"`
	SourceKind     string   `json:"source_kind"`
	TenantScope    string   `json:"tenant_scope"`
	Capabilities   []string `json:"capabilities"`
	Constraints    []string `json:"constraints"`
	ReturnTargetID string   `json:"return_target_id"`
	// CampaignRef is deliberately absent for standalone (键必须缺席).
}

type wireDoc struct {
	SchemaVersion    string       `json:"schema_version"`
	HandoffID        string       `json:"handoff_id"`
	SourceApp        string       `json:"source_app"`
	TargetApp        string       `json:"target_app"`
	PrincipalID      string       `json:"principal_id"`
	BriefVersion     string       `json:"brief_version"`
	SourceProjectRef string       `json:"source_project_ref"`
	SourceRevision   string       `json:"source_revision"`
	Actor            wireActor    `json:"actor"`
	Binding          wireBinding  `json:"binding"`
	Gating           wireGating   `json:"gating"`
	Assets           []wireAsset  `json:"assets"`
	DeliverySpec     wireDelivery `json:"delivery_spec"`
	Scopes           []string     `json:"scopes"`
	IssuedAt         string       `json:"issued_at"`
	ExpiresAt        string       `json:"expires_at"`
	SourceProfile    wireProfile  `json:"source_profile"`
}

// BuildParams carries the ecosystem + deployment facts for one document.
type BuildParams struct {
	// SourceApp / TargetApp come from the app registry entries.
	SourceApp string
	TargetApp string
	// TenantScope is the deployment-co-configured scope (empty = fail closed
	// before build; the receiver does an exact match).
	TenantScope string
	// PrincipalID + ActorIssuer reuse the real caller identity.
	PrincipalID string
	ActorIssuer string
	// ProofSalt is an optional deployment salt for the PROVISIONAL binding
	// proof digest (no real secret ever lives in this repo).
	ProofSalt string
	// Now is the issuance clock (UTC; tests pin it).
	Now time.Time
}

// BuildDocument renders the exact wire bytes for one confirmed snapshot.
// handoffID must be the persisted, stable id; sourceVersion the per-opportunity
// monotonic version (goes on the wire as brief_version).
func BuildDocument(in ConfirmedInput, fingerprint, handoffID string, sourceVersion int64, p BuildParams) ([]byte, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if len(fingerprint) != 64 || !hexDigestRe.MatchString(fingerprint) {
		return nil, fmt.Errorf("fingerprint must be 64 lowercase hex")
	}
	if handoffID == "" || strings.TrimSpace(handoffID) == "" || runeLen(handoffID) > 256 {
		return nil, fmt.Errorf("handoff_id is required")
	}
	if p.SourceApp == "" || p.TargetApp == "" {
		return nil, fmt.Errorf("source_app/target_app must come from the app registry")
	}
	if strings.TrimSpace(p.TenantScope) == "" {
		// 租户域任一侧为空 fail-closed:不出租户域为空的交接文档。
		return nil, fmt.Errorf("tenant_scope is required (fail-closed)")
	}
	if strings.TrimSpace(p.PrincipalID) == "" {
		return nil, fmt.Errorf("principal_id (caller principal) is required")
	}
	if sourceVersion < 1 {
		return nil, fmt.Errorf("source_version must be >= 1")
	}

	issued := p.Now.UTC().Truncate(time.Second)
	expires := issued.Add(600 * time.Second)
	if !expires.After(issued) || expires.Sub(issued) > maxValidityWindow {
		return nil, fmt.Errorf("validity window must be within issued_at+900s")
	}

	constraints := []string{
		ConstraintPurpose + PurposeServiceProcurement,
		ConstraintCategory + in.ServiceCategory,
	}
	if in.BudgetCents != nil {
		constraints = append(constraints, ConstraintBudget+strconv.FormatInt(*in.BudgetCents, 10))
	}
	if in.Deadline != "" {
		constraints = append(constraints, ConstraintDeadline+in.Deadline)
	}

	assets := make([]wireAsset, 0, len(in.Assets))
	for _, a := range in.Assets {
		assets = append(assets, wireAsset{AssetRef: a.Ref, SHA256: a.SHA256, SizeBytes: a.SizeBytes, MediaType: a.MediaType})
	}

	doc := wireDoc{
		SchemaVersion:    SchemaVersionV1,
		HandoffID:        handoffID,
		SourceApp:        p.SourceApp,
		TargetApp:        p.TargetApp,
		PrincipalID:      p.PrincipalID,
		BriefVersion:     strconv.FormatInt(sourceVersion, 10),
		SourceProjectRef: in.OpportunityID,
		SourceRevision:   fingerprint,
		Actor:            wireActor{Issuer: p.ActorIssuer, AppID: p.SourceApp, Subject: p.PrincipalID},
		Binding:          wireBinding{BindingRef: "binding-" + handoffID, ProofDigest: proofDigest(handoffID, in.OpportunityID, sourceVersion, p.ProofSalt)},
		Gating:           wireGating{PolicyVersion: GatingPolicyVersion, OpenGates: []string{}},
		Assets:           assets,
		DeliverySpec:     wireDelivery{MediaType: "text/brief", Description: deliveryDescription(in)},
		Scopes:           append([]string{}, DeliveryScopes...),
		IssuedAt:         issued.Format("2006-01-02T15:04:05Z"),
		ExpiresAt:        expires.Format("2006-01-02T15:04:05Z"),
		SourceProfile: wireProfile{
			ProfileVersion: ProfileVersionV1,
			SourceKind:     SourceKindStandalone,
			TenantScope:    p.TenantScope,
			Capabilities:   []string{CapabilityHandoff},
			Constraints:    constraints,
			ReturnTargetID: ReturnTargetID,
		},
	}
	raw, err := json.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxDocBytes {
		return nil, fmt.Errorf("document exceeds the frozen 1 MiB cap")
	}
	// 发送端自校验是硬闸:任何构建产物必须先通过冻结语义+发送端不变式
	// 自检才能落库。
	if err := ValidateSenderDocument(raw); err != nil {
		return nil, fmt.Errorf("built document failed self-validation: %w", err)
	}
	return raw, nil
}

// deliveryDescription composes the confirmed requirement text. It contains
// ONLY confirmed facts — never CRM notes, contact fields or followups.
func deliveryDescription(in ConfirmedInput) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(in.Summary))
	if in.BudgetCents != nil {
		fmt.Fprintf(&b, "\n[预算] %d 分(用户确认)", *in.BudgetCents)
	} else {
		b.WriteString("\n[预算] 缺失(待补)")
	}
	if in.Deadline != "" {
		fmt.Fprintf(&b, "\n[截止] %s(用户确认)", in.Deadline)
	} else {
		b.WriteString("\n[截止] 缺失(待补)")
	}
	if len(in.Assets) == 0 {
		b.WriteString("\n[素材] 无(纯文字需求,未选择任何素材引用)")
	} else {
		b.WriteString("\n[素材引用] ")
		refs := make([]string, 0, len(in.Assets))
		for _, a := range in.Assets {
			refs = append(refs, a.Ref)
		}
		b.WriteString(strings.Join(refs, "; "))
	}
	return b.String()
}

// proofDigest derives the PROVISIONAL binding proof digest deterministically
// (same snapshot ⇒ same document; the real one-time credential issuance is
// the identity domain, deferred to the co-test ticket).
func proofDigest(handoffID, opportunityID string, version int64, salt string) string {
	h := sha256.New()
	h.Write([]byte("leads-engine-handoff|" + handoffID + "|" + opportunityID + "|" + strconv.FormatInt(version, 10) + "|" + salt))
	return hex.EncodeToString(h.Sum(nil))
}

// NewHandoffID mints a fresh stable handoff id (persisted before first use).
func NewHandoffID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("entropy unavailable: %w", err)
	}
	return "handoff-" + hex.EncodeToString(b[:]), nil
}
