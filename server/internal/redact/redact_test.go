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
