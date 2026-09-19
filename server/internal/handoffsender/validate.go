// validate.go — the sender-side strict validator over the frozen line format.
//
// 语义与 public-ai internal/handoffcontract(及 guanlan-order leadsdraft.Parse)
// 同源,由共享向量 source-profile-vectors.json 钉版:本发送端在构建后、落库前
// 以自校验兜底,保证任何出仓文档都满足冻结语义(17 键封闭、无 order_ref/
// stage_ref、窗口 ≤900s、scopes 闭合枚举、profile 全属性、租户域非空)。
package handoffsender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// LineKind reports which of the two frozen line shapes a document is.
type LineKind string

const (
	// KindOrder is a plain v1 order-source document (NOT produced by this
	// sender; recognized for vector parity).
	KindOrder LineKind = "order"
	// KindProfiled is a source_profile extension document (this sender's
	// only product).
	KindProfiled LineKind = "profiled"
)

// RejectError is a structured, machine-readable refusal.
type RejectError struct {
	Code string
	Msg  string
}

func (e *RejectError) Error() string { return e.Code + ": " + e.Msg }

func rej(code, format string, a ...any) error {
	return &RejectError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Reject codes (mirror the frozen discipline; kept sender-side minimal).
const (
	RejInvalidDocument     = "INVALID_DOCUMENT"
	RejUnsupportedProfile  = "UNSUPPORTED_PROFILE"
	RejSourceKindForbidden = "SOURCE_KIND_ORDER_FORBIDDEN"
)

// whiteSpaceRunes is the fixed Unicode White_Space set (never \s).
var whiteSpaceRunes = map[rune]bool{}

func init() {
	for _, r := range []rune{0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0,
		0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007,
		0x2008, 0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000} {
		whiteSpaceRunes[r] = true
	}
}

func validStrField(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 256 {
		return false
	}
	for _, r := range s {
		if !whiteSpaceRunes[r] {
			return true
		}
	}
	return false
}

func rawNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// strictObject decodes an object rejecting duplicate keys / trailing JSON /
// lone surrogates / non-objects (frozen JSON discipline).
func strictObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, rej(RejInvalidDocument, "invalid JSON: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, rej(RejInvalidDocument, "must be an object")
	}
	m := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, rej(RejInvalidDocument, "invalid key: %v", err)
		}
		key, ok := kt.(string)
		if !ok {
			return nil, rej(RejInvalidDocument, "key must be a string")
		}
		if _, dup := m[key]; dup {
			return nil, rej(RejInvalidDocument, "duplicate key: %s", key)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, rej(RejInvalidDocument, "invalid value: %v", err)
		}
		if hasLoneSurrogate(v) {
			return nil, rej(RejInvalidDocument, "lone surrogate escape rejected")
		}
		m[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, rej(RejInvalidDocument, "object not closed")
	}
	if dec.More() {
		return nil, rej(RejInvalidDocument, "trailing JSON rejected")
	}
	return m, nil
}

func stringField(m map[string]json.RawMessage, key string) (string, error) {
	raw, ok := m[key]
	if !ok || rawNull(raw) {
		return "", rej(RejInvalidDocument, "%s is required", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", rej(RejInvalidDocument, "%s must be a string", key)
	}
	if !validStrField(s) {
		return "", rej(RejInvalidDocument, "%s must be 1..256 non-blank characters", key)
	}
	return s, nil
}

// hasLoneSurrogate mirrors the frozen escape discipline.
func hasLoneSurrogate(raw []byte) bool {
	s := string(raw)
	for i := 0; i+6 <= len(s); {
		if s[i] == '\\' && s[i+1] == '\\' {
			i += 2
			continue
		}
		if s[i] == '\\' && s[i+1] == 'u' {
			v1, ok1 := hexVal(s[i+2], s[i+3])
			v2, ok2 := hexVal(s[i+4], s[i+5])
			if ok1 && ok2 {
				hi := v1<<8 | v2
				if hi >= 0xD800 && hi <= 0xDBFF {
					if i+12 <= len(s) && s[i+6] == '\\' && s[i+7] == 'u' {
						w1, okl := hexVal(s[i+8], s[i+9])
						w2, okl2 := hexVal(s[i+10], s[i+11])
						lo := w1<<8 | w2
						if okl && okl2 && lo >= 0xDC00 && lo <= 0xDFFF {
							i += 12
							continue
						}
					}
					return true
				}
				if hi >= 0xDC00 && hi <= 0xDFFF {
					return true
				}
			}
			i += 6
			continue
		}
		i++
	}
	return false
}

func hexVal(a, b byte) (int, bool) {
	hd := func(c byte) (int, bool) {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0'), true
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10, true
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10, true
		}
		return 0, false
	}
	va, ok1 := hd(a)
	vb, ok2 := hd(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	return va<<4 | vb, true
}

// parseFrozenTime only accepts UTC second precision YYYY-MM-DDTHH:mm:ssZ.
func parseFrozenTime(s string) (time.Time, error) {
	if len(s) != 20 {
		return time.Time{}, rej(RejInvalidDocument, "time must be UTC second precision YYYY-MM-DDTHH:mm:ssZ")
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		return time.Time{}, rej(RejInvalidDocument, "invalid time %q", s)
	}
	return t, nil
}

// ExpiredClock is the vector-pinned clock decision: expires_at ≤ now is
// expired; unparseable input fails closed (expired).
func ExpiredClock(now time.Time, expiresAt string) bool {
	t, err := parseFrozenTime(expiresAt)
	if err != nil {
		return true
	}
	return !t.After(now)
}

// TenantScopeMatches is the exact comparison (任一侧为空 fail-closed,
// 不 trim 不归一) — same function the receiver runs.
func TenantScopeMatches(declared, expected string) bool {
	if declared == "" || expected == "" {
		return false
	}
	return declared == expected
}

// scopeEnum is the closed scope set.
var scopeEnum = map[string]bool{
	"project.resume": true, "asset.import": true, "receipt.write": true, "receipt.read": true,
}

// profiledTopKeys is the exact closed key set of a profiled document.
var profiledTopKeys = map[string]bool{
	"schema_version": true, "handoff_id": true, "source_app": true, "target_app": true,
	"principal_id": true, "brief_version": true, "source_project_ref": true,
	"source_revision": true, "actor": true, "binding": true, "gating": true,
	"assets": true, "delivery_spec": true, "scopes": true, "issued_at": true,
	"expires_at": true, "source_profile": true,
}

// plainV1TopKeys is the plain v1 (order) key set, recognized for vector
// parity only (this sender never emits it).
var plainV1TopKeys = map[string]bool{
	"schema_version": true, "handoff_id": true, "source_app": true, "target_app": true,
	"principal_id": true, "order_ref": true, "stage_ref": true, "brief_version": true,
	"source_project_ref": true, "source_revision": true, "actor": true, "binding": true,
	"gating": true, "assets": true, "delivery_spec": true, "scopes": true,
	"issued_at": true, "expires_at": true,
}

// ValidateDocument checks one document against the frozen line-format
// semantics and returns its kind. Profiled documents get the full extension
// validation; plain v1 documents get structural recognition only (the sender
// produces profiled documents exclusively).
func ValidateDocument(raw []byte) (LineKind, error) {
	if len(raw) == 0 {
		return "", rej(RejInvalidDocument, "empty document")
	}
	if len(raw) > MaxDocBytes {
		return "", rej(RejInvalidDocument, "document exceeds 1 MiB")
	}
	top, err := strictObject(raw)
	if err != nil {
		return "", err
	}
	sv, err := stringField(top, "schema_version")
	if err != nil {
		return "", rej(RejUnsupportedProfile, "schema_version missing/invalid")
	}
	if sv != SchemaVersionV1 {
		return "", rej(RejUnsupportedProfile, "unsupported schema_version: %s", sv)
	}
	if _, has := top["source_profile"]; has {
		if _, bad := top["order_ref"]; bad {
			return "", rej(RejInvalidDocument, "order_ref must be absent in profiled documents")
		}
		if _, bad := top["stage_ref"]; bad {
			return "", rej(RejInvalidDocument, "stage_ref must be absent in profiled documents")
		}
		for k := range top {
			if !profiledTopKeys[k] {
				return "", rej(RejInvalidDocument, "unknown field: %s", k)
			}
		}
		if err := validateEnvelope(top, true); err != nil {
			return "", err
		}
		if err := validateProfile(top["source_profile"]); err != nil {
			return "", err
		}
		return KindProfiled, nil
	}
	for k := range top {
		if !plainV1TopKeys[k] {
			return "", rej(RejInvalidDocument, "unknown field: %s", k)
		}
	}
	if err := validateEnvelope(top, false); err != nil {
		return "", err
	}
	return KindOrder, nil
}

// validateEnvelope checks the shared frozen envelope fields.
func validateEnvelope(top map[string]json.RawMessage, profiled bool) error {
	for _, k := range []string{"handoff_id", "source_app", "target_app", "principal_id",
		"brief_version", "source_project_ref", "source_revision"} {
		if _, err := stringField(top, k); err != nil {
			return err
		}
	}
	// actor: exactly issuer+app_id+subject; app_id must equal source_app.
	actorRaw, ok := top["actor"]
	if !ok || rawNull(actorRaw) {
		return rej(RejInvalidDocument, "actor is required")
	}
	am, err := strictObject(actorRaw)
	if err != nil {
		return err
	}
	if len(am) != 3 {
		return rej(RejInvalidDocument, "actor must be exactly issuer+app_id+subject")
	}
	issuer, err := stringField(am, "issuer")
	if err != nil {
		return rej(RejInvalidDocument, "actor.issuer: %v", err)
	}
	appID, err := stringField(am, "app_id")
	if err != nil {
		return rej(RejInvalidDocument, "actor.app_id: %v", err)
	}
	if _, err := stringField(am, "subject"); err != nil {
		return rej(RejInvalidDocument, "actor.subject: %v", err)
	}
	srcApp, err := stringField(top, "source_app")
	if err != nil {
		return err
	}
	if appID != srcApp {
		return rej(RejInvalidDocument, "actor.app_id must equal source_app")
	}
	_ = issuer

	// binding: exactly binding_ref+proof_digest, 64 lowercase hex.
	bm, err := strictObject(top["binding"])
	if err != nil {
		return err
	}
	if len(bm) != 2 {
		return rej(RejInvalidDocument, "binding must be exactly binding_ref+proof_digest")
	}
	if _, err := stringField(bm, "binding_ref"); err != nil {
		return rej(RejInvalidDocument, "binding.binding_ref: %v", err)
	}
	digest, err := stringField(bm, "proof_digest")
	if err != nil {
		return rej(RejInvalidDocument, "binding.proof_digest: %v", err)
	}
	if !hexDigestRe.MatchString(digest) {
		return rej(RejInvalidDocument, "proof_digest must be 64 lowercase hex")
	}

	// gating: exactly policy_version+open_gates (≤100 unique non-blank).
	gm, err := strictObject(top["gating"])
	if err != nil {
		return err
	}
	if len(gm) != 2 {
		return rej(RejInvalidDocument, "gating must be exactly policy_version+open_gates")
	}
	if _, err := stringField(gm, "policy_version"); err != nil {
		return rej(RejInvalidDocument, "gating.policy_version: %v", err)
	}
	var gates []string
	if raw, ok := gm["open_gates"]; !ok || rawNull(raw) {
		return rej(RejInvalidDocument, "gating.open_gates is required")
	} else if err := json.Unmarshal(raw, &gates); err != nil {
		return rej(RejInvalidDocument, "open_gates must be a string array")
	}
	if len(gates) > 100 {
		return rej(RejInvalidDocument, "open_gates exceeds 100")
	}
	gseen := map[string]bool{}
	for _, g := range gates {
		if !validStrField(g) || gseen[g] {
			return rej(RejInvalidDocument, "open_gates entries must be unique and non-blank")
		}
		gseen[g] = true
	}

	// assets: array (may be empty = text-only), each exactly 4 fields.
	var assetsRaw json.RawMessage
	if assetsRaw, ok = top["assets"]; !ok || rawNull(assetsRaw) {
		return rej(RejInvalidDocument, "assets is required")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(assetsRaw, &items); err != nil {
		return rej(RejInvalidDocument, "assets must be an array")
	}
	if len(items) > 100 {
		return rej(RejInvalidDocument, "assets exceeds 100")
	}
	aseen := map[string]bool{}
	for _, it := range items {
		am2, err := strictObject(it)
		if err != nil {
			return rej(RejInvalidDocument, "asset: %v", err)
		}
		if len(am2) != 4 {
			return rej(RejInvalidDocument, "asset must be exactly asset_ref+sha256+size_bytes+media_type")
		}
		ref, err := stringField(am2, "asset_ref")
		if err != nil {
			return rej(RejInvalidDocument, "asset_ref: %v", err)
		}
		if aseen[ref] {
			return rej(RejInvalidDocument, "duplicate asset_ref: %s", ref)
		}
		aseen[ref] = true
		sha, err := stringField(am2, "sha256")
		if err != nil {
			return rej(RejInvalidDocument, "asset sha256: %v", err)
		}
		if !hexDigestRe.MatchString(sha) {
			return rej(RejInvalidDocument, "asset sha256 must be 64 lowercase hex")
		}
		sizeRaw, ok := am2["size_bytes"]
		if !ok || rawNull(sizeRaw) {
			return rej(RejInvalidDocument, "size_bytes is required")
		}
		var size int64
		if err := json.Unmarshal(sizeRaw, &size); err != nil || size < 1 {
			return rej(RejInvalidDocument, "size_bytes must be a positive integer")
		}
		if _, err := stringField(am2, "media_type"); err != nil {
			return rej(RejInvalidDocument, "asset media_type: %v", err)
		}
	}

	// delivery_spec: exactly media_type+description (≤4096 runes).
	dm, err := strictObject(top["delivery_spec"])
	if err != nil {
		return err
	}
	if len(dm) != 2 {
		return rej(RejInvalidDocument, "delivery_spec must be exactly media_type+description")
	}
	if _, err := stringField(dm, "media_type"); err != nil {
		return rej(RejInvalidDocument, "delivery_spec.media_type: %v", err)
	}
	desc, err := stringField(dm, "description")
	if err != nil {
		return rej(RejInvalidDocument, "delivery_spec.description: %v", err)
	}
	if utf8.RuneCountInString(desc) > 4096 {
		return rej(RejInvalidDocument, "description exceeds 4096 runes")
	}

	// scopes: closed enum subset, 1..4 unique.
	var scopes []string
	if raw, ok := top["scopes"]; !ok || rawNull(raw) {
		return rej(RejInvalidDocument, "scopes is required")
	} else if err := json.Unmarshal(raw, &scopes); err != nil {
		return rej(RejInvalidDocument, "scopes must be a string array")
	}
	if len(scopes) < 1 || len(scopes) > 4 {
		return rej(RejInvalidDocument, "scopes must be 1..4 items")
	}
	sseen := map[string]bool{}
	for _, sc := range scopes {
		if !scopeEnum[sc] || sseen[sc] {
			return rej(RejInvalidDocument, "scopes must be a unique closed-enum subset")
		}
		sseen[sc] = true
	}

	// clock: issued < expires ≤ +900s, frozen second precision.
	issuedAt, err := stringField(top, "issued_at")
	if err != nil {
		return err
	}
	expiresAt, err := stringField(top, "expires_at")
	if err != nil {
		return err
	}
	iss, err := parseFrozenTime(issuedAt)
	if err != nil {
		return err
	}
	exp, err := parseFrozenTime(expiresAt)
	if err != nil {
		return err
	}
	if !exp.After(iss) {
		return rej(RejInvalidDocument, "issued_at must precede expires_at")
	}
	if exp.Sub(iss) > maxValidityWindow {
		return rej(RejInvalidDocument, "expires_at exceeds issued_at+900s")
	}
	return nil
}

// ValidateSenderDocument is the sender's stricter gate on ITS OWN product:
// frozen semantics (ValidateDocument) plus the sender's construction
// invariants — source_revision is the content digest and brief_version is an
// integer string (O2 alignment expectations). These two are sender promises,
// not frozen parser requirements, so they stay out of the vector-pinned path.
func ValidateSenderDocument(raw []byte) error {
	kind, err := ValidateDocument(raw)
	if err != nil {
		return err
	}
	if kind != KindProfiled {
		return rej(RejInvalidDocument, "sender documents must be profiled (source_profile present)")
	}
	var probe struct {
		BriefVersion   string `json:"brief_version"`
		SourceRevision string `json:"source_revision"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return rej(RejInvalidDocument, "unreadable document")
	}
	if !isUnsignedIntString(probe.BriefVersion) {
		return rej(RejInvalidDocument, "sender brief_version must be an integer string (source_version)")
	}
	sr := strings.ToLower(probe.SourceRevision)
	if !hexDigestRe.MatchString(sr) || probe.SourceRevision != sr {
		return rej(RejInvalidDocument, "sender source_revision must be a 64 lowercase hex digest")
	}
	return nil
}

func isUnsignedIntString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validateProfile checks the source_profile/v1 section (all attributes
// required; campaign_ref presence adjudicated by kind).
func validateProfile(raw json.RawMessage) error {
	m, err := strictObject(raw)
	if err != nil {
		return rej(RejInvalidDocument, "source_profile: %v", err)
	}
	allowed := map[string]bool{
		"profile_version": true, "source_kind": true, "tenant_scope": true,
		"capabilities": true, "constraints": true, "return_target_id": true,
		"campaign_ref": true,
	}
	for k := range m {
		if !allowed[k] {
			return rej(RejInvalidDocument, "source_profile unknown field: %s", k)
		}
	}
	pv, err := stringField(m, "profile_version")
	if err != nil {
		return rej(RejInvalidDocument, "source_profile.profile_version: %v", err)
	}
	if pv != ProfileVersionV1 {
		return rej(RejUnsupportedProfile, "unsupported profile_version: %s", pv)
	}
	kind, err := stringField(m, "source_kind")
	if err != nil {
		return rej(RejInvalidDocument, "source_profile.source_kind: %v", err)
	}
	if kind == "order" {
		return rej(RejSourceKindForbidden, "source_kind=order is forbidden in extension documents")
	}
	if kind != "standalone" && kind != "campaign" {
		return rej(RejInvalidDocument, "source_profile.source_kind invalid: %s", kind)
	}
	if _, err := stringField(m, "tenant_scope"); err != nil {
		return rej(RejInvalidDocument, "source_profile.tenant_scope: %v", err)
	}
	if err := stringListField(m, "capabilities", 1, 16, true); err != nil {
		return err
	}
	if err := stringListField(m, "constraints", 0, 16, false); err != nil {
		return err
	}
	if _, err := stringField(m, "return_target_id"); err != nil {
		return rej(RejInvalidDocument, "source_profile.return_target_id: %v", err)
	}
	_, hasCampaignRef := m["campaign_ref"]
	switch kind {
	case "campaign":
		if !hasCampaignRef {
			return rej(RejInvalidDocument, "campaign_ref is required for campaign kind")
		}
		if _, err := stringField(m, "campaign_ref"); err != nil {
			return rej(RejInvalidDocument, "source_profile.campaign_ref: %v", err)
		}
	case "standalone":
		if hasCampaignRef {
			return rej(RejInvalidDocument, "campaign_ref must be absent for standalone kind")
		}
	}
	return nil
}

func stringListField(m map[string]json.RawMessage, key string, min, max int, capName bool) error {
	raw, ok := m[key]
	if !ok || rawNull(raw) {
		return rej(RejInvalidDocument, "source_profile.%s is required", key)
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return rej(RejInvalidDocument, "source_profile.%s must be a string array", key)
	}
	if len(list) < min || len(list) > max {
		return rej(RejInvalidDocument, "source_profile.%s must have %d..%d items", key, min, max)
	}
	seen := map[string]bool{}
	for _, s := range list {
		if !validStrField(s) {
			return rej(RejInvalidDocument, "source_profile.%s has a blank item", key)
		}
		if capName && !isCapName(s) {
			return rej(RejInvalidDocument, "source_profile.%s has an invalid capability name: %s", key, s)
		}
		if seen[s] {
			return rej(RejInvalidDocument, "source_profile.%s has a duplicate item: %s", key, s)
		}
		seen[s] = true
	}
	return nil
}

func isCapName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return false
			}
		}
	}
	return true
}
