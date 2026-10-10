package notifyingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const profileVersion = "directed-event/v1"

const (
	EventLeadAuthorizedSubmitted = "lead.authorized_submitted"
	EventLeadConsentRevoked      = "lead.consent_revoked"
)

// Delivery is one signed Notify event after verification.
type Delivery struct {
	NotifyEventID  string
	Type           string
	SchemaVersion  int
	AppID          string
	TenantID       string
	OccurredAt     time.Time
	IdempotencyKey string
	Profile        Profile
	Payload        Payload
}

// Profile is the directed-event/v1 event_profile section.
type Profile struct {
	ProfileVersion string `json:"profile_version"`
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	SourceApp      string `json:"source_app"`
	TargetApp      string `json:"target_app"`
	TenantScope    string `json:"tenant_scope"`
	SourceRef      string `json:"source_ref"`
	SourceVersion  int    `json:"source_version"`
	Correlation    *struct {
		HandoffID string `json:"handoff_id,omitempty"`
		Ref       string `json:"ref,omitempty"`
	} `json:"correlation,omitempty"`
	PayloadRef PayloadRef `json:"payload_ref"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// PayloadRef points at the authorized submission. Only touch:// refs are
// accepted; http(s) callback URLs are rejected.
type PayloadRef struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

// Payload is non-contact provenance carried beside the reference.
// CampaignVersion is a string and is not the integer SourceVersion.
// BrandDisplayName is display copy only; it is never a tenant id.
// GrantRef is stored as a reference and is not a payment result.
type Payload struct {
	CampaignRef          string `json:"campaign_ref"`
	StoreRef             string `json:"store_ref"`
	Channel              string `json:"channel"`
	Tag                  string `json:"tag"`
	AssetRef             string `json:"asset_ref"`
	SourceVersion        int    `json:"source_version"`
	ConsentRef           string `json:"consent_ref"`
	ConsentVersion       string `json:"consent_version"`
	ConsentAt            string `json:"consent_at"`
	MarketingOptin       bool   `json:"marketing_optin"`
	Revoked              bool   `json:"revoked"`
	TraceID              string `json:"trace_id,omitempty"`
	ReturnTarget         string `json:"return_target,omitempty"`
	CampaignVersion      string `json:"campaign_version,omitempty"`
	GrantRef             string `json:"grant_ref,omitempty"`
	BrandDisplayName     string `json:"brand_display_name,omitempty"`
	NotificationBrandRef string `json:"notification_brand_ref,omitempty"`
}

var contactKeyFragments = []string{
	"phone", "mobile", "tel", "email", "wechat", "weixin", "whatsapp", "msisdn",
}

// ParseDelivery strictly reads a Notify event body. Contact-shaped keys are
// refused so a payload that smuggled a phone never reaches the database.
func ParseDelivery(body []byte) (Delivery, error) {
	if !utf8.Valid(body) || !json.Valid(body) {
		return Delivery{}, errors.New("invalid JSON body")
	}
	if keys := contactKeys(body); len(keys) > 0 {
		return Delivery{}, fmt.Errorf("event carries contact-shaped keys")
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return Delivery{}, err
	}
	var d Delivery
	if err := json.Unmarshal(body, &struct {
		ID             *string          `json:"id"`
		Type           *string          `json:"type"`
		SchemaVersion  *int             `json:"schema_version"`
		AppID          *string          `json:"app_id"`
		TenantID       *string          `json:"tenant_id"`
		OccurredAt     *time.Time       `json:"occurred_at"`
		IdempotencyKey *string          `json:"idempotency_key"`
		Data           *json.RawMessage `json:"data"`
	}{
		ID: &d.NotifyEventID, Type: &d.Type, SchemaVersion: &d.SchemaVersion,
		AppID: &d.AppID, TenantID: &d.TenantID, OccurredAt: &d.OccurredAt,
		IdempotencyKey: &d.IdempotencyKey,
	}); err != nil {
		return Delivery{}, err
	}
	rawData, ok := top["data"]
	if !ok {
		return Delivery{}, errors.New("missing data")
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &data); err != nil {
		return Delivery{}, errors.New("data must be an object")
	}
	for k := range data {
		if k != "event_profile" && k != "payload" {
			return Delivery{}, fmt.Errorf("unknown data key %q", k)
		}
	}
	rawProfile, ok := data["event_profile"]
	if !ok {
		return Delivery{}, errors.New("missing event_profile")
	}
	dec := json.NewDecoder(bytes.NewReader(rawProfile))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d.Profile); err != nil {
		return Delivery{}, fmt.Errorf("event_profile: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Delivery{}, errors.New("event_profile trailing content")
	}
	payloadPresent := false
	if rawPayload, ok := data["payload"]; ok {
		payloadPresent = true
		pdec := json.NewDecoder(bytes.NewReader(rawPayload))
		pdec.DisallowUnknownFields()
		if err := pdec.Decode(&d.Payload); err != nil {
			return Delivery{}, fmt.Errorf("payload: %w", err)
		}
	}
	if err := validateDelivery(&d, payloadPresent); err != nil {
		return Delivery{}, err
	}
	return d, nil
}

func validateDelivery(d *Delivery, payloadPresent bool) error {
	if d.SchemaVersion < 1 {
		return errors.New("schema_version must be >= 1")
	}
	if d.Profile.ProfileVersion != profileVersion {
		return fmt.Errorf("unsupported profile version %q", d.Profile.ProfileVersion)
	}
	if d.Type != d.Profile.EventType || d.AppID != d.Profile.SourceApp || d.TenantID != d.Profile.TenantScope {
		return errors.New("envelope type/app/tenant must match event_profile")
	}
	switch d.Profile.EventType {
	case EventLeadAuthorizedSubmitted, EventLeadConsentRevoked:
	default:
		return fmt.Errorf("event_type %q is not accepted", d.Profile.EventType)
	}
	if d.Profile.SourceVersion < 0 {
		return errors.New("source_version must be non-negative")
	}
	if !token(d.Profile.EventID, 128) || !token(d.Profile.SourceRef, 128) || !token(d.Profile.SourceApp, 64) {
		return errors.New("event_id/source_ref/source_app shape")
	}
	if strings.TrimSpace(d.Profile.TenantScope) == "" || utf8.RuneCountInString(d.Profile.TenantScope) > 256 {
		return errors.New("tenant_scope")
	}
	wantRef := "touch://leads/" + d.Profile.SourceRef
	if d.Profile.PayloadRef.Ref != wantRef {
		return errors.New("payload_ref must be the registered touch://leads/<source_ref> reference")
	}
	if len(d.Profile.PayloadRef.SHA256) != 64 {
		return errors.New("payload_ref.sha256")
	}
	if _, err := hex.DecodeString(d.Profile.PayloadRef.SHA256); err != nil || strings.ToLower(d.Profile.PayloadRef.SHA256) != d.Profile.PayloadRef.SHA256 {
		return errors.New("payload_ref.sha256")
	}
	if payloadPresent && d.Payload.SourceVersion != d.Profile.SourceVersion {
		return errors.New("payload source_version must match event_profile source_version")
	}
	if d.Payload.TraceID != "" && d.Payload.TraceID != "lead:"+d.Profile.SourceRef {
		return errors.New("trace_id must be lead:<source_ref>")
	}
	if !campaignVersionOK(d.Payload.CampaignVersion) {
		return errors.New("campaign_version")
	}
	if !returnTargetOK(d.Payload.ReturnTarget) {
		return errors.New("return_target")
	}
	if d.Payload.GrantRef != "" && !token(d.Payload.GrantRef, 128) {
		return errors.New("grant_ref")
	}
	if d.Payload.NotificationBrandRef != "" && !token(d.Payload.NotificationBrandRef, 128) {
		return errors.New("notification_brand_ref")
	}
	if utf8.RuneCountInString(d.Payload.BrandDisplayName) > 128 || strings.ContainsAny(d.Payload.BrandDisplayName, "\r\n") {
		return errors.New("brand_display_name")
	}
	return nil
}

func campaignVersionOK(s string) bool {
	if s == "" {
		return true
	}
	return token(s, 64)
}

func returnTargetOK(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 256 || !strings.HasPrefix(s, "/") || strings.Contains(s, "://") || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	return true
}

// intakeFact is the idempotency identity of one trace version.
// Event id and transport fields are not part of it.
type intakeFact struct {
	EventType      string     `json:"event_type"`
	SourceRef      string     `json:"source_ref"`
	SourceVersion  int        `json:"source_version"`
	PayloadRef     PayloadRef `json:"payload_ref"`
	CorrelationRef string     `json:"correlation_ref,omitempty"`
	Payload        Payload    `json:"payload"`
}

func factOf(d Delivery) intakeFact {
	ref := ""
	if d.Profile.Correlation != nil {
		ref = d.Profile.Correlation.Ref
	}
	return intakeFact{
		EventType: d.Profile.EventType, SourceRef: d.Profile.SourceRef, SourceVersion: d.Profile.SourceVersion,
		PayloadRef: d.Profile.PayloadRef, CorrelationRef: ref, Payload: d.Payload,
	}
}

// CanonicalFact is the stored JSON for one accepted fact.
func CanonicalFact(d Delivery) string {
	b, _ := json.Marshal(factOf(d))
	return string(b)
}

// SameFact reports whether stored JSON is the same fact as d.
// An empty stored fact is not a match.
func SameFact(stored string, d Delivery) bool {
	if stored == "" {
		return false
	}
	var got intakeFact
	if err := json.Unmarshal([]byte(stored), &got); err != nil {
		return false
	}
	return got == factOf(d)
}

func token(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':', r == '@':
		default:
			return false
		}
	}
	return true
}

func contactKeys(body []byte) []string {
	var top any
	if err := json.Unmarshal(body, &top); err != nil {
		return []string{"invalid"}
	}
	var hits []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				lk := strings.ToLower(k)
				for _, frag := range contactKeyFragments {
					if strings.Contains(lk, frag) {
						hits = append(hits, k)
						break
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(top)
	return hits
}

// RecordSHA256 is the touch-engine canonical record hash: JSON object of
// name/phone/wechat with encoding/json key order. The contact values travel
// only on the authorized fetch, never on the event.
func RecordSHA256(name, phone, wechat string) string {
	b, _ := json.Marshal(map[string]string{"name": name, "phone": phone, "wechat": wechat})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NonLeadChannel reports browse / 企微点击 style facts that must not create a contact.
func NonLeadChannel(channel, purpose string) bool {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "browse", "view", "wecom_click", "wecom-click", "weixin_click":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case "browse", "view", "wecom_click", "wecom-click", "weixin_click":
		return true
	}
	return false
}
