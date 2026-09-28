// Package lightcopy decides in-scene reply, email, and marketing-brief drafts.
//
// 本包不访问网络，不调用模型，不建钱包，也不制作视频、产品图或数字人。
// 没有真实模型产出时生成必须失败，不能用模板占位冒充正文。
// 用户确认只记下确认，不表示已发送、已发布或已取得营销许可。
package lightcopy

import (
	"strings"
	"time"
)

const (
	KindReply = "reply"
	KindEmail = "email"
	KindBrief = "marketing_brief"

	KindOpportunity = "opportunity"
	KindCampaign    = "campaign"

	ToolGoBoost      = "goboost"
	ToolProductImage = "product_image"
	ToolDigitalHuman = "digital_human"
	ToolAiCut        = "aicut"

	OriginManual = "manual"

	StatusFailed   = "failed"
	StatusRecorded = "recorded"
	StatusDeclined = "declined"
	StatusRefused  = "refused"

	ReasonModelUnavailable = "model_unavailable"
	ReasonPlaceholder      = "placeholder_refused"
	ReasonEmpty            = "empty_body"
	ReasonTenant           = "tenant_mismatch"
	ReasonPrice            = "price_expired"
	ReasonUnauthorized     = "unauthorized_field"
	ReasonMissing          = "fact_missing"
	ReasonNothing          = "nothing_selected"
	ReasonToolEntry        = "tool_entry_unavailable"
	ReasonOpenExisting     = "open_existing_entry"
	ReasonUnknownTool      = "unknown_tool"
	ReasonAdvancedDeclined = "advanced_tool_declined"
)

// Draft is one saved text. Generated, Sent, Published and MarketingPermitted
// stay false in this build: there is no live model and no send connector.
type Draft struct {
	Body               string
	Origin             string
	ContentVersion     int
	Generated          bool
	Sent               bool
	Published          bool
	MarketingPermitted bool
	UserConfirmed      bool
	LiveCharge         int
}

// ModelOutput is text claimed to come from a model. FromLiveModel is never
// set by this repository: there is no model client here.
type ModelOutput struct {
	FromLiveModel bool
	Text          string
}

// Subject is the current opportunity or campaign snapshot. Contact text,
// sales notes and profiles are carried only so Project can reject them.
type Subject struct {
	Kind            string
	ID              string
	TenantID        string
	Title           string
	ActivityRef     string
	PriceCents      *int64
	PriceValidUntil time.Time
	ContactText     string
	SalesNote       string
	Profile         string
}

// Packet is the only payload a later tool may receive.
type Packet struct {
	SubjectKind string
	SubjectID   string
	Facts       map[string]any
}

// Handoff is one attempt to open an existing professional tool.
// ProjectCreated stays false: this CRM does not create those projects.
type Handoff struct {
	ID             string
	Key            string
	Tool           string
	SourceKind     string
	SourceID       string
	DraftID        string
	ContentVersion int
	Facts          map[string]any
	Status         string
	Reason         string
	ProjectCreated bool
	Regenerated    bool
}

// HandoffInput is one open attempt. Existing is the row already stored for Key.
type HandoffInput struct {
	Key             string
	Tool            string
	Decline         bool
	EntryConfigured bool
	SourceKind      string
	SourceID        string
	DraftID         string
	ContentVersion  int
	Facts           map[string]any
	Existing        *Handoff
}

// Generate refuses to write model text unless a live model produced it, and
// refuses template placeholders even then. This process never sets FromLiveModel.
func Generate(current Draft, model ModelOutput) (Draft, string) {
	if !model.FromLiveModel || strings.TrimSpace(model.Text) == "" {
		return current, ReasonModelUnavailable
	}
	if isPlaceholder(model.Text) {
		return current, ReasonPlaceholder
	}
	next := current
	next.Body = strings.TrimSpace(model.Text)
	next.Origin = OriginManual
	next.Generated = false
	next.Sent = false
	next.Published = false
	next.MarketingPermitted = false
	next.UserConfirmed = false
	next.LiveCharge = 0
	if next.ContentVersion < 1 {
		next.ContentVersion = 1
	} else {
		next.ContentVersion++
	}
	return next, ""
}

func isPlaceholder(text string) bool {
	return strings.Contains(text, "{{") || strings.Contains(text, "}}") || strings.Contains(text, "模板占位")
}

// SaveManual stores the user's text. An edit clears confirmation.
func SaveManual(current Draft, body string) (Draft, string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return current, ReasonEmpty
	}
	next := current
	next.Body = body
	next.Origin = OriginManual
	next.Generated = false
	next.Sent = false
	next.Published = false
	next.MarketingPermitted = false
	next.UserConfirmed = false
	next.LiveCharge = 0
	if next.ContentVersion < 1 {
		next.ContentVersion = 1
	} else if current.Body != body {
		next.ContentVersion++
	}
	return next, ""
}

// Confirm records the user's acceptance and does not send, publish, or grant
// marketing permission.
func Confirm(current Draft) Draft {
	current.UserConfirmed = true
	current.Sent = false
	current.Published = false
	current.MarketingPermitted = false
	current.Generated = false
	current.LiveCharge = 0
	return current
}

var allowedFact = map[string]bool{
	"title":        true,
	"activity_ref": true,
	"price_cents":  true,
}

var forbiddenFact = map[string]bool{
	"contact_text":        true,
	"contact_body":        true,
	"sales_note":          true,
	"internal_note":       true,
	"profile":             true,
	"auth_scope_snapshot": true,
}

// Project copies only selected necessary facts. Any forbidden or unknown key
// rejects the whole packet so a partial leak cannot proceed.
func Project(callerTenant string, sub Subject, selected []string, now time.Time) (Packet, string) {
	packet := Packet{SubjectKind: sub.Kind, SubjectID: sub.ID, Facts: map[string]any{}}
	if callerTenant == "" || callerTenant != sub.TenantID {
		return packet, ReasonTenant
	}
	if len(selected) == 0 {
		return packet, ReasonNothing
	}
	for _, key := range selected {
		if forbiddenFact[key] || !allowedFact[key] {
			return Packet{SubjectKind: sub.Kind, SubjectID: sub.ID, Facts: map[string]any{}}, ReasonUnauthorized
		}
	}
	facts := map[string]any{}
	for _, key := range selected {
		switch key {
		case "title":
			if strings.TrimSpace(sub.Title) == "" {
				return Packet{SubjectKind: sub.Kind, SubjectID: sub.ID, Facts: map[string]any{}}, ReasonMissing
			}
			facts["title"] = sub.Title
		case "activity_ref":
			if strings.TrimSpace(sub.ActivityRef) == "" {
				return Packet{SubjectKind: sub.Kind, SubjectID: sub.ID, Facts: map[string]any{}}, ReasonMissing
			}
			facts["activity_ref"] = sub.ActivityRef
		case "price_cents":
			if sub.PriceCents == nil || sub.PriceValidUntil.IsZero() || !sub.PriceValidUntil.After(now) {
				return Packet{SubjectKind: sub.Kind, SubjectID: sub.ID, Facts: map[string]any{}}, ReasonPrice
			}
			facts["price_cents"] = *sub.PriceCents
		}
	}
	packet.Facts = facts
	return packet, ""
}

var knownTool = map[string]bool{
	ToolGoBoost:      true,
	ToolProductImage: true,
	ToolDigitalHuman: true,
	ToolAiCut:        true,
}

// OpenHandoff records a request to open an existing tool entry.
// A repeated key returns the stored attempt and does not regenerate.
// A configured entry still does not create a project inside this CRM.
func OpenHandoff(in HandoffInput) Handoff {
	if in.Existing != nil && in.Existing.Key == in.Key && in.Key != "" {
		again := *in.Existing
		again.Regenerated = false
		again.ProjectCreated = false
		return again
	}
	out := Handoff{
		ID:             in.Key,
		Key:            in.Key,
		Tool:           in.Tool,
		SourceKind:     in.SourceKind,
		SourceID:       in.SourceID,
		DraftID:        in.DraftID,
		ContentVersion: in.ContentVersion,
		Facts:          in.Facts,
		ProjectCreated: false,
		Regenerated:    false,
	}
	if in.Decline {
		out.Status = StatusDeclined
		out.Reason = ReasonAdvancedDeclined
		return out
	}
	if !knownTool[in.Tool] {
		out.Status = StatusRefused
		out.Reason = ReasonUnknownTool
		return out
	}
	if !in.EntryConfigured {
		out.Status = StatusFailed
		out.Reason = ReasonToolEntry
		return out
	}
	out.Status = StatusRecorded
	out.Reason = ReasonOpenExisting
	return out
}
