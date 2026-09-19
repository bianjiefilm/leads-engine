// Package redact masks personal identifiers before anything reaches logs.
// 安全基线:联系人手机号/邮箱只允许以掩码形态出现在日志与错误消息里。
package redact

import "strings"

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
