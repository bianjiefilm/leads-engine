package httpapi

import (
	"encoding/json"
	"net/http"
)

// ---- auth handlers (identity is the only path; no stub mode) ----------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Email == "" || in.Password == "" {
		fail(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}
	pair, err := s.ID.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		// Never distinguish user-facing reasons beyond rejection; never log tokens.
		fail(w, http.StatusUnauthorized, "login_rejected", "login rejected by platform identity")
		return
	}
	s.setSessionCookies(w, pair)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	refresh := ""
	if ck, err := r.Cookie(s.refreshCookieName()); err == nil {
		refresh = ck.Value
	}
	if refresh == "" {
		fail(w, http.StatusUnauthorized, "unauthenticated", "no refresh token")
		return
	}
	pair, err := s.ID.Refresh(r.Context(), refresh)
	if err != nil {
		fail(w, http.StatusUnauthorized, "refresh_rejected", "refresh rejected by platform identity")
		return
	}
	s.setSessionCookies(w, pair)
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	refresh := ""
	if ck, err := r.Cookie(s.refreshCookieName()); err == nil {
		refresh = ck.Value
	}
	// Best-effort server-side revocation; local cookies are always cleared.
	if refresh != "" {
		userID := ""
		if c := callerFrom(r); c != nil {
			userID = c.Principal.ID
		}
		_ = s.ID.Logout(r.Context(), refresh, userID)
	}
	s.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "principal is not a member of this tenant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"principal_ref": c.Principal.ID,
		"email":         maskOrEmpty(c.Principal.Email),
		"tenant_id":     c.Member.TenantID,
		// member_id lets the web tier hide stage actions for non-assignees
		// (UI concern only; the server re-validates every action).
		"member_id":   c.Member.ID,
		"role":        c.Member.Role,
		"enabled":     c.Member.Enabled,
		"agent_grant": c.Grant != nil,
	})
}

func maskOrEmpty(v string) string {
	if v == "" {
		return ""
	}
	return maskedEmail(v)
}
