// HUI-1691 / FEAT-0192 客户档案域端点:
//   - owner 专属 CSV 导出(审计一行,不含跟进全文);
//   - 软删除(owner 专属,脱敏占位);
//   - 按 (来源提交, 渠道) 维度的 consent 记录 / 撤销(撤销持久,重放不恢复)/ 停止营销;
//   - 只追加的跟进时间线(全文不入日志)。
//
// 权限沿用 L0:owner 全部;sales/agent 仅 assignee 相关记录;非 assignee 404 掩码;
// 跨租户 / disabled / 无 grant agent 403。BFF 与 web 层零业务判断。
package httpapi

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/redact"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// capped reports whether s fits within max runes.
func capped(s string, max int) bool { return runeLen(s) <= max }

// ---- CSV export (owner only, audited) ---------------------------------------

// handleContactExport streams the tenant's contact profiles as CSV.
//   - 仅 owner(authz.ActionExport):sales/agent 一律 403;
//   - format=csv 必填,缺省/其他值 400;
//   - 只导出档案字段(含联系方式与备注),**不含跟进全文**;
//   - 服务端审计一行:谁、何时、多少条(无任何联系方式明文)。
func (s *Server) handleContactExport(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionExport, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	if got := r.URL.Query().Get("format"); got != "csv" {
		fail(w, http.StatusBadRequest, "bad_request", "format=csv is required (csv is the only supported export format)")
		return
	}
	if rejectPhoneSearch(w, r) {
		return
	}
	items, err := s.St.ListContacts(c.Member.TenantID, "", store.ContactFilter{})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export failed")
		return
	}
	// 审计一行(无 PII):principal/member 为服务端解析出的身份标识,非手机号。
	s.Log.Printf("contact export tenant=%s member=%s principal=%s rows=%d",
		c.Member.TenantID, c.Member.ID, c.Principal.ID, len(items))

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contacts-export.csv"`)
	w.WriteHeader(http.StatusOK)
	// BOM:便于 Excel 识别 UTF-8 中文。
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "name", "phone", "email", "business_category", "source_type",
		"consent_status", "tags", "notes", "assigned_member_id", "created_at", "updated_at"})
	for _, ct := range items {
		_ = cw.Write([]string{
			ct.ID, csvSafe(ct.Name), csvSafe(ct.Phone), csvSafe(ct.Email),
			ct.BusinessCategory, ct.SourceType, ct.ConsentStatus, csvSafe(ct.Tags),
			csvSafe(ct.Notes), ct.AssignedMemberID, ct.CreatedAt, ct.UpdatedAt,
		})
	}
	cw.Flush()
}

// csvSafe neutralizes CSV formula injection: cells starting with = + - @ get
// a leading apostrophe so spreadsheets treat them as text.
func csvSafe(v string) string {
	if v == "" {
		return ""
	}
	switch v[0] {
	case '=', '+', '-', '@':
		return "'" + v
	}
	return v
}

// ---- soft delete (owner only) ------------------------------------------------

// handleContactDelete soft-deletes the profile: deleted_at + 脱敏占位
// (name -> store.DeletedContactName,联系方式/备注/标签清空),行保留维持引用;
// consents/followups 行保留 = 最小审计。此后档案对所有角色 404。
func (s *Server) handleContactDelete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionDelete)
	if !ok {
		return
	}
	if err := s.St.SoftDeleteContact(rec.ID, rec.TenantID); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "contact delete failed")
		return
	}
	s.Log.Printf("contact soft-deleted id=%s by=%s", rec.ID, c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": rec.ID})
}

// ---- consents ------------------------------------------------------------------

// handleContactConsentList answers every per-source consent row plus a summary.
// 每行独立可查:一来源撤销不影响其他来源的展示与状态。
func (s *Server) handleContactConsentList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	items, err := s.St.ListContactConsents(rec.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "consent list failed")
		return
	}
	if items == nil {
		items = []store.ContactConsent{}
	}
	summary := map[string]int{"total": len(items), "marketing_active": 0, "revoked": 0}
	for _, cs := range items {
		switch cs.Status() {
		case "revoked":
			summary["revoked"]++
		default:
			if cs.MarketingAllowed {
				summary["marketing_active"]++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "summary": summary})
}

// handleContactConsentUpsert records/refreshes one consent event keyed by
// (contact, source_submission_ref, source_channel). 撤销红线在此生效:
// 已撤销键的事件重放只能得到 state=replay_unchanged,revoked_at 原样保留。
func (s *Server) handleContactConsentUpsert(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		SourceSubmissionRef string `json:"source_submission_ref"`
		SourceChannel       string `json:"source_channel"`
		NoticeVersion       string `json:"notice_version"`
		Purpose             string `json:"purpose"`
		MarketingAllowed    bool   `json:"marketing_allowed"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.SourceSubmissionRef) == "" {
		fail(w, http.StatusBadRequest, "bad_request",
			"source_submission_ref is required (consent is kept per source submission/channel)")
		return
	}
	if !capped(in.SourceSubmissionRef, 128) || !capped(in.SourceChannel, 64) ||
		!capped(in.NoticeVersion, 64) || !capped(in.Purpose, 64) {
		fail(w, http.StatusBadRequest, "bad_request", "consent fields exceed their length caps (128/64/64/64)")
		return
	}
	if in.Purpose == "" {
		in.Purpose = "marketing"
	}
	created, state, err := s.St.UpsertContactConsent(rec.TenantID, rec.ID, store.ConsentUpsert{
		SourceSubmissionRef: in.SourceSubmissionRef,
		SourceChannel:       in.SourceChannel,
		NoticeVersion:       in.NoticeVersion,
		Purpose:             in.Purpose,
		MarketingAllowed:    in.MarketingAllowed,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "consent upsert failed")
		return
	}
	// 日志只含键与状态,不含任何联系方式/告知内容明文。
	s.Log.Printf("consent %s contact=%s key=ref:%s,channel:%s state=%s marketing=%t",
		state, rec.ID, in.SourceSubmissionRef, in.SourceChannel, created.Status(), created.MarketingAllowed)
	writeJSON(w, http.StatusOK, map[string]any{
		"consent": created,
		"state":   state,
		"status":  created.Status(),
	})
}

// handleContactConsentRevoke sets the persistent revoked marker on ONE source.
// 重复撤销幂等(revoked_at 保持首次时间);同键事件此后重放一律不恢复。
func (s *Server) handleContactConsentRevoke(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !capped(in.Reason, 200) {
		fail(w, http.StatusBadRequest, "bad_request", "reason must be at most 200 characters")
		return
	}
	updated, changed, err := s.St.RevokeContactConsent(rec.TenantID, rec.ID, r.PathValue("consentId"), in.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "consent not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "consent revoke failed")
		return
	}
	s.Log.Printf("consent revoke contact=%s consent=%s changed=%t status=%s %s",
		rec.ID, updated.ID, changed, updated.Status(), redact.Note(in.Reason))
	writeJSON(w, http.StatusOK, map[string]any{"consent": updated, "changed": changed, "status": updated.Status()})
}

// handleContactRevokeMarketing is 「停止营销」: batch-revoke all still
// effective marketing consents of the contact across sources. Idempotent.
// 此后重放任何旧 consent 事件都不会恢复营销权限。
func (s *Server) handleContactRevokeMarketing(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if !capped(in.Reason, 200) {
		fail(w, http.StatusBadRequest, "bad_request", "reason must be at most 200 characters")
		return
	}
	n, err := s.St.RevokeContactMarketing(rec.TenantID, rec.ID, in.Reason)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "marketing revoke failed")
		return
	}
	s.Log.Printf("marketing revoked contact=%s sources=%d by=%s %s", rec.ID, n, c.Member.ID, redact.Note(in.Reason))
	writeJSON(w, http.StatusOK, map[string]any{
		"marketing_revoked": true,
		"sources_revoked":   n,
	})
}

// ---- followups -----------------------------------------------------------------

// handleContactFollowupList answers the append-only timeline, oldest first.
// 全文仅回给有读取权的人;不进入日志、URL 或公共 Context。
func (s *Server) handleContactFollowupList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	items, err := s.St.ListContactFollowups(rec.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "followup list failed")
		return
	}
	if items == nil {
		items = []store.ContactFollowup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleContactFollowupCreate appends one timeline entry authored by the
// caller. 只追加、无编辑端点(停用成员的历史跟进因此保留且不可再改);
// note 全文不入日志(只记 redact.Note 长度摘要)。
func (s *Server) handleContactFollowupCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		fail(w, http.StatusBadRequest, "bad_request", "note is required")
		return
	}
	if !capped(note, 2000) {
		fail(w, http.StatusBadRequest, "bad_request", "note must be at most 2000 characters")
		return
	}
	f, err := s.St.CreateContactFollowup(rec.TenantID, rec.ID, c.Member.ID, note)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "followup create failed")
		return
	}
	// 跟进全文零泄漏:日志只带长度摘要。
	s.Log.Printf("followup created id=%s contact=%s author=%s %s", f.ID, rec.ID, c.Member.ID, redact.Note(f.Note))
	writeJSON(w, http.StatusCreated, f)
}
