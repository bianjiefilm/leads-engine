// Package appregistry is the versioned static app manifest loader (HUI-1749).
//
// 契约对齐 public-ai docs/contracts/app-registry/v1/README.md(冻结面):静态
// manifest + 严格校验器,不是服务、无端口、无动态发现。加载即校验(失败拒绝,
// 无半生效状态):严格 JSON、唯一键、未知字段拒绝、全部属性必填、URL 只允许
// 精确 https 或 loopback http(禁 query/fragment/userinfo,path 必填)。
//
// 用途(HUI-1749 发送端):交接文档的生态事实(source_app/能力/return_target_id/
// 接收端 app 登记)全部从本登记读,绝不硬编码在业务逻辑里;投递 URL 不在本表
// (app-registry/v1 只有 launch/receipt 两类 target),由部署配置注入。
package appregistry

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ManifestVersion is the only accepted manifest version (versioned: v2 is
// explicitly refused, no best-effort parsing).
const ManifestVersion = "app-registry/v1"

// MaxManifestBytes is the manifest size cap (1 MiB, aligned with the contract).
const MaxManifestBytes = 1 << 20

// Capability is one declared capability of an app. 声明≠放行:能力只是静态
// 事实,放行与否由各运行时校验链独立裁决。
type Capability struct {
	Name            string `json:"name"`
	MenuVisible     bool   `json:"menu_visible"`
	RequiresBilling bool   `json:"requires_billing"`
}

// Target is one registered launch/receipt target.
type Target struct {
	TargetID string `json:"target_id"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
}

// App is one registered ecosystem app.
type App struct {
	AppID                string       `json:"app_id"`
	DisplayName          string       `json:"display_name"`
	Enabled              bool         `json:"enabled"`
	SupportedSourceKinds []string     `json:"supported_source_kinds"`
	Capabilities         []Capability `json:"capabilities"`
	LaunchTargets        []Target     `json:"launch_targets"`
	ReceiptTargets       []Target     `json:"receipt_targets"`
}

// Manifest is the loaded, validated registry.
type Manifest struct {
	ManifestVersion string `json:"manifest_version"`
	Apps            []App  `json:"apps"`
}

// Registry is an immutable validated view over a Manifest.
type Registry struct {
	apps    map[string]*App
	targets map[string]*Target // key: appID + "|" + targetID + "|" + kind
	selfID  string
}

// WithSelf marks this deployment's own app id and returns the registry.
func (r *Registry) WithSelf(appID string) *Registry {
	r.selfID = appID
	return r
}

// App returns the registered app by id.
func (r *Registry) App(appID string) (App, bool) {
	a, ok := r.apps[appID]
	if !ok {
		return App{}, false
	}
	return *a, true
}

// Self returns this deployment's own app entry.
func (r *Registry) Self() (App, bool) { return r.App(r.selfID) }

// ResolveTarget resolves (app, target, kind) to the exact registered URL.
// 自由 URL、伪造 target、跨应用 target、kind 不匹配一律不命中。
func (r *Registry) ResolveTarget(appID, targetID, kind string) (string, bool) {
	t, ok := r.targets[appID+"|"+targetID+"|"+kind]
	if !ok {
		return "", false
	}
	return t.URL, true
}

// HasCapability reports whether the app declares the capability (静态事实)。
func (r *Registry) HasCapability(appID, capability string) bool {
	a, ok := r.apps[appID]
	if !ok {
		return false
	}
	for _, c := range a.Capabilities {
		if c.Name == capability {
			return true
		}
	}
	return false
}

// ---- strict loading -----------------------------------------------------------

// strictObject decodes one JSON object rejecting duplicate keys, trailing
// data, lone surrogates and non-object shapes (mirror of the frozen JSON
// discipline used across the ecosystem contracts).
func strictObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("must be an object")
	}
	m := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid key: %w", err)
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("key must be a string")
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("duplicate key: %s", key)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("invalid value for %s: %w", key, err)
		}
		m[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("object not closed")
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing JSON rejected")
	}
	return m, nil
}

var validSourceKinds = map[string]bool{"order": true, "standalone": true, "campaign": true}

func isSlug(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return !strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-") && !strings.Contains(s, "--")
}

// validTargetURL enforces the URL discipline: exact https, or http on
// loopback only (127.0.0.1/::1/localhost); no query/fragment/userinfo; path
// required. 注册表里永远没有可注入参数的目的地。
func validTargetURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable url")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("query/fragment/userinfo forbidden in registry urls")
	}
	if u.Path == "" || u.Path == "/" {
		return fmt.Errorf("path required")
	}
	host := strings.Trim(u.Hostname(), "[]")
	switch u.Scheme {
	case "https":
	case "http":
		if host != "127.0.0.1" && host != "::1" && host != "localhost" {
			return fmt.Errorf("http allowed on loopback only")
		}
	default:
		return fmt.Errorf("scheme must be https (or http on loopback)")
	}
	if u.Port() == "" {
		return fmt.Errorf("explicit port required")
	}
	return nil
}

// Load parses and validates a manifest; any violation is an error (fail on
// deploy, no half-effective state).
func Load(raw []byte) (*Registry, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty manifest")
	}
	if len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds 1 MiB")
	}
	top, err := strictObject(raw)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{"manifest_version": true, "apps": true}
	for k := range top {
		if !allowed[k] {
			return nil, fmt.Errorf("unknown field: %s", k)
		}
	}
	var mv string
	if err := json.Unmarshal(top["manifest_version"], &mv); err != nil {
		return nil, fmt.Errorf("manifest_version must be a string")
	}
	if mv != ManifestVersion {
		return nil, fmt.Errorf("unsupported manifest_version: %s", mv)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid manifest body: %w", err)
	}
	r := &Registry{apps: map[string]*App{}, targets: map[string]*Target{}}
	// target_id is globally unique per contract (not per app/kind).
	seenTargetIDs := map[string]bool{}
	for i := range m.Apps {
		a := &m.Apps[i]
		if a.AppID == "" || !isSlug(a.AppID) {
			return nil, fmt.Errorf("app %d: app_id must be a lowercase slug", i)
		}
		if _, dup := r.apps[a.AppID]; dup {
			return nil, fmt.Errorf("duplicate app_id: %s", a.AppID)
		}
		if a.DisplayName == "" {
			return nil, fmt.Errorf("app %s: display_name required", a.AppID)
		}
		if len(a.SupportedSourceKinds) < 1 || len(a.SupportedSourceKinds) > 3 {
			return nil, fmt.Errorf("app %s: supported_source_kinds must be a 1..3 subset", a.AppID)
		}
		seenKind := map[string]bool{}
		for _, k := range a.SupportedSourceKinds {
			if !validSourceKinds[k] || seenKind[k] {
				return nil, fmt.Errorf("app %s: bad source kind %q", a.AppID, k)
			}
			seenKind[k] = true
		}
		if len(a.Capabilities) < 1 {
			return nil, fmt.Errorf("app %s: at least one capability required", a.AppID)
		}
		capSeen := map[string]bool{}
		for _, c := range a.Capabilities {
			if !isCapName(c.Name) {
				return nil, fmt.Errorf("app %s: capability name %q invalid (lowercase dot-separated)", a.AppID, c.Name)
			}
			if capSeen[c.Name] {
				return nil, fmt.Errorf("app %s: duplicate capability %s", a.AppID, c.Name)
			}
			capSeen[c.Name] = true
		}
		if len(a.LaunchTargets) < 1 {
			return nil, fmt.Errorf("app %s: at least one launch target required", a.AppID)
		}
		all := append(append([]Target{}, a.LaunchTargets...), a.ReceiptTargets...)
		for _, t := range all {
			if t.TargetID == "" || !isSlug(t.TargetID) {
				return nil, fmt.Errorf("app %s: bad target_id %q", a.AppID, t.TargetID)
			}
			if t.Kind != "launch" && t.Kind != "receipt" {
				return nil, fmt.Errorf("app %s: bad target kind %q", a.AppID, t.Kind)
			}
			if err := validTargetURL(t.URL); err != nil {
				return nil, fmt.Errorf("app %s target %s: %v", a.AppID, t.TargetID, err)
			}
			if seenTargetIDs[t.TargetID] {
				return nil, fmt.Errorf("target_id not globally unique: %s", t.TargetID)
			}
			seenTargetIDs[t.TargetID] = true
			cp := t
			r.targets[a.AppID+"|"+t.TargetID+"|"+t.Kind] = &cp
		}
		cp := *a
		r.apps[a.AppID] = &cp
	}
	return r, nil
}

func isCapName(s string) bool {
	// 小写点分段(registry 管辖命名空间,无下划线):a.b 或 a.b.c
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return false
			}
		}
	}
	return true
}

//go:embed manifest.json
var embedded []byte

// Embedded loads the compiled-in ecosystem manifest (加载即校验;坏清单拒绝启动)。
func Embedded() (*Registry, error) { return Load(embedded) }
