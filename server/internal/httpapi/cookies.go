package httpapi

import (
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/identity"
	"github.com/bianjiefilm/leads-engine/server/internal/redact"
)

// Cookie helpers. Values are identity tokens: HttpOnly, SameSite=Lax, no
// explicit Domain (bind to the BFF origin), never logged.
const refreshCookieSuffix = "_refresh"

func (s *Server) refreshCookieName() string { return s.Cfg.SessionCookie + refreshCookieSuffix }

func (s *Server) setSessionCookies(w http.ResponseWriter, pair identity.TokenPair) {
	http.SetCookie(w, &http.Cookie{
		Name: s.Cfg.SessionCookie, Value: pair.AccessToken,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: s.refreshCookieName(), Value: pair.RefreshToken,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.Cfg.SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: s.refreshCookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func maskedEmail(v string) string { return redact.MaskEmail(v) }
