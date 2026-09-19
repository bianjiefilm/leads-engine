// Package platform holds the Notify/Upload client scaffolding.
//
// 本票(HUI-1748 L0)只做脚手架 + 配置 + 开关:不实现真实事件、不伪造成功。
// 开关关(FEATURE_* off)或缺专用 token 时,一切调用返回显式错误(fail-closed),
// 调用方不得把"未发生"当"成功"上报。
package platform

import "errors"

// ErrFeatureDisabled is returned when the feature flag is off.
var ErrFeatureDisabled = errors.New("platform: feature disabled (FEATURE_* off)")

// ErrNotConfigured is returned when the feature is on but its dedicated token
// or base URL is missing. Never silently degrade.
var ErrNotConfigured = errors.New("platform: feature enabled but missing base url or dedicated token")

// NotifyClient is the platform-notify client skeleton (HUI-1748: scaffolding).
type NotifyClient struct {
	BaseURL string
	Token   string
	Enabled bool
}

// Publish would emit an event on the notify bus. L0: gate only.
func (c NotifyClient) Publish(event, idempotencyKey string, payload []byte) error {
	if !c.Enabled {
		return ErrFeatureDisabled
	}
	if c.BaseURL == "" || c.Token == "" {
		return ErrNotConfigured
	}
	// Real delivery belongs to a later FEAT ticket (线索接收 HUI-1683/1680 会
	// 在此挂接)。本票绝不实现、也绝不伪造发送成功。
	return ErrNotImplemented
}

// ErrNotImplemented marks scaffolding bodies that a later FEAT ticket fills in.
var ErrNotImplemented = errors.New("platform: not implemented in L0 (scaffolding only)")

// UploadClient is the platform-upload client skeleton (HUI-1748: scaffolding).
type UploadClient struct {
	BaseURL string
	Token   string
	Enabled bool
}

// CreateSession would open a direct-to-OSS upload session via platform-upload.
// L0: gate only.
func (c UploadClient) CreateSession(assetName string, sizeBytes int64) error {
	if !c.Enabled {
		return ErrFeatureDisabled
	}
	if c.BaseURL == "" || c.Token == "" {
		return ErrNotConfigured
	}
	return ErrNotImplemented
}
