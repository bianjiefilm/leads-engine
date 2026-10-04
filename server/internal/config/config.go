// Package config loads leads-engine server configuration from environment.
//
// 纪律:本进程只有 platform 一条身份路径(无 stub 模式);缺配置时服务可以
// 启动(便于健康探针与运维观察),但一切鉴权动作必须 fail-closed 显式报错,
// 绝不伪造成功。配置键名对齐 public-ai services checklist v3.1。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
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
	// EnvFeatureChannelAnalytics gates the read-only channel-effect analysis
	// surface (HUI-1695 / FEAT-0196): the FEAT-0195 funnel grouped by channel
	// (contacts.source_type, the existing source domain). Default off: the
	// route is not even registered (404 invisible). Independent of
	// FEATURE_FUNNEL (each flag registers its own routes). 纯只读 group-by,
	// 零迁移、零状态;low_sample 常量阈值机器标注;ROI 无本地费用事实 ->
	// available=false + 中文 reason(引 HUI-1696),绝不推算、绝不置 0。
	EnvFeatureChannelAnalytics = "FEATURE_CHANNEL_ANALYTICS"
	// EnvFeatureContactTags gates the customer profile tag surface
	// (HUI-1690 / FEAT-0191): 人工标签定义 CRUD + 联系人打标/去标(幂等留痕)+
	// 派生标签(生命周期/活跃度/来源/跟进状态,纯只读按需重算不落库)+
	// 「千人千面」v1 分群查询(组间 AND、组内 OR)。Default off: none of the
	// routes are even registered (404 不可见). On: 纯服务端单点判定,无 AI 评分
	// (外部依赖,deferred)、无推送/广告/外呼能力。
	EnvFeatureContactTags = "FEATURE_CONTACT_TAGS"
	// EnvFeatureNotifyIngest gates the registered Notify HTTP receiver
	// (HUI-1680). Default off: the route is not registered. On: only
	// deployment-registered sources may deliver; missing per-source secret
	// or fetch base fails that route closed.
	EnvFeatureNotifyIngest = "FEATURE_NOTIFY_INGEST"
	// EnvFeatureEnterpriseDirectory gates customer-authorized enterprise
	// screening (HUI-1678). Default off: routes are not registered. On: a
	// tenant may import records it has rights to reuse. There is no official
	// public directory client in this process.
	EnvFeatureEnterpriseDirectory = "FEATURE_ENTERPRISE_DIRECTORY"
	// EnvFeatureReception gates the unified reception core (HUI-1688).
	// Default off: none of the routes are registered. On: H5 sessions,
	// FAQ knowledge, and human takeover. The visitor-key pepper is separate
	// from the intake pepper and is required by those routes (503 if empty).
	EnvFeatureReception = "FEATURE_RECEPTION"
	// EnvFeatureIntentGrade gates explainable intent grades (HUI-1684).
	// Default off: routes are not registered. On: versioned rules score a
	// tenant's own lead or session and only suggest a next step. There is
	// no calibrated model and no outbound call, SMS, group, or order.
	EnvFeatureIntentGrade = "FEATURE_INTENT_GRADE"
	// EnvFeatureROI gates restricted source-chain recalculation (HUI-1696).
	// Default off: the route is not registered. On: a tenant may submit
	// citations and receive a report. The handler does not write leads or costs.
	EnvFeatureROI = "FEATURE_ROI"
	// EnvFeatureSOPReach gates follow-up reminders, reply drafts, and human
	// confirmation (HUI-1689). Default off. Unattended SMS, email, WeCom, and
	// SOP stay closed; a balance or an AI score cannot open them.
	EnvFeatureSOPReach = "FEATURE_SOP_REACH"
	// EnvFeatureLightCopy gates in-scene reply, email, and marketing-brief
	// drafts (HUI-1682). Default off. There is no text model in this process,
	// so generation fails closed. Saving and editing a draft does not.
	EnvFeatureLightCopy = "FEATURE_LIGHT_COPY"
	// EnvFeatureOutbound gates the HUI-1687 call safety checks. Default off.
	// On still does not place a call: this process has no phone line, and
	// production auto-dial stays closed.
	EnvFeatureOutbound = "FEATURE_OUTBOUND_CALL"
	// EnvFeatureCampaignMotion gates the leads-side campaign handoff
	// (HUI-2747). Default off: routes are not registered. On still does not
	// call a model, render a piece, or write a creative project.
	EnvFeatureCampaignMotion = "FEATURE_CAMPAIGN_MOTION"
	// EnvReceptionVisitorPepper HMACs anonymous visitor keys. It is never a
	// billing secret and never a long-lived presentation token.
	EnvReceptionVisitorPepper = "RECEPTION_VISITOR_PEPPER"
	// EnvIngestSources is a comma-separated allowlist of source app ids.
	// Each app reads LEADS_INGEST_<APP>_SECRET / _FETCH_BASE / _FETCH_TOKEN,
	// where <APP> is the app id uppercased with '-' replaced by '_'.
	EnvIngestSources = "LEADS_INGEST_SOURCES"
	// EnvTenantBindings is an explicit source→target list. It does not create
	// tenants and it is not inferred from a local row. A duplicate or invalid
	// entry rejects the whole list. Example:
	// touch-engine/notify/touchTenant=leadsTenant@1
	EnvTenantBindings = "LEADS_TENANT_BINDINGS"
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

	// FeatureChannelAnalytics: read-only channel-effect analysis (HUI-1695 /
	// FEAT-0196). Default off (route not registered); independent of
	// FeatureFunnel.
	FeatureChannelAnalytics bool

	// FeatureContactTags: customer profile tags + segment query (HUI-1690 /
	// FEAT-0191). Default off (routes not registered).
	FeatureContactTags bool

	// EcoHandoff carries the deployment-injected receiver facts (HUI-1749).
	// TargetAppID defaults to "orders" (the receiver's registered app id).
	EcoHandoff EcoHandoffConfig

	// DedupPepper is the HMAC pepper for intake phone fingerprints (HUI-1683).
	// Deployment-injected; the intake endpoint fails closed when empty.
	DedupPepper string

	// FeatureNotifyIngest mounts the HUI-1680 Notify receiver. Default off.
	FeatureNotifyIngest bool
	// FeatureEnterpriseDirectory mounts HUI-1678 customer-authorized screening.
	// Default off (routes not registered).
	FeatureEnterpriseDirectory bool
	// FeatureReception mounts HUI-1688. Default off (routes not registered).
	FeatureReception bool
	// FeatureIntentGrade mounts HUI-1684. Default off (routes not registered).
	FeatureIntentGrade bool
	// FeatureROI mounts HUI-1696 recalculation. Default off (route not registered).
	FeatureROI bool
	// FeatureSOPReach mounts HUI-1689 reminders, drafts, and human confirmation.
	// Default off. This build has no live SMS, email, or WeCom connector.
	FeatureSOPReach bool
	// FeatureLightCopy mounts HUI-1682 in-scene drafts and optional handoff.
	// Default off. This build has no text model and no professional-tool engine.
	FeatureLightCopy bool
	// FeatureOutbound mounts HUI-1687 call gates. Default off. There is no
	// live line, so isolation runs stay simulated and nothing is charged.
	FeatureOutbound bool
	// FeatureCampaignMotion mounts the HUI-2747 leads-side handoff.
	// Default off. This process does not render or send a finished piece.
	FeatureCampaignMotion bool
	// VisitorPepper HMACs anonymous reception visitor keys.
	VisitorPepper string
	// TaskBaseURL, TaskToken, and TaskAccountID are optional. Reception calls
	// the platform task API only when all three are set. They are not a gate.
	TaskBaseURL   string
	TaskToken     string
	TaskAccountID string
	// IngestSources is the deployment allowlist. Empty unless the flag is on.
	IngestSources []IngestSource
	// TenantBindings is the operator map. Empty means no explicit binding.
	// TenantBindingProblems is set instead of a partial list when the env
	// value is duplicate or invalid. Callers must refuse rather than guess.
	TenantBindings        []tenantmap.Binding
	TenantBindingProblems []string
}

// IngestSource is one registered Notify sender (HUI-1680). The fetch base is
// deployment configuration, never a URL taken from the event.
type IngestSource struct {
	AppID      string
	Secret     string
	FetchBase  string
	FetchToken string
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
	bindings, bindingProblems := loadTenantBindings(get(EnvTenantBindings))
	return Config{
		HTTPAddr:                   firstNonEmpty(get("LEADS_HTTP_ADDR"), "127.0.0.1:18230"),
		DBPath:                     firstNonEmpty(get("LEADS_DB_PATH"), "data/leads.db"),
		Env:                        firstNonEmpty(get("LEADS_ENV"), "development"),
		AppID:                      firstNonEmpty(get("LEADS_APP_ID"), "leads-engine"),
		InternalToken:              get("LEADS_INTERNAL_TOKEN"),
		SessionCookie:              firstNonEmpty(get("LEADS_SESSION_COOKIE"), "leads_session"),
		IdentityBaseURL:            get("PLATFORM_IDENTITY_BASE_URL"),
		IdentityToken:              get("PLATFORM_IDENTITY_TOKEN"),
		IdentityAppHost:            get("PLATFORM_IDENTITY_APP_HOST"),
		NotifyBaseURL:              get("PLATFORM_NOTIFY_BASE_URL"),
		NotifyToken:                get("PLATFORM_NOTIFY_TOKEN"),
		UploadBaseURL:              get("PLATFORM_UPLOAD_BASE_URL"),
		UploadToken:                get("PLATFORM_UPLOAD_TOKEN"),
		FeatureNotify:              isTruthy(get(EnvFeatureNotify)),
		FeatureUpload:              isTruthy(get(EnvFeatureUpload)),
		FeatureServiceDraft:        isTruthy(get(EnvFeatureServiceDraft)),
		FeatureLeadsFilter:         isTruthy(get(EnvFeatureLeadsFilter)),
		FeatureFollowups:           isTruthy(get(EnvFeatureFollowups)),
		FeatureLeadsAssign:         isTruthy(get(EnvFeatureLeadsAssign)),
		FeatureFunnel:              isTruthy(get(EnvFeatureFunnel)),
		FeatureChannelAnalytics:    isTruthy(get(EnvFeatureChannelAnalytics)),
		FeatureContactTags:         isTruthy(get(EnvFeatureContactTags)),
		FeatureNotifyIngest:        isTruthy(get(EnvFeatureNotifyIngest)),
		FeatureEnterpriseDirectory: isTruthy(get(EnvFeatureEnterpriseDirectory)),
		FeatureReception:           isTruthy(get(EnvFeatureReception)),
		FeatureIntentGrade:         isTruthy(get(EnvFeatureIntentGrade)),
		FeatureROI:                 isTruthy(get(EnvFeatureROI)),
		FeatureSOPReach:            isTruthy(get(EnvFeatureSOPReach)),
		FeatureLightCopy:           isTruthy(get(EnvFeatureLightCopy)),
		FeatureOutbound:            isTruthy(get(EnvFeatureOutbound)),
		FeatureCampaignMotion:      isTruthy(get(EnvFeatureCampaignMotion)),
		VisitorPepper:              get(EnvReceptionVisitorPepper),
		TaskBaseURL:                get("PLATFORM_TASK_BASE_URL"),
		TaskToken:                  get("PLATFORM_TASK_TOKEN"),
		TaskAccountID:              get("PLATFORM_TASK_ACCOUNT_ID"),
		IngestSources:              parseIngestSources(get),
		TenantBindings:             bindings,
		TenantBindingProblems:      bindingProblems,
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
		{"channel_analytics", c.FeatureChannelAnalytics},
		{"contact_tags", c.FeatureContactTags},
		{"notify_ingest", c.FeatureNotifyIngest},
		{"enterprise_directory", c.FeatureEnterpriseDirectory},
		{"reception", c.FeatureReception},
		{"intent_grade", c.FeatureIntentGrade},
		{"roi", c.FeatureROI},
		{"sop_reach", c.FeatureSOPReach},
		{"light_copy", c.FeatureLightCopy},
		{"outbound_call", c.FeatureOutbound},
		{"campaign_motion", c.FeatureCampaignMotion},
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

// parseIngestSources reads the deployment allowlist. It does not validate;
// IngestGate reports problems when the feature is on.
func parseIngestSources(get func(string) string) []IngestSource {
	raw := strings.TrimSpace(get(EnvIngestSources))
	if raw == "" {
		return nil
	}
	var out []IngestSource
	for _, part := range strings.Split(raw, ",") {
		app := strings.TrimSpace(part)
		if app == "" {
			continue
		}
		key := ingestEnvKey(app)
		out = append(out, IngestSource{
			AppID:      app,
			Secret:     strings.TrimSpace(get("LEADS_INGEST_" + key + "_SECRET")),
			FetchBase:  strings.TrimRight(strings.TrimSpace(get("LEADS_INGEST_"+key+"_FETCH_BASE")), "/"),
			FetchToken: strings.TrimSpace(get("LEADS_INGEST_" + key + "_FETCH_TOKEN")),
		})
	}
	return out
}

func ingestEnvKey(appID string) string {
	return strings.ToUpper(strings.ReplaceAll(appID, "-", "_"))
}

// loadTenantBindings parses the operator list. Problems mean the list was
// dropped entirely; a valid prefix is not kept.
func loadTenantBindings(raw string) ([]tenantmap.Binding, []string) {
	bindings, err := tenantmap.Parse(raw)
	if err == nil {
		return bindings, nil
	}
	var me *tenantmap.Error
	if errors.As(err, &me) && me.Detail != "" {
		return nil, []string{me.Detail}
	}
	return nil, []string{err.Error()}
}

// IngestGate lists fail-closed problems for the Notify receiver. An empty
// list means the route may accept deliveries. The flag off yields nil (the
// route is not mounted).
func (c Config) IngestGate() []string {
	if !c.FeatureNotifyIngest {
		return nil
	}
	var problems []string
	if strings.TrimSpace(c.DedupPepper) == "" {
		problems = append(problems, EnvDedupPepper+" is required when FEATURE_NOTIFY_INGEST=on")
	}
	if len(c.IngestSources) == 0 {
		problems = append(problems, EnvIngestSources+" is required when FEATURE_NOTIFY_INGEST=on")
	}
	seen := map[string]bool{}
	for _, src := range c.IngestSources {
		if seen[src.AppID] {
			problems = append(problems, "duplicate ingest source "+src.AppID)
		}
		seen[src.AppID] = true
		prefix := "LEADS_INGEST_" + ingestEnvKey(src.AppID)
		if src.Secret == "" {
			problems = append(problems, prefix+"_SECRET is required")
		}
		if src.FetchToken == "" {
			problems = append(problems, prefix+"_FETCH_TOKEN is required")
		}
		if err := validateFetchBase(src.FetchBase); err != nil {
			problems = append(problems, prefix+"_FETCH_BASE: "+err.Error())
		}
	}
	return problems
}

func validateFetchBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be an http(s) origin without userinfo, query, or fragment")
	}
	return nil
}
