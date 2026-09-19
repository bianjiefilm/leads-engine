// Package redact masks personal identifiers before anything reaches logs.
// 安全基线:联系人手机号/邮箱只允许以掩码形态出现在日志与错误消息里;
// 跟进/备注等自由文本(HUI-1691)永不入日志,只允许以长度摘要出现。
package redact

import (
	"fmt"
	"strings"
)

// MaskPhone keeps the first 3 and last 4 digits of an 11-digit CN mobile
// number; a leading +86 country code is ignored. Anything else non-empty gets
// a fixed placeholder that leaks nothing.
func MaskPhone(phone string) string {
	p := strings.TrimSpace(phone)
	digits := make([]rune, 0, len(p))
	for _, r := range p {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) == 13 && digits[0] == '8' && digits[1] == '6' {
		digits = digits[2:]
	}
	if len(digits) >= 7 {
		return string(digits[:3]) + "****" + string(digits[len(digits)-4:])
	}
	if len(p) == 0 {
		return ""
	}
	return "***"
}

// MaskEmail keeps the first character of the local part and the domain.
func MaskEmail(email string) string {
	e := strings.TrimSpace(email)
	if e == "" {
		return ""
	}
	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 {
		return "***"
	}
	local, domain := e[:at], e[at+1:]
	keep := string(local[0])
	return keep + "***@" + domain
}

// Note renders a log-safe reference for free text (follow-up / contact notes,
// HUI-1691): the content itself NEVER reaches the log, only its rune length.
func Note(content string) string {
	n := len([]rune(strings.TrimSpace(content)))
	if n == 0 {
		return "note=empty"
	}
	return fmt.Sprintf("note=redacted(len=%d)", n)
}

// TagSummary renders the number of tags only; tag values may carry customer
// wording and are therefore kept out of logs entirely.
func TagSummary(tags string) string {
	if strings.TrimSpace(tags) == "" {
		return "tags=0"
	}
	n := strings.Count(tags, ",") + 1
	return fmt.Sprintf("tags=%d", n)
}

// Person renders a log-safe person label from name/phone/email.
func Person(name, phone, email string) string {
	parts := make([]string, 0, 3)
	if name = strings.TrimSpace(name); name != "" {
		parts = append(parts, "name="+name)
	}
	if m := MaskPhone(phone); m != "" {
		parts = append(parts, "phone="+m)
	}
	if m := MaskEmail(email); m != "" {
		parts = append(parts, "email="+m)
	}
	if len(parts) == 0 {
		return "person=unknown"
	}
	return strings.Join(parts, " ")
}
