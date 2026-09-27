// HUI-1688 reception persistence. Queries are tenant-scoped and parameterized.
// visitor keys are stored only as HMAC. live_charge is inserted as 0.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var (
	// ErrTakeoverLost means another member already owns this epoch.
	ErrTakeoverLost = errors.New("reception: takeover lost")
	// ErrNotDeliverable means the reply's epoch or status forbids sending.
	ErrNotDeliverable = errors.New("reception: reply not deliverable")
	// ErrSessionClosed means the session is not open.
	ErrSessionClosed = errors.New("reception: session closed")
	// ErrReplyConflict means the same idempotency key carried a different body.
	ErrReplyConflict = errors.New("reception: reply idempotency conflict")
)

// ReceptionWidget is one owned H5 entry.
type ReceptionWidget struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	Channel        string `json:"channel"`
	Enabled        bool   `json:"enabled"`
	DefaultMode    string `json:"default_mode"`
	PersonaWording string `json:"persona_wording,omitempty"`
	Language       string `json:"language"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// KnowledgeSource is one FAQ row.
type KnowledgeSource struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Kind        string `json:"kind"`
	Question    string `json:"question"`
	Answer      string `json:"answer"`
	Version     int    `json:"version"`
	Enabled     bool   `json:"enabled"`
	WithdrawnAt string `json:"withdrawn_at,omitempty"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ReceptionSession is one visitor conversation.
type ReceptionSession struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	WidgetID       string `json:"widget_id"`
	VisitorKeyHash string `json:"-"`
	Mode           string `json:"mode"`
	OwnerMemberID  string `json:"owner_member_id,omitempty"`
	Epoch          int    `json:"epoch"`
	Version        int    `json:"version"`
	Status         string `json:"status"`
	PendingReason  string `json:"pending_reason,omitempty"`
	Language       string `json:"language"`
	ContactID      string `json:"contact_id,omitempty"`
	LeadID         string `json:"lead_id,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// ReceptionMessage is one visitor turn.
type ReceptionMessage struct {
	ID          string `json:"id"`
	TenantID    string `json:"-"`
	SessionID   string `json:"session_id"`
	ClientMsgID string `json:"client_msg_id"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at"`
}

// ReceptionReply is one outbound draft or send.
type ReceptionReply struct {
	ID             string `json:"id"`
	TenantID       string `json:"-"`
	SessionID      string `json:"session_id"`
	Epoch          int    `json:"epoch"`
	SessionVersion int    `json:"session_version"`
	Kind           string `json:"kind"`
	Body           string `json:"body"`
	CitationsJSON  string `json:"-"`
	GapsJSON       string `json:"-"`
	Status         string `json:"status"`
	GeneratedAt    string `json:"generated_at,omitempty"`
	ApprovedAt     string `json:"approved_at,omitempty"`
	SentAt         string `json:"sent_at,omitempty"`
	ClientMsgID    string `json:"client_msg_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ReceptionEvent is takeover, release, or interruption.
type ReceptionEvent struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Epoch     int    `json:"epoch"`
	CreatedAt string `json:"created_at"`
}

// DeskItem is the workbench row HUI-1893 can read.
type DeskItem struct {
	SessionID      string `json:"session_id"`
	Mode           string `json:"mode"`
	Epoch          int    `json:"epoch"`
	OwnerMemberID  string `json:"owner_member_id,omitempty"`
	PendingReason  string `json:"pending_reason,omitempty"`
	HumanTodo      bool   `json:"human_todo"`
	NextFollowUpAt string `json:"next_follow_up_at,omitempty"`
	Version        int    `json:"version"`
}

func scanWidget(sc interface{ Scan(...any) error }) (ReceptionWidget, error) {
	var w ReceptionWidget
	var enabled int
	if err := sc.Scan(&w.ID, &w.TenantID, &w.Channel, &enabled, &w.DefaultMode, &w.PersonaWording, &w.Language, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return ReceptionWidget{}, err
	}
	w.Enabled = enabled == 1
	return w, nil
}

const widgetCols = `id,tenant_id,channel,enabled,default_mode,persona_wording,language,created_by,created_at,updated_at`

// CreateReceptionWidget inserts an H5 entry for the tenant.
func (s *Store) CreateReceptionWidget(tenantID, mode, persona, language, createdBy string) (ReceptionWidget, error) {
	if language == "" {
		language = "zh"
	}
	w := ReceptionWidget{
		ID: newID("rcw_"), TenantID: tenantID, Channel: "h5", Enabled: true,
		DefaultMode: mode, PersonaWording: persona, Language: language, CreatedBy: createdBy,
	}
	w.CreatedAt, w.UpdatedAt = now(), now()
	_, err := s.DB.Exec(`INSERT INTO reception_widgets(`+widgetCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		w.ID, w.TenantID, w.Channel, 1, w.DefaultMode, w.PersonaWording, w.Language, w.CreatedBy, w.CreatedAt, w.UpdatedAt)
	return w, err
}

// GetReceptionWidget loads one entry. Public callers still check enabled.
func (s *Store) GetReceptionWidget(id string) (ReceptionWidget, error) {
	row := s.DB.QueryRow(`SELECT `+widgetCols+` FROM reception_widgets WHERE id=?`, id)
	return scanWidget(row)
}

const knowledgeCols = `id,tenant_id,kind,question,answer,version,enabled,withdrawn_at,created_by,created_at,updated_at`

func scanKnowledge(sc interface{ Scan(...any) error }) (KnowledgeSource, error) {
	var k KnowledgeSource
	var enabled int
	var withdrawn sql.NullString
	if err := sc.Scan(&k.ID, &k.TenantID, &k.Kind, &k.Question, &k.Answer, &k.Version, &enabled, &withdrawn, &k.CreatedBy, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return KnowledgeSource{}, err
	}
	k.Enabled = enabled == 1
	k.WithdrawnAt = withdrawn.String
	return k, nil
}

// CreateKnowledge inserts an enabled FAQ at version 1.
func (s *Store) CreateKnowledge(tenantID, question, answer, createdBy string) (KnowledgeSource, error) {
	k := KnowledgeSource{
		ID: newID("kns_"), TenantID: tenantID, Kind: "faq", Question: question, Answer: answer,
		Version: 1, Enabled: true, CreatedBy: createdBy,
	}
	k.CreatedAt, k.UpdatedAt = now(), now()
	_, err := s.DB.Exec(`INSERT INTO knowledge_sources(`+knowledgeCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		k.ID, k.TenantID, k.Kind, k.Question, k.Answer, k.Version, 1, nil, k.CreatedBy, k.CreatedAt, k.UpdatedAt)
	return k, err
}

// ListKnowledge returns every FAQ of the tenant, including withdrawn rows.
func (s *Store) ListKnowledge(tenantID string) ([]KnowledgeSource, error) {
	rows, err := s.DB.Query(`SELECT `+knowledgeCols+` FROM knowledge_sources WHERE tenant_id=? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeSource
	for rows.Next() {
		k, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetKnowledge loads one FAQ in the tenant.
func (s *Store) GetKnowledge(tenantID, id string) (KnowledgeSource, error) {
	row := s.DB.QueryRow(`SELECT `+knowledgeCols+` FROM knowledge_sources WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanKnowledge(row)
}

// PatchKnowledge updates question and answer and bumps version when either changes.
func (s *Store) PatchKnowledge(tenantID, id, question, answer string) (KnowledgeSource, error) {
	cur, err := s.GetKnowledge(tenantID, id)
	if err != nil {
		return KnowledgeSource{}, err
	}
	version := cur.Version
	if question != cur.Question || answer != cur.Answer {
		version++
	}
	ts := now()
	_, err = s.DB.Exec(`UPDATE knowledge_sources SET question=?, answer=?, version=?, updated_at=? WHERE id=? AND tenant_id=?`,
		question, answer, version, ts, id, tenantID)
	if err != nil {
		return KnowledgeSource{}, err
	}
	return s.GetKnowledge(tenantID, id)
}

// SetKnowledgeEnabled turns a FAQ on or withdraws it. Withdrawal stamps withdrawn_at.
func (s *Store) SetKnowledgeEnabled(tenantID, id string, enabled bool) (KnowledgeSource, error) {
	ts := now()
	if enabled {
		_, err := s.DB.Exec(`UPDATE knowledge_sources SET enabled=1, withdrawn_at=NULL, updated_at=? WHERE id=? AND tenant_id=?`, ts, id, tenantID)
		if err != nil {
			return KnowledgeSource{}, err
		}
	} else {
		_, err := s.DB.Exec(`UPDATE knowledge_sources SET enabled=0, withdrawn_at=?, updated_at=? WHERE id=? AND tenant_id=?`, ts, ts, id, tenantID)
		if err != nil {
			return KnowledgeSource{}, err
		}
	}
	return s.GetKnowledge(tenantID, id)
}

const sessionCols = `id,tenant_id,widget_id,visitor_key_hash,mode,owner_member_id,epoch,version,status,pending_reason,language,contact_id,lead_id,created_at,updated_at`

func scanSession(sc interface{ Scan(...any) error }) (ReceptionSession, error) {
	var sess ReceptionSession
	var owner, contact, lead sql.NullString
	if err := sc.Scan(&sess.ID, &sess.TenantID, &sess.WidgetID, &sess.VisitorKeyHash, &sess.Mode, &owner, &sess.Epoch, &sess.Version, &sess.Status, &sess.PendingReason, &sess.Language, &contact, &lead, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
		return ReceptionSession{}, err
	}
	sess.OwnerMemberID, sess.ContactID, sess.LeadID = owner.String, contact.String, lead.String
	return sess, nil
}

// OpenReceptionSession returns the open session for this visitor, or creates one.
// resumed is true when the existing open session was returned.
func (s *Store) OpenReceptionSession(tenantID, widgetID, visitorHash, mode, language string) (ReceptionSession, bool, error) {
	existing, err := s.openSessionByVisitor(widgetID, visitorHash)
	if err == nil {
		if existing.TenantID != tenantID {
			return ReceptionSession{}, false, sql.ErrNoRows
		}
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReceptionSession{}, false, err
	}
	if language == "" {
		language = "zh"
	}
	sess := ReceptionSession{
		ID: newID("rcs_"), TenantID: tenantID, WidgetID: widgetID, VisitorKeyHash: visitorHash,
		Mode: mode, Epoch: 1, Version: 1, Status: "open", Language: language,
	}
	sess.CreatedAt, sess.UpdatedAt = now(), now()
	_, err = s.DB.Exec(`INSERT INTO reception_sessions(`+sessionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.TenantID, sess.WidgetID, sess.VisitorKeyHash, sess.Mode, nil, sess.Epoch, sess.Version, sess.Status, "", sess.Language, nil, nil, sess.CreatedAt, sess.UpdatedAt)
	if isUniqueViolation(err) {
		existing, err = s.openSessionByVisitor(widgetID, visitorHash)
		return existing, true, err
	}
	return sess, false, err
}

func (s *Store) openSessionByVisitor(widgetID, visitorHash string) (ReceptionSession, error) {
	row := s.DB.QueryRow(`SELECT `+sessionCols+` FROM reception_sessions WHERE widget_id=? AND visitor_key_hash=? AND status='open'`, widgetID, visitorHash)
	return scanSession(row)
}

// GetReceptionSession loads one session in the tenant.
func (s *Store) GetReceptionSession(tenantID, id string) (ReceptionSession, error) {
	row := s.DB.QueryRow(`SELECT `+sessionCols+` FROM reception_sessions WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanSession(row)
}

// GetReceptionSessionAny loads by id only. Callers must still check the visitor hash.
func (s *Store) GetReceptionSessionAny(id string) (ReceptionSession, error) {
	row := s.DB.QueryRow(`SELECT `+sessionCols+` FROM reception_sessions WHERE id=?`, id)
	return scanSession(row)
}

func scanMessage(sc interface{ Scan(...any) error }) (ReceptionMessage, error) {
	var m ReceptionMessage
	if err := sc.Scan(&m.ID, &m.TenantID, &m.SessionID, &m.ClientMsgID, &m.Body, &m.CreatedAt); err != nil {
		return ReceptionMessage{}, err
	}
	return m, nil
}

// GetReceptionMessageByClient returns the stored visitor turn, if any.
func (s *Store) GetReceptionMessageByClient(sessionID, clientMsgID string) (ReceptionMessage, error) {
	row := s.DB.QueryRow(`SELECT id,tenant_id,session_id,client_msg_id,body,created_at FROM reception_messages WHERE session_id=? AND client_msg_id=?`, sessionID, clientMsgID)
	return scanMessage(row)
}

// InsertReceptionMessage stores one visitor turn.
func (s *Store) InsertReceptionMessage(tenantID, sessionID, clientMsgID, body string) (ReceptionMessage, error) {
	m := ReceptionMessage{ID: newID("rcm_"), TenantID: tenantID, SessionID: sessionID, ClientMsgID: clientMsgID, Body: body, CreatedAt: now()}
	_, err := s.DB.Exec(`INSERT INTO reception_messages(id,tenant_id,session_id,client_msg_id,body,created_at) VALUES(?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.SessionID, m.ClientMsgID, m.Body, m.CreatedAt)
	return m, err
}

// ListReceptionMessages returns turns in arrival order.
func (s *Store) ListReceptionMessages(tenantID, sessionID string) ([]ReceptionMessage, error) {
	rows, err := s.DB.Query(`SELECT id,tenant_id,session_id,client_msg_id,body,created_at FROM reception_messages WHERE tenant_id=? AND session_id=? ORDER BY created_at`, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceptionMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanReply(sc interface{ Scan(...any) error }) (ReceptionReply, error) {
	var rp ReceptionReply
	var gen, appr, sent sql.NullString
	if err := sc.Scan(&rp.ID, &rp.TenantID, &rp.SessionID, &rp.Epoch, &rp.SessionVersion, &rp.Kind, &rp.Body, &rp.CitationsJSON, &rp.GapsJSON, &rp.Status, &gen, &appr, &sent, &rp.ClientMsgID, &rp.CreatedAt); err != nil {
		return ReceptionReply{}, err
	}
	rp.GeneratedAt, rp.ApprovedAt, rp.SentAt = gen.String, appr.String, sent.String
	return rp, nil
}

const replyCols = `id,tenant_id,session_id,epoch,session_version,kind,body,citations_json,gaps_json,status,generated_at,approved_at,sent_at,client_msg_id,created_at`

// GetReceptionReplyByClient returns the reply for an idempotency key.
func (s *Store) GetReceptionReplyByClient(sessionID, clientMsgID string) (ReceptionReply, error) {
	row := s.DB.QueryRow(`SELECT `+replyCols+` FROM reception_replies WHERE session_id=? AND client_msg_id=?`, sessionID, clientMsgID)
	return scanReply(row)
}

// GetReceptionReply loads one reply in the tenant.
func (s *Store) GetReceptionReply(tenantID, id string) (ReceptionReply, error) {
	row := s.DB.QueryRow(`SELECT `+replyCols+` FROM reception_replies WHERE id=? AND tenant_id=?`, id, tenantID)
	return scanReply(row)
}

// InsertReceptionReply stores a generated reply. approved sets approved_at.
func (s *Store) InsertReceptionReply(rp ReceptionReply) (ReceptionReply, error) {
	if rp.ID == "" {
		rp.ID = newID("rcr_")
	}
	rp.CreatedAt = now()
	if rp.GeneratedAt == "" && rp.Kind != "" {
		rp.GeneratedAt = rp.CreatedAt
	}
	if rp.CitationsJSON == "" {
		rp.CitationsJSON = "[]"
	}
	if rp.GapsJSON == "" {
		rp.GapsJSON = "[]"
	}
	var approved any
	if rp.Status == "approved" || rp.Status == "sent" {
		if rp.ApprovedAt == "" {
			rp.ApprovedAt = rp.CreatedAt
		}
		approved = rp.ApprovedAt
	}
	_, err := s.DB.Exec(`INSERT INTO reception_replies(`+replyCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rp.ID, rp.TenantID, rp.SessionID, rp.Epoch, rp.SessionVersion, rp.Kind, rp.Body, rp.CitationsJSON, rp.GapsJSON, rp.Status,
		nullable(rp.GeneratedAt), approved, nil, rp.ClientMsgID, rp.CreatedAt)
	return rp, err
}

// ListReceptionReplies returns replies in arrival order.
func (s *Store) ListReceptionReplies(tenantID, sessionID string) ([]ReceptionReply, error) {
	rows, err := s.DB.Query(`SELECT `+replyCols+` FROM reception_replies WHERE tenant_id=? AND session_id=? ORDER BY created_at`, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceptionReply
	for rows.Next() {
		rp, err := scanReply(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, rows.Err()
}

// InsertReceptionUsage records one metering row. live_charge is always 0.
// created is false when the idempotency key already existed.
func (s *Store) InsertReceptionUsage(tenantID, replyID, key string, units int) (bool, error) {
	id := newID("rcu_")
	res, err := s.DB.Exec(`INSERT INTO reception_usage(id,tenant_id,reply_id,idempotency_key,units,live_charge,created_at) VALUES(?,?,?,?,?,0,?)
		ON CONFLICT(tenant_id, idempotency_key) DO NOTHING`,
		id, tenantID, replyID, key, units, now())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CountReceptionUsage returns rows, unit sum, and max live_charge for a key.
func (s *Store) CountReceptionUsage(tenantID, key string) (rows, units, live int, err error) {
	err = s.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(units),0), COALESCE(MAX(live_charge),0) FROM reception_usage WHERE tenant_id=? AND idempotency_key=?`, tenantID, key).Scan(&rows, &units, &live)
	return rows, units, live, err
}

// AddReceptionEvent appends a session event.
func (s *Store) AddReceptionEvent(tenantID, sessionID, typ string, epoch int) error {
	_, err := s.DB.Exec(`INSERT INTO reception_events(id,tenant_id,session_id,type,epoch,created_at) VALUES(?,?,?,?,?,?)`,
		newID("rce_"), tenantID, sessionID, typ, epoch, now())
	return err
}

// ListReceptionEvents returns events in order.
func (s *Store) ListReceptionEvents(tenantID, sessionID string) ([]ReceptionEvent, error) {
	rows, err := s.DB.Query(`SELECT id,type,epoch,created_at FROM reception_events WHERE tenant_id=? AND session_id=? ORDER BY created_at`, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceptionEvent
	for rows.Next() {
		var ev ReceptionEvent
		if err := rows.Scan(&ev.ID, &ev.Type, &ev.Epoch, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// TouchReceptionSession sets pending reason and bumps version.
func (s *Store) TouchReceptionSession(tenantID, id, pending string) (ReceptionSession, error) {
	_, err := s.DB.Exec(`UPDATE reception_sessions SET pending_reason=?, version=version+1, updated_at=? WHERE id=? AND tenant_id=?`,
		pending, now(), id, tenantID)
	if err != nil {
		return ReceptionSession{}, err
	}
	return s.GetReceptionSession(tenantID, id)
}

func (s *Store) supersedeUnsent(tx *sql.Tx, tenantID, sessionID string, epoch int) error {
	_, err := tx.Exec(`UPDATE reception_replies SET status='superseded' WHERE tenant_id=? AND session_id=? AND epoch < ? AND status IN ('generated','approved','blocked')`,
		tenantID, sessionID, epoch)
	return err
}

// TakeoverSession moves the session to human mode and bumps epoch.
// A member who already owns the current human epoch is returned unchanged.
func (s *Store) TakeoverSession(tenantID, sessionID, memberID string, expectedEpoch int) (ReceptionSession, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ReceptionSession{}, err
	}
	defer tx.Rollback()
	sess, err := scanSession(tx.QueryRow(`SELECT `+sessionCols+` FROM reception_sessions WHERE id=? AND tenant_id=?`, sessionID, tenantID))
	if err != nil {
		return ReceptionSession{}, err
	}
	if sess.Status != "open" {
		return ReceptionSession{}, ErrSessionClosed
	}
	if sess.Mode == "human" && sess.OwnerMemberID == memberID && sess.Epoch == expectedEpoch {
		return sess, nil
	}
	res, err := tx.Exec(`UPDATE reception_sessions SET mode='human', owner_member_id=?, epoch=epoch+1, version=version+1, pending_reason='human_takeover', updated_at=?
		WHERE id=? AND tenant_id=? AND epoch=? AND status='open' AND (owner_member_id IS NULL OR owner_member_id=?)`,
		memberID, now(), sessionID, tenantID, expectedEpoch, memberID)
	if err != nil {
		return ReceptionSession{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ReceptionSession{}, ErrTakeoverLost
	}
	next := expectedEpoch + 1
	if err := s.supersedeUnsent(tx, tenantID, sessionID, next); err != nil {
		return ReceptionSession{}, err
	}
	if _, err := tx.Exec(`INSERT INTO reception_events(id,tenant_id,session_id,type,epoch,created_at) VALUES(?,?,?,?,?,?)`,
		newID("rce_"), tenantID, sessionID, "takeover", next, now()); err != nil {
		return ReceptionSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReceptionSession{}, err
	}
	return s.GetReceptionSession(tenantID, sessionID)
}

// ReleaseSessionToAI returns the session to AI mode. Only the owner member
// or a tenant owner may do it, and only by this explicit call.
func (s *Store) ReleaseSessionToAI(tenantID, sessionID, actorID, role string, expectedEpoch int) (ReceptionSession, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ReceptionSession{}, err
	}
	defer tx.Rollback()
	sess, err := scanSession(tx.QueryRow(`SELECT `+sessionCols+` FROM reception_sessions WHERE id=? AND tenant_id=?`, sessionID, tenantID))
	if err != nil {
		return ReceptionSession{}, err
	}
	if sess.Status != "open" {
		return ReceptionSession{}, ErrSessionClosed
	}
	if role != "owner" && sess.OwnerMemberID != actorID {
		return ReceptionSession{}, ErrTakeoverLost
	}
	res, err := tx.Exec(`UPDATE reception_sessions SET mode='ai', owner_member_id=NULL, epoch=epoch+1, version=version+1, pending_reason='', updated_at=?
		WHERE id=? AND tenant_id=? AND epoch=? AND status='open'`,
		now(), sessionID, tenantID, expectedEpoch)
	if err != nil {
		return ReceptionSession{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ReceptionSession{}, ErrTakeoverLost
	}
	next := expectedEpoch + 1
	if err := s.supersedeUnsent(tx, tenantID, sessionID, next); err != nil {
		return ReceptionSession{}, err
	}
	if _, err := tx.Exec(`INSERT INTO reception_events(id,tenant_id,session_id,type,epoch,created_at) VALUES(?,?,?,?,?,?)`,
		newID("rce_"), tenantID, sessionID, "release", next, now()); err != nil {
		return ReceptionSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReceptionSession{}, err
	}
	return s.GetReceptionSession(tenantID, sessionID)
}

// ApproveReceptionReply marks a current-epoch generated draft approved.
func (s *Store) ApproveReceptionReply(tenantID, sessionID, replyID string) (ReceptionReply, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ReceptionReply{}, err
	}
	defer tx.Rollback()
	rp, err := scanReply(tx.QueryRow(`SELECT `+replyCols+` FROM reception_replies WHERE id=? AND tenant_id=? AND session_id=?`, replyID, tenantID, sessionID))
	if err != nil {
		return ReceptionReply{}, err
	}
	if rp.Status == "approved" || rp.Status == "sent" {
		return rp, nil
	}
	res, err := tx.Exec(`UPDATE reception_replies SET status='approved', approved_at=?
		WHERE id=? AND tenant_id=? AND status='generated'
		AND epoch=(SELECT epoch FROM reception_sessions WHERE id=? AND tenant_id=? AND status='open' AND mode='assist')`,
		now(), replyID, tenantID, sessionID, tenantID)
	if err != nil {
		return ReceptionReply{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ReceptionReply{}, ErrNotDeliverable
	}
	if err := tx.Commit(); err != nil {
		return ReceptionReply{}, err
	}
	return s.GetReceptionReply(tenantID, replyID)
}

// SendReceptionReply emits an approved current-epoch reply once.
// already is true when the reply was already sent; a second receipt does not send again.
func (s *Store) SendReceptionReply(tenantID, sessionID, replyID, receiptID string) (ReceptionReply, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ReceptionReply{}, false, err
	}
	defer tx.Rollback()
	rp, err := scanReply(tx.QueryRow(`SELECT `+replyCols+` FROM reception_replies WHERE id=? AND tenant_id=? AND session_id=?`, replyID, tenantID, sessionID))
	if err != nil {
		return ReceptionReply{}, false, err
	}
	if rp.Status == "sent" {
		if err := insertReceipt(tx, tenantID, replyID, receiptID); err != nil {
			return ReceptionReply{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return ReceptionReply{}, false, err
		}
		return rp, true, nil
	}
	ts := now()
	res, err := tx.Exec(`UPDATE reception_replies SET status='sent', sent_at=?
		WHERE id=? AND tenant_id=? AND status='approved'
		AND epoch=(SELECT epoch FROM reception_sessions WHERE id=? AND tenant_id=? AND status='open')`,
		ts, replyID, tenantID, sessionID, tenantID)
	if err != nil {
		return ReceptionReply{}, false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		_, _ = tx.Exec(`UPDATE reception_replies SET status='blocked' WHERE id=? AND tenant_id=? AND status='approved'`, replyID, tenantID)
		_ = tx.Commit()
		return rp, false, ErrNotDeliverable
	}
	if err := insertReceipt(tx, tenantID, replyID, receiptID); err != nil {
		return ReceptionReply{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ReceptionReply{}, false, err
	}
	got, err := s.GetReceptionReply(tenantID, replyID)
	return got, false, err
}

func insertReceipt(tx *sql.Tx, tenantID, replyID, receiptID string) error {
	if strings.TrimSpace(receiptID) == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO reception_receipts(id,tenant_id,reply_id,receipt_id,created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(reply_id, receipt_id) DO NOTHING`,
		newID("rcp_"), tenantID, replyID, receiptID, now())
	return err
}

// CloseReceptionSession closes without creating a lead.
func (s *Store) CloseReceptionSession(tenantID, id string) (ReceptionSession, error) {
	_, err := s.DB.Exec(`UPDATE reception_sessions SET status='closed', version=version+1, updated_at=? WHERE id=? AND tenant_id=? AND status='open'`,
		now(), id, tenantID)
	if err != nil {
		return ReceptionSession{}, err
	}
	return s.GetReceptionSession(tenantID, id)
}

// AttachReceptionLead runs intake inside the session transaction when the
// session has no lead yet. A second call returns the already bound ids.
func (s *Store) AttachReceptionLead(sessionID, ownerMemberID string, in IntakeInput) (IntakeResult, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return IntakeResult{}, err
	}
	defer tx.Rollback()
	var leadID, contactID sql.NullString
	var st string
	if err := tx.QueryRow(`SELECT lead_id, contact_id, status FROM reception_sessions WHERE id=? AND tenant_id=?`, sessionID, in.TenantID).Scan(&leadID, &contactID, &st); err != nil {
		return IntakeResult{}, err
	}
	if st != "open" {
		return IntakeResult{}, ErrSessionClosed
	}
	if leadID.String != "" {
		return IntakeResult{LeadID: leadID.String, ContactID: contactID.String, Duplicate: true, Class: IntakeClassExactDuplicate}, nil
	}
	res, err := IntakeLeadInTx(tx, in)
	if err != nil {
		return IntakeResult{}, err
	}
	if ownerMemberID != "" {
		if _, err := tx.Exec(`UPDATE contacts SET assigned_member_id=? WHERE id=? AND tenant_id=? AND assigned_member_id IS NULL`, ownerMemberID, res.ContactID, in.TenantID); err != nil {
			return IntakeResult{}, err
		}
		if _, err := tx.Exec(`UPDATE leads SET assigned_member_id=? WHERE id=? AND tenant_id=? AND assigned_member_id IS NULL`, ownerMemberID, res.LeadID, in.TenantID); err != nil {
			return IntakeResult{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE reception_sessions SET contact_id=?, lead_id=?, version=version+1, updated_at=? WHERE id=? AND tenant_id=? AND lead_id IS NULL`,
		res.ContactID, res.LeadID, now(), sessionID, in.TenantID); err != nil {
		return IntakeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IntakeResult{}, err
	}
	return res, nil
}

// ListReceptionDesk returns open sessions the member may see, with the next follow-up.
func (s *Store) ListReceptionDesk(tenantID, memberID, role string) ([]DeskItem, error) {
	q := `SELECT s.id, s.mode, s.epoch, s.owner_member_id, s.pending_reason, s.version,
		(SELECT f.next_follow_up_at FROM follow_ups f
		   WHERE f.tenant_id=s.tenant_id AND f.contact_id=s.contact_id AND (f.completed_at IS NULL OR f.completed_at='')
		   ORDER BY f.next_follow_up_at DESC LIMIT 1)
		FROM reception_sessions s WHERE s.tenant_id=? AND s.status='open'`
	args := []any{tenantID}
	if role != "owner" {
		q += ` AND (s.owner_member_id=? OR (s.owner_member_id IS NULL AND s.pending_reason!=''))`
		args = append(args, memberID)
	}
	q += ` ORDER BY s.updated_at DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeskItem
	for rows.Next() {
		var item DeskItem
		var owner, pending, next sql.NullString
		if err := rows.Scan(&item.SessionID, &item.Mode, &item.Epoch, &owner, &pending, &item.Version, &next); err != nil {
			return nil, err
		}
		item.OwnerMemberID = owner.String
		item.PendingReason = pending.String
		item.NextFollowUpAt = next.String
		item.HumanTodo = item.Mode == "human" || item.PendingReason == "clarify_or_handoff" || item.PendingReason == "awaiting_approval" || item.PendingReason == "model_unavailable" || item.PendingReason == "human_takeover"
		out = append(out, item)
	}
	return out, rows.Err()
}

// SessionVisible reports whether a non-public member may read the session.
func SessionVisible(role, memberID string, sess ReceptionSession) bool {
	if role == "owner" {
		return true
	}
	if sess.OwnerMemberID == memberID {
		return true
	}
	return sess.OwnerMemberID == "" && sess.PendingReason != ""
}

// CitationsOf decodes a reply's citation JSON.
func CitationsOf(raw string) []any {
	var v []any
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v == nil {
		return []any{}
	}
	return v
}

// GapsOf decodes a reply's gap JSON.
func GapsOf(raw string) []any {
	return CitationsOf(raw)
}
