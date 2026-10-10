package store

import (
	"database/sql"
	"errors"
)

// NotifyInbox is one accepted (or revocation) Notify delivery.
// ReceiptJSON is the body returned to the sender; it must not contain contact data.
type NotifyInbox struct {
	ID                string
	TenantID          string
	SourceApp         string
	EventType         string
	SourceRef         string
	SourceVersion     int
	ProfileEventID    string
	NotifyEventID     string
	DeliveryID        string
	BodySHA256        string
	LeadID            string
	ContactID         string
	ReceiptJSON       string
	OccurredAt        string
	CreatedAt         string
	UpdatedAt         string
	SourceNS          string
	SourceTenantID    string
	MapTargetTenantID string
	MapVersion        int
	MapBasis          string
	TraceID           string
	PayloadJSON       string
}

// ErrAmbiguousNotify means stored rows for one source identity name more than one target.
var ErrAmbiguousNotify = errors.New("notify: ambiguous stored ownership")

// NotifyRevocation is the marketing-revocation marker for one source ref.
type NotifyRevocation struct {
	TenantID          string
	SourceApp         string
	SourceNS          string
	SourceRef         string
	SourceTenantID    string
	SourceVersion     int
	MapTargetTenantID string
	MapVersion        int
	MapBasis          string
}

// NotifySticky is the single stored target for a source key, if there is one.
type NotifySticky struct {
	Found     bool
	Ambiguous bool
	Target    string
	Basis     string
	Version   int
}

func scanInbox(sc interface{ Scan(...any) error }) (NotifyInbox, error) {
	var n NotifyInbox
	var lead, contact sql.NullString
	err := sc.Scan(&n.ID, &n.TenantID, &n.SourceApp, &n.EventType, &n.SourceRef, &n.SourceVersion,
		&n.ProfileEventID, &n.NotifyEventID, &n.DeliveryID, &n.BodySHA256, &lead, &contact,
		&n.ReceiptJSON, &n.OccurredAt, &n.CreatedAt, &n.UpdatedAt,
		&n.SourceNS, &n.SourceTenantID, &n.MapTargetTenantID, &n.MapVersion, &n.MapBasis,
		&n.TraceID, &n.PayloadJSON)
	if err != nil {
		return NotifyInbox{}, err
	}
	n.LeadID, n.ContactID = lead.String, contact.String
	return n, nil
}

const inboxCols = `id,tenant_id,source_app,event_type,source_ref,source_version,profile_event_id,notify_event_id,delivery_id,body_sha256,lead_id,contact_id,receipt_json,occurred_at,created_at,updated_at,source_ns,source_tenant_id,map_target_tenant_id,map_version,map_basis,trace_id,payload_json`

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
	_, err := tx.Exec(`INSERT INTO notify_inbox(`+inboxCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.TenantID, n.SourceApp, n.EventType, n.SourceRef, n.SourceVersion, n.ProfileEventID,
		n.NotifyEventID, n.DeliveryID, n.BodySHA256, nullable(n.LeadID), nullable(n.ContactID),
		n.ReceiptJSON, n.OccurredAt, n.CreatedAt, n.UpdatedAt,
		n.SourceNS, n.SourceTenantID, n.MapTargetTenantID, n.MapVersion, n.MapBasis,
		n.TraceID, n.PayloadJSON)
	return err
}

// UpdateNotifyInboxFactTx advances the one fact row to a higher source_version.
// The fact key stays; the winning event id and receipt replace the older ones.
func UpdateNotifyInboxFactTx(tx *sql.Tx, id string, n NotifyInbox) error {
	_, err := tx.Exec(`UPDATE notify_inbox SET source_version=?, profile_event_id=?, notify_event_id=?, delivery_id=?, body_sha256=?, lead_id=?, contact_id=?, receipt_json=?, occurred_at=?, updated_at=?, trace_id=?, payload_json=? WHERE id=?`,
		n.SourceVersion, n.ProfileEventID, n.NotifyEventID, n.DeliveryID, n.BodySHA256,
		nullable(n.LeadID), nullable(n.ContactID), n.ReceiptJSON, n.OccurredAt, now(),
		n.TraceID, n.PayloadJSON, id)
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

func UpsertNotifyRevocationTx(tx *sql.Tx, rev NotifyRevocation) error {
	_, err := tx.Exec(`INSERT INTO notify_revocations(
		tenant_id,source_app,source_ref,source_version,created_at,
		source_ns,source_tenant_id,map_target_tenant_id,map_version,map_basis)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id,source_app,source_ref) DO UPDATE SET
		  source_version=CASE WHEN excluded.source_version>notify_revocations.source_version THEN excluded.source_version ELSE notify_revocations.source_version END,
		  source_ns=CASE WHEN notify_revocations.source_ns='' THEN excluded.source_ns ELSE notify_revocations.source_ns END,
		  source_tenant_id=CASE WHEN notify_revocations.source_tenant_id='' THEN excluded.source_tenant_id ELSE notify_revocations.source_tenant_id END,
		  map_target_tenant_id=CASE WHEN notify_revocations.map_target_tenant_id='' THEN excluded.map_target_tenant_id ELSE notify_revocations.map_target_tenant_id END,
		  map_basis=CASE WHEN notify_revocations.map_basis='' THEN excluded.map_basis ELSE notify_revocations.map_basis END,
		  map_version=CASE WHEN notify_revocations.map_version=0 THEN excluded.map_version ELSE notify_revocations.map_version END`,
		rev.TenantID, rev.SourceApp, rev.SourceRef, rev.SourceVersion, now(),
		rev.SourceNS, rev.SourceTenantID, rev.MapTargetTenantID, rev.MapVersion, rev.MapBasis)
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

type rowQuery interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) GetNotifyInboxByTrace(app, ns, sourceTenant, traceID string, sourceVersion int) (NotifyInbox, error) {
	return getInboxByTrace(s.DB, app, ns, sourceTenant, traceID, sourceVersion)
}

func GetNotifyInboxByTraceTx(tx *sql.Tx, app, ns, sourceTenant, traceID string, sourceVersion int) (NotifyInbox, error) {
	return getInboxByTrace(tx, app, ns, sourceTenant, traceID, sourceVersion)
}

func getInboxByTrace(q rowQuery, app, ns, sourceTenant, traceID string, sourceVersion int) (NotifyInbox, error) {
	if traceID == "" {
		return NotifyInbox{}, sql.ErrNoRows
	}
	rows, err := q.Query(`SELECT `+inboxCols+` FROM notify_inbox WHERE source_app=? AND source_ns=? AND source_tenant_id=? AND trace_id=? AND source_version=?`,
		app, ns, sourceTenant, traceID, sourceVersion)
	if err != nil {
		return NotifyInbox{}, err
	}
	defer rows.Close()
	var out []NotifyInbox
	for rows.Next() {
		n, err := scanInbox(rows)
		if err != nil {
			return NotifyInbox{}, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return NotifyInbox{}, err
	}
	switch len(out) {
	case 0:
		return NotifyInbox{}, sql.ErrNoRows
	case 1:
		return out[0], nil
	default:
		return NotifyInbox{}, ErrAmbiguousNotify
	}
}

func (s *Store) GetNotifyInboxBySourceEvent(app, ns, sourceTenant, eventID string) (NotifyInbox, error) {
	return getInboxBySourceEvent(s.DB, app, ns, sourceTenant, eventID)
}

func GetNotifyInboxBySourceEventTx(tx *sql.Tx, app, ns, sourceTenant, eventID string) (NotifyInbox, error) {
	return getInboxBySourceEvent(tx, app, ns, sourceTenant, eventID)
}

func getInboxBySourceEvent(q rowQuery, app, ns, sourceTenant, eventID string) (NotifyInbox, error) {
	return legacyInboxOne(q, `source_app=? AND source_ns=? AND source_tenant_id=? AND profile_event_id=?`,
		app, ns, sourceTenant, eventID)
}

func (s *Store) GetLegacyNotifyInboxByEvent(app, eventID string) (NotifyInbox, error) {
	return legacyInboxOne(s.DB, `source_app=? AND profile_event_id=? AND map_basis=?`, app, eventID, "legacy_stored_tenant")
}

func GetLegacyNotifyInboxByEventTx(tx *sql.Tx, app, eventID string) (NotifyInbox, error) {
	return legacyInboxOne(tx, `source_app=? AND profile_event_id=? AND map_basis=?`, app, eventID, "legacy_stored_tenant")
}

func (s *Store) GetNotifyInboxBySourceFact(app, ns, sourceTenant, eventType, sourceRef string) (NotifyInbox, error) {
	return getInboxBySourceFact(s.DB, app, ns, sourceTenant, eventType, sourceRef)
}

func GetNotifyInboxBySourceFactTx(tx *sql.Tx, app, ns, sourceTenant, eventType, sourceRef string) (NotifyInbox, error) {
	return getInboxBySourceFact(tx, app, ns, sourceTenant, eventType, sourceRef)
}

func getInboxBySourceFact(q rowQuery, app, ns, sourceTenant, eventType, sourceRef string) (NotifyInbox, error) {
	return legacyInboxOne(q, `source_app=? AND source_ns=? AND source_tenant_id=? AND event_type=? AND source_ref=?`,
		app, ns, sourceTenant, eventType, sourceRef)
}

func (s *Store) GetLegacyNotifyInboxByFact(app, eventType, sourceRef string) (NotifyInbox, error) {
	return legacyInboxOne(s.DB, `source_app=? AND event_type=? AND source_ref=? AND map_basis=?`, app, eventType, sourceRef, "legacy_stored_tenant")
}

func GetLegacyNotifyInboxByFactTx(tx *sql.Tx, app, eventType, sourceRef string) (NotifyInbox, error) {
	return legacyInboxOne(tx, `source_app=? AND event_type=? AND source_ref=? AND map_basis=?`, app, eventType, sourceRef, "legacy_stored_tenant")
}

func (s *Store) NotifySourceRefOwner(app, ns, sourceTenant, sourceRef string) (NotifyInbox, error) {
	return sourceRefOwner(s.DB, app, ns, sourceTenant, sourceRef)
}

func NotifySourceRefOwnerTx(tx *sql.Tx, app, ns, sourceTenant, sourceRef string) (NotifyInbox, error) {
	return sourceRefOwner(tx, app, ns, sourceTenant, sourceRef)
}

func sourceRefOwner(q rowQuery, app, ns, sourceTenant, sourceRef string) (NotifyInbox, error) {
	return legacyInboxOne(q, `source_app=? AND source_ns=? AND source_tenant_id=? AND source_ref=?`, app, ns, sourceTenant, sourceRef)
}

func (s *Store) LegacyNotifySourceRefOwner(app, sourceRef string) (NotifyInbox, error) {
	return legacyInboxOne(s.DB, `source_app=? AND source_ref=? AND map_basis=?`, app, sourceRef, "legacy_stored_tenant")
}

func LegacyNotifySourceRefOwnerTx(tx *sql.Tx, app, sourceRef string) (NotifyInbox, error) {
	return legacyInboxOne(tx, `source_app=? AND source_ref=? AND map_basis=?`, app, sourceRef, "legacy_stored_tenant")
}

func legacyInboxOne(q rowQuery, where string, args ...any) (NotifyInbox, error) {
	rows, err := q.Query(`SELECT `+inboxCols+` FROM notify_inbox WHERE `+where, args...)
	if err != nil {
		return NotifyInbox{}, err
	}
	defer rows.Close()
	var out []NotifyInbox
	for rows.Next() {
		n, err := scanInbox(rows)
		if err != nil {
			return NotifyInbox{}, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return NotifyInbox{}, err
	}
	return oneInboxOwner(out)
}

func oneInboxOwner(rows []NotifyInbox) (NotifyInbox, error) {
	if len(rows) == 0 {
		return NotifyInbox{}, sql.ErrNoRows
	}
	seen := map[string]bool{}
	newest := rows[0]
	for _, row := range rows {
		seen[inboxTarget(row)] = true
		if row.SourceVersion > newest.SourceVersion || (row.SourceVersion == newest.SourceVersion && row.UpdatedAt > newest.UpdatedAt) {
			newest = row
		}
	}
	if len(seen) > 1 {
		return NotifyInbox{}, ErrAmbiguousNotify
	}
	return newest, nil
}

func inboxTarget(n NotifyInbox) string {
	if n.MapTargetTenantID != "" {
		return n.MapTargetTenantID
	}
	return n.TenantID
}

func (s *Store) NotifyRevocationBySource(app, ns, sourceTenant, sourceRef string) (NotifyRevocation, error) {
	return revocationBySource(s.DB, app, ns, sourceTenant, sourceRef)
}

func NotifyRevocationBySourceTx(tx *sql.Tx, app, ns, sourceTenant, sourceRef string) (NotifyRevocation, error) {
	return revocationBySource(tx, app, ns, sourceTenant, sourceRef)
}

func revocationBySource(q rowQuery, app, ns, sourceTenant, sourceRef string) (NotifyRevocation, error) {
	rows, err := q.Query(`SELECT tenant_id,source_app,source_ref,source_version,source_ns,source_tenant_id,map_target_tenant_id,map_version,map_basis
		FROM notify_revocations WHERE source_app=? AND source_ns=? AND source_tenant_id=? AND source_ref=?`,
		app, ns, sourceTenant, sourceRef)
	if err != nil {
		return NotifyRevocation{}, err
	}
	return oneRevocation(rows)
}

func (s *Store) LegacyNotifyRevocation(app, sourceRef string) (NotifyRevocation, error) {
	return legacyRevocation(s.DB, app, sourceRef)
}

func LegacyNotifyRevocationTx(tx *sql.Tx, app, sourceRef string) (NotifyRevocation, error) {
	return legacyRevocation(tx, app, sourceRef)
}

func legacyRevocation(q rowQuery, app, sourceRef string) (NotifyRevocation, error) {
	rows, err := q.Query(`SELECT tenant_id,source_app,source_ref,source_version,source_ns,source_tenant_id,map_target_tenant_id,map_version,map_basis
		FROM notify_revocations WHERE source_app=? AND source_ref=? AND map_basis=?`,
		app, sourceRef, "legacy_stored_tenant")
	if err != nil {
		return NotifyRevocation{}, err
	}
	return oneRevocation(rows)
}

func oneRevocation(rows *sql.Rows) (NotifyRevocation, error) {
	defer rows.Close()
	var out []NotifyRevocation
	for rows.Next() {
		var rev NotifyRevocation
		if err := rows.Scan(&rev.TenantID, &rev.SourceApp, &rev.SourceRef, &rev.SourceVersion,
			&rev.SourceNS, &rev.SourceTenantID, &rev.MapTargetTenantID, &rev.MapVersion, &rev.MapBasis); err != nil {
			return NotifyRevocation{}, err
		}
		out = append(out, rev)
	}
	if err := rows.Err(); err != nil {
		return NotifyRevocation{}, err
	}
	if len(out) == 0 {
		return NotifyRevocation{}, sql.ErrNoRows
	}
	seen := map[string]bool{}
	newest := out[0]
	for _, rev := range out {
		target := rev.MapTargetTenantID
		if target == "" {
			target = rev.TenantID
		}
		seen[target] = true
		if rev.SourceVersion > newest.SourceVersion {
			newest = rev
		}
	}
	if len(seen) > 1 {
		return NotifyRevocation{}, ErrAmbiguousNotify
	}
	return newest, nil
}

// DistinctNotifyTargets lists stored targets for an external tenant.
// An empty sourceApp reads every app. Callers must refuse len > 1.
func (s *Store) DistinctNotifyTargets(sourceTenant, sourceApp string) ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT target FROM (
			SELECT COALESCE(NULLIF(map_target_tenant_id, ''), tenant_id) AS target
			FROM notify_inbox
			WHERE source_tenant_id=? AND source_ns='notify' AND (?='' OR source_app=?)
			UNION
			SELECT COALESCE(NULLIF(map_target_tenant_id, ''), tenant_id) AS target
			FROM notify_revocations
			WHERE source_tenant_id=? AND source_ns='notify' AND (?='' OR source_app=?)
		) ORDER BY target`, sourceTenant, sourceApp, sourceApp, sourceTenant, sourceApp, sourceApp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, err
		}
		if target != "" {
			out = append(out, target)
		}
	}
	return out, rows.Err()
}

// StickyNotifyMap is the stored target for one source app, namespace, and external tenant.
func (s *Store) StickyNotifyMap(app, ns, sourceTenant string) (NotifySticky, error) {
	inbox, inboxErr := sourceRefRows(s.DB, `source_app=? AND source_ns=? AND source_tenant_id=?`, app, ns, sourceTenant)
	if inboxErr != nil {
		return NotifySticky{}, inboxErr
	}
	revs, revErr := revocationRows(s.DB, `source_app=? AND source_ns=? AND source_tenant_id=?`, app, ns, sourceTenant)
	if revErr != nil {
		return NotifySticky{}, revErr
	}
	seen := map[string]bool{}
	var basis string
	var version int
	var target string
	consider := func(t, b string, v int) {
		if t == "" {
			return
		}
		seen[t] = true
		if target == "" || v >= version {
			target, basis, version = t, b, v
		}
	}
	for _, row := range inbox {
		consider(inboxTarget(row), row.MapBasis, row.MapVersion)
	}
	for _, rev := range revs {
		t := rev.MapTargetTenantID
		if t == "" {
			t = rev.TenantID
		}
		consider(t, rev.MapBasis, rev.MapVersion)
	}
	if len(seen) > 1 {
		return NotifySticky{Ambiguous: true}, nil
	}
	if len(seen) == 0 {
		return NotifySticky{}, nil
	}
	if version < 1 {
		version = 1
	}
	if basis == "" {
		basis = "legacy_stored_tenant"
	}
	return NotifySticky{Found: true, Target: target, Basis: basis, Version: version}, nil
}

func sourceRefRows(q rowQuery, where string, args ...any) ([]NotifyInbox, error) {
	rows, err := q.Query(`SELECT `+inboxCols+` FROM notify_inbox WHERE `+where, args...)
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

func revocationRows(q rowQuery, where string, args ...any) ([]NotifyRevocation, error) {
	rows, err := q.Query(`SELECT tenant_id,source_app,source_ref,source_version,source_ns,source_tenant_id,map_target_tenant_id,map_version,map_basis
		FROM notify_revocations WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyRevocation
	for rows.Next() {
		var rev NotifyRevocation
		if err := rows.Scan(&rev.TenantID, &rev.SourceApp, &rev.SourceRef, &rev.SourceVersion,
			&rev.SourceNS, &rev.SourceTenantID, &rev.MapTargetTenantID, &rev.MapVersion, &rev.MapBasis); err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}
