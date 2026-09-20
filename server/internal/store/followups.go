// HUI-1692 / FEAT-0193 销售跟进记录域存储层(follow_ups,additive 新表):
//   - 记录挂 contact(必填)/ lead(可空);记录级授权作用域 = 挂靠 lead(优先)
//     或 contact 的 assigned_member_id(COALESCE 单点判定,HTTP 层只消费);
//   - next_follow_up_at / completed_at 由 HTTP 层校验并规范化(UTC RFC3339 秒
//     精度)后写入,本层是全参数化的哑管道;
//   - 软删 contact 的跟进行保留(最小审计),但一切读取路径 JOIN 掉墓碑行,
//     使其对所有角色与到期面不可见。
//
// 每个查询都租户作用域并全参数化,沿用本包既有纪律。
package store

import (
	"database/sql"
)

// FollowUp is one editable sales follow-up record (HUI-1692 / FEAT-0193).
// 与 HUI-1691 的 ContactFollowup(只追加审计时间线)正交。Note 内容不入日志、
// 不入 URL(HTTP 层负责,redact.Note 长度摘要)。
type FollowUp struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	ContactID string `json:"contact_id"`
	// LeadID 非空表示该记录挂靠线索;此时记录级作用域取该 lead 的
	// assigned_member_id,否则取 contact 的。
	LeadID string `json:"lead_id,omitempty"`
	Note   string `json:"note"`
	// NextFollowUpAt is a UTC RFC3339 second-precision timestamp (already
	// normalized by the HTTP layer); empty = 无下次跟进安排。
	NextFollowUpAt string `json:"next_follow_up_at,omitempty"`
	// CompletedAt non-empty = 已完结(首次完结的服务端时间戳,重开置空)。
	// 完结记录不进「我的到期跟进」。
	CompletedAt string `json:"completed_at,omitempty"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

const followUpCols = `id,tenant_id,contact_id,lead_id,note,next_follow_up_at,completed_at,created_by,created_at,updated_at`

// followUpColsF is the same column list qualified for the JOIN queries (the
// joined contacts/leads tables carry identically named columns).
const followUpColsF = `f.id,f.tenant_id,f.contact_id,f.lead_id,f.note,f.next_follow_up_at,f.completed_at,f.created_by,f.created_at,f.updated_at`

func scanFollowUp(sc interface{ Scan(...any) error }) (FollowUp, error) {
	var f FollowUp
	var lead, next, done sql.NullString
	if err := sc.Scan(&f.ID, &f.TenantID, &f.ContactID, &lead, &f.Note, &next, &done, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return FollowUp{}, err
	}
	f.LeadID, f.NextFollowUpAt, f.CompletedAt = lead.String, next.String, done.String
	return f, nil
}

// CreateFollowUp inserts one record. contactID must reference a live contact,
// leadID (when set) a lead of that same contact, both within the tenant — the
// HTTP layer validates before calling; the FKs are the last line of defense.
func (s *Store) CreateFollowUp(tenantID, contactID, leadID, note, nextFollowUpAt, createdBy string) (FollowUp, error) {
	f := FollowUp{
		ID: newID("fup_"), TenantID: tenantID, ContactID: contactID, LeadID: leadID,
		Note: note, NextFollowUpAt: nextFollowUpAt, CreatedBy: createdBy,
	}
	f.CreatedAt, f.UpdatedAt = now(), now()
	_, err := s.DB.Exec(
		`INSERT INTO follow_ups(`+followUpCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.TenantID, f.ContactID, nullable(f.LeadID), f.Note, nullable(f.NextFollowUpAt), nil,
		f.CreatedBy, f.CreatedAt, f.UpdatedAt)
	return f, err
}

// followUpScopeJoin is the parent scope join shared by every read: the live
// contact (tombstones drop the row so soft-deleted parents 404 and never reach
// the due surface) plus the lead when attached. The effective record-level
// assignee is the lead's assignee when a lead is attached, else the contact's
// (COALESCE single point of truth for L0 authorization).
const followUpScopeJoin = `
	FROM follow_ups f
	JOIN contacts c ON c.id = f.contact_id AND c.deleted_at IS NULL
	LEFT JOIN leads l ON l.id = f.lead_id`

// GetFollowUp fetches one record WITH its scope: scopeAssignee is the members.id
// the record is reachable through (lead assignee first, else contact assignee;
// empty for an unassigned parent). A soft-deleted parent answers sql.ErrNoRows
// so tombstoned follow-ups are 404 for every role, matching GetContact
// semantics — the row itself stays for minimal audit.
func (s *Store) GetFollowUp(id, tenantID string) (FollowUp, string, error) {
	row := s.DB.QueryRow(
		`SELECT `+followUpColsF+`, COALESCE(l.assigned_member_id, c.assigned_member_id, '') AS scope_assignee`+
			followUpScopeJoin+` WHERE f.id=? AND f.tenant_id=?`, id, tenantID)
	return scanFollowUpScoped(row)
}

func scanFollowUpScoped(row *sql.Row) (FollowUp, string, error) {
	var f FollowUp
	var lead, next, done, scope sql.NullString
	err := row.Scan(&f.ID, &f.TenantID, &f.ContactID, &lead, &f.Note, &next, &done,
		&f.CreatedBy, &f.CreatedAt, &f.UpdatedAt, &scope)
	if err != nil {
		return FollowUp{}, "", err
	}
	f.LeadID, f.NextFollowUpAt, f.CompletedAt = lead.String, next.String, done.String
	return f, scope.String, nil
}

// FollowUpPatch carries optional field updates; nil leaves the field unchanged.
type FollowUpPatch struct {
	Note *string
	// NextFollowUpAt + NextSet: Set=false 不变;Set=true 且 nil = 清空;
	// Set=true 且非 nil = 写入(HTTP 层已规范化为 UTC RFC3339)。
	NextFollowUpAt *string
	NextSet        bool
}

// UpdateFollowUp edits note / next_follow_up_at only. completed_at is NOT
// patchable: 完结/重开必须走显式端点(与商机阶段同款纪律)。
func (s *Store) UpdateFollowUp(id, tenantID string, p FollowUpPatch) (FollowUp, string, error) {
	cur, scope, err := s.GetFollowUp(id, tenantID)
	if err != nil {
		return FollowUp{}, "", err
	}
	if p.Note != nil {
		cur.Note = *p.Note
	}
	if p.NextSet {
		if p.NextFollowUpAt == nil {
			cur.NextFollowUpAt = ""
		} else {
			cur.NextFollowUpAt = *p.NextFollowUpAt
		}
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE follow_ups SET note=?,next_follow_up_at=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Note, nullable(cur.NextFollowUpAt), cur.UpdatedAt, cur.ID, tenantID)
	return cur, scope, err
}

// CompleteFollowUp stamps completed_at. Idempotent and deterministic: an
// already-completed row keeps its FIRST stamp (changed=false), so replays never
// rewrite the audit value.
func (s *Store) CompleteFollowUp(id, tenantID string) (FollowUp, bool, string, error) {
	cur, scope, err := s.GetFollowUp(id, tenantID)
	if err != nil {
		return FollowUp{}, false, "", err
	}
	if cur.CompletedAt != "" {
		return cur, false, scope, nil
	}
	ts := now()
	if _, err := s.DB.Exec(
		`UPDATE follow_ups SET completed_at=?,updated_at=? WHERE id=? AND tenant_id=? AND completed_at IS NULL`,
		ts, ts, cur.ID, tenantID); err != nil {
		return FollowUp{}, false, "", err
	}
	cur.CompletedAt, cur.UpdatedAt = ts, ts
	return cur, true, scope, nil
}

// ReopenFollowUp clears completed_at. Idempotent on an open row (changed=false).
func (s *Store) ReopenFollowUp(id, tenantID string) (FollowUp, bool, string, error) {
	cur, scope, err := s.GetFollowUp(id, tenantID)
	if err != nil {
		return FollowUp{}, false, "", err
	}
	if cur.CompletedAt == "" {
		return cur, false, scope, nil
	}
	ts := now()
	if _, err := s.DB.Exec(
		`UPDATE follow_ups SET completed_at=NULL,updated_at=? WHERE id=? AND tenant_id=? AND completed_at IS NOT NULL`,
		ts, cur.ID, tenantID); err != nil {
		return FollowUp{}, false, "", err
	}
	cur.CompletedAt, cur.UpdatedAt = "", ts
	return cur, true, scope, nil
}

// ListFollowUpsByContact answers the per-contact page, newest first, with the
// unpaginated total for the pager. The parent contact is resolved (live) by the
// HTTP layer before this runs.
func (s *Store) ListFollowUpsByContact(tenantID, contactID string, limit, offset int) ([]FollowUp, int, error) {
	var total int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM follow_ups WHERE tenant_id=? AND contact_id=?`,
		tenantID, contactID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(
		`SELECT `+followUpCols+` FROM follow_ups WHERE tenant_id=? AND contact_id=?
		 ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		tenantID, contactID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := collectFollowUps(rows)
	return out, total, err
}

// ListFollowUpsByLead answers the per-lead page: only records attached to this
// lead, newest first, with the unpaginated total.
func (s *Store) ListFollowUpsByLead(tenantID, leadID string, limit, offset int) ([]FollowUp, int, error) {
	var total int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM follow_ups WHERE tenant_id=? AND lead_id=?`,
		tenantID, leadID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(
		`SELECT `+followUpCols+` FROM follow_ups WHERE tenant_id=? AND lead_id=?
		 ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		tenantID, leadID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := collectFollowUps(rows)
	return out, total, err
}

// ListDueFollowUps answers 「我的到期跟进」 (deterministic server-side query):
// records whose effective assignee (lead first, else contact) is memberID, with
// a next_follow_up_at at or before nowUTC (due AND overdue), excluding completed
// rows and rows whose parent contact was soft-deleted (scope join). Ascending
// by next_follow_up_at — earliest obligation first; id breaks ties. Both sides
// of the comparison are UTC RFC3339 second-precision strings, so lexicographic
// order equals chronological order regardless of how the caller expressed the
// original timezone.
func (s *Store) ListDueFollowUps(tenantID, memberID, nowUTC string) ([]FollowUp, error) {
	rows, err := s.DB.Query(
		`SELECT `+followUpColsF+followUpScopeJoin+`
		 WHERE f.tenant_id=? AND f.completed_at IS NULL
		   AND f.next_follow_up_at IS NOT NULL AND f.next_follow_up_at <= ?
		   AND COALESCE(l.assigned_member_id, c.assigned_member_id, '') = ?
		 ORDER BY f.next_follow_up_at ASC, f.id ASC`,
		tenantID, nowUTC, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FollowUp
	for rows.Next() {
		var f FollowUp
		var lead, next, done sql.NullString
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ContactID, &lead, &f.Note, &next, &done,
			&f.CreatedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.LeadID, f.NextFollowUpAt, f.CompletedAt = lead.String, next.String, done.String
		out = append(out, f)
	}
	return out, rows.Err()
}

func collectFollowUps(rows *sql.Rows) ([]FollowUp, error) {
	var out []FollowUp
	for rows.Next() {
		f, err := scanFollowUp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
