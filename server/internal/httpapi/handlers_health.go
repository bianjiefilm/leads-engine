package httpapi

import (
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
)

// handleHealthz is unauthenticated: it reports process liveness and the
// configuration gate state. It never reports "ready" for a platform
// integration that is not configured (fail-closed visibility for ops).
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	problems := s.Cfg.Gate()
	status := http.StatusOK
	ready := "ready"
	if len(problems) > 0 {
		ready = "degraded (authenticated actions fail-closed)"
	}
	writeJSON(w, status, map[string]any{
		"service":        "leads-server",
		"app_id":         s.Cfg.AppID,
		"env":            s.Cfg.Env,
		"status":         ready,
		"config_issues":  len(problems),
		"feature_notify": boolOnOff(s.Cfg.FeatureNotify),
		"feature_upload": boolOnOff(s.Cfg.FeatureUpload),
		"identity_mode":  "platform",
		"detail":         problems,
	})
}

func boolOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

var _ = config.EnvFeatureNotify
