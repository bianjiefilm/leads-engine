// Package campaignmotion builds the leads-side handoff for one campaign.
//
// 只交出活动目标、受众、已确认的品牌或产品事实、已授权素材 id、CTA、渠道名和预算归属 id。
// 聊天、电话、邮箱、未授权线索和客户隐私默认不进交出的 JSON。
// Motion 引用只有 project_id、revision_id、campaign_id、digest。
// 本包不调用模型，不按渠道次数生成，也不写创作工程。转化留在获客侧。
package campaignmotion

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const Notice = "只记下交给动效工程的活动事实。没有生成成片，也没有送出成片。"

var (
	ErrInvalid = errors.New("campaignmotion: invalid")
	ErrDigest  = errors.New("campaignmotion: digest")
	ErrMotion  = errors.New("campaignmotion: motion")
	ErrChannel = errors.New("campaignmotion: channel")
)

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	phoneRe  = regexp.MustCompile(`(?:\+?86[-\s]?)?1[3-9]\d{9}`)
	emailRe  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	tokenRe  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

var allowedTop = map[string]bool{
	"campaign_goal":         true,
	"target_audience":       true,
	"approved_facts":        true,
	"facts":                 true,
	"assets":                true,
	"authorized_asset_ids":  true,
	"cta":                   true,
	"channels":              true,
	"budget_attribution_id": true,
	"motion":                true,
}

var sensitiveKey = map[string]bool{
	"phone": true, "email": true, "mobile": true, "tel": true,
	"chat": true, "chats": true, "chat_record": true, "transcript": true,
	"message": true, "messages": true, "text": true, "conversation": true,
	"lead": true, "leads": true, "unauthorized_leads": true, "unauthorized_lead": true,
	"privacy": true, "customer_privacy": true, "profile": true, "crm": true, "crm_profile": true,
	"customer": true, "name": true, "address": true, "wechat": true, "id_card": true,
	"sales_note": true, "note": true, "contact": true, "contacts": true,
	"prompt": true, "render": true, "timeline": true, "source": true, "ast": true,
	"body": true, "video": true, "url": true, "renderer": true,
}

var forbiddenFactKey = map[string]bool{
	"phone": true, "email": true, "mobile": true, "tel": true,
	"chat": true, "chats": true, "chat_record": true, "transcript": true,
	"message": true, "messages": true, "conversation": true,
	"lead": true, "leads": true, "unauthorized_leads": true,
	"privacy": true, "customer_privacy": true, "profile": true, "crm": true,
	"customer": true, "name": true, "address": true, "wechat": true, "id_card": true,
	"sales_note": true, "note": true, "contact": true, "contacts": true,
}

var motionKey = map[string]bool{
	"project_id": true, "revision_id": true, "campaign_id": true, "digest": true,
}

var exportKey = map[string]bool{
	"campaign_goal": true, "target_audience": true, "approved_facts": true,
	"key": true, "value": true, "authorized_asset_ids": true, "cta": true,
	"channels": true, "budget_attribution_id": true, "variants": true, "channel": true,
	"motion": true, "project_id": true, "revision_id": true, "campaign_id": true, "digest": true,
}

// ApprovedFact is one confirmed brand or product fact.
type ApprovedFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Variant records a channel name. It is not a generated piece.
type Variant struct {
	Channel string `json:"channel"`
}

// MotionRef is the only Motion citation this handoff may carry.
type MotionRef struct {
	ProjectID  string `json:"project_id"`
	RevisionID string `json:"revision_id"`
	CampaignID string `json:"campaign_id"`
	Digest     string `json:"digest"`
}

// Document is the JSON handed out of leads. Conversion metrics are not on it.
type Document struct {
	CampaignGoal        string         `json:"campaign_goal"`
	TargetAudience      string         `json:"target_audience"`
	ApprovedFacts       []ApprovedFact `json:"approved_facts"`
	AuthorizedAssetIDs  []string       `json:"authorized_asset_ids"`
	CTA                 string         `json:"cta"`
	Channels            []string       `json:"channels"`
	BudgetAttributionID string         `json:"budget_attribution_id"`
	Variants            []Variant      `json:"variants"`
	Motion              MotionRef      `json:"motion"`
}

// Conversion stays on the leads side. It is not written to a creative project.
type Conversion struct {
	Leads        int64 `json:"leads"`
	Conversions  int64 `json:"conversions"`
	RevenueCents int64 `json:"revenue_cents"`
}

// Result is one leads-side handoff. ModelCalls and CreativeProjectWrites stay zero.
type Result struct {
	Document              Document
	Export                []byte
	Local                 Conversion
	ModelCalls            int
	CreativeProjectWrites int
	PieceGenerated        bool
	PieceSent             bool
	Notice                string
}

// CreativeProjectWriter is the creative-project port. Prepare does not call it.
type CreativeProjectWriter interface {
	WriteCreativeProject(projectID, revisionID string, export []byte) error
}

type factCandidate struct {
	key   string
	value string
}

// Prepare builds the export from the raw request JSON.
// writer may be non-nil; this slice still does not call it and does not call a model.
// Channel names are recorded once each. The call count does not follow the channel count.
func Prepare(campaignID string, raw []byte, writer CreativeProjectWriter) (Result, error) {
	_ = writer
	campaignID = strings.TrimSpace(campaignID)
	if !tokenRe.MatchString(campaignID) {
		return Result{}, ErrInvalid
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return Result{}, ErrInvalid
	}

	var secrets []string
	for key, value := range obj {
		if allowedTop[key] {
			continue
		}
		collectSecrets(value, strings.ToLower(key), &secrets)
	}
	local := parseConversion(obj)

	facts, secrets, err := parseFacts(obj, secrets)
	if err != nil {
		return Result{}, err
	}
	assets, secrets, err := parseAssets(obj, secrets)
	if err != nil {
		return Result{}, err
	}

	goal, err := requiredText(obj, "campaign_goal", secrets)
	if err != nil {
		return Result{}, err
	}
	audience, err := optionalText(obj, "target_audience", secrets)
	if err != nil {
		return Result{}, err
	}
	cta, err := optionalText(obj, "cta", secrets)
	if err != nil {
		return Result{}, err
	}
	budget, err := requiredToken(obj, "budget_attribution_id", secrets)
	if err != nil {
		return Result{}, err
	}
	channels, err := parseChannels(obj["channels"], secrets)
	if err != nil {
		return Result{}, err
	}
	motion, err := parseMotion(obj["motion"], campaignID)
	if err != nil {
		return Result{}, err
	}

	doc := Document{
		CampaignGoal:        goal,
		TargetAudience:      audience,
		ApprovedFacts:       scrubFacts(facts, secrets),
		AuthorizedAssetIDs:  scrubAssets(assets, secrets),
		CTA:                 cta,
		Channels:            channels,
		BudgetAttributionID: budget,
		Variants:            variantsFrom(channels),
		Motion:              motion,
	}
	if doc.ApprovedFacts == nil {
		doc.ApprovedFacts = []ApprovedFact{}
	}
	if doc.AuthorizedAssetIDs == nil {
		doc.AuthorizedAssetIDs = []string{}
	}
	export, err := json.Marshal(doc)
	if err != nil {
		return Result{}, ErrInvalid
	}
	if err := assertExportClean(export, secrets); err != nil {
		return Result{}, err
	}
	return Result{
		Document:              doc,
		Export:                export,
		Local:                 local,
		ModelCalls:            0,
		CreativeProjectWrites: 0,
		PieceGenerated:        false,
		PieceSent:             false,
		Notice:                Notice,
	}, nil
}

func variantsFrom(channels []string) []Variant {
	out := make([]Variant, 0, len(channels))
	for _, channel := range channels {
		out = append(out, Variant{Channel: channel})
	}
	return out
}

func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing json")
	}
	if obj == nil {
		return nil, errors.New("null")
	}
	return obj, nil
}

func parseConversion(obj map[string]json.RawMessage) Conversion {
	var c Conversion
	if raw, ok := obj["conversion"]; ok {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}

func parseFacts(obj map[string]json.RawMessage, secrets []string) ([]factCandidate, []string, error) {
	var out []factCandidate
	for _, key := range []string{"approved_facts", "facts"} {
		raw, ok := obj[key]
		if !ok || isNull(raw) {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, secrets, ErrInvalid
		}
		for _, item := range items {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(item, &fields); err != nil {
				return nil, secrets, ErrInvalid
			}
			for field, value := range fields {
				if field == "key" || field == "value" || field == "confirmed" {
					continue
				}
				collectSecrets(value, strings.ToLower(field), &secrets)
			}
			factKey, err := fieldString(fields, "key")
			if err != nil {
				return nil, secrets, err
			}
			factValue, err := fieldString(fields, "value")
			if err != nil {
				return nil, secrets, err
			}
			confirmed, err := fieldBool(fields, "confirmed")
			if err != nil {
				return nil, secrets, err
			}
			factKey = strings.TrimSpace(factKey)
			factValue = strings.TrimSpace(factValue)
			if factKey == "" {
				continue
			}
			if !confirmed || forbiddenFactKey[strings.ToLower(factKey)] {
				remember(&secrets, factValue)
				remember(&secrets, factKey)
				continue
			}
			out = append(out, factCandidate{key: factKey, value: factValue})
		}
	}
	return out, secrets, nil
}

func parseAssets(obj map[string]json.RawMessage, secrets []string) ([]string, []string, error) {
	var ids []string
	if raw, ok := obj["authorized_asset_ids"]; ok && !isNull(raw) {
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, secrets, ErrInvalid
		}
		ids = append(ids, list...)
	}
	raw, ok := obj["assets"]
	if !ok || isNull(raw) {
		return ids, secrets, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, secrets, ErrInvalid
	}
	for _, item := range items {
		var asString string
		if err := json.Unmarshal(item, &asString); err == nil {
			ids = append(ids, asString)
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, secrets, ErrInvalid
		}
		for field, value := range fields {
			if field == "id" || field == "authorized" {
				continue
			}
			collectSecrets(value, strings.ToLower(field), &secrets)
		}
		id, err := fieldString(fields, "id")
		if err != nil {
			return nil, secrets, err
		}
		authorized, err := fieldBool(fields, "authorized")
		if err != nil {
			return nil, secrets, err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !authorized {
			remember(&secrets, id)
			continue
		}
		ids = append(ids, id)
	}
	return ids, secrets, nil
}

func parseChannels(raw json.RawMessage, secrets []string) ([]string, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrChannel
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, ErrChannel
	}
	seen := map[string]bool{}
	var out []string
	for _, channel := range list {
		channel = scrub(channel, secrets)
		if channel == "" || seen[channel] || !validChannel(channel) {
			continue
		}
		seen[channel] = true
		out = append(out, channel)
	}
	if len(out) == 0 {
		return nil, ErrChannel
	}
	return out, nil
}

func parseMotion(raw json.RawMessage, campaignID string) (MotionRef, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return MotionRef{}, ErrMotion
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return MotionRef{}, ErrMotion
	}
	for key := range obj {
		if !motionKey[key] {
			return MotionRef{}, ErrMotion
		}
	}
	project, err := motionString(obj, "project_id")
	if err != nil {
		return MotionRef{}, err
	}
	revision, err := motionString(obj, "revision_id")
	if err != nil {
		return MotionRef{}, err
	}
	campaign := campaignID
	if _, ok := obj["campaign_id"]; ok {
		campaign, err = motionString(obj, "campaign_id")
		if err != nil {
			return MotionRef{}, err
		}
	}
	digest, err := motionString(obj, "digest")
	if err != nil {
		return MotionRef{}, err
	}
	if campaign != campaignID || !tokenRe.MatchString(project) || !tokenRe.MatchString(revision) {
		return MotionRef{}, ErrMotion
	}
	if !digestRe.MatchString(digest) {
		return MotionRef{}, ErrDigest
	}
	return MotionRef{ProjectID: project, RevisionID: revision, CampaignID: campaign, Digest: digest}, nil
}

func motionString(obj map[string]json.RawMessage, key string) (string, error) {
	raw, ok := obj[key]
	if !ok {
		return "", ErrMotion
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrMotion
	}
	return strings.TrimSpace(s), nil
}

func scrubFacts(facts []factCandidate, secrets []string) []ApprovedFact {
	var out []ApprovedFact
	seen := map[string]bool{}
	for _, fact := range facts {
		key := scrub(fact.key, secrets)
		value := scrub(fact.value, secrets)
		if key == "" || value == "" || forbiddenFactKey[strings.ToLower(key)] || seen[key] {
			continue
		}
		if utf8.RuneCountInString(key) > 40 || utf8.RuneCountInString(value) > 500 {
			continue
		}
		if phoneRe.MatchString(key+value) || emailRe.MatchString(key+value) {
			continue
		}
		seen[key] = true
		out = append(out, ApprovedFact{Key: key, Value: value})
	}
	return out
}

func scrubAssets(ids, secrets []string) []string {
	var out []string
	seen := map[string]bool{}
	blocked := map[string]bool{}
	for _, secret := range secrets {
		blocked[secret] = true
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || blocked[id] || !tokenRe.MatchString(id) {
			continue
		}
		if phoneRe.MatchString(id) || emailRe.MatchString(id) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func requiredText(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	text, err := optionalText(obj, key, secrets)
	if err != nil || text == "" {
		return "", ErrInvalid
	}
	return text, nil
}

func optionalText(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	raw, ok := obj[key]
	if !ok || isNull(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	s = scrub(s, secrets)
	if utf8.RuneCountInString(s) > 500 {
		return "", ErrInvalid
	}
	if phoneRe.MatchString(s) || emailRe.MatchString(s) {
		return "", ErrInvalid
	}
	return s, nil
}

func requiredToken(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	raw, ok := obj[key]
	if !ok {
		return "", ErrInvalid
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	s = scrub(s, secrets)
	if !tokenRe.MatchString(s) || phoneRe.MatchString(s) || emailRe.MatchString(s) {
		return "", ErrInvalid
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s, secret) {
			return "", ErrInvalid
		}
	}
	return s, nil
}

func validChannel(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 40 {
		return false
	}
	if strings.ContainsAny(s, "@\n\r\t") || phoneRe.MatchString(s) || emailRe.MatchString(s) {
		return false
	}
	return true
}

func scrub(s string, secrets []string) string {
	ordered := append([]string(nil), secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "")
	}
	s = emailRe.ReplaceAllString(s, "")
	s = phoneRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func assertExportClean(export []byte, secrets []string) error {
	var parsed any
	if err := json.Unmarshal(export, &parsed); err != nil {
		return ErrInvalid
	}
	keys := map[string]struct{}{}
	walkKeys(parsed, keys)
	for key := range keys {
		if !exportKey[key] {
			return ErrInvalid
		}
	}
	text := string(export)
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			return ErrInvalid
		}
	}
	return nil
}

func walkKeys(v any, keys map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			keys[key] = struct{}{}
			walkKeys(child, keys)
		}
	case []any:
		for _, child := range t {
			walkKeys(child, keys)
		}
	}
}

func collectSecrets(raw json.RawMessage, parent string, into *[]string) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return
	}
	walkSecrets(v, parent, into)
}

func walkSecrets(v any, parent string, into *[]string) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return
		}
		if sensitiveKey[parent] || utf8.RuneCountInString(s) >= 12 || phoneRe.MatchString(s) || emailRe.MatchString(s) {
			remember(into, s)
		}
	case map[string]any:
		for key, child := range t {
			walkSecrets(child, strings.ToLower(key), into)
		}
	case []any:
		for _, child := range t {
			walkSecrets(child, parent, into)
		}
	}
}

func remember(into *[]string, s string) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) < 4 {
		return
	}
	*into = append(*into, s)
}

func fieldString(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok || isNull(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	return s, nil
}

func fieldBool(fields map[string]json.RawMessage, key string) (bool, error) {
	raw, ok := fields[key]
	if !ok || isNull(raw) {
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, ErrInvalid
	}
	return b, nil
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}
