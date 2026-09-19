// HUI-1749 受限交接台账 SQL 层(哑管道:全参数化、租户作用域、无业务判断)。
// 业务编排(指纹幂等/版本推进/撤销守卫)在 internal/handoffsender.Service。
package store

import (
	"database/sql"
	"errors"
)

// ErrNoRows is re-exported for handler mapping convenience.
var ErrNoRows = sql.ErrNoRows

// OpportunityHandoff is one persisted confirmation snapshot of a service
// draft handoff (see migration 0004 for the field discipline).
type OpportunityHandoff struct {
	ID             string  `json:"id"`
	TenantID       string  `json:"tenant_id"`
	OpportunityID  string  `json:"opportunity_id"`
	SourceVersion  int64   `json:"source_version"`
	Fingerprint    string  `json:"fingerprint"`
	HandoffID      string  `json:"handoff_id"`
	DocJSON        string  `json:"doc_json"`
	DocSHA256      string  `json:"doc_sha256"`
	SourceRevision string  `json:"source_revision"`
	PrincipalID    string  `json:"principal_id"`
	ActorIssuer    string  `json:"actor_issuer"`
	ActorSubject   string  `json:"actor_subject"`
	TargetApp      string  `json:"target_app"`
	ReturnTargetID string  `json:"return_target_id"`
	LocalStatus    string  `json:"local_status"`
	DraftRef       string  `json:"draft_ref"`
	TargetStatus   string  `json:"target_status"`
	TargetDirty    bool    `json:"target_dirty"`
	TargetRevoked  bool    `json:"target_revoked"`
	LastHTTPStatus *int64  `json:"last_http_status"`
	LastError      string  `json:"last_error"`
	ConfirmedBy    string  `json:"confirmed_by"`
	ConfirmedAt    string  `json:"confirmed_at"`
	DeliveredAt    *string `json:"delivered_at"`
	RevokedAt      *string `json:"revoked_at"`
	UpdatedAt      string  `json:"updated_at"`
}

const handoffCols = `id,tenant_id,opportunity_id,source_version,fingerprint,handoff_id,doc_json,doc_sha256,
	source_revision,principal_id,actor_issuer,actor_subject,target_app,return_target_id,
	local_status,draft_ref,target_status,target_dirty,target_revoked,last_http_status,last_error,
	confirmed_by,confirmed_at,delivered_at,revoked_at,updated_at`

func scanHandoff(sc interface{ Scan(...any) error }) (OpportunityHandoff, error) {
	var h OpportunityHandoff
	var httpStatus sql.NullInt64
	var delivered, revoked sql.NullString
	var dirty, tRevoked int
	err := sc.Scan(&h.ID, &h.TenantID, &h.OpportunityID, &h.SourceVersion, &h.Fingerprint,
		&h.HandoffID, &h.DocJSON, &h.DocSHA256, &h.SourceRevision,
		&h.PrincipalID, &h.ActorIssuer, &h.ActorSubject, &h.TargetApp, &h.ReturnTargetID,
		&h.LocalStatus, &h.DraftRef, &h.TargetStatus, &dirty, &tRevoked, &httpStatus, &h.LastError,
		&h.ConfirmedBy, &h.ConfirmedAt, &delivered, &revoked, &h.UpdatedAt)
	if err != nil {
		return OpportunityHandoff{}, err
	}
	h.TargetDirty = dirty == 1
	h.TargetRevoked = tRevoked == 1
	if httpStatus.Valid {
		v := httpStatus.Int64
		h.LastHTTPStatus = &v
	}
	if delivered.Valid {
		v := delivered.String
		h.DeliveredAt = &v
	}
	if revoked.Valid {
		v := revoked.String
		h.RevokedAt = &v
	}
	return h, nil
}

// InsertOpportunityHandoff persists a new snapshot. Uniqueness violations
// (concurrent double-confirm) surface as sql errors for the caller's
// fallback re-read.
func (s *Store) InsertOpportunityHandoff(h OpportunityHandoff) error {
	_, err := s.DB.Exec(
		`INSERT INTO opportunity_handoffs(`+handoffCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.TenantID, h.OpportunityID, h.SourceVersion, h.Fingerprint, h.HandoffID,
		h.DocJSON, h.DocSHA256, h.SourceRevision, h.PrincipalID, h.ActorIssuer, h.ActorSubject,
		h.TargetApp, h.ReturnTargetID, h.LocalStatus, h.DraftRef, h.TargetStatus,
		boolInt(h.TargetDirty), boolInt(h.TargetRevoked), nullableInt64(h.LastHTTPStatus), h.LastError,
		h.ConfirmedBy, h.ConfirmedAt, nullableStr(h.DeliveredAt), nullableStr(h.RevokedAt), h.UpdatedAt)
	return err
}

// LatestOpportunityHandoff returns the newest snapshot (max source_version)
// for one opportunity within the tenant.
func (s *Store) LatestOpportunityHandoff(opportunityID, tenantID string) (*OpportunityHandoff, error) {
	row := s.DB.QueryRow(
		`SELECT `+handoffCols+` FROM opportunity_handoffs
		 WHERE opportunity_id=? AND tenant_id=? ORDER BY source_version DESC LIMIT 1`,
		opportunityID, tenantID)
	h, err := scanHandoff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// OpportunityHandoffByHandoffID resolves one snapshot by its stable handoff id
// (tenant-scoped: another tenant's handoff id is not found).
func (s *Store) OpportunityHandoffByHandoffID(handoffID, tenantID string) (*OpportunityHandoff, error) {
	row := s.DB.QueryRow(
		`SELECT `+handoffCols+` FROM opportunity_handoffs WHERE handoff_id=? AND tenant_id=?`,
		handoffID, tenantID)
	h, err := scanHandoff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// OpportunityHandoffByFingerprint resolves the snapshot with the exact
// content fingerprint (the idempotency lookup).
func (s *Store) OpportunityHandoffByFingerprint(opportunityID, tenantID, fingerprint string) (*OpportunityHandoff, error) {
	row := s.DB.QueryRow(
		`SELECT `+handoffCols+` FROM opportunity_handoffs
		 WHERE opportunity_id=? AND tenant_id=? AND fingerprint=?`,
		opportunityID, tenantID, fingerprint)
	h, err := scanHandoff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// NextOpportunityHandoffVersion returns max(source_version)+1 (first = 1).
func (s *Store) NextOpportunityHandoffVersion(opportunityID, tenantID string) (int64, error) {
	var max sql.NullInt64
	if err := s.DB.QueryRow(
		`SELECT MAX(source_version) FROM opportunity_handoffs WHERE opportunity_id=? AND tenant_id=?`,
		opportunityID, tenantID).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 1, nil
	}
	return max.Int64 + 1, nil
}

// UpdateOpportunityHandoffDelivery records a delivery attempt outcome.
func (s *Store) UpdateOpportunityHandoffDelivery(id string, status, draftRef, targetStatus string, dirty, revoked bool, httpStatus *int64, lastErr string) error {
	var deliveredAt, revokedAt any
	if status == "delivered" {
		deliveredAt = now()
	}
	if status == "revoked" {
		revokedAt = now()
	}
	_, err := s.DB.Exec(
		`UPDATE opportunity_handoffs SET local_status=?, draft_ref=?, target_status=?, target_dirty=?,
		 target_revoked=?, last_http_status=?, last_error=?, delivered_at=COALESCE(?, delivered_at),
		 revoked_at=COALESCE(?, revoked_at), updated_at=? WHERE id=?`,
		status, draftRef, targetStatus, boolInt(dirty), boolInt(revoked),
		nullableInt64(httpStatus), lastErr, deliveredAt, revokedAt, now(), id)
	return err
}

// MarkOpportunityHandoffRevoked flips a non-revoked snapshot to revoked
// (terminal state); returns whether a row changed.
func (s *Store) MarkOpportunityHandoffRevoked(id, tenantID string) (bool, error) {
	res, err := s.DB.Exec(
		`UPDATE opportunity_handoffs SET local_status='revoked', revoked_at=?, updated_at=?
		 WHERE id=? AND tenant_id=? AND local_status != 'revoked'`,
		now(), now(), id, tenantID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListOpportunityHandoffs returns all snapshots of one opportunity, oldest
// first (audit view).
func (s *Store) ListOpportunityHandoffs(opportunityID, tenantID string) ([]OpportunityHandoff, error) {
	rows, err := s.DB.Query(
		`SELECT `+handoffCols+` FROM opportunity_handoffs
		 WHERE opportunity_id=? AND tenant_id=? ORDER BY source_version`,
		opportunityID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpportunityHandoff
	for rows.Next() {
		h, err := scanHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
