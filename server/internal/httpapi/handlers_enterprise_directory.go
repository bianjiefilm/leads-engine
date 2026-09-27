// HUI-1678 企业资料筛选 HTTP 面。
//
// 官方企业公开库没有已授权接口：capability 明确写 unavailable，不编造目录。
// 导入的是客户声明有权再利用的样本。确认才进入本租户候选池，且不写营销同意、
// 不写 Notify 收件箱、不触发外呼/短信/SOP。lead.consent_revoked 不是本票的验收，保持原样。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const enterpriseImportMaxBody = 256 << 10

const enterpriseCapabilityReason = "没有已授权的官方企业公开库接口。本版只接受客户声明有权导入的企业资料，不编造公开库，也不绕过登录或访问限制。"

func (s *Server) handleEnterpriseCapability(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"official_directory": "unavailable",
		"reason":             enterpriseCapabilityReason,
		"accepted_source":    store.EnterpriseSourceApp,
		"marketing_implied":  false,
		"outbound_linked":    false,
	})
}

func (s *Server) handleEnterpriseImport(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, enterpriseImportMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable")
		return
	}
	var in struct {
		SourceKey       string `json:"source_key"`
		SourceName      string `json:"source_name"`
		CollectedAt     string `json:"collected_at"`
		UpdateCycleDays int    `json:"update_cycle_days"`
		License         string `json:"license"`
		Correction      string `json:"correction"`
		Records         []struct {
			EnterpriseID   string `json:"enterprise_id"`
			EnterpriseName string `json:"enterprise_name"`
			Industry       string `json:"industry"`
			Region         string `json:"region"`
			Scale          string `json:"scale"`
			PersonName     string `json:"person_name"`
			PersonPhone    string `json:"person_phone"`
			PersonEmail    string `json:"person_email"`
		} `json:"records"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	batch := store.EnterpriseImportIn{
		SourceKey: in.SourceKey, SourceName: in.SourceName, CollectedAt: in.CollectedAt,
		UpdateCycleDays: in.UpdateCycleDays, License: in.License, Correction: in.Correction,
	}
	for _, rec := range in.Records {
		batch.Records = append(batch.Records, store.EnterpriseRecordIn{
			EnterpriseID: rec.EnterpriseID, EnterpriseName: rec.EnterpriseName,
			Industry: rec.Industry, Region: rec.Region, Scale: rec.Scale,
			PersonName: rec.PersonName, PersonPhone: rec.PersonPhone, PersonEmail: rec.PersonEmail,
		})
	}
	result, err := s.St.UpsertEnterpriseImport(c.Member.TenantID, c.Member.ID, batch)
	if err != nil {
		writeEnterpriseErr(w, err)
		return
	}
	code := http.StatusOK
	if result.Inserted > 0 {
		code = http.StatusCreated
	}
	s.Log.Printf("enterprise import tenant=%s source_key=%s inserted=%d", c.Member.TenantID, result.SourceKey, result.Inserted)
	writeJSON(w, code, map[string]any{
		"import_id":  result.ImportID,
		"source_key": result.SourceKey,
		"records":    result.Records,
	})
}

func (s *Server) handleEnterpriseRecordList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	q := r.URL.Query()
	for key := range q {
		switch key {
		case "industry", "region", "scale":
		default:
			fail(w, http.StatusBadRequest, "bad_request", "筛选只接受企业的 industry、region、scale，不按自然人联系方式筛选")
			return
		}
	}
	items, err := s.St.ListEnterpriseRecords(c.Member.TenantID,
		strings.TrimSpace(q.Get("industry")), strings.TrimSpace(q.Get("region")), strings.TrimSpace(q.Get("scale")))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "enterprise list failed")
		return
	}
	if items == nil {
		items = []store.EnterpriseView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleEnterprisePreview(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	view, err := s.St.GetEnterpriseRecord(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		writeEnterpriseErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record":             view,
		"will_enter_pool":    false,
		"marketing_consent":  false,
		"outbound":           "none",
		"official_directory": "unavailable",
		"freshness":          view.Freshness,
		"allowed_uses":       view.AllowedUses,
		"missing_fields":     view.MissingFields,
		"field_sources":      view.FieldSources,
	})
}

func (s *Server) handleEnterpriseConfirm(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	if strings.TrimSpace(s.Cfg.DedupPepper) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config_gate_dedup",
			"message": "LEADS_DEDUP_PEPPER is required for lead intake (fail-closed)",
		})
		return
	}
	result, err := s.St.ConfirmEnterpriseRecord(c.Member.TenantID, r.PathValue("id"), c.Member.ID, s.Cfg.DedupPepper,
		s.Cfg.FeatureLeadsFilter, s.Cfg.FeatureLeadsAssign)
	if err != nil {
		writeEnterpriseErr(w, err)
		return
	}
	code := http.StatusCreated
	if result.Duplicate {
		code = http.StatusOK
	}
	s.Log.Printf("enterprise confirm tenant=%s record=%s lead=%s duplicate=%t", c.Member.TenantID, r.PathValue("id"), result.LeadID, result.Duplicate)
	writeJSON(w, code, result)
}

func (s *Server) handleEnterpriseRefuse(w http.ResponseWriter, r *http.Request) {
	s.suppressEnterprise(w, r, "refused")
}

func (s *Server) handleEnterpriseDelete(w http.ResponseWriter, r *http.Request) {
	s.suppressEnterprise(w, r, "deleted")
}

func (s *Server) suppressEnterprise(w http.ResponseWriter, r *http.Request, reason string) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	if err := s.St.SuppressEnterpriseRecord(c.Member.TenantID, r.PathValue("id"), c.Member.ID, reason); err != nil {
		writeEnterpriseErr(w, err)
		return
	}
	s.Log.Printf("enterprise %s tenant=%s record=%s", reason, c.Member.TenantID, r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            reason,
		"marketing_consent": false,
		"outbound":          "none",
	})
}

func writeEnterpriseErr(w http.ResponseWriter, err error) {
	var bad *store.EnterpriseBadInput
	switch {
	case errors.As(err, &bad):
		fail(w, http.StatusBadRequest, "bad_request", bad.Msg)
	case errors.Is(err, store.ErrEnterpriseNotFound):
		fail(w, http.StatusNotFound, "not_found", "enterprise record not found")
	case errors.Is(err, store.ErrEnterpriseSuppressed):
		fail(w, http.StatusConflict, "suppression_held", "同一来源下，拒绝或删除后的企业不能再次进入候选池，该来源的刷新也不会恢复营销")
	case errors.Is(err, store.ErrEventContentConflict):
		fail(w, http.StatusConflict, "event_content_conflict", "同一企业的资料与已入库内容不一致，已标记冲突，未覆盖原线索")
	default:
		fail(w, http.StatusInternalServerError, "internal", "enterprise directory failed")
	}
}
