// Package notifyingest is the HUI-1680 Notify receiver: HMAC verification,
// directed-event parsing, and the authorized submission fetch.
//
// The signature frame is the existing platform-notify body HMAC
// (X-Notify-Signature: sha256=<hex>), not a second scheme.
package notifyingest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// SignBody matches platform-notify SignBody so a real dispatcher and this
// receiver agree on the wire bytes.
func SignBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature compares a signed body in constant time. Only the exact
// sha256=<hex> form is accepted.
func VerifySignature(secret, header string, body []byte) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	provided := header[len("sha256="):]
	if len(provided) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(provided)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(decoded, mac.Sum(nil))
}

// BodySHA256 is the content identity stored beside an inbox row.
func BodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
