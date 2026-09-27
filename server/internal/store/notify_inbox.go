package store

import (
	"database/sql"
	"errors"
)

// NotifyInbox is one accepted (or revocation) Notify delivery.
// ReceiptJSON is the body returned to the sender; it must not contain contact data.
type NotifyInbox struct {
	ID             string
	TenantID       string
	SourceApp      string
	EventType      string
	SourceRef      string
	SourceVersion  int
	ProfileEventID string
	NotifyEventID  string
	DeliveryID     string
	BodySHA256     string
	LeadID         string
	ContactID      string
	ReceiptJSON    string
	OccurredAt     string
	CreatedAt      string
	UpdatedAt      string
}

func scanInbox(sc interface{ Scan(...any) error }) (NotifyInbox, error) {
	var n NotifyInbox
	var lead, contact sql.NullString
	err := sc.Scan(&n.ID, &n.TenantID, &n.SourceApp, &n.EventType, &n.SourceRef, &n.SourceVersion,
		&n.ProfileEventID, &n.NotifyEventID, &n.DeliveryID, &n.BodySHA256, &lead, &contact,
		&n.ReceiptJSON, &n.OccurredAt, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return NotifyInbox{}, err
	}
	n.LeadID, n.ContactID = lead.String, contact.String
	return n, nil
}

const inboxCols = `id,tenant_id,source_app,event_type,source_ref,source_version,profile_event_id,notify_event_id,delivery_id,body_sha256,lead_id,contact_id,receipt_json,occurred_at,created_at,updated_at`

func (s *Store) GetNotifyInboxByEvent(tenantID, sourceApp, eventID string) (NotifyInbox, error) {
	row := s.DB.QueryRow(`SELECT `+inboxCols+` FROM notify_inbox WHERE tenant_id=? AND source_app=? AND profile_event_id=?`,
		tenantID, sourceApp, eventID)
	return scanInbox(row)
}

func GetNotifyInboxByEventTx(tx *sql.Tx, tenantID, sourceApp, eventID string) (NotifyInbox, error) {
	row := tx.QueryRow(`SELECT `+inboxCols+` FROM notify_inbox WHERE tenant_id=? AND source_app=? AND profile_event_id=?`,
		tenantID, sourceApp, eventID)
	return scanInbox(row)
}

func (s *Store) GetNotifyInboxByFact(tenantID, sourceApp, eventType, sourceRef string) (NotifyInbox, error) {
	row := s.DB.QueryRow(`SELECT `+inboxCols+` FROM notify_inbox WHERE tenant_id=? AND source_app=? AND event_type=? AND source_ref=?`,
		tenantID, sourceApp, eventType, sourceRef)
	return scanInbox(row)
}

func GetNotifyInboxByFactTx(tx *sql.Tx, tenantID, sourceApp, eventType, sourceRef string) (NotifyInbox, error) {
	row := tx.QueryRow(`SELECT `+inboxCols+` FROM notify_inbox WHERE tenant_id=? AND source_app=? AND event_type=? AND source_ref=?`,
		tenantID, sourceApp, eventType, sourceRef)
	return scanInbox(row)
}

func InsertNotifyInboxTx(tx *sql.Tx, n NotifyInbox) error {
	if n.ID == "" {
		n.ID = newID("nin_")
	}
	if n.CreatedAt == "" {
		n.CreatedAt = now()
	}
	n.UpdatedAt = n.CreatedAt
	_, err := tx.Exec(`INSERT INTO notify_inbox(`+inboxCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.TenantID, n.SourceApp, n.EventType, n.SourceRef, n.SourceVersion, n.ProfileEventID,
		n.NotifyEventID, n.DeliveryID, n.BodySHA256, nullable(n.LeadID), nullable(n.ContactID),
		n.ReceiptJSON, n.OccurredAt, n.CreatedAt, n.UpdatedAt)
	return err
}

// UpdateNotifyInboxFactTx advances the one fact row to a higher source_version.
// The fact key stays; the winning event id and receipt replace the older ones.
func UpdateNotifyInboxFactTx(tx *sql.Tx, id string, n NotifyInbox) error {
	_, err := tx.Exec(`UPDATE notify_inbox SET source_version=?, profile_event_id=?, notify_event_id=?, delivery_id=?, body_sha256=?, lead_id=?, contact_id=?, receipt_json=?, occurred_at=?, updated_at=? WHERE id=?`,
		n.SourceVersion, n.ProfileEventID, n.NotifyEventID, n.DeliveryID, n.BodySHA256,
		nullable(n.LeadID), nullable(n.ContactID), n.ReceiptJSON, n.OccurredAt, now(), id)
	return err
}

// NotifyRevocationVersion returns the stored revocation version, if any.
func NotifyRevocationVersion(tx *sql.Tx, tenantID, sourceApp, sourceRef string) (int, bool, error) {
	var v int
	err := tx.QueryRow(`SELECT source_version FROM notify_revocations WHERE tenant_id=? AND source_app=? AND source_ref=?`,
		tenantID, sourceApp, sourceRef).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

func UpsertNotifyRevocationTx(tx *sql.Tx, tenantID, sourceApp, sourceRef string, version int) error {
	_, err := tx.Exec(`INSERT INTO notify_revocations(tenant_id,source_app,source_ref,source_version,created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(tenant_id,source_app,source_ref) DO UPDATE SET
		  source_version=CASE WHEN excluded.source_version>notify_revocations.source_version THEN excluded.source_version ELSE notify_revocations.source_version END`,
		tenantID, sourceApp, sourceRef, version, now())
	return err
}

// UpsertConsentsTx records consent rows inside the caller's transaction.
func UpsertConsentsTx(tx *sql.Tx, tenantID, contactID string, items []ConsentUpsert) error {
	for _, it := range items {
		if _, _, err := upsertConsentTx(tx, tenantID, contactID, it); err != nil {
			return err
		}
	}
	return nil
}

// RevokeMarketingTx marks still-effective marketing consents revoked. It does
// not clear revoked_at on any later call.
func RevokeMarketingTx(tx *sql.Tx, tenantID, contactID, reason string) error {
	_, err := tx.Exec(`UPDATE contact_consents SET revoked_at=?,revoked_reason=?
		WHERE tenant_id=? AND contact_id=? AND marketing_allowed=1 AND revoked_at IS NULL`,
		now(), reason, tenantID, contactID)
	return err
}

// RevokeConsentChannelsTx persists a revocation marker on the named channels,
// including rows that were stored with marketing_allowed=0. A later upsert of
// the same key then hits the revoked marker and cannot turn permission back on.
func RevokeConsentChannelsTx(tx *sql.Tx, tenantID, contactID string, channels []string, reason string) error {
	for _, ch := range channels {
		if _, err := tx.Exec(`UPDATE contact_consents SET revoked_at=COALESCE(revoked_at, ?), revoked_reason=CASE WHEN revoked_at IS NULL THEN ? ELSE revoked_reason END
			WHERE tenant_id=? AND contact_id=? AND source_channel=?`,
			now(), reason, tenantID, contactID, ch); err != nil {
			return err
		}
	}
	return nil
}

// ListNotifyInboxByEvent returns every inbox row for one source event id.
func (s *Store) ListNotifyInboxByEvent(sourceApp, eventID string) ([]NotifyInbox, error) {
	rows, err := s.DB.Query(`SELECT `+inboxCols+` FROM notify_inbox WHERE source_app=? AND profile_event_id=?`, sourceApp, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyInbox
	for rows.Next() {
		n, err := scanInbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func FindLeadBySourceTx(tx *sql.Tx, tenantID, sourceApp, sourceRef string) (leadID, contactID string, ok bool, err error) {
	err = tx.QueryRow(`SELECT l.id, l.contact_id FROM leads l
		JOIN source_refs r ON r.id=l.source_ref_id
		WHERE l.tenant_id=? AND r.tenant_id=? AND r.source_app=? AND r.source_ref=?
		ORDER BY l.created_at LIMIT 1`, tenantID, tenantID, sourceApp, sourceRef).Scan(&leadID, &contactID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return leadID, contactID, true, nil
}

func EnsureSourceRefTx(tx *sql.Tx, tenantID, sourceApp, sourceRef, snapshot string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM source_refs WHERE tenant_id=? AND source_app=? AND source_ref=? ORDER BY created_at LIMIT 1`,
		tenantID, sourceApp, sourceRef).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID("src_")
	_, err = tx.Exec(`INSERT INTO source_refs(id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at)
		VALUES(?,?,?,?,?,?,?)`, id, tenantID, sourceApp, sourceRef, snapshot, "notify-ingest", now())
	return id, err
}

func SetContactNotesIfEmptyTx(tx *sql.Tx, tenantID, contactID, notes string) error {
	if notes == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE contacts SET notes=? WHERE id=? AND tenant_id=? AND (notes IS NULL OR notes='')`,
		notes, contactID, tenantID)
	return err
}
