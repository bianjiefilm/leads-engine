// HUI-1683 / FEAT-0184 联系人合并域:候选池、显式合并、可纠错的 undo。
//
// 红线:
//   - 合并是 owner 专属的显式动作(权限在 HTTP 层用 authz.ActionMerge 裁决);
//     共享手机号等信号只产生候选(merge_candidates),本包没有任何"静默合并"路径。
//   - 字段级 provenance + 两侧合并前快照 + 重指明细 id 列表,使 undo 忠实可回放;
//     undo 仅在合并后未发生后续写时允许,且本身写入审计(undone_at/by)。
//   - consent 合并取最严格:任一侧存在 revoked(或 coarse denied)→ 合并后 coarse
//     状态 denied;marketing_allowed 取 AND。per-source consent 行随 contact 重指、
//     不归并语义:revoked_at 永不被清除/改写(1691 红线在合并后依然成立);
//     重指撞唯一键时整行 JSON 存档进审计,undo 原样回插。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MergedContactName is the masking placeholder on the merged-away (source)
// profile. 行保留以维持引用与审计;undo 依快照原样恢复。
const MergedContactName = "[已并入其他联系人]"

// Merge domain errors (HTTP 层映射 404/409)。
var (
	ErrMergeNotFound            = errors.New("contact merge: not found")
	ErrMergeAlreadyUndone       = errors.New("contact merge: already undone")
	ErrMergeHasSubsequentWrites = errors.New("contact merge: subsequent writes since merge")
	ErrMergeContactNotFound     = errors.New("contact merge: contact not found")
	ErrMergeSameContact         = errors.New("contact merge: target equals source")
	ErrMergeCategoryMismatch    = errors.New("contact merge: business category mismatch")
	ErrCandidateNotFound        = errors.New("merge candidate: not found")
)

// ContactMerge is one audited merge record.
type ContactMerge struct {
	ID                   string          `json:"id"`
	TenantID             string          `json:"tenant_id"`
	TargetContact        string          `json:"target_contact"`
	SourceContact        string          `json:"source_contact"`
	FieldProvenance      json.RawMessage `json:"field_provenance"`
	TargetBefore         json.RawMessage `json:"target_before"`
	SourceBefore         json.RawMessage `json:"source_before"`
	LeadRepointCount     int             `json:"lead_repoint_count"`
	OppRepointCount      int             `json:"opp_repoint_count"`
	ConsentRepointCount  int             `json:"consent_repoint_count"`
	FollowupRepointCount int             `json:"followup_repoint_count"`
	RepointedLeadIDs     json.RawMessage `json:"repointed_lead_ids"`
	RepointedConsentIDs  json.RawMessage `json:"repointed_consent_ids"`
	RepointedOppIDs      json.RawMessage `json:"repointed_opp_ids"`
	RepointedFollowupIDs json.RawMessage `json:"repointed_followup_ids"`
	CollapsedConsentRows json.RawMessage `json:"collapsed_consent_rows"`
	MergedBy             string          `json:"merged_by"`
	MergedAt             string          `json:"merged_at"`
	UndoneAt             string          `json:"undone_at,omitempty"`
	UndoneBy             string          `json:"undone_by,omitempty"`
}

const mergeCols = `id,tenant_id,target_contact,source_contact,field_provenance,target_before,source_before,
	lead_repoint_count,opp_repoint_count,consent_repoint_count,followup_repoint_count,
	repointed_lead_ids,repointed_consent_ids,repointed_opp_ids,repointed_followup_ids,collapsed_consent_rows,
	merged_by,merged_at,undone_at,undone_by`

func scanMerge(sc interface{ Scan(...any) error }) (ContactMerge, error) {
	var m ContactMerge
	var undone sql.NullString
	err := sc.Scan(&m.ID, &m.TenantID, &m.TargetContact, &m.SourceContact,
		&m.FieldProvenance, &m.TargetBefore, &m.SourceBefore,
		&m.LeadRepointCount, &m.OppRepointCount, &m.ConsentRepointCount, &m.FollowupRepointCount,
		&m.RepointedLeadIDs, &m.RepointedConsentIDs, &m.RepointedOppIDs, &m.RepointedFollowupIDs,
		&m.CollapsedConsentRows,
		&m.MergedBy, &m.MergedAt, &undone, &m.UndoneBy)
	if err != nil {
		return ContactMerge{}, err
	}
	m.UndoneAt = undone.String
	return m, nil
}

// MergeCandidate is one "疑似同人" proposal awaiting an explicit owner decision.
type MergeCandidate struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	ContactA   string `json:"contact_a"`
	ContactB   string `json:"contact_b"`
	Reason     string `json:"reason"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	ResolvedBy string `json:"resolved_by"`
	ResolvedAt string `json:"resolved_at,omitempty"`
}

func scanCandidate(sc interface{ Scan(...any) error }) (MergeCandidate, error) {
	var c MergeCandidate
	var resolved sql.NullString
	err := sc.Scan(&c.ID, &c.TenantID, &c.ContactA, &c.ContactB, &c.Reason, &c.Status,
		&c.CreatedAt, &c.ResolvedBy, &resolved)
	if err != nil {
		return MergeCandidate{}, err
	}
	c.ResolvedAt = resolved.String
	return c, nil
}

// canonicalPair orders a contact pair so candidate rows are unique per pair.
func canonicalPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

// upsertMergeCandidateTx idempotently records an ambiguity signal.
func upsertMergeCandidateTx(tx *sql.Tx, tenantID, c1, c2, reason string) error {
	if c1 == c2 {
		return nil
	}
	a, b := canonicalPair(c1, c2)
	_, err := tx.Exec(
		`INSERT INTO merge_candidates(id,tenant_id,contact_a,contact_b,reason,status,created_at,resolved_by,resolved_at)
		 VALUES(?,?,?,?,?,'pending',?,'',NULL)
		 ON CONFLICT(tenant_id,contact_a,contact_b,reason) DO NOTHING`,
		newID("mc_"), tenantID, a, b, reason, now())
	return err
}

// ListMergeCandidates answers the candidate pool, newest first, optionally
// filtered by status (pending|merged|dismissed).
func (s *Store) ListMergeCandidates(tenantID, status string) ([]MergeCandidate, error) {
	q := `SELECT id,tenant_id,contact_a,contact_b,reason,status,created_at,resolved_by,resolved_at
	      FROM merge_candidates WHERE tenant_id=?`
	args := []any{tenantID}
	switch status {
	case "", "pending", "merged", "dismissed":
		if status != "" {
			q += ` AND status=?`
			args = append(args, status)
		}
	default:
		return nil, errors.New("merge candidates: bad status filter")
	}
	q += ` ORDER BY created_at DESC, id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MergeCandidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DismissMergeCandidate resolves one pending candidate as dismissed (an
// explicit human "not the same person"). Merged candidates are stamped by the
// merge itself, not through this path.
func (s *Store) DismissMergeCandidate(tenantID, id, resolvedBy string) (MergeCandidate, error) {
	res, err := s.DB.Exec(
		`UPDATE merge_candidates SET status='dismissed',resolved_by=?,resolved_at=?
		 WHERE id=? AND tenant_id=? AND status='pending'`,
		resolvedBy, now(), id, tenantID)
	if err != nil {
		return MergeCandidate{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return MergeCandidate{}, ErrCandidateNotFound
	}
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,contact_a,contact_b,reason,status,created_at,resolved_by,resolved_at
		 FROM merge_candidates WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanCandidate(row)
}

// ListContactMerges answers the merge audit trail, newest first.
func (s *Store) ListContactMerges(tenantID string) ([]ContactMerge, error) {
	rows, err := s.DB.Query(`SELECT `+mergeCols+` FROM contact_merges WHERE tenant_id=? ORDER BY merged_at DESC, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactMerge
	for rows.Next() {
		m, err := scanMerge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetContactMerge fetches one merge record within the tenant.
func (s *Store) GetContactMerge(tenantID, id string) (ContactMerge, error) {
	row := s.DB.QueryRow(`SELECT `+mergeCols+` FROM contact_merges WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanMerge(row)
}

// ---- merge -----------------------------------------------------------------

// mergedField is one field-level provenance entry.
type mergedField struct {
	From string `json:"from"` // "target" | "source"
}

// strictestConsent combines the two sides' coarse consent_status with the
// per-source consent rows: any revoked row (either side) forces "denied";
// otherwise the stricter coarse value wins (denied > pending > granted).
// marketing_allowed 的 AND 语义由 coarse 映射承载:任一侧 pending 即不低于 pending。
func strictestConsent(t, s Contact, tRows, sRows []ContactConsent) string {
	rank := map[string]int{"granted": 0, "pending": 1, "denied": 2}
	level := rank[t.ConsentStatus]
	if rank[s.ConsentStatus] > level {
		level = rank[s.ConsentStatus]
	}
	for _, rows := range [][]ContactConsent{tRows, sRows} {
		for _, r := range rows {
			if r.RevokedAt != "" {
				return "denied" // 任一侧存在 revoked → 合并后 revoked(最严格)
			}
		}
	}
	for _, v := range []string{"granted", "pending", "denied"} {
		if level == rank[v] {
			return v
		}
	}
	return "denied"
}

// MergeContacts merges source INTO target inside one transaction and writes
// the full audit record. The source profile becomes a masked tombstone (its
// pre-merge snapshot lives in the audit row for undo).
func (s *Store) MergeContacts(tenantID, targetID, sourceID, mergedBy string) (ContactMerge, error) {
	if targetID == sourceID {
		return ContactMerge{}, ErrMergeSameContact
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return ContactMerge{}, err
	}
	defer tx.Rollback()

	target, err := scanContact(tx.QueryRow(
		`SELECT `+contactCols+` FROM contacts WHERE id=? AND tenant_id=? AND deleted_at IS NULL`, targetID, tenantID))
	if err != nil {
		return ContactMerge{}, ErrMergeContactNotFound
	}
	source, err := scanContact(tx.QueryRow(
		`SELECT `+contactCols+` FROM contacts WHERE id=? AND tenant_id=? AND deleted_at IS NULL`, sourceID, tenantID))
	if err != nil {
		return ContactMerge{}, ErrMergeContactNotFound
	}
	if target.BusinessCategory != source.BusinessCategory {
		// 类目是把商家经营与创意服务隔开的域边界,不因合并而打通。
		return ContactMerge{}, ErrMergeCategoryMismatch
	}

	tRows, err := listConsentsTx(tx, tenantID, target.ID)
	if err != nil {
		return ContactMerge{}, err
	}
	sRows, err := listConsentsTx(tx, tenantID, source.ID)
	if err != nil {
		return ContactMerge{}, err
	}

	ts := now()
	// ---- field-level provenance: target keeps its values, source fills gaps.
	prov := map[string]mergedField{
		"business_category": {From: "target"},
		"source_type":       {From: "target"},
	}
	merged := map[string]string{
		"name": target.Name, "phone": target.Phone, "email": target.Email,
		"notes": target.Notes, "tags": target.Tags,
	}
	for _, f := range []struct{ key, tv, sv string }{
		{"name", target.Name, source.Name},
		{"phone", target.Phone, source.Phone},
		{"email", target.Email, source.Email},
		{"notes", target.Notes, source.Notes},
		{"tags", target.Tags, source.Tags},
	} {
		if strings.TrimSpace(f.tv) == "" && strings.TrimSpace(f.sv) != "" {
			merged[f.key] = f.sv
			prov[f.key] = mergedField{From: "source"}
		} else {
			prov[f.key] = mergedField{From: "target"}
		}
	}
	coarse := strictestConsent(target, source, tRows, sRows)
	provJSON, _ := json.Marshal(map[string]any{
		"fields":         prov,
		"consent_status": map[string]any{"before": target.ConsentStatus, "after": coarse, "rule": "strictest"},
		"consent_policy": "per-source rows re-pointed, never merged; any revoked stays revoked",
	})

	// ---- repoint leads / opportunities / followups -------------------------
	leadIDs, err := repointTx(tx, tenantID, "leads", targetID, sourceID)
	if err != nil {
		return ContactMerge{}, err
	}
	oppIDs, err := repointTx(tx, tenantID, "opportunities", targetID, sourceID)
	if err != nil {
		return ContactMerge{}, err
	}
	followIDs, err := repointTx(tx, tenantID, "contact_followups", targetID, sourceID)
	if err != nil {
		return ContactMerge{}, err
	}

	// ---- consents: re-point per-source rows; collapse only on key clash ----
	var consentIDs []string
	var collapsed []map[string]any
	for _, r := range sRows {
		var tID string
		err := tx.QueryRow(
			`SELECT id FROM contact_consents
			 WHERE tenant_id=? AND contact_id=? AND source_submission_ref=? AND source_channel=?`,
			tenantID, targetID, r.SourceSubmissionRef, r.SourceChannel).Scan(&tID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(`UPDATE contact_consents SET contact_id=? WHERE id=? AND tenant_id=?`,
				targetID, r.ID, tenantID); err != nil {
				return ContactMerge{}, err
			}
			consentIDs = append(consentIDs, r.ID)
		case err != nil:
			return ContactMerge{}, err
		default:
			// 键撞车:把 target 行收敛为两侧最严格(撤销优先、marketing AND),
			// 源行整行 JSON 存档(undo 时原样回插),绝不丢失授权明细。
			tRow, err := scanConsent(tx.QueryRow(
				`SELECT `+consentCols+` FROM contact_consents WHERE id=? AND tenant_id=?`, tID, tenantID))
			if err != nil {
				return ContactMerge{}, err
			}
			revokedAt, revokedReason := tRow.RevokedAt, tRow.RevokedReason
			if tRow.RevokedAt == "" && r.RevokedAt != "" {
				revokedAt, revokedReason = r.RevokedAt, r.RevokedReason
			}
			marketing := tRow.MarketingAllowed && r.MarketingAllowed
			if _, err := tx.Exec(
				`UPDATE contact_consents SET revoked_at=?,revoked_reason=?,marketing_allowed=? WHERE id=? AND tenant_id=?`,
				nilIfEmpty(revokedAt), revokedReason, boolInt(marketing), tID, tenantID); err != nil {
				return ContactMerge{}, err
			}
			if _, err := tx.Exec(`DELETE FROM contact_consents WHERE id=? AND tenant_id=?`, r.ID, tenantID); err != nil {
				return ContactMerge{}, err
			}
			collapsed = append(collapsed, map[string]any{"source_row": r, "target_before": tRow})
		}
	}

	// ---- tombstone the source profile (row kept; snapshot enables undo) ----
	if _, err := tx.Exec(
		`UPDATE contacts SET name=?,phone='',email='',notes='',tags='',deleted_at=?,updated_at=?
		 WHERE id=? AND tenant_id=?`,
		MergedContactName, ts, ts, sourceID, tenantID); err != nil {
		return ContactMerge{}, err
	}
	// ---- write the merged profile onto the target ---------------------------
	if _, err := tx.Exec(
		`UPDATE contacts SET name=?,phone=?,email=?,notes=?,tags=?,consent_status=?,updated_at=?
		 WHERE id=? AND tenant_id=? AND deleted_at IS NULL`,
		merged["name"], merged["phone"], merged["email"], merged["notes"], merged["tags"], coarse, ts,
		targetID, tenantID); err != nil {
		return ContactMerge{}, err
	}

	// ---- resolve pending candidates for this pair ---------------------------
	if _, err := tx.Exec(
		`UPDATE merge_candidates SET status='merged',resolved_by=?,resolved_at=?
		 WHERE tenant_id=? AND status='pending'
		   AND ((contact_a=? AND contact_b=?) OR (contact_a=? AND contact_b=?))`,
		mergedBy, ts, tenantID, targetID, sourceID, sourceID, targetID); err != nil {
		return ContactMerge{}, err
	}

	tBefore, _ := json.Marshal(target)
	sBefore, _ := json.Marshal(source)
	m := ContactMerge{
		ID: newID("mrg_"), TenantID: tenantID,
		TargetContact: targetID, SourceContact: sourceID,
		FieldProvenance:      provJSON,
		TargetBefore:         tBefore,
		SourceBefore:         sBefore,
		LeadRepointCount:     len(leadIDs),
		OppRepointCount:      len(oppIDs),
		ConsentRepointCount:  len(consentIDs),
		FollowupRepointCount: len(followIDs),
		RepointedLeadIDs:     mustJSON(leadIDs),
		RepointedConsentIDs:  mustJSON(consentIDs),
		RepointedOppIDs:      mustJSON(oppIDs),
		RepointedFollowupIDs: mustJSON(followIDs),
		CollapsedConsentRows: mustJSON(collapsed),
		MergedBy:             mergedBy,
		MergedAt:             ts,
	}
	if _, err := tx.Exec(
		`INSERT INTO contact_merges(`+mergeCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.TargetContact, m.SourceContact, m.FieldProvenance, m.TargetBefore, m.SourceBefore,
		m.LeadRepointCount, m.OppRepointCount, m.ConsentRepointCount, m.FollowupRepointCount,
		m.RepointedLeadIDs, m.RepointedConsentIDs, m.RepointedOppIDs, m.RepointedFollowupIDs, m.CollapsedConsentRows,
		m.MergedBy, m.MergedAt, nil, ""); err != nil {
		return ContactMerge{}, err
	}
	if err := tx.Commit(); err != nil {
		return ContactMerge{}, err
	}
	return m, nil
}

// repointTx moves every row of table referencing source's contact to target
// within the tenant and returns the repointed row ids (undo replays exactly
// these). table comes only from the fixed call sites above.
func repointTx(tx *sql.Tx, tenantID, table, targetID, sourceID string) ([]string, error) {
	rows, err := tx.Query(`SELECT id FROM `+table+` WHERE tenant_id=? AND contact_id=?`, tenantID, sourceID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE `+table+` SET contact_id=? WHERE id=? AND tenant_id=?`, targetID, id, tenantID); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func listConsentsTx(tx *sql.Tx, tenantID, contactID string) ([]ContactConsent, error) {
	rows, err := tx.Query(
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

// ---- undo ------------------------------------------------------------------

// UndoContactMerge reverts one merge, restoring both profiles, the repointed
// leads/opportunities/followups/consents and the collapsed consent rows, then
// stamps the undo into the audit record.
// 仅当合并后未发生后续写时允许:target 行未被修改、target 名下没有新增的
// lead/opp/followup/consent、也没有更晚的合并触及任一侧。
func (s *Store) UndoContactMerge(tenantID, mergeID, undoneBy string) (ContactMerge, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ContactMerge{}, err
	}
	defer tx.Rollback()
	m, err := scanMerge(tx.QueryRow(
		`SELECT `+mergeCols+` FROM contact_merges WHERE id=? AND tenant_id=?`, mergeID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContactMerge{}, ErrMergeNotFound
	}
	if err != nil {
		return ContactMerge{}, err
	}
	if m.UndoneAt != "" {
		return ContactMerge{}, ErrMergeAlreadyUndone
	}
	mergedAt, err := time.Parse(time.RFC3339Nano, m.MergedAt)
	if err != nil {
		return ContactMerge{}, ErrMergeHasSubsequentWrites
	}

	// ---- subsequent-write guards -------------------------------------------
	var tUpd string
	if err := tx.QueryRow(`SELECT updated_at FROM contacts WHERE id=? AND tenant_id=?`, m.TargetContact, tenantID).
		Scan(&tUpd); err != nil {
		return ContactMerge{}, ErrMergeContactNotFound
	}
	if after(tUpd, mergedAt) {
		return ContactMerge{}, ErrMergeHasSubsequentWrites
	}
	for _, table := range []string{"leads", "opportunities", "contact_followups", "contact_consents"} {
		fresh, err := countCreatedAfterTx(tx, table, tenantID, m.TargetContact, mergedAt)
		if err != nil {
			return ContactMerge{}, err
		}
		if fresh > 0 {
			return ContactMerge{}, ErrMergeHasSubsequentWrites
		}
	}
	// 更晚的合并(字符串时间不可靠,取出后在 Go 里按解析时间比较)。
	otherRows, err := tx.Query(
		`SELECT id, merged_at FROM contact_merges
		 WHERE tenant_id=? AND id!=?
		   AND (target_contact IN (?,?) OR source_contact IN (?,?))`,
		tenantID, m.ID, m.TargetContact, m.SourceContact, m.TargetContact, m.SourceContact)
	if err != nil {
		return ContactMerge{}, err
	}
	later := false
	for otherRows.Next() {
		var id, mats string
		if err := otherRows.Scan(&id, &mats); err != nil {
			otherRows.Close()
			return ContactMerge{}, err
		}
		if after(mats, mergedAt) {
			later = true
		}
	}
	if err := otherRows.Err(); err != nil {
		otherRows.Close()
		return ContactMerge{}, err
	}
	otherRows.Close()
	if later {
		return ContactMerge{}, ErrMergeHasSubsequentWrites
	}

	// ---- restore -------------------------------------------------------------
	ts := now()
	var tBefore, sBefore Contact
	if err := json.Unmarshal(m.TargetBefore, &tBefore); err != nil {
		return ContactMerge{}, err
	}
	if err := json.Unmarshal(m.SourceBefore, &sBefore); err != nil {
		return ContactMerge{}, err
	}
	// source: un-tombstone and restore identity/content fields from snapshot.
	if _, err := tx.Exec(
		`UPDATE contacts SET name=?,phone=?,email=?,notes=?,tags=?,consent_status=?,deleted_at=NULL,updated_at=?
		 WHERE id=? AND tenant_id=?`,
		sBefore.Name, sBefore.Phone, sBefore.Email, sBefore.Notes, sBefore.Tags, sBefore.ConsentStatus, ts,
		m.SourceContact, tenantID); err != nil {
		return ContactMerge{}, err
	}
	// target: restore pre-merge content fields.
	if _, err := tx.Exec(
		`UPDATE contacts SET name=?,phone=?,email=?,notes=?,tags=?,consent_status=?,updated_at=?
		 WHERE id=? AND tenant_id=?`,
		tBefore.Name, tBefore.Phone, tBefore.Email, tBefore.Notes, tBefore.Tags, tBefore.ConsentStatus, ts,
		m.TargetContact, tenantID); err != nil {
		return ContactMerge{}, err
	}
	var ids []string
	if err := json.Unmarshal(m.RepointedLeadIDs, &ids); err != nil {
		return ContactMerge{}, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE leads SET contact_id=? WHERE id=? AND tenant_id=?`, m.SourceContact, id, tenantID); err != nil {
			return ContactMerge{}, err
		}
	}
	if err := json.Unmarshal(m.RepointedOppIDs, &ids); err != nil {
		return ContactMerge{}, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE opportunities SET contact_id=? WHERE id=? AND tenant_id=?`, m.SourceContact, id, tenantID); err != nil {
			return ContactMerge{}, err
		}
	}
	if err := json.Unmarshal(m.RepointedFollowupIDs, &ids); err != nil {
		return ContactMerge{}, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE contact_followups SET contact_id=? WHERE id=? AND tenant_id=?`, m.SourceContact, id, tenantID); err != nil {
			return ContactMerge{}, err
		}
	}
	if err := json.Unmarshal(m.RepointedConsentIDs, &ids); err != nil {
		return ContactMerge{}, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE contact_consents SET contact_id=? WHERE id=? AND tenant_id=?`, m.SourceContact, id, tenantID); err != nil {
			return ContactMerge{}, err
		}
	}
	// collapsed rows: re-insert the archived source rows and restore the
	// target's row to its pre-merge values (撤销标记永不被清除:恢复的是合并前
	// 的原值,即该侧原本的 revoked_at 原样回来)。
	var collapsed []map[string]any
	if err := json.Unmarshal(m.CollapsedConsentRows, &collapsed); err != nil {
		return ContactMerge{}, err
	}
	for _, pair := range collapsed {
		sr, err := consentFromMap(pair["source_row"])
		if err != nil {
			return ContactMerge{}, err
		}
		tb, err := consentFromMap(pair["target_before"])
		if err != nil {
			return ContactMerge{}, err
		}
		if _, err := tx.Exec(
			`INSERT INTO contact_consents(`+consentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(contact_id, source_submission_ref, source_channel) DO NOTHING`,
			sr.ID, sr.TenantID, sr.ContactID, sr.TenantScope, sr.SourceSubmissionRef, sr.SourceChannel,
			sr.NoticeVersion, sr.Purpose, boolInt(sr.MarketingAllowed), nilIfEmpty(sr.RevokedAt), sr.RevokedReason, sr.CreatedAt); err != nil {
			return ContactMerge{}, err
		}
		if _, err := tx.Exec(
			`UPDATE contact_consents SET notice_version=?,purpose=?,marketing_allowed=?,revoked_at=?,revoked_reason=?
			 WHERE id=? AND tenant_id=?`,
			tb.NoticeVersion, tb.Purpose, boolInt(tb.MarketingAllowed), nilIfEmpty(tb.RevokedAt), tb.RevokedReason,
			tb.ID, tenantID); err != nil {
			return ContactMerge{}, err
		}
	}
	// candidates resolved by THIS merge go back to pending.
	if _, err := tx.Exec(
		`UPDATE merge_candidates SET status='pending',resolved_by='',resolved_at=NULL
		 WHERE tenant_id=? AND status='merged' AND resolved_at=?
		   AND ((contact_a=? AND contact_b=?) OR (contact_a=? AND contact_b=?))`,
		tenantID, m.MergedAt, m.TargetContact, m.SourceContact, m.SourceContact, m.TargetContact); err != nil {
		return ContactMerge{}, err
	}
	// ---- stamp the undo into the audit record --------------------------------
	if _, err := tx.Exec(`UPDATE contact_merges SET undone_at=?,undone_by=? WHERE id=? AND tenant_id=?`,
		ts, undoneBy, m.ID, tenantID); err != nil {
		return ContactMerge{}, err
	}
	if err := tx.Commit(); err != nil {
		return ContactMerge{}, err
	}
	return s.GetContactMerge(tenantID, m.ID)
}

// ---- stats ------------------------------------------------------------------

// DedupStats is the tenant's dedup/merge summary. 注意:exact_duplicate 的账本
// 计数恒为 0 —— 重放零写入,不产生新账本行(重复投递的总次数不在账本语义内)。
type DedupStats struct {
	IntakeTotal       int            `json:"intake_total"`
	ByClass           map[string]int `json:"by_class"`
	PendingCandidates int            `json:"pending_candidates"`
	MergesTotal       int            `json:"merges_total"`
	MergesUndone      int            `json:"merges_undone"`
}

func (s *Store) DedupStats(tenantID string) (DedupStats, error) {
	st := DedupStats{ByClass: map[string]int{}}
	rows, err := s.DB.Query(
		`SELECT class, COUNT(1) FROM lead_intake_events WHERE tenant_id=? GROUP BY class`, tenantID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var class string
		var n int
		if err := rows.Scan(&class, &n); err != nil {
			return st, err
		}
		st.ByClass[class] = n
		st.IntakeTotal += n
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM merge_candidates WHERE tenant_id=? AND status='pending'`, tenantID).Scan(&st.PendingCandidates); err != nil {
		return st, err
	}
	if err := s.DB.QueryRow(
		`SELECT COUNT(1), COALESCE(SUM(CASE WHEN undone_at IS NOT NULL THEN 1 ELSE 0 END),0)
		 FROM contact_merges WHERE tenant_id=?`, tenantID).Scan(&st.MergesTotal, &st.MergesUndone); err != nil {
		return st, err
	}
	return st, nil
}

// ---- small helpers -----------------------------------------------------------

// after reports whether tsStr parses to a time strictly after ref. Unparseable
// timestamps are treated as older (every repo timestamp comes from now()).
func after(tsStr string, ref time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		return false
	}
	return t.After(ref)
}

// countCreatedAfterTx counts rows of table for a contact created after ref.
func countCreatedAfterTx(tx *sql.Tx, table, tenantID, contactID string, ref time.Time) (int, error) {
	rows, err := tx.Query(`SELECT created_at FROM `+table+` WHERE tenant_id=? AND contact_id=?`, tenantID, contactID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return 0, err
		}
		if after(ts, ref) {
			n++
		}
	}
	return n, rows.Err()
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// consentFromMap rebuilds a consent row from its archived JSON form.
func consentFromMap(v any) (ContactConsent, error) {
	var c ContactConsent
	raw, err := json.Marshal(v)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(raw, &c)
}
