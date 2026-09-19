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
)

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
		HTTPAddr:        firstNonEmpty(get("LEADS_HTTP_ADDR"), "127.0.0.1:18230"),
		DBPath:          firstNonEmpty(get("LEADS_DB_PATH"), "data/leads.db"),
		Env:             firstNonEmpty(get("LEADS_ENV"), "development"),
		AppID:           firstNonEmpty(get("LEADS_APP_ID"), "leads-engine"),
		InternalToken:   get("LEADS_INTERNAL_TOKEN"),
		SessionCookie:   firstNonEmpty(get("LEADS_SESSION_COOKIE"), "leads_session"),
		IdentityBaseURL: get("PLATFORM_IDENTITY_BASE_URL"),
		IdentityToken:   get("PLATFORM_IDENTITY_TOKEN"),
		IdentityAppHost: get("PLATFORM_IDENTITY_APP_HOST"),
		NotifyBaseURL:   get("PLATFORM_NOTIFY_BASE_URL"),
		NotifyToken:     get("PLATFORM_NOTIFY_TOKEN"),
		UploadBaseURL:   get("PLATFORM_UPLOAD_BASE_URL"),
		UploadToken:     get("PLATFORM_UPLOAD_TOKEN"),
		FeatureNotify:   isTruthy(get(EnvFeatureNotify)),
		FeatureUpload:   isTruthy(get(EnvFeatureUpload)),
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
	flags := "off/off"
	switch {
	case c.FeatureNotify && c.FeatureUpload:
		flags = "on/on"
	case c.FeatureNotify:
		flags = "on/off"
	case c.FeatureUpload:
		flags = "off/on"
	}
	return fmt.Sprintf("env=%s app_id=%s addr=%s db=%s features(notify/upload)=%s",
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
