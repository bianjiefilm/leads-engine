// Package reception is the pure core of HUI-1688.
//
// 人设不进正文。价格、库存、订单状态只来自 FactBook 的未过期结果。
// 访客文字和知识正文都是数据，不能改租户、权限或调用未批准工具。
// 文本与音频共用同一发送条件。本包不访问网络，也不记账。
package reception

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ModeAI     = "ai"
	ModeAssist = "assist"
	ModeHuman  = "human"

	PendingClarify = "clarify_or_handoff"
	PendingModel   = "model_unavailable"
	PendingHuman   = "human_takeover"
	PendingApprove = "awaiting_approval"

	ClarifyBody   = "我没有这项实时依据，不能把文档里的数字当成交易事实。需要人工核对后再回复。"
	UnknownBody   = "我没有这项知识依据，不能编造回答。需要人工确认。"
	ModelDownBody = "模型暂不可用。这次没有生成新的答复，文字接待和人工接管可以继续。"
	ToolBody      = "这个请求需要未批准的写入操作，我不能执行。需要人工处理。"
)

// FAQ is one enabled-or-not knowledge row. Withdrawn rows must not be matched.
type FAQ struct {
	ID        string
	Question  string
	Answer    string
	Version   int
	UpdatedAt string
	Enabled   bool
	Withdrawn bool
}

// Fact is one authorized read. A zero ExpiresAt is not live truth.
type Fact struct {
	Kind      string
	Value     string
	ExpiresAt time.Time
}

// FactBook is the only source of price, inventory, and order status.
// code is "", "missing", or "timeout". The engine treats expiry itself.
type FactBook interface {
	Lookup(tenantID, kind string, now time.Time) (value string, expires time.Time, code string)
}

// Gap is an insufficiency recorded on the reply.
type Gap struct {
	Code string `json:"code"`
}

// Citation points at the knowledge row or fact kind used, not the full corpus.
type Citation struct {
	SourceID  string `json:"source_id"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
	Kind      string `json:"kind"`
}

// ComposeInput is everything the answerer may see. Persona is accepted so
// callers can pass the stored wording, and is never copied into Body.
type ComposeInput struct {
	Mode        string
	VisitorText string
	Persona     string
	FAQs        []FAQ
	Now         time.Time
	Fact        *Fact
	FactCode    string
	ModelDown   bool
}

// ComposeOutput is a reply draft. ShouldSend is true only for a grounded AI
// answer in ai mode. Assist mode never sends. Human mode skips the answerer.
type ComposeOutput struct {
	Body          string
	Citations     []Citation
	Gaps          []Gap
	Kind          string
	ShouldSend    bool
	PendingReason string
	SkipModel     bool
	Hostile       bool
}

// MayDeliver is the single send gate for text and audio.
// Only an approved reply from the session's current epoch may be emitted.
func MayDeliver(replyEpoch, sessionEpoch int, status string) (textOK, audioOK bool) {
	ok := replyEpoch == sessionEpoch && status == "approved"
	return ok, ok
}

// HashVisitor returns HMAC-SHA256(pepper, key). The raw key is never stored.
func HashVisitor(pepper, key string) (string, error) {
	if strings.TrimSpace(pepper) == "" {
		return "", errors.New("reception: visitor pepper required")
	}
	if !ValidVisitorKey(key) {
		return "", errors.New("reception: visitor key must be 16-128 ascii token characters")
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// SameVisitor reports whether key matches a stored hash.
func SameVisitor(pepper, key, storedHash string) bool {
	got, err := HashVisitor(pepper, key)
	if err != nil || len(got) != len(storedHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}

// ValidVisitorKey is the public token shape. It is not a principal id.
func ValidVisitorKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// Hostile reports prompt-injection phrasing. The text stays data either way.
func Hostile(text string) bool {
	n := fold(text)
	for _, needle := range []string{
		"ignoreprevious", "ignoreallinstructions", "systemprompt",
		"忽略之前", "忽略以上", "你现在是", "setrole", "droptable",
		"toolcall", "functioncall",
	} {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

// Intent classifies a transactional read or a forbidden write.
// Empty means an ordinary knowledge question.
func Intent(text string) string {
	n := fold(text)
	for _, w := range []string{"退款", "退货", "改价", "取消订单", "refund", "chargeback"} {
		if strings.Contains(n, fold(w)) {
			return "write"
		}
	}
	for _, w := range []string{"价格", "价目", "报价", "价钱", "费用", "收费", "单价", "标价", "多少钱", "售价", "price", "cost", "fee"} {
		if strings.Contains(n, fold(w)) {
			return "price"
		}
	}
	for _, w := range []string{"库存", "还有货", "有货", "缺货", "现货", "inventory", "stock"} {
		if strings.Contains(n, fold(w)) {
			return "inventory"
		}
	}
	for _, w := range []string{"订单状态", "物流", "订单", "发货", "运单", "orderstatus"} {
		if strings.Contains(n, fold(w)) {
			return "order_status"
		}
	}
	if priceAmount.MatchString(text) {
		return "price"
	}
	return ""
}

var priceAmount = regexp.MustCompile(`[0-9０-９]+(?:\.[0-9０-９]+)?\s*元`)

// MatchFAQ returns the longest enabled, not-withdrawn question contained in
// the visitor text. A short visitor fragment inside a longer question does not match.
func MatchFAQ(faqs []FAQ, text string) (FAQ, bool) {
	n := fold(text)
	var best FAQ
	found := false
	for _, f := range faqs {
		if !f.Enabled || f.Withdrawn || strings.TrimSpace(f.Question) == "" {
			continue
		}
		q := fold(f.Question)
		if q == "" || !strings.Contains(n, q) {
			continue
		}
		if !found || utf8.RuneCountInString(q) > utf8.RuneCountInString(fold(best.Question)) {
			best = f
			found = true
		}
	}
	return best, found
}

// faqStatesTrade is true when a static card itself states price, stock, or order status.
// Those cards are not a FactBook result and must not be sent as live trade truth.
func faqStatesTrade(f FAQ) bool {
	switch Intent(f.Question + "\n" + f.Answer) {
	case "price", "inventory", "order_status":
		return true
	default:
		return false
	}
}

// Compose drafts a reply. It never interpolates Persona into Body.
func Compose(in ComposeInput) ComposeOutput {
	_ = in.Persona
	if in.Mode == ModeHuman {
		return ComposeOutput{SkipModel: true, PendingReason: PendingHuman, Kind: "human"}
	}
	out := ComposeOutput{Kind: "ai"}
	if Hostile(in.VisitorText) {
		out.Hostile = true
		out.Gaps = append(out.Gaps, Gap{Code: "untrusted_input"})
	}
	if in.ModelDown {
		out.Gaps = append(out.Gaps, Gap{Code: "model_unavailable"})
	}
	switch Intent(in.VisitorText) {
	case "write":
		out.Body = ToolBody
		out.Gaps = append(out.Gaps, Gap{Code: "unapproved_tool"})
		out.PendingReason = PendingClarify
	case "price", "inventory", "order_status":
		applyFact(&out, in)
	default:
		faq, ok := MatchFAQ(in.FAQs, in.VisitorText)
		if ok && faqStatesTrade(faq) {
			applyFact(&out, in)
		} else if ok {
			out.Body = faq.Answer
			out.Citations = []Citation{{
				Kind: "faq", SourceID: faq.ID, Version: faq.Version, UpdatedAt: faq.UpdatedAt,
			}}
			out.ShouldSend = in.Mode == ModeAI
		} else if in.ModelDown {
			out.Body = ModelDownBody
			out.PendingReason = PendingModel
		} else {
			out.Body = UnknownBody
			out.Gaps = append(out.Gaps, Gap{Code: "unknown"})
			out.PendingReason = PendingClarify
		}
	}
	if in.Mode == ModeAssist {
		out.Kind = "draft"
		out.ShouldSend = false
		if out.PendingReason == "" {
			out.PendingReason = PendingApprove
		}
	}
	return out
}

func applyFact(out *ComposeOutput, in ComposeInput) {
	switch {
	case in.FactCode == "timeout":
		out.Gaps = append(out.Gaps, Gap{Code: "fact_timeout"})
		out.Body = ClarifyBody
		out.PendingReason = PendingClarify
	case in.FactCode == "missing" || in.Fact == nil:
		out.Gaps = append(out.Gaps, Gap{Code: "fact_missing"})
		out.Body = ClarifyBody
		out.PendingReason = PendingClarify
	case in.Fact.ExpiresAt.IsZero() || !in.Fact.ExpiresAt.After(in.Now):
		out.Gaps = append(out.Gaps, Gap{Code: "fact_expired"})
		out.Body = ClarifyBody
		out.PendingReason = PendingClarify
	default:
		out.Body = in.Fact.Value
		out.Citations = []Citation{{
			Kind: "fact", SourceID: "fact:" + in.Fact.Kind, Version: 1,
			UpdatedAt: in.Fact.ExpiresAt.UTC().Format(time.RFC3339),
		}}
		out.ShouldSend = in.Mode == ModeAI
	}
}

func fold(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}
