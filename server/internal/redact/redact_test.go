// Package redact tests: PII must never appear unmasked in log-shaped output.
package redact

import (
	"strings"
	"testing"
)

func TestMaskPhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"13812345678", "138****5678"},
		{" 13812345678 ", "138****5678"},
		{"+86 138-1234-5678", "138****5678"},
		{"", ""},
		{"123", "***"},
		{"notaphone", "***"},
	}
	for _, c := range cases {
		if got := MaskPhone(c.in); got != c.want {
			t.Errorf("MaskPhone(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice@example.com", "a***@example.com"},
		{"  bob.smith@sub.example.cn ", "b***@sub.example.cn"},
		{"", ""},
		{"nodomain", "***"},
		{"@example.com", "***"},
	}
	for _, c := range cases {
		if got := MaskEmail(c.in); got != c.want {
			t.Errorf("MaskEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPersonNeverLeaksPlaintextPII(t *testing.T) {
	phone, email := "13812345678", "alice@example.com"
	line := Person("张三", phone, email)
	if strings.Contains(line, phone) {
		t.Fatalf("log line leaks plaintext phone: %s", line)
	}
	if strings.Contains(line, email) {
		t.Fatalf("log line leaks plaintext email: %s", line)
	}
	if !strings.Contains(line, "138****5678") || !strings.Contains(line, "a***@example.com") {
		t.Fatalf("log line missing masked identifiers: %s", line)
	}
}

// HUI-1691: 跟进/备注等自由文本只允许以长度摘要入日志,内容零泄漏。
func TestNoteNeverLeaksContent(t *testing.T) {
	marker := "客户说密码是 SECRET-7391"
	line := Note("  " + marker + "  ")
	if strings.Contains(line, "SECRET") || strings.Contains(line, marker) {
		t.Fatalf("Note leaked content: %s", line)
	}
	if line != "note=redacted(len=18)" {
		t.Fatalf("Note = %q, want length digest", line)
	}
	if Note("") != "note=empty" || Note("   ") != "note=empty" {
		t.Fatalf("empty note digest wrong")
	}
}

func TestTagSummaryCountsOnly(t *testing.T) {
	if got := TagSummary("vip,重点客户,华东"); got != "tags=3" {
		t.Fatalf("TagSummary = %q", got)
	}
	if got := TagSummary("  "); got != "tags=0" {
		t.Fatalf("TagSummary empty = %q", got)
	}
}
