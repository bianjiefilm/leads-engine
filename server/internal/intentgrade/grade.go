// Package intentgrade scores a tenant's own lead or session with a versioned
// rule set. There is no calibrated model here, so a confidence number is never
// presented as a close probability. The package only suggests a next step for
// HUI-1893; it does not call, text, pull a group, or create an order.
package intentgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	RuleVersion  = "rules-hui-1684-v1"
	ModelVersion = "none"
	Disclaimer   = "规则评分，不是真人成交预测，也不是校准后的成交概率。"
	Freshness    = 24 * time.Hour

	GradeHigh         = "high"
	GradeMedium       = "medium"
	GradeLow          = "low"
	GradeInsufficient = "insufficient"

	RefusalWrongTenant     = "wrong_tenant"
	RefusalPromptInjection = "prompt_injection"
	RefusalReasonRequired  = "correction_reason_required"

	DispositionAdopted  = "adopted"
	DispositionRejected = "rejected"

	StaleNewMessage      = "new_message"
	StaleRetraction      = "retraction"
	StaleHumanCorrection = "human_correction"
	StaleExpired         = "expired"

	OriginFixture = "fixture"
	OriginRules   = "rules"
	OriginHuman   = "human"
)

// Evidence is one lead or session fact the caller is allowed to pass in.
// Text is data. It never changes tenant, permission, or outreach.
type Evidence struct {
	ID        string
	TenantID  string
	Kind      string
	Text      string
	Field     string
	At        time.Time
	Retracted bool
}

// ConfirmedFact is a sales-confirmed field. A later score must keep it.
type ConfirmedFact struct {
	Field string
	Value string
}

// HumanConfirmation is a sales correction or misjudgment mark.
type HumanConfirmation struct {
	Grade       string
	Misjudgment bool
	Disposition string
	Reason      string
	Facts       []ConfirmedFact
}

// SnapshotRef is the previous score. Newer evidence, retraction, a human
// correction, or expiry makes that snapshot stale.
type SnapshotRef struct {
	ID          string
	Fingerprint string
	FreshUntil  time.Time
	HumanLocked bool
}

// Input is everything the rule scorer may see.
type Input struct {
	TenantID    string
	SubjectKind string
	SubjectID   string
	Now         time.Time
	Evidence    []Evidence
	Human       *HumanConfirmation
	Prior       *SnapshotRef
	// Correction is a new sales correction. Reapplying a stored lock leaves it false.
	Correction bool
}

// Citation quotes the evidence row that supported the grade.
type Citation struct {
	EvidenceID string `json:"evidence_id"`
	Excerpt    string `json:"excerpt"`
	At         string `json:"at"`
}

// Suggestion is a next step for the sales desk. The auto flags stay false;
// contact permission is not derived from the grade.
type Suggestion struct {
	ForTicket             string `json:"for_ticket"`
	Kind                  string `json:"kind"`
	Label                 string `json:"label"`
	AutoCall              bool   `json:"auto_call"`
	AutoSMS               bool   `json:"auto_sms"`
	AutoGroup             bool   `json:"auto_group"`
	CreateOrder           bool   `json:"create_order"`
	ContactDecidedByScore bool   `json:"contact_decided_by_score"`
}

// Result is one rule assessment. Calibrated is always false in this version,
// and no probability field is serialized.
type Result struct {
	Refusal          string             `json:"refusal,omitempty"`
	Grade            string             `json:"grade,omitempty"`
	Reason           string             `json:"reason,omitempty"`
	Citations        []Citation         `json:"citations,omitempty"`
	AssessedAt       time.Time          `json:"assessed_at"`
	FreshUntil       time.Time          `json:"fresh_until"`
	PriorStale       bool               `json:"prior_stale"`
	PriorStaleReason string             `json:"prior_stale_reason,omitempty"`
	Missing          []string           `json:"missing_fields,omitempty"`
	RuleVersion      string             `json:"rule_version"`
	ModelVersion     string             `json:"model_version"`
	Calibrated       bool               `json:"calibrated"`
	Suggestion       Suggestion         `json:"suggestion"`
	HumanLocked      bool               `json:"human_locked"`
	PreservedFacts   []ConfirmedFact    `json:"preserved_facts,omitempty"`
	Human            *HumanConfirmation `json:"-"`
	Disclaimer       string             `json:"disclaimer"`
	Fingerprint      string             `json:"fingerprint,omitempty"`
	ChargeKey        string             `json:"charge_key,omitempty"`
}

// Counterexample is one frozen fixture. It is not a real customer's close.
type Counterexample struct {
	ID             string
	Kind           string
	Text           string
	Grade          string
	Reason         string
	SuggestionKind string
	Missing        []string
}

// LabeledSample is a hand-labeled fixture. It is not a real customer's close.
type LabeledSample struct {
	ID    string
	Kind  string
	Text  string
	Label string
}

// Outcome is one sample judged by the current rule version.
type Outcome struct {
	ID          string
	Kind        string
	Label       string
	Predicted   string
	Match       bool
	Reason      string
	Suggestion  string
	RuleVersion string
	Refusal     string
	Missing     []string
	Origin      string
}

// Report compares labels with rule output and lists the misses.
type Report struct {
	RuleVersion               string
	ModelVersion              string
	Disclaimer                string
	RealPersonClosePrediction bool
	SampleCount               int
	MisjudgmentCount          int
	Outcomes                  []Outcome
	Misjudgments              []Outcome
	RealModelCompleted        bool
	ModelVerdict              string
}

// PresentConfidence returns a percentage only for a calibrated model.
// This rule version is not calibrated, so the number is dropped.
func PresentConfidence(calibrated bool, confidence float64) (string, bool) {
	if !calibrated {
		return "", false
	}
	return fmt.Sprintf("%.0f%%", confidence*100), true
}

// Score applies RuleVersion to one authorized subject.
func Score(in Input) Result {
	res := Result{
		AssessedAt:   in.Now,
		FreshUntil:   in.Now.Add(Freshness),
		RuleVersion:  RuleVersion,
		ModelVersion: ModelVersion,
		Calibrated:   false,
		Disclaimer:   Disclaimer,
		ChargeKey:    chargeKey(in.TenantID, in.SubjectKind, in.SubjectID),
	}
	if foreignTenant(in) {
		res.Refusal = RefusalWrongTenant
		res.Reason = "证据属于其他租户，已拒绝，不参与分级。"
		res.Suggestion = suggestion("review_only", "已拒绝其他租户的证据，不自动触达。")
		return res
	}
	if in.Human != nil && !humanOK(in.Human) {
		res.Refusal = RefusalReasonRequired
		res.Reason = "人工修正必须写下采纳或驳回的原因。"
		res.Suggestion = suggestion("review_only", "修正没有原因，分级保持不动。")
		return res
	}
	locked := in.Human != nil && humanOK(in.Human)
	usable, hostileHit := splitEvidence(in)
	if hostileHit && !locked {
		res.Refusal = RefusalPromptInjection
		res.Reason = "证据含有提示注入，已拒绝，不按其中的指令分级或触达。"
		res.Suggestion = suggestion("review_only", "已拒绝提示注入，不执行外呼、短信、拉群或建单。")
		res.Fingerprint = fingerprint(in, nil)
		return res
	}
	texts := make([]string, 0, len(usable))
	for _, ev := range usable {
		if strings.TrimSpace(ev.Text) != "" {
			texts = append(texts, ev.Text)
		}
	}
	grade, reason, kind := classify(texts)
	if locked {
		grade = in.Human.Grade
		reason = humanReason(in.Human)
		kind = suggestionKindForLocked(in.Human.Grade, texts)
		res.HumanLocked = true
		res.Human = in.Human
		res.PreservedFacts = append([]ConfirmedFact(nil), in.Human.Facts...)
	}
	res.Grade = grade
	res.Reason = reason
	res.Missing = missingFields(texts, res.PreservedFacts)
	res.Citations = cite(usable)
	res.Suggestion = suggestion(kind, suggestionLabel(kind))
	res.Fingerprint = fingerprint(in, lockedHuman(in.Human, locked))
	applyPrior(&res, in)
	return res
}

// FrozenCounterexamples are the five acceptance rows. The text is a fixture,
// not a model conclusion and not an observed win or loss.
func frozenMissing(fields ...string) []string {
	return append([]string(nil), fields...)
}

func FrozenCounterexamples() []Counterexample {
	all := frozenMissing("buyer", "need", "timeline", "quantity_or_budget")
	return []Counterexample{
		{ID: "sample-strong", Kind: "strong_intent", Grade: GradeHigh, SuggestionKind: "suggest_follow_up",
			Text: "我们这周要采购 50 套，请发合同和报价。", Reason: "出现采购、合同或打款等购买承诺。", Missing: frozenMissing("buyer")},
		{ID: "sample-qa", Kind: "ordinary_qa", Grade: GradeLow, SuggestionKind: "review_only",
			Text: "请问你们的营业时间是几点？地址在哪里？", Reason: "这是普通问答，没有购买承诺。", Missing: append([]string(nil), all...)},
		{ID: "sample-after", Kind: "after_sales", Grade: GradeLow, SuggestionKind: "review_only",
			Text: "我上周买的设备坏了，要申请维修。", Reason: "内容是售后问题，不是新购意向。", Missing: frozenMissing("buyer", "quantity_or_budget")},
		{ID: "sample-missing", Kind: "missing_data", Grade: GradeInsufficient, SuggestionKind: "collect_missing",
			Text: "", Reason: "没有足够的线索或会话内容，信息不足。", Missing: append([]string(nil), all...)},
		{ID: "sample-refuse", Kind: "refuse_marketing", Grade: GradeLow, SuggestionKind: "do_not_contact",
			Text: "请不要再打电话，也不要发短信，我不需要。", Reason: "对方明确拒绝营销触达。", Missing: append([]string(nil), all...)},
	}
}

// GradeOrigin labels a score. A human lock wins. Fixture text is never a model conclusion.
func GradeOrigin(humanLocked bool, texts []string) string {
	if humanLocked {
		return OriginHuman
	}
	joined := strings.TrimSpace(strings.Join(texts, "\n"))
	for _, row := range FrozenCounterexamples() {
		if strings.TrimSpace(row.Text) == joined {
			return OriginFixture
		}
	}
	return OriginRules
}

// OutreachPermissions is not derived from a grade. High still cannot call, message, or order.
func OutreachPermissions(string) (call, directMessage, createOrder bool) {
	return false, false, false
}

// LabeledSamples are the acceptance fixtures for this rule version.
// They are written examples, not observed wins or losses.
func LabeledSamples() []LabeledSample {
	rows := FrozenCounterexamples()
	out := make([]LabeledSample, 0, len(rows))
	for _, row := range rows {
		out = append(out, LabeledSample{ID: row.ID, Kind: row.Kind, Text: row.Text, Label: row.Grade})
	}
	return out
}

// EvaluateLabeled runs the rule scorer and lists every label miss.
func EvaluateLabeled(samples []LabeledSample) Report {
	report := Report{
		RuleVersion:               RuleVersion,
		ModelVersion:              ModelVersion,
		Disclaimer:                Disclaimer,
		RealPersonClosePrediction: false,
		RealModelCompleted:        false,
		ModelVerdict:              "not_completed",
		SampleCount:               len(samples),
	}
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	for _, sample := range samples {
		var evidenceRows []Evidence
		if strings.TrimSpace(sample.Text) != "" {
			evidenceRows = []Evidence{{
				ID: sample.ID, TenantID: "tnt_labeled", Kind: "message", Text: sample.Text, At: now,
			}}
		}
		res := Score(Input{
			TenantID: "tnt_labeled", SubjectKind: "lead", SubjectID: sample.ID, Now: now, Evidence: evidenceRows,
		})
		outcome := Outcome{
			ID: sample.ID, Kind: sample.Kind, Label: sample.Label, Predicted: res.Grade,
			Reason: res.Reason, Suggestion: res.Suggestion.Label, RuleVersion: res.RuleVersion,
			Refusal: res.Refusal, Missing: append([]string(nil), res.Missing...), Origin: OriginFixture,
			Match: res.Refusal == "" && res.Grade == sample.Label,
		}
		report.Outcomes = append(report.Outcomes, outcome)
		if !outcome.Match {
			report.Misjudgments = append(report.Misjudgments, outcome)
		}
	}
	report.MisjudgmentCount = len(report.Misjudgments)
	return report
}

func chargeKey(tenant, kind, id string) string {
	return tenant + "|" + kind + "|" + id
}

func foreignTenant(in Input) bool {
	for _, ev := range in.Evidence {
		if ev.TenantID == "" || ev.TenantID != in.TenantID {
			return true
		}
	}
	return false
}

func humanOK(h *HumanConfirmation) bool {
	if h == nil || strings.TrimSpace(h.Reason) == "" {
		return false
	}
	if h.Disposition != DispositionAdopted && h.Disposition != DispositionRejected {
		return false
	}
	switch h.Grade {
	case GradeHigh, GradeMedium, GradeLow, GradeInsufficient:
		return true
	default:
		return false
	}
}

func lockedHuman(h *HumanConfirmation, locked bool) *HumanConfirmation {
	if !locked {
		return nil
	}
	return h
}

func splitEvidence(in Input) (usable []Evidence, hostileHit bool) {
	for _, ev := range in.Evidence {
		if ev.Retracted {
			continue
		}
		if hostile(ev.Text) {
			hostileHit = true
			continue
		}
		usable = append(usable, ev)
	}
	return usable, hostileHit
}

func hostile(text string) bool {
	return hasAny(fold(text),
		"忽略之前", "忽略以上", "忽略人工结论", "把意向标成", "立即外呼",
		"ignoreprevious", "ignoreallinstructions", "systemprompt", "你现在是",
	)
}

func classify(texts []string) (grade, reason, kind string) {
	blob := fold(strings.Join(texts, "\n"))
	if blob == "" {
		return GradeInsufficient, "没有足够的线索或会话内容，信息不足。", "collect_missing"
	}
	if hasAny(blob, "不要再打", "别打电话", "不要发短信", "别发短信", "不需要", "别联系", "拒绝营销", "退订") {
		return GradeLow, "对方明确拒绝营销触达。", "do_not_contact"
	}
	strong := hasAny(blob, "采购", "发合同", "签合同", "签约", "下单", "预算已批", "今天打款", "打款")
	if hasAny(blob, "维修", "坏了", "售后", "退货", "退款", "保修") && !strong {
		return GradeLow, "内容是售后问题，不是新购意向。", "review_only"
	}
	if strong {
		return GradeHigh, "出现采购、合同或打款等购买承诺。", "suggest_follow_up"
	}
	if hasAny(blob, "多少钱", "报价", "价格", "有兴趣", "想了解", "考虑一下", "发资料", "套餐") {
		return GradeMedium, "有询价或了解意愿，但没有购买承诺。", "suggest_follow_up"
	}
	if hasAny(blob, "营业时间", "地址", "怎么走", "在哪里", "请问") {
		return GradeLow, "这是普通问答，没有购买承诺。", "review_only"
	}
	return GradeInsufficient, "有文本但缺少可判断的购买、售后或问答信号，信息不足。", "collect_missing"
}

func humanReason(h *HumanConfirmation) string {
	verb := "采纳"
	if h.Disposition == DispositionRejected {
		verb = "驳回"
	}
	return "销售已" + verb + "这次分级，AI 不覆盖已确认事实。原因：" + strings.TrimSpace(h.Reason)
}

func suggestionKindForLocked(grade string, texts []string) string {
	if hasAny(fold(strings.Join(texts, "\n")), "不要再打", "别打电话", "不要发短信", "不需要", "别联系") {
		return "do_not_contact"
	}
	switch grade {
	case GradeHigh, GradeMedium:
		return "suggest_follow_up"
	case GradeInsufficient:
		return "collect_missing"
	default:
		return "review_only"
	}
}

func missingFields(texts []string, facts []ConfirmedFact) []string {
	blob := fold(strings.Join(texts, "\n"))
	known := map[string]bool{}
	for _, fact := range facts {
		if fact.Field != "" && strings.TrimSpace(fact.Value) != "" {
			known[fact.Field] = true
		}
	}
	var out []string
	if !known["buyer"] && !hasAny(blob, "我是", "公司", "我们是") {
		out = append(out, "buyer")
	}
	if !known["need"] && !hasAny(blob, "采购", "购买", "设备", "产品", "套餐", "维修", "报价", "价格", "资料") {
		out = append(out, "need")
	}
	if !known["timeline"] && !hasAny(blob, "这周", "今天", "本月", "尽快", "上周", "明天") {
		out = append(out, "timeline")
	}
	if !known["quantity_or_budget"] && !hasAny(blob, "套", "预算", "元", "万", "报价") {
		out = append(out, "quantity_or_budget")
	}
	return out
}

func cite(rows []Evidence) []Citation {
	var out []Citation
	for _, ev := range rows {
		text := strings.TrimSpace(ev.Text)
		if text == "" {
			continue
		}
		out = append(out, Citation{
			EvidenceID: ev.ID,
			Excerpt:    excerpt(text, 80),
			At:         formatAt(ev.At),
		})
	}
	return out
}

func excerpt(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func formatAt(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

func suggestion(kind, label string) Suggestion {
	return Suggestion{
		ForTicket:             "HUI-1893",
		Kind:                  kind,
		Label:                 label,
		AutoCall:              false,
		AutoSMS:               false,
		AutoGroup:             false,
		CreateOrder:           false,
		ContactDecidedByScore: false,
	}
}

func suggestionLabel(kind string) string {
	switch kind {
	case "suggest_follow_up":
		return "建议销售人工确认下一步。是否可联系由授权和渠道规则决定，不自动外呼、短信、拉群或建单。"
	case "do_not_contact":
		return "建议不要外呼或发短信。是否可联系由授权和渠道规则决定，不由分数决定。"
	case "collect_missing":
		return "建议先补齐缺失字段。不外呼、不发短信、不拉群、不建单。"
	default:
		return "建议只作人工复核，不自动外呼、短信、拉群或创建订单。"
	}
}

func applyPrior(res *Result, in Input) {
	if in.Prior == nil {
		return
	}
	switch {
	case res.HumanLocked && (in.Correction || !in.Prior.HumanLocked):
		res.PriorStale = true
		res.PriorStaleReason = StaleHumanCorrection
	case res.Fingerprint != in.Prior.Fingerprint && hasRetracted(in.Evidence):
		res.PriorStale = true
		res.PriorStaleReason = StaleRetraction
	case res.Fingerprint != in.Prior.Fingerprint:
		res.PriorStale = true
		res.PriorStaleReason = StaleNewMessage
	case in.Now.After(in.Prior.FreshUntil):
		res.PriorStale = true
		res.PriorStaleReason = StaleExpired
	}
}

func hasRetracted(rows []Evidence) bool {
	for _, ev := range rows {
		if ev.Retracted {
			return true
		}
	}
	return false
}

func fingerprint(in Input, human *HumanConfirmation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s", in.TenantID, in.SubjectKind, in.SubjectID)
	for _, ev := range in.Evidence {
		fmt.Fprintf(&b, "|%s|%s|%s|%t|%s", ev.ID, ev.TenantID, ev.Text, ev.Retracted, ev.At.UTC().Format(time.RFC3339Nano))
	}
	if human != nil {
		fmt.Fprintf(&b, "|human|%s|%s|%t|%s", human.Grade, human.Disposition, human.Misjudgment, strings.TrimSpace(human.Reason))
		for _, fact := range human.Facts {
			fmt.Fprintf(&b, "|%s=%s", fact.Field, fact.Value)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasAny(blob string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(blob, fold(needle)) {
			return true
		}
	}
	return false
}
