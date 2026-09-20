// Package config loads leads-engine server configuration from environment.
//
// 纪律:本进程只有 platform 一条身份路径(无 stub 模式);缺配置时服务可以
// 启动(便于健康探针与运维观察),但一切鉴权动作必须 fail-closed 显式报错,
// 绝不伪造成功。配置键名对齐 public-ai services checklist v3.1。
package config

import (
	"fmt"
	"os"
	"strings"
)

// Feature flag environment keys (default: off).
const (
	EnvFeatureNotify = "FEATURE_NOTIFY"
	EnvFeatureUpload = "FEATURE_UPLOAD"
	// EnvFeatureServiceDraft gates the L1 service-draft handoff surface
	// (HUI-1749). Default off: none of the routes are even registered.
	// On: the REAL implementation (preview/confirm/projection/retry/revoke);
	// missing ECO_HANDOFF_* facts fail those endpoints closed (503).
	EnvFeatureServiceDraft = "FEATURE_SERVICE_DRAFT"
	// EnvFeatureLeadsFilter gates deterministic invalid-lead filtering at the
	// intake seam (HUI-1686 / FEAT-0187). Default off = byte-identical legacy
	// behavior. On: intake classifies obviously-invalid contact facts
	// (deterministic rules only) and ledger marks the lead filtered instead of
	// new, so it never enters the marketing pool. 纯服务端判定,无外部能力。
	EnvFeatureLeadsFilter = "FEATURE_LEADS_FILTER"
	// EnvFeatureFollowups gates the sales follow-up record surface
	// (HUI-1692 / FEAT-0193): CRUD on follow_ups plus the deterministic
	// 「我的到期跟进」query. Default off = none of the routes are even
	// registered (404 invisible). On: pure server-side domain, no push/email/
	// outbound capability (reminders' actual reach belongs to future tickets).
	EnvFeatureFollowups = "FEATURE_FOLLOWUPS"
	// EnvFeatureLeadsAssign gates deterministic lead auto-assignment
	// (HUI-1685 / FEAT-0186): pool configuration CRUD plus the intake-time
	// routing of first deliveries. Default off = byte-identical legacy intake
	// behavior and no pool routes. On: deterministic smooth weighted
	// round-robin over the tenant's enabled sales pool, region/industry
	// exact-match first; replays never reassign and manual reassignment is
	// never overridden. 纯服务端单点判定,BFF 零业务判断。
	EnvFeatureLeadsAssign = "FEATURE_LEADS_ASSIGN"
	// EnvFeatureFunnel gates the read-only full-funnel analysis surface
	// (HUI-1694 / FEAT-0195). Default off: the route is not even registered
	// (404 invisible). On: GET /api/v1/funnel computes the leads-domain
	// trusted fact chain (建档/跟进/商机/成交) over an explicit RFC3339 window
	// — 纯只读计算,零迁移、零状态;曝光/留资触点没有本仓可信事实,按 UNKNOWN
	// 诚实降级范式输出 available=false + 中文 reason,绝不推算、绝不置 0。
	EnvFeatureFunnel = "FEATURE_FUNNEL"
)

// Eco handoff deployment keys (HUI-1749; required only when
// FEATURE_SERVICE_DRAFT=on). 交接接收端 URL/令牌/租户域全部由部署注入,
// 绝不硬编码、绝不由客户端传入。
const (
	EnvEcoHandoffTargetApp   = "ECO_HANDOFF_TARGET_APP"
	EnvEcoHandoffIntakeURL   = "ECO_HANDOFF_INTAKE_URL"
	EnvEcoHandoffToken       = "ECO_HANDOFF_TOKEN"
	EnvEcoHandoffTenantScope = "ECO_HANDOFF_TENANT_SCOPE"
	EnvEcoHandoffProofSalt   = "ECO_HANDOFF_PROOF_SALT"
)

// EnvDedupPepper is the deployment-injected HMAC pepper for the lead-intake
// phone fingerprint (HUI-1683). 手机号指纹的 pepper 由部署注入,绝不硬编码、
// 绝不由请求传入;未配置时 intake 端点 fail-closed(503 config_gate_dedup)。
// 有意不复用 ECO_HANDOFF_PROOF_SALT:不同用途绝不共享同一盐。
const EnvDedupPepper = "LEADS_DEDUP_PEPPER"

// Config is the resolved server configuration.
type Config struct {
	// HTTPAddr is the loopback listen address, e.g. 127.0.0.1:18230.
	HTTPAddr string
	// DBPath is the sqlite database file path.
	DBPath string
	// Env is "development" or "production".
	Env string
	// AppID is the platform app id (JWT aud), e.g. leads-engine.
	AppID string
	// InternalToken is the shared secret between the web BFF and this server.
	InternalToken string
	// SessionCookie is the cookie name carrying the identity session token.
	SessionCookie string

	// Identity (platform-identity, loopback).
	IdentityBaseURL string
	IdentityToken   string
	IdentityAppHost string

	// Notify / Upload clients (scaffolding only in L0).
	NotifyBaseURL string
	NotifyToken   string
	UploadBaseURL string
	UploadToken   string

	FeatureNotify bool
	FeatureUpload bool

	// FeatureServiceDraft: L1 service-draft handoff surface (HUI-1749).
	// Default off (routes not registered).
	FeatureServiceDraft bool

	// FeatureLeadsFilter: deterministic invalid-lead filtering at intake
	// (HUI-1686 / FEAT-0187). Default off = exactly the pre-flag behavior.
	FeatureLeadsFilter bool

	// FeatureFollowups: sales follow-up record surface (HUI-1692 / FEAT-0193).
	// Default off (routes not registered).
	FeatureFollowups bool

	// FeatureLeadsAssign: deterministic lead auto-assignment (HUI-1685 /
	// FEAT-0186). Default off (no pool routes; intake untouched).
	FeatureLeadsAssign bool

	// FeatureFunnel: read-only full-funnel analysis (HUI-1694 / FEAT-0195).
	// Default off (route not registered).
	FeatureFunnel bool

	// EcoHandoff carries the deployment-injected receiver facts (HUI-1749).
	// TargetAppID defaults to "orders" (the receiver's registered app id).
	EcoHandoff EcoHandoffConfig

	// DedupPepper is the HMAC pepper for intake phone fingerprints (HUI-1683).
	// Deployment-injected; the intake endpoint fails closed when empty.
	DedupPepper string
}

// EcoHandoffConfig is the constrained handoff delivery configuration
// (HUI-1749). All values are deployment-injected; no secrets live in code.
type EcoHandoffConfig struct {
	// TargetAppID is the receiver app id (resolved against the registry).
	TargetAppID string
	// IntakeURL is the receiver's handoff POST endpoint (https, or loopback
	// http for co-located deployments).
	IntakeURL string
	// Token is the receiver internal token (service-to-service).
	Token string
	// TenantScope is the exact tenant_scope value the receiver expects
	// (empty = fail-closed: no handoff ever ships).
	TenantScope string
	// ProofSalt optionally salts the PROVISIONAL binding proof digest.
	ProofSalt string
}

// EcoGate problems: FEATURE_SERVICE_DRAFT=on requires every core fact.
func (c Config) EcoGate() []string {
	if !c.FeatureServiceDraft {
		return nil
	}
	var problems []string
	if strings.TrimSpace(c.EcoHandoff.TargetAppID) == "" {
		problems = append(problems, EnvEcoHandoffTargetApp+" is required when FEATURE_SERVICE_DRAFT=on")
	}
	if strings.TrimSpace(c.EcoHandoff.IntakeURL) == "" {
		problems = append(problems, EnvEcoHandoffIntakeURL+" is required when FEATURE_SERVICE_DRAFT=on")
	}
	if strings.TrimSpace(c.EcoHandoff.Token) == "" {
		problems = append(problems, EnvEcoHandoffToken+" is required when FEATURE_SERVICE_DRAFT=on")
	}
	if strings.TrimSpace(c.EcoHandoff.TenantScope) == "" {
		problems = append(problems, EnvEcoHandoffTenantScope+" is required when FEATURE_SERVICE_DRAFT=on (fail-closed: empty scope never ships)")
	}
	return problems
}

// FromEnv reads configuration from the process environment.
func FromEnv() Config {
	return Load(os.Getenv)
}

// Load builds a Config from any key->value getter (injection point for tests
// and for alternate config sources).
func Load(get func(string) string) Config {
	return fromEnv(get)
}

func fromEnv(get func(string) string) Config {
	return Config{
		HTTPAddr:            firstNonEmpty(get("LEADS_HTTP_ADDR"), "127.0.0.1:18230"),
		DBPath:              firstNonEmpty(get("LEADS_DB_PATH"), "data/leads.db"),
		Env:                 firstNonEmpty(get("LEADS_ENV"), "development"),
		AppID:               firstNonEmpty(get("LEADS_APP_ID"), "leads-engine"),
		InternalToken:       get("LEADS_INTERNAL_TOKEN"),
		SessionCookie:       firstNonEmpty(get("LEADS_SESSION_COOKIE"), "leads_session"),
		IdentityBaseURL:     get("PLATFORM_IDENTITY_BASE_URL"),
		IdentityToken:       get("PLATFORM_IDENTITY_TOKEN"),
		IdentityAppHost:     get("PLATFORM_IDENTITY_APP_HOST"),
		NotifyBaseURL:       get("PLATFORM_NOTIFY_BASE_URL"),
		NotifyToken:         get("PLATFORM_NOTIFY_TOKEN"),
		UploadBaseURL:       get("PLATFORM_UPLOAD_BASE_URL"),
		UploadToken:         get("PLATFORM_UPLOAD_TOKEN"),
		FeatureNotify:       isTruthy(get(EnvFeatureNotify)),
		FeatureUpload:       isTruthy(get(EnvFeatureUpload)),
		FeatureServiceDraft: isTruthy(get(EnvFeatureServiceDraft)),
		FeatureLeadsFilter:  isTruthy(get(EnvFeatureLeadsFilter)),
		FeatureFollowups:    isTruthy(get(EnvFeatureFollowups)),
		FeatureLeadsAssign:  isTruthy(get(EnvFeatureLeadsAssign)),
		FeatureFunnel:       isTruthy(get(EnvFeatureFunnel)),
		EcoHandoff: EcoHandoffConfig{
			TargetAppID: firstNonEmpty(get(EnvEcoHandoffTargetApp), "orders"),
			IntakeURL:   get(EnvEcoHandoffIntakeURL),
			Token:       get(EnvEcoHandoffToken),
			TenantScope: get(EnvEcoHandoffTenantScope),
			ProofSalt:   get(EnvEcoHandoffProofSalt),
		},
		DedupPepper: get(EnvDedupPepper),
	}
}

// Gate returns the list of configuration problems that must fail closed.
// An empty list means every authenticated path is fully configured.
func (c Config) Gate() []string {
	var problems []string
	if strings.TrimSpace(c.InternalToken) == "" {
		problems = append(problems, "LEADS_INTERNAL_TOKEN is required (web BFF -> server shared secret)")
	}
	if strings.TrimSpace(c.IdentityBaseURL) == "" {
		problems = append(problems, "PLATFORM_IDENTITY_BASE_URL is required (platform-identity loopback url)")
	}
	if strings.TrimSpace(c.IdentityToken) == "" {
		problems = append(problems, "PLATFORM_IDENTITY_TOKEN is required (this app's dedicated identity token; never a shared master token)")
	}
	if c.FeatureNotify {
		if strings.TrimSpace(c.NotifyBaseURL) == "" {
			problems = append(problems, "FEATURE_NOTIFY=on requires PLATFORM_NOTIFY_BASE_URL")
		}
		if strings.TrimSpace(c.NotifyToken) == "" {
			problems = append(problems, "FEATURE_NOTIFY=on requires PLATFORM_NOTIFY_TOKEN")
		}
	}
	if c.FeatureUpload {
		if strings.TrimSpace(c.UploadBaseURL) == "" {
			problems = append(problems, "FEATURE_UPLOAD=on requires PLATFORM_UPLOAD_BASE_URL")
		}
		if strings.TrimSpace(c.UploadToken) == "" {
			problems = append(problems, "FEATURE_UPLOAD=on requires PLATFORM_UPLOAD_TOKEN")
		}
	}
	return problems
}

// Production reports whether the process runs with production discipline.
func (c Config) Production() bool { return strings.EqualFold(strings.TrimSpace(c.Env), "production") }

func (c Config) Describe() string {
	flags := ""
	for _, f := range []struct {
		name string
		on   bool
	}{
		{"notify", c.FeatureNotify},
		{"upload", c.FeatureUpload},
		{"service_draft", c.FeatureServiceDraft},
		{"leads_filter", c.FeatureLeadsFilter},
		{"followups", c.FeatureFollowups},
		{"leads_assign", c.FeatureLeadsAssign},
		{"funnel", c.FeatureFunnel},
	} {
		v := "off"
		if f.on {
			v = "on"
		}
		if flags != "" {
			flags += ","
		}
		flags += f.name + "=" + v
	}
	return fmt.Sprintf("env=%s app_id=%s addr=%s db=%s features(%s)",
		c.Env, c.AppID, c.HTTPAddr, c.DBPath, flags)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
