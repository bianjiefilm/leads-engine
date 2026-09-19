// HUI-1691 / FEAT-0192 客户档案域存储层:
//   - contact_consents:按 (contact, source_submission_ref, source_channel) 维度的
//     授权记录。撤销是持久标记:任何同键重放只返回现状,绝不清除/改写 revoked_at;
//     重新授权必须换新来源提交引用(新键新行)。
//   - contact_followups:只追加的跟进时间线;note 全文不进日志(HTTP 层负责)。
//   - 软删除:deleted_at + 字段脱敏占位;consents/followups 行保留 = 最小审计。
//
// 每个查询都租户作用域并全参数化,沿用本包既有纪律。
package store

import (
	"database/sql"
	"errors"
)

// DeletedContactName is the masking placeholder written over the profile name
// on soft delete. 联系方式/备注/标签同刻清空;行本身保留以维持引用与最小审计。
const DeletedContactName = "[已删除联系人]"

// ---- consents -----------------------------------------------------------------

type ContactConsent struct {
	ID                  string `json:"id"`
	TenantID            string `json:"tenant_id"`
	ContactID           string `json:"contact_id"`
	TenantScope         string `json:"tenant_scope"`
	SourceSubmissionRef string `json:"source_submission_ref"`
	SourceChannel       string `json:"source_channel"`
	NoticeVersion       string `json:"notice_version"`
	Purpose             string `json:"purpose"`
	MarketingAllowed    bool   `json:"marketing_allowed"`
	// RevokedAt non-empty = 持久撤销标记,存在即不可恢复。
	RevokedAt     string `json:"revoked_at,omitempty"`
	RevokedReason string `json:"revoked_reason"`
	CreatedAt     string `json:"created_at"`
}

// Status derives the effective per-source marketing permission:
// revoked beats marketing_allowed —— 撤销后即使行内仍存有历史 granted 值,
// 有效状态也是 revoked。
func (c ContactConsent) Status() string {
	if c.RevokedAt != "" {
		return "revoked"
	}
	return "active"
}

const consentCols = `id,tenant_id,contact_id,tenant_scope,source_submission_ref,source_channel,notice_version,purpose,marketing_allowed,revoked_at,revoked_reason,created_at`

func scanConsent(sc interface{ Scan(...any) error }) (ContactConsent, error) {
	var c ContactConsent
	var mk int
	var revoked sql.NullString
	err := sc.Scan(&c.ID, &c.TenantID, &c.ContactID, &c.TenantScope, &c.SourceSubmissionRef,
		&c.SourceChannel, &c.NoticeVersion, &c.Purpose, &mk, &revoked, &c.RevokedReason, &c.CreatedAt)
	if err != nil {
		return ContactConsent{}, err
	}
	c.MarketingAllowed = mk == 1
	c.RevokedAt = revoked.String
	return c, nil
}

// ConsentUpsert is one arriving consent event for a consent key
// (contact, source_submission_ref, source_channel).
type ConsentUpsert struct {
	SourceSubmissionRef string
	SourceChannel       string
	NoticeVersion       string
	Purpose             string
	MarketingAllowed    bool
}

// Consent states returned by UpsertContactConsent.
const (
	ConsentCreated         = "created"          // new key -> new row
	ConsentUpdated         = "updated"          // live row refreshed in place
	ConsentReplayUnchanged = "replay_unchanged" // revoked row: 重放不得恢复,现状原样返回
)

// UpsertContactConsent records one consent event.
// 撤销红线:对已撤销(RevokedAt != NULL)的行,同一键的再次到达(旧事件重放)
// 只能幂等返回现状 —— 不清除、不改写 revoked_at,也不刷新告知版本等任何字段;
// 真正的新授权必须使用新的 source_submission_ref(新键)。
func (s *Store) UpsertContactConsent(tenantID, contactID string, in ConsentUpsert) (ContactConsent, string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ContactConsent{}, "", err
	}
	defer tx.Rollback()
	c, state, err := upsertConsentTx(tx, tenantID, contactID, in)
	if err != nil {
		return ContactConsent{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return ContactConsent{}, "", err
	}
	return c, state, nil
}

// upsertConsentTx is the consent upsert inside a caller-owned transaction
// (lead intake HUI-1683 records the event's consent in the SAME tx as the
// lead). Rules identical to UpsertContactConsent, incl. the persistent
// revocation marker: a replayed event on a revoked key returns the current
// row with state=replay_unchanged and never touches revoked_at.
func upsertConsentTx(tx *sql.Tx, tenantID, contactID string, in ConsentUpsert) (ContactConsent, string, error) {
	row := tx.QueryRow(
		`SELECT `+consentCols+` FROM contact_consents
		 WHERE tenant_id=? AND contact_id=? AND source_submission_ref=? AND source_channel=?`,
		tenantID, contactID, in.SourceSubmissionRef, in.SourceChannel)
	cur, err := scanConsent(row)
	if err == nil {
		if cur.RevokedAt != "" {
			// 持久撤销标记:重放只能幂等返回,零写入。
			return cur, ConsentReplayUnchanged, nil
		}
		cur.NoticeVersion = in.NoticeVersion
		cur.Purpose = in.Purpose
		cur.MarketingAllowed = in.MarketingAllowed
		if _, err := tx.Exec(
			`UPDATE contact_consents SET notice_version=?,purpose=?,marketing_allowed=? WHERE id=? AND tenant_id=?`,
			cur.NoticeVersion, cur.Purpose, boolInt(cur.MarketingAllowed), cur.ID, tenantID); err != nil {
			return ContactConsent{}, "", err
		}
		if err := tx.Commit(); err != nil {
			return ContactConsent{}, "", err
		}
		return cur, ConsentUpdated, nil
	}
	if err != sql.ErrNoRows {
		return ContactConsent{}, "", err
	}
	c := ContactConsent{
		ID: newID("cns_"), TenantID: tenantID, ContactID: contactID,
		TenantScope:         tenantID, // 写入时租户快照(审计锚)
		SourceSubmissionRef: in.SourceSubmissionRef, SourceChannel: in.SourceChannel,
		NoticeVersion: in.NoticeVersion, Purpose: in.Purpose,
		MarketingAllowed: in.MarketingAllowed,
		CreatedAt:        now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO contact_consents(`+consentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.TenantID, c.ContactID, c.TenantScope, c.SourceSubmissionRef, c.SourceChannel,
		c.NoticeVersion, c.Purpose, boolInt(c.MarketingAllowed), nil, "", c.CreatedAt); err != nil {
		return ContactConsent{}, "", err
	}
	return c, ConsentCreated, nil
}

// GetContactConsent fetches one consent row scoped to tenant+contact.
func (s *Store) GetContactConsent(tenantID, contactID, consentID string) (ContactConsent, error) {
	row := s.DB.QueryRow(
		`SELECT `+consentCols+` FROM contact_consents WHERE id=? AND tenant_id=? AND contact_id=?`,
		consentID, tenantID, contactID)
	return scanConsent(row)
}

func (s *Store) ListContactConsents(tenantID, contactID string) ([]ContactConsent, error) {
	rows, err := s.DB.Query(
		`SELECT `+consentCols+` FROM contact_consents WHERE tenant_id=? AND contact_id=? ORDER BY created_at, id`,
		tenantID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactConsent
	for rows.Next() {
		c, err := scanConsent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RevokeContactConsent sets the persistent revoked marker on ONE source's
// consent. 重复撤销幂等:revoked_at 保持首次撤销时间,原因不被覆盖。
// 其他来源的授权不受影响(独立可查、独立撤销)。
func (s *Store) RevokeContactConsent(tenantID, contactID, consentID, reason string) (ContactConsent, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ContactConsent{}, false, err
	}
	defer tx.Rollback()
	cur, err := scanConsent(tx.QueryRow(
		`SELECT `+consentCols+` FROM contact_consents WHERE id=? AND tenant_id=? AND contact_id=?`,
		consentID, tenantID, contactID))
	if err != nil {
		return ContactConsent{}, false, err
	}
	if cur.RevokedAt != "" {
		return cur, false, nil // already revoked: marker stays the original one
	}
	ts := now()
	if _, err := tx.Exec(
		`UPDATE contact_consents SET revoked_at=?,revoked_reason=? WHERE id=? AND tenant_id=? AND revoked_at IS NULL`,
		ts, reason, cur.ID, tenantID); err != nil {
		return ContactConsent{}, false, err
	}
	cur.RevokedAt, cur.RevokedReason = ts, reason
	if err := tx.Commit(); err != nil {
		return ContactConsent{}, false, err
	}
	return cur, true, nil
}

// RevokeContactMarketing is 「停止营销」: batch-revoke every still-effective
// marketing consent of the contact, across all sources. Idempotent; returns
// the number of rows newly revoked. Revocations are final —— 重放任何旧事件
// 都不会恢复(store 层无任何清除 revoked_at 的代码路径)。
func (s *Store) RevokeContactMarketing(tenantID, contactID, reason string) (int, error) {
	res, err := s.DB.Exec(
		`UPDATE contact_consents SET revoked_at=?,revoked_reason=?
		 WHERE tenant_id=? AND contact_id=? AND marketing_allowed=1 AND revoked_at IS NULL`,
		now(), reason, tenantID, contactID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ---- followups ------------------------------------------------------------------

// ContactFollowup is one append-only timeline entry. Note content never
// reaches logs or URLs (HTTP layer logs redact.Note only).
type ContactFollowup struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	ContactID string `json:"contact_id"`
	MemberID  string `json:"member_id"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreateContactFollowup(tenantID, contactID, memberID, note string) (ContactFollowup, error) {
	f := ContactFollowup{ID: newID("flw_"), TenantID: tenantID, ContactID: contactID,
		MemberID: memberID, Note: note, CreatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO contact_followups(id,tenant_id,contact_id,member_id,note,created_at) VALUES(?,?,?,?,?,?)`,
		f.ID, f.TenantID, f.ContactID, f.MemberID, f.Note, f.CreatedAt)
	return f, err
}

func (s *Store) ListContactFollowups(tenantID, contactID string) ([]ContactFollowup, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,contact_id,member_id,note,created_at
		 FROM contact_followups WHERE tenant_id=? AND contact_id=? ORDER BY created_at, id`,
		tenantID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactFollowup
	for rows.Next() {
		var f ContactFollowup
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ContactID, &f.MemberID, &f.Note, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---- soft delete ------------------------------------------------------------------

// SoftDeleteContact tombstones the profile: deleted_at is stamped and the
// identity/contact fields are masked in place (脱敏占位). The row itself is
// kept so leads/opportunities/consents/followups references and the minimal
// audit trail survive. Live lookups (GetContact etc.) filter deleted rows, so
// tombstones answer "not found" for every role from this moment on.
func (s *Store) SoftDeleteContact(id, tenantID string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Only a live row can be deleted; deleting again is ErrNoRows.
	if _, err := scanContact(tx.QueryRow(
		`SELECT `+contactCols+` FROM contacts WHERE id=? AND tenant_id=? AND deleted_at IS NULL`, id, tenantID)); err != nil {
		return err
	}
	ts := now()
	res, err := tx.Exec(
		`UPDATE contacts SET name=?,phone='',email='',notes='',tags='',deleted_at=?,updated_at=?
		 WHERE id=? AND tenant_id=? AND deleted_at IS NULL`,
		DeletedContactName, ts, ts, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("soft delete affected no rows")
	}
	// consents / followups rows are deliberately NOT touched: minimal audit.
	return tx.Commit()
}
