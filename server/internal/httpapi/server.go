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
	"sync"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/appregistry"
	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/db"
	"github.com/bianjiefilm/leads-engine/server/internal/handoffsender"
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
	Eco     *handoffsender.Service
	Log     *log.Logger
	closeDB func()

	// FormRatePerMinute / FormResubmitWindow tune the public form surface
	// (HUI-1679); zero selects the production defaults. Tests may inject.
	FormRatePerMinute int
	FormResubmitWindow time.Duration

	formLimitOnce sync.Once
	formLimit     *formRateLimiter
}

// New builds a Server over an opened database.
func New(cfg config.Config, database *sql.DB, idc *identity.Client, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{Cfg: cfg, St: store.New(database), ID: idc, Log: logger}
	// The eco handoff sender is wired ONLY from deployment config + the
	// embedded app registry (URL/token/app ids never come from requests).
	// A broken embedded manifest is a build defect: log it and leave Eco
	// nil — every service-draft endpoint then fails closed with
	// config_gate_eco.
	if reg, err := appregistry.Embedded(); err != nil {
		logger.Printf("appregistry: embedded manifest invalid: %v", err)
	} else {
		s.Eco = handoffsender.NewService(s.St, reg.WithSelf(cfg.AppID), handoffsender.Config{
			TargetAppID: cfg.EcoHandoff.TargetAppID,
			TenantScope: cfg.EcoHandoff.TenantScope,
			IntakeURL:   cfg.EcoHandoff.IntakeURL,
			Token:       cfg.EcoHandoff.Token,
			ProofSalt:   cfg.EcoHandoff.ProofSalt,
		})
	}
	return s
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
	// exact literal segments win over {id} (ServeMux precedence)
	mux.Handle("GET /api/v1/contacts/export", s.requireSession(s.handleContactExport))
	mux.Handle("GET /api/v1/contacts/{id}", s.requireSession(s.handleContactGet))
	mux.Handle("PATCH /api/v1/contacts/{id}", s.requireSession(s.handleContactPatch))
	mux.Handle("DELETE /api/v1/contacts/{id}", s.requireSession(s.handleContactDelete))
	// HUI-1691 客户档案:consent(按来源维度、撤销持久)与跟进时间线(只追加)
	mux.Handle("GET /api/v1/contacts/{id}/consents", s.requireSession(s.handleContactConsentList))
	mux.Handle("POST /api/v1/contacts/{id}/consents", s.requireSession(s.handleContactConsentUpsert))
	mux.Handle("POST /api/v1/contacts/{id}/consents/{consentId}/revoke", s.requireSession(s.handleContactConsentRevoke))
	mux.Handle("POST /api/v1/contacts/{id}/revoke-marketing", s.requireSession(s.handleContactRevokeMarketing))
	mux.Handle("GET /api/v1/contacts/{id}/followups", s.requireSession(s.handleContactFollowupList))
	mux.Handle("POST /api/v1/contacts/{id}/followups", s.requireSession(s.handleContactFollowupCreate))

	// HUI-1692 / FEAT-0193 销售跟进记录(/follow-ups,与 HUI-1691 的 /followups
	// 只追加时间线正交):FEATURE_FOLLOWUPS 闸控,默认 off -> 路由不注册(404
	// 不可见);on -> CRUD + 完结/重开 + 「我的到期跟进」确定性查询。全部路由挂
	// 既有鉴权中间件,L0 记录级作用域,零新增权限模型。
	if s.Cfg.FeatureFollowups {
		mux.Handle("POST /api/v1/follow-ups", s.requireSession(s.handleFollowUpCreate))
		// exact literal segments win over {id} (ServeMux precedence)
		mux.Handle("GET /api/v1/follow-ups/due", s.requireSession(s.handleFollowUpDue))
		mux.Handle("GET /api/v1/follow-ups/{id}", s.requireSession(s.handleFollowUpGet))
		mux.Handle("PATCH /api/v1/follow-ups/{id}", s.requireSession(s.handleFollowUpPatch))
		mux.Handle("POST /api/v1/follow-ups/{id}/complete", s.requireSession(s.handleFollowUpComplete))
		mux.Handle("POST /api/v1/follow-ups/{id}/reopen", s.requireSession(s.handleFollowUpReopen))
		mux.Handle("GET /api/v1/contacts/{id}/follow-ups", s.requireSession(s.handleContactFollowUpPageList))
		mux.Handle("GET /api/v1/leads/{id}/follow-ups", s.requireSession(s.handleLeadFollowUpPageList))
	}

	mux.Handle("POST /api/v1/leads", s.requireSession(s.handleLeadCreate))
	mux.Handle("GET /api/v1/leads", s.requireSession(s.handleLeadList))
	mux.Handle("GET /api/v1/leads/{id}", s.requireSession(s.handleLeadGet))
	mux.Handle("PATCH /api/v1/leads/{id}", s.requireSession(s.handleLeadPatch))
	// HUI-1685 / FEAT-0186 线索自动分配配置面:FEATURE_LEADS_ASSIGN 闸控,默认
	// off -> 路由不注册(404 不可见)且 intake 行为与既往逐字节一致;on ->
	// owner 专属池配置 CRUD(服务端单点判定) + intake 首投确定性加权轮询。
	// 归 admin 命名空间:与 members/agent-grants 同类的租户级运营配置,且避开
	// /leads/{id} 模式家族(否则 off 时同路径会以 405 而非 404 应答)。
	if s.Cfg.FeatureLeadsAssign {
		mux.Handle("GET /api/v1/admin/leads-assign-pool", s.requireSession(s.handleAssignPoolList))
		mux.Handle("POST /api/v1/admin/leads-assign-pool", s.requireSession(s.handleAssignPoolCreate))
		mux.Handle("PATCH /api/v1/admin/leads-assign-pool/{id}", s.requireSession(s.handleAssignPoolPatch))
		mux.Handle("DELETE /api/v1/admin/leads-assign-pool/{id}", s.requireSession(s.handleAssignPoolDelete))
	}
	// HUI-1694 / FEAT-0195 全漏斗分析(只读):FEATURE_FUNNEL 闸控,默认 off ->
	// 路由不注册(404 不可见);on -> GET 分析端点。零迁移零状态,同窗口重算
	// 幂等;曝光/留资触点无本仓可信事实,UNKNOWN 诚实降级(available=false +
	// 中文 reason,count=null)。租户级聚合复用 read_list 鉴权与记录级作用域
	// 推导(owner 全量 / 非 owner 只看自己人群)。
	if s.Cfg.FeatureFunnel {
		mux.Handle("GET /api/v1/funnel", s.requireSession(s.handleFunnel))
	}
	// HUI-1683 线索 intake 去重:三分类领域规则的唯一 HTTP 形态(HUI-1680 的
	// inbox 与它共用 store.IntakeLeadInTx;本票不做接收渠道本身)。
	mux.Handle("POST /api/v1/leads/intake", s.requireSession(s.handleLeadIntake))
	// HUI-1683 联系人合并面:owner 专属(显式合并/可纠错 undo/候选池/统计)。
	mux.Handle("POST /api/v1/contacts/{id}/merge/{otherId}", s.requireSession(s.handleContactMerge))
	mux.Handle("GET /api/v1/merges", s.requireSession(s.handleMergeList))
	mux.Handle("POST /api/v1/merges/{id}/undo", s.requireSession(s.handleMergeUndo))
	mux.Handle("GET /api/v1/merge-candidates", s.requireSession(s.handleMergeCandidateList))
	mux.Handle("POST /api/v1/merge-candidates/{id}/dismiss", s.requireSession(s.handleMergeCandidateDismiss))
	mux.Handle("GET /api/v1/dedup/stats", s.requireSession(s.handleDedupStats))

	mux.Handle("POST /api/v1/opportunities", s.requireSession(s.handleOppCreate))
	mux.Handle("GET /api/v1/opportunities", s.requireSession(s.handleOppList))
	// exact literal segments win over {id} (ServeMux precedence)
	mux.Handle("GET /api/v1/opportunities/stats", s.requireSession(s.handleOppStats))
	mux.Handle("GET /api/v1/opportunities/{id}", s.requireSession(s.handleOppGet))
	mux.Handle("PATCH /api/v1/opportunities/{id}", s.requireSession(s.handleOppPatch))
	mux.Handle("POST /api/v1/opportunities/{id}/stage", s.requireSession(s.handleOppStage))
	mux.Handle("GET /api/v1/opportunities/{id}/stage-history", s.requireSession(s.handleOppStageHistory))
	// 服务需求草稿交接面(HUI-1749):仅 FEATURE_SERVICE_DRAFT=on 时注册;
	// 默认 off -> 路由不存在 -> 404,动作对不适用商机不可见。
	if s.Cfg.FeatureServiceDraft {
		mux.Handle("POST /api/v1/opportunities/{id}/service-draft-intent", s.requireSession(s.handleServiceDraftIntent))
		mux.Handle("GET /api/v1/opportunities/{id}/service-draft", s.requireSession(s.handleServiceDraftGet))
		mux.Handle("POST /api/v1/opportunities/{id}/service-draft/refresh", s.requireSession(s.handleServiceDraftRefresh))
		mux.Handle("POST /api/v1/opportunities/{id}/service-draft/retry", s.requireSession(s.handleServiceDraftRetry))
		mux.Handle("POST /api/v1/opportunities/{id}/service-draft/revoke", s.requireSession(s.handleServiceDraftRevoke))
	}

	mux.Handle("GET /api/v1/admin/members", s.requireSession(s.handleMemberList))
	mux.Handle("POST /api/v1/admin/members", s.requireSession(s.handleMemberCreate))
	mux.Handle("PATCH /api/v1/admin/members/{id}", s.requireSession(s.handleMemberPatch))

	mux.Handle("POST /api/v1/admin/agent-grants", s.requireSession(s.handleGrantCreate))
	mux.Handle("GET /api/v1/admin/agent-grants", s.requireSession(s.handleGrantList))
	mux.Handle("DELETE /api/v1/admin/agent-grants/{id}", s.requireSession(s.handleGrantDelete))

	mux.Handle("GET /api/v1/admin/export", s.requireSession(s.handleExport))

	// HUI-1679 / FEAT-0180 版本化留资表单:管理面(owner)与公共提交面(无会话)。
	// 公共端点只挂 requireInternal:接收租户恒为表单归属,与会话/租户头无关。
	mux.Handle("POST /api/v1/forms", s.requireSession(s.handleFormCreate))
	mux.Handle("GET /api/v1/forms", s.requireSession(s.handleFormList))
	mux.Handle("GET /api/v1/forms/{id}", s.requireSession(s.handleFormGet))
	mux.Handle("PATCH /api/v1/forms/{id}", s.requireSession(s.handleFormPatch))
	mux.Handle("POST /api/v1/forms/{id}/publish", s.requireSession(s.handleFormPublish))
	mux.Handle("POST /api/v1/forms/{id}/disable", s.requireSession(s.handleFormDisable))
	mux.Handle("GET /api/v1/forms/{id}/schema", s.requireSession(s.handleFormSchema))
	mux.Handle("GET /api/v1/public/forms/{id}", s.requireInternal(s.handlePublicFormGet))
	mux.Handle("POST /api/v1/public/forms/{id}/submissions", s.requireInternal(s.handlePublicFormSubmit))

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
