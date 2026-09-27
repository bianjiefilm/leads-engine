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
type Payload struct {
	CampaignRef    string `json:"campaign_ref"`
	StoreRef       string `json:"store_ref"`
	Channel        string `json:"channel"`
	Tag            string `json:"tag"`
	AssetRef       string `json:"asset_ref"`
	SourceVersion  int    `json:"source_version"`
	ConsentRef     string `json:"consent_ref"`
	ConsentVersion string `json:"consent_version"`
	ConsentAt      string `json:"consent_at"`
	MarketingOptin bool   `json:"marketing_optin"`
	Revoked        bool   `json:"revoked"`
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
	if rawPayload, ok := data["payload"]; ok {
		pdec := json.NewDecoder(bytes.NewReader(rawPayload))
		pdec.DisallowUnknownFields()
		if err := pdec.Decode(&d.Payload); err != nil {
			return Delivery{}, fmt.Errorf("payload: %w", err)
		}
	}
	if err := validateDelivery(&d); err != nil {
		return Delivery{}, err
	}
	return d, nil
}

func validateDelivery(d *Delivery) error {
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
	return nil
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
