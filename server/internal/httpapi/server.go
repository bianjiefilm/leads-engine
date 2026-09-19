// Package httpapi wires the HTTP surface: middleware (internal token ->
// identity session resolve -> tenant member resolution) and handlers.
//
// Fail-closed rules:
//   - missing internal token -> 401
//   - config gate problems   -> 503 config_gate (explicit, lists keys)
//   - identity unreachable   -> 503 identity_unavailable (no fake data)
//   - unauthenticated        -> 401
//   - no membership row      -> 403 not_member (never auto-provision)
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/db"
	"github.com/bianjiefilm/leads-engine/server/internal/identity"
	"github.com/bianjiefilm/leads-engine/server/internal/redact"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const internalTokenHeader = "X-Internal-Token"
const tenantHeader = "X-Tenant-ID"

// Server is the API server.
type Server struct {
	Cfg     config.Config
	St      *store.Store
	ID      *identity.Client
	Log     *log.Logger
	closeDB func()
}

// New builds a Server over an opened database.
func New(cfg config.Config, database *sql.DB, idc *identity.Client, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{Cfg: cfg, St: store.New(database), ID: idc, Log: logger}
}

// Open opens the database and returns a ready Server.
func Open(cfg config.Config, logger *log.Logger) (*Server, error) {
	d, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	idc := &identity.Client{BaseURL: cfg.IdentityBaseURL, Token: cfg.IdentityToken, AppID: cfg.AppID}
	s := New(cfg, d, idc, logger)
	s.closeDB = func() { d.Close() }
	return s, nil
}

// Close releases the database handle.
func (s *Server) Close() {
	if s.closeDB != nil {
		s.closeDB()
	}
}

// ---- caller context ---------------------------------------------------------

type caller struct {
	Principal identity.Principal
	Member    *store.Member
	Grant     *store.AgentGrant // non-nil only for agents with a per-tenant grant
}

type ctxKey int

const callerKey ctxKey = 1

func callerFrom(r *http.Request) *caller {
	if v, ok := r.Context().Value(callerKey).(*caller); ok {
		return v
	}
	return nil
}

func authzMember(c *caller) *authz.Member {
	if c == nil || c.Member == nil {
		return nil
	}
	return &authz.Member{ID: c.Member.ID, TenantID: c.Member.TenantID, PrincipalRef: c.Member.PrincipalRef,
		Role: authz.Role(c.Member.Role), Enabled: c.Member.Enabled}
}

func authzGrant(c *caller) *authz.AgentGrant {
	if c == nil || c.Grant == nil {
		return nil
	}
	return &authz.AgentGrant{TenantID: c.Grant.TenantID, PrincipalRef: c.Grant.PrincipalRef}
}

// ---- responses --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": code, "message": message})
}

// ---- routing ----------------------------------------------------------------

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// auth (internal token required; no session yet for login/refresh)
	mux.Handle("POST /api/v1/auth/login", s.requireInternal(s.handleLogin))
	mux.Handle("POST /api/v1/auth/refresh", s.requireInternal(s.handleRefresh))
	mux.Handle("POST /api/v1/auth/logout", s.requireInternal(s.handleLogout))

	// authenticated business surface
	mux.Handle("GET /api/v1/whoami", s.requireSession(s.handleWhoami))

	mux.Handle("POST /api/v1/contacts", s.requireSession(s.handleContactCreate))
	mux.Handle("GET /api/v1/contacts", s.requireSession(s.handleContactList))
	mux.Handle("GET /api/v1/contacts/{id}", s.requireSession(s.handleContactGet))
	mux.Handle("PATCH /api/v1/contacts/{id}", s.requireSession(s.handleContactPatch))

	mux.Handle("POST /api/v1/leads", s.requireSession(s.handleLeadCreate))
	mux.Handle("GET /api/v1/leads", s.requireSession(s.handleLeadList))
	mux.Handle("GET /api/v1/leads/{id}", s.requireSession(s.handleLeadGet))
	mux.Handle("PATCH /api/v1/leads/{id}", s.requireSession(s.handleLeadPatch))

	mux.Handle("POST /api/v1/opportunities", s.requireSession(s.handleOppCreate))
	mux.Handle("GET /api/v1/opportunities", s.requireSession(s.handleOppList))
	// exact literal segments win over {id} (ServeMux precedence)
	mux.Handle("GET /api/v1/opportunities/stats", s.requireSession(s.handleOppStats))
	mux.Handle("GET /api/v1/opportunities/{id}", s.requireSession(s.handleOppGet))
	mux.Handle("PATCH /api/v1/opportunities/{id}", s.requireSession(s.handleOppPatch))
	mux.Handle("POST /api/v1/opportunities/{id}/stage", s.requireSession(s.handleOppStage))
	mux.Handle("GET /api/v1/opportunities/{id}/stage-history", s.requireSession(s.handleOppStageHistory))
	// L1 挂载点(归 HUI-1749/1751):仅 FEATURE_SERVICE_DRAFT=on 时注册;
	// 默认 off -> 路由不存在 -> 404,功能不可见。
	if s.Cfg.FeatureServiceDraft {
		mux.Handle("POST /api/v1/opportunities/{id}/service-draft-intent", s.requireSession(s.handleServiceDraftIntent))
	}

	mux.Handle("GET /api/v1/admin/members", s.requireSession(s.handleMemberList))
	mux.Handle("POST /api/v1/admin/members", s.requireSession(s.handleMemberCreate))
	mux.Handle("PATCH /api/v1/admin/members/{id}", s.requireSession(s.handleMemberPatch))

	mux.Handle("POST /api/v1/admin/agent-grants", s.requireSession(s.handleGrantCreate))
	mux.Handle("GET /api/v1/admin/agent-grants", s.requireSession(s.handleGrantList))
	mux.Handle("DELETE /api/v1/admin/agent-grants/{id}", s.requireSession(s.handleGrantDelete))

	mux.Handle("GET /api/v1/admin/export", s.requireSession(s.handleExport))

	return s.withRequestLog(mux)
}

// ---- middleware -------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		c := callerFrom(r)
		who := "anonymous"
		if c != nil {
			who = redact.Person("", "", c.Principal.Email) + " principal=" + c.Principal.ID
		}
		s.Log.Printf("%s %s -> %d (%s) %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), who)
	})
}

// requireInternal enforces the BFF->server shared secret.
func (s *Server) requireInternal(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg.InternalToken == "" || r.Header.Get(internalTokenHeader) != s.Cfg.InternalToken {
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong internal token")
			return
		}
		next(w, r)
	})
}

// requireSession chains: internal token -> config gate -> identity resolve ->
// tenant membership resolve. It is the single identity path; there is no
// alternate or stub mode.
func (s *Server) requireSession(next http.HandlerFunc) http.Handler {
	return s.requireInternal(func(w http.ResponseWriter, r *http.Request) {
		if problems := s.Cfg.Gate(); len(problems) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "config_gate",
				"message": "server platform integration is not configured; refusing to act (fail-closed)",
				"detail":  problems,
			})
			return
		}

		sessionToken := sessionTokenFromRequest(r, s.Cfg.SessionCookie)
		if sessionToken == "" {
			fail(w, http.StatusUnauthorized, "unauthenticated", "no session")
			return
		}
		principal, err := s.ID.ResolveSession(r.Context(), sessionToken)
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrUnauthenticated):
				fail(w, http.StatusUnauthorized, "unauthenticated", "session rejected by identity")
			default:
				// transport/config failure: explicit 503, never fake a session
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error":   "identity_unavailable",
					"message": "platform identity could not be reached; refusing to act (fail-closed)",
				})
			}
			return
		}

		tenantID := strings.TrimSpace(r.Header.Get(tenantHeader))
		if tenantID == "" {
			fail(w, http.StatusBadRequest, "tenant_required", "header "+tenantHeader+" selects the workspace tenant")
			return
		}

		member, err := s.St.GetMemberByPrincipal(tenantID, principal.ID)
		if errors.Is(err, sql.ErrNoRows) {
			// 身份纪律:principal 无成员行 -> 拒绝,绝不自动入租户/开户
			fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "member lookup failed")
			return
		}

		c := &caller{Principal: principal, Member: &member}
		if authz.Role(member.Role) == authz.RoleAgent {
			g, err := s.St.GetAgentGrant(tenantID, principal.ID)
			if err != nil {
				fail(w, http.StatusInternalServerError, "internal", "grant lookup failed")
				return
			}
			c.Grant = g
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey, c)))
	})
}

func sessionTokenFromRequest(r *http.Request, cookieName string) string {
	if h := r.Header.Get("X-Session-Token"); h != "" {
		return h
	}
	if ck, err := r.Cookie(cookieName); err == nil {
		return ck.Value
	}
	return ""
}

// requireAction wraps a handler with an authz decision for a specific action.
// recFor is evaluated only after basic resolution so handlers stay thin.
func (s *Server) requireAction(c *caller, action authz.Action, rec authz.RecordScope, w http.ResponseWriter) bool {
	d := authz.Authorize(authzMember(c), authzGrant(c), action, rec)
	if d.Allowed {
		return true
	}
	if d.MaskAs404 {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return false
	}
	fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
	return false
}
