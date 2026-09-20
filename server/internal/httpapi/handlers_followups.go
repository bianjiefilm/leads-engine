// HUI-1692 / FEAT-0193 销售跟进记录端点(FEATURE_FOLLOWUPS 闸控,默认 off ->
// 路由不注册 -> 404 不可见;on 时全部挂 requireSession 既有鉴权中间件):
//   - CRUD:创建(挂 contact 必填 / lead 可选)、按 contact/lead 分页列表、
//     读取、编辑(note / next_follow_up_at)、完结 / 重开;
//   - 「我的到期跟进」:服务端确定性查询(<=now 升序、完结排除、软删 contact
//     排除、只含当前成员被分配的 contact/lead);不做推送/邮件/外呼,
//     提醒触达由未来票承接;
//   - 权限沿用 L0 记录级作用域:作用域 = 挂靠 lead(优先)或 contact 的
//     assigned_member_id;非 assignee 404 掩码;跨租户 / disabled / 无 grant
//     agent 403;owner 在本租户内全量;
//   - PII 纪律:note 全文不入日志(redact.Note 长度摘要)、不入 URL;
//     next_follow_up_at 是时间戳不是 PII,可进日志。
package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/redact"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// followUpNoteMaxRunes caps the note in runes so CJK text is not penalized
// against byte limits (same discipline as contact notes).
const followUpNoteMaxRunes = 2000

// normalizeFollowUpWhen validates an optional RFC3339 timestamp and returns it
// normalized to UTC second precision: lexicographic string comparison in SQL
// then equals chronological comparison, so the due surface stays deterministic
// regardless of the timezone the client used. 纪律:只有「字段缺席/JSON null」
// 表示不设值;显式传来的值(含空串)必须是合法 RFC3339,否则 400 —— 非法格式
// 绝不静默当缺省。
func normalizeFollowUpWhen(v string) (string, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return "", false
	}
	return t.UTC().Truncate(time.Second).Format(time.RFC3339), true
}

// followUpRecord fetches one record within the caller's tenant and authorizes
// the action against its parent scope; a missing record, a tombstoned parent
// and a non-assignee caller all answer 404 (assignment topology never leaks).
func (s *Server) followUpRecord(w http.ResponseWriter, r *http.Request, c *caller, action authz.Action) (store.FollowUp, bool) {
	f, scope, err := s.St.GetFollowUp(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return store.FollowUp{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up lookup failed")
		return store.FollowUp{}, false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: f.TenantID, AssigneeMemberID: scope}, w) {
		return store.FollowUp{}, false
	}
	return f, true
}

// handleFollowUpCreate appends a follow-up to a contact (required) and
// optionally its lead. lead-attached records authorize on the LEAD assignee
// (a sales owns their lead even when the contact carries a different owner);
// contact-only records authorize on the contact assignee — same rule the due
// surface and every other path use (single scope derivation).
func (s *Server) handleFollowUpCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	var in struct {
		ContactID      string  `json:"contact_id"`
		LeadID         string  `json:"lead_id"`
		Note           string  `json:"note"`
		NextFollowUpAt *string `json:"next_follow_up_at"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ContactID == "" {
		fail(w, http.StatusBadRequest, "bad_request", "contact_id is required")
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		fail(w, http.StatusBadRequest, "bad_request", "note is required")
		return
	}
	if runeLen(note) > followUpNoteMaxRunes {
		fail(w, http.StatusBadRequest, "bad_request", "note must be at most 2000 characters")
		return
	}
	next := ""
	if in.NextFollowUpAt != nil {
		var ok bool
		if next, ok = normalizeFollowUpWhen(*in.NextFollowUpAt); !ok {
			fail(w, http.StatusBadRequest, "bad_request", "next_follow_up_at must be an RFC3339 timestamp")
			return
		}
	}
	if in.LeadID != "" {
		lead, err := s.St.GetLead(in.LeadID, c.Member.TenantID)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusBadRequest, "bad_lead", "lead_id does not exist in this tenant")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
			return
		}
		if lead.ContactID != in.ContactID {
			fail(w, http.StatusBadRequest, "bad_lead", "lead_id does not belong to contact_id")
			return
		}
		// 挂靠线索也要确认 contact 存活:软删 contact 上的跟进处处不可见,
		// 绝不允许借 lead 路径写入一条「影子记录」。
		if _, err := s.St.GetContact(in.ContactID, c.Member.TenantID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fail(w, http.StatusBadRequest, "bad_contact", "contact_id does not exist in this tenant")
				return
			}
			fail(w, http.StatusInternalServerError, "internal", "contact lookup failed")
			return
		}
		if !s.requireAction(c, authz.ActionUpdate,
			authz.RecordScope{TenantID: lead.TenantID, AssigneeMemberID: lead.AssignedMemberID}, w) {
			return
		}
	} else {
		contact, err := s.St.GetContact(in.ContactID, c.Member.TenantID)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusBadRequest, "bad_contact", "contact_id does not exist in this tenant")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "contact lookup failed")
			return
		}
		if !s.requireAction(c, authz.ActionUpdate,
			authz.RecordScope{TenantID: contact.TenantID, AssigneeMemberID: contact.AssignedMemberID}, w) {
			return
		}
	}
	f, err := s.St.CreateFollowUp(c.Member.TenantID, in.ContactID, in.LeadID, note, next, c.Member.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up create failed")
		return
	}
	// 日志零联系方式/零跟进全文:只有 id、归属与时间戳,note 只带长度摘要。
	s.Log.Printf("follow-up created id=%s contact=%s lead=%s author=%s due=%s %s",
		f.ID, f.ContactID, f.LeadID, c.Member.ID, f.NextFollowUpAt, redact.Note(note))
	writeJSON(w, http.StatusCreated, f)
}

// handleFollowUpDue is 「我的到期跟进」: a personal, deterministic query —
// ALWAYS scoped to the caller's own assignments (owner included; owner 是成员,
// 不是租户级到期批量视图的入口). Due AND overdue (next_follow_up_at <= now)
// ascending; completed excluded by definition; soft-deleted parents excluded by
// the scope join. 提醒触达(推送/邮件/外呼)不属于本端点。
func (s *Server) handleFollowUpDue(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	items, err := s.St.ListDueFollowUps(c.Member.TenantID, c.Member.ID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "due follow-up query failed")
		return
	}
	if items == nil {
		items = []store.FollowUp{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleFollowUpGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	f, ok := s.followUpRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleFollowUpPatch edits note / next_follow_up_at. completed_at is not a
// patchable field: 完结/重开必须走显式端点(与商机阶段转换同款纪律)。
func (s *Server) handleFollowUpPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.followUpRecord(w, r, c, authz.ActionUpdate); !ok {
		return
	}
	var in struct {
		Note           *string       `json:"note"`
		NextFollowUpAt jsonOptString `json:"next_follow_up_at"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	var patch store.FollowUpPatch
	if in.Note != nil {
		note := strings.TrimSpace(*in.Note)
		if note == "" {
			fail(w, http.StatusBadRequest, "bad_request", "note must be a non-empty string")
			return
		}
		if runeLen(note) > followUpNoteMaxRunes {
			fail(w, http.StatusBadRequest, "bad_request", "note must be at most 2000 characters")
			return
		}
		patch.Note = &note
	}
	if in.NextFollowUpAt.Set {
		patch.NextSet = true
		if in.NextFollowUpAt.Value != nil {
			next, ok := normalizeFollowUpWhen(*in.NextFollowUpAt.Value)
			if !ok {
				fail(w, http.StatusBadRequest, "bad_request", "next_follow_up_at must be an RFC3339 timestamp")
				return
			}
			patch.NextFollowUpAt = &next
		}
	}
	updated, _, err := s.St.UpdateFollowUp(r.PathValue("id"), c.Member.TenantID, patch)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up update failed")
		return
	}
	s.Log.Printf("follow-up updated id=%s by=%s due=%s %s",
		updated.ID, c.Member.ID, updated.NextFollowUpAt, redact.Note(updated.Note))
	writeJSON(w, http.StatusOK, updated)
}

// handleFollowUpComplete stamps completed_at (first stamp wins, idempotent).
func (s *Server) handleFollowUpComplete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.followUpRecord(w, r, c, authz.ActionUpdate); !ok {
		return
	}
	updated, _, _, err := s.St.CompleteFollowUp(r.PathValue("id"), c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up complete failed")
		return
	}
	s.Log.Printf("follow-up completed id=%s by=%s completed_at=%s", updated.ID, c.Member.ID, updated.CompletedAt)
	writeJSON(w, http.StatusOK, updated)
}

// handleFollowUpReopen clears completed_at (idempotent on an open row).
func (s *Server) handleFollowUpReopen(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.followUpRecord(w, r, c, authz.ActionUpdate); !ok {
		return
	}
	updated, _, _, err := s.St.ReopenFollowUp(r.PathValue("id"), c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up reopen failed")
		return
	}
	s.Log.Printf("follow-up reopened id=%s by=%s", updated.ID, c.Member.ID)
	writeJSON(w, http.StatusOK, updated)
}

// followUpPageParams parses the deterministic pager: limit default 50, 1..200;
// offset >= 0. Malformed or out-of-range values are 400 (no silent clamping).
func followUpPageParams(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(w, http.StatusBadRequest, "bad_request", "limit must be an integer between 1 and 200")
			return 0, 0, false
		}
		limit = n
	}
	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fail(w, http.StatusBadRequest, "bad_request", "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}

// handleContactFollowUpPageList answers the per-contact page (newest first,
// total included). Authz on the parent contact (L0 read-record scope).
func (s *Server) handleContactFollowUpPageList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.contactRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	limit, offset, ok := followUpPageParams(w, r)
	if !ok {
		return
	}
	items, total, err := s.St.ListFollowUpsByContact(rec.TenantID, rec.ID, limit, offset)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up list failed")
		return
	}
	if items == nil {
		items = []store.FollowUp{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

// handleLeadFollowUpPageList answers the per-lead page: only records attached
// to the lead. Authz on the lead (L0 read-record scope).
func (s *Server) handleLeadFollowUpPageList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.leadRecord(w, r, c, authz.ActionReadRecord)
	if !ok {
		return
	}
	limit, offset, ok := followUpPageParams(w, r)
	if !ok {
		return
	}
	items, total, err := s.St.ListFollowUpsByLead(rec.TenantID, rec.ID, limit, offset)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "follow-up list failed")
		return
	}
	if items == nil {
		items = []store.FollowUp{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
