package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/channelix"
)

// ChannelGrantIn is an authorized connector binding for one account.
// It is not proof that a live platform credential was verified.
type ChannelGrantIn struct {
	Provider     string   `json:"provider"`
	AccountID    string   `json:"account_id"`
	AppID        string   `json:"app_id"`
	SubjectNS    string   `json:"subject_ns"`
	MessageNS    string   `json:"message_ns"`
	PostNS       string   `json:"post_ns"`
	Capabilities []string `json:"capabilities"`
}

// ChannelEventIn is one authorized comment or direct message.
type ChannelEventIn struct {
	TargetTenantID        string `json:"target_tenant_id"`
	Provider              string `json:"provider"`
	AccountID             string `json:"account_id"`
	AppID                 string `json:"app_id"`
	SubjectNS             string `json:"subject_ns"`
	MessageNS             string `json:"message_ns"`
	PostNS                string `json:"post_ns"`
	EventID               string `json:"event_id"`
	Kind                  string `json:"kind"`
	SubjectID             string `json:"subject_id"`
	Nickname              string `json:"nickname"`
	Phone                 string `json:"phone"`
	Text                  string `json:"text"`
	ExplicitIntent        bool   `json:"explicit_intent"`
	Purpose               string `json:"purpose"`
	Retracted             bool   `json:"retracted"`
	Score                 int    `json:"score"`
	PhoneMarketingConsent bool   `json:"phone_marketing_consent"`
	SMSMarketingConsent   bool   `json:"sms_marketing_consent"`
	WantReply             bool   `json:"want_reply"`
	HumanProcessed        bool   `json:"human_processed"`
}

// ChannelIngestResult is the stored decision. AutoReach and Delivered stay false.
type ChannelIngestResult struct {
	Refusal         string
	InteractionID   string
	CandidateID     string
	CandidateStatus string
	Idempotent      bool
	AutoReach       bool
	PhoneMarketing  bool
	SMSMarketing    bool
	ReplyAllowed    bool
	ReplyRefusal    string
	Delivered       bool
	Verification    string
}

// ChannelInteractionRow is one tenant-scoped interaction for the business UI.
type ChannelInteractionRow struct {
	ID               string   `json:"id"`
	Provider         string   `json:"provider"`
	AccountID        string   `json:"account_id"`
	EventID          string   `json:"event_id"`
	Kind             string   `json:"kind"`
	SubjectID        string   `json:"subject_id"`
	Nickname         string   `json:"nickname"`
	Body             string   `json:"body"`
	Purpose          string   `json:"purpose"`
	CandidateID      string   `json:"candidate_id,omitempty"`
	CandidateStatus  string   `json:"candidate_status,omitempty"`
	Path             string   `json:"path"`
	Verification     string   `json:"verification"`
	LiveProviders    []string `json:"live_providers"`
	Retracted        bool     `json:"retracted"`
	AutoReach        bool     `json:"auto_reach"`
	ReceiptPlatform  bool     `json:"receipt_platform"`
	ReceiptPersisted bool     `json:"receipt_persisted"`
	ReceiptHuman     bool     `json:"receipt_human"`
	ChannelContact   bool     `json:"channel_contact"`
	PhoneMarketing   bool     `json:"phone_marketing"`
	SMSMarketing     bool     `json:"sms_marketing"`
}

// SaveChannelGrant upserts a binding. A later save does not clear revocation.
func (s *Store) SaveChannelGrant(tenantID string, in ChannelGrantIn) error {
	if tenantID == "" || !channelToken(in.Provider, 64) || !channelToken(in.AccountID, 128) || !channelToken(in.AppID, 64) {
		return errors.New("channel grant: tenant/provider/account/app")
	}
	caps := strings.Join(in.Capabilities, ",")
	stamp := now()
	_, err := s.DB.Exec(`
		INSERT INTO channel_grants(tenant_id,provider,account_id,app_id,subject_ns,message_ns,post_ns,capabilities,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id,provider,account_id) DO UPDATE SET
			app_id=excluded.app_id, subject_ns=excluded.subject_ns, message_ns=excluded.message_ns,
			post_ns=excluded.post_ns, capabilities=excluded.capabilities, updated_at=excluded.updated_at`,
		tenantID, in.Provider, in.AccountID, in.AppID, in.SubjectNS, in.MessageNS, in.PostNS, caps, stamp, stamp)
	return err
}

// RevokeChannelGrant marks the binding revoked. Replaying an old event cannot clear it.
func (s *Store) RevokeChannelGrant(tenantID, provider, accountID string) error {
	res, err := s.DB.Exec(`UPDATE channel_grants SET revoked_at=?, updated_at=? WHERE tenant_id=? AND provider=? AND account_id=?`,
		now(), now(), tenantID, provider, accountID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetChannelFloor records who may speak for this subject. The other bot must not reply.
func (s *Store) SetChannelFloor(tenantID, provider, accountID, subjectID, holder string) error {
	switch holder {
	case channelix.SpeakerLeads, channelix.SpeakerMatrix, channelix.SpeakerHuman:
	default:
		return errors.New("channel floor: holder")
	}
	_, err := s.DB.Exec(`
		INSERT INTO channel_reply_floor(tenant_id,provider,account_id,subject_id,holder,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(tenant_id,provider,account_id,subject_id) DO UPDATE SET holder=excluded.holder, updated_at=excluded.updated_at`,
		tenantID, provider, accountID, subjectID, holder, now())
	return err
}

// IngestChannelEvent admits one event for the caller's tenant only.
func (s *Store) IngestChannelEvent(tenantID string, in ChannelEventIn) (ChannelIngestResult, error) {
	out := ChannelIngestResult{Verification: channelix.Capability().Verification}
	if in.TargetTenantID != tenantID {
		out.Refusal = channelix.RefusalForgedTenant
		return out, nil
	}
	grant, err := s.loadChannelGrant(tenantID, in.Provider, in.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		out.Refusal = "unknown_grant"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	prior, err := s.loadChannelPrior(tenantID, in.Provider, in.AccountID, in.EventID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	var stored *channelix.Stored
	if err == nil {
		stored = &prior
	}
	floor, err := s.loadChannelFloor(tenantID, in.Provider, in.AccountID, in.SubjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	decision := channelix.Admit(channelix.Input{
		Grant: grant, Event: channelEventFrom(in), Prior: stored, Floor: floor,
		WantReply: in.WantReply, HumanProcessed: in.HumanProcessed,
	})
	out.AutoReach = false
	out.Delivered = false
	out.PhoneMarketing = decision.PhoneMarketing
	out.SMSMarketing = decision.SMSMarketing
	out.ReplyAllowed = decision.ReplyAllowed
	out.ReplyRefusal = decision.ReplyRefusal
	out.Idempotent = decision.Idempotent
	if !decision.OK {
		out.Refusal = decision.Refusal
		return out, nil
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	ixID := decision.InteractionID
	if decision.CreateInteraction {
		ixID = newID("ix_")
		_, err = tx.Exec(`
			INSERT INTO channel_interactions(
				id,tenant_id,provider,account_id,app_id,event_id,kind,subject_id,nickname,phone,body,text_sha256,
				explicit_intent,purpose,retracted,score,channel_contact,phone_marketing,sms_marketing,auto_reach,
				receipt_platform,receipt_persisted,receipt_human,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			ixID, tenantID, in.Provider, in.AccountID, in.AppID, in.EventID, in.Kind, in.SubjectID, in.Nickname, in.Phone, in.Text, decision.TextSHA256,
			boolInt(in.ExplicitIntent), in.Purpose, boolInt(decision.Retract), in.Score, boolInt(decision.ChannelContact),
			boolInt(decision.PhoneMarketing), boolInt(decision.SMSMarketing), 0,
			1, 1, boolInt(in.HumanProcessed), now(), now())
	} else if decision.Update {
		_, err = tx.Exec(`UPDATE channel_interactions SET body=?, text_sha256=?, explicit_intent=?, purpose=?, score=?, updated_at=?
			WHERE id=? AND tenant_id=?`,
			in.Text, decision.TextSHA256, boolInt(in.ExplicitIntent), in.Purpose, in.Score, now(), ixID, tenantID)
	} else if decision.Retract {
		_, err = tx.Exec(`UPDATE channel_interactions SET retracted=1, updated_at=? WHERE id=? AND tenant_id=?`, now(), ixID, tenantID)
	}
	if err != nil {
		return out, err
	}

	candID := decision.CandidateID
	status := ""
	if decision.CreateCandidate {
		candID = newID("clc_")
		status = "open"
		_, err = tx.Exec(`INSERT INTO channel_lead_candidates(id,tenant_id,interaction_id,provider,account_id,event_id,subject_id,purpose,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			candID, tenantID, ixID, in.Provider, in.AccountID, in.EventID, in.SubjectID, in.Purpose, status, now(), now())
		if err == nil {
			_, err = tx.Exec(`UPDATE channel_interactions SET candidate_id=? WHERE id=? AND tenant_id=?`, candID, ixID, tenantID)
		}
	} else if decision.WithdrawCandidate {
		status = "withdrawn"
		_, err = tx.Exec(`UPDATE channel_lead_candidates SET status='withdrawn', updated_at=? WHERE id=? AND tenant_id=? AND status='open'`,
			now(), candID, tenantID)
	} else if candID != "" {
		err = tx.QueryRow(`SELECT status FROM channel_lead_candidates WHERE id=? AND tenant_id=?`, candID, tenantID).Scan(&status)
	}
	if err != nil {
		return out, err
	}
	if decision.ReplyAllowed && floor == "" {
		_, err = tx.Exec(`INSERT INTO channel_reply_floor(tenant_id,provider,account_id,subject_id,holder,updated_at) VALUES(?,?,?,?,?,?)`,
			tenantID, in.Provider, in.AccountID, in.SubjectID, channelix.SpeakerLeads, now())
		if err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	out.InteractionID = ixID
	out.CandidateID = candID
	out.CandidateStatus = status
	return out, nil
}

// ConfirmChannelCandidate turns one open candidate into a tenant lead through
// the existing intake seam. The channel phone is not copied onto the contact,
// so a shared number cannot merge two accounts or two brands.
func (s *Store) ConfirmChannelCandidate(tenantID, candidateID, actor string) (string, error) {
	var interactionID, provider, accountID, eventID, subjectID, purpose, status, leadID, nickname string
	var retracted int
	err := s.DB.QueryRow(`
		SELECT c.interaction_id, c.provider, c.account_id, c.event_id, c.subject_id, c.purpose, c.status, COALESCE(c.lead_id,''),
		       i.nickname, i.retracted
		FROM channel_lead_candidates c
		JOIN channel_interactions i ON i.id=c.interaction_id AND i.tenant_id=c.tenant_id
		WHERE c.id=? AND c.tenant_id=?`, candidateID, tenantID).
		Scan(&interactionID, &provider, &accountID, &eventID, &subjectID, &purpose, &status, &leadID, &nickname, &retracted)
	if err != nil {
		return "", err
	}
	if status == "confirmed" && leadID != "" {
		return leadID, nil
	}
	if status != "open" || retracted != 0 {
		return "", errors.New("channel candidate is not open")
	}
	grant, err := s.loadChannelGrant(tenantID, provider, accountID)
	if err != nil {
		return "", err
	}
	if grant.Revoked {
		return "", errors.New("channel grant revoked")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	name := strings.TrimSpace(nickname)
	if name == "" {
		name = "渠道访客"
	}
	refID := newID("src_")
	sourceRef := accountID + ":" + eventID
	if _, err := tx.Exec(`INSERT INTO source_refs(id,tenant_id,source_app,source_ref,auth_scope_snapshot,created_by,created_at)
		VALUES(?,?,?,?,?,?,?)`, refID, tenantID, provider, sourceRef, "verification=unverified", actor, now()); err != nil {
		return "", err
	}
	result, err := IntakeLeadInTx(tx, IntakeInput{
		TenantID: tenantID, SourceApp: provider, SourceNS: accountID, EventID: eventID,
		Content: []byte(accountID + "/" + eventID), ContactName: name,
		BusinessCategory: "merchant_customer", SourceType: "manual", SourceRefID: refID,
		Consent: &ConsentUpsert{
			SourceSubmissionRef: eventID, SourceChannel: provider,
			NoticeVersion: "channel-unverified", Purpose: purpose, MarketingAllowed: false,
		},
	})
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE channel_lead_candidates SET status='confirmed', lead_id=?, updated_at=? WHERE id=? AND tenant_id=? AND status='open'`,
		result.LeadID, now(), candidateID, tenantID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE channel_interactions SET receipt_human=1, updated_at=? WHERE id=? AND tenant_id=?`,
		now(), interactionID, tenantID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return result.LeadID, nil
}

// ListChannelInteractions returns this tenant's interactions. Paths carry ids only.
func (s *Store) ListChannelInteractions(tenantID string) ([]ChannelInteractionRow, error) {
	rows, err := s.DB.Query(`
		SELECT i.id, i.provider, i.account_id, i.event_id, i.kind, i.subject_id, i.nickname, i.body, i.purpose,
		       COALESCE(i.candidate_id,''), COALESCE(c.status,''), i.retracted, i.auto_reach,
		       i.receipt_platform, i.receipt_persisted, i.receipt_human, i.channel_contact, i.phone_marketing, i.sms_marketing
		FROM channel_interactions i
		LEFT JOIN channel_lead_candidates c ON c.id=i.candidate_id AND c.tenant_id=i.tenant_id
		WHERE i.tenant_id=?
		ORDER BY i.created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cap := channelix.Capability()
	var out []ChannelInteractionRow
	for rows.Next() {
		var row ChannelInteractionRow
		var retracted, autoReach, platform, persisted, human, contact, phone, sms int
		if err := rows.Scan(&row.ID, &row.Provider, &row.AccountID, &row.EventID, &row.Kind, &row.SubjectID, &row.Nickname, &row.Body, &row.Purpose,
			&row.CandidateID, &row.CandidateStatus, &retracted, &autoReach, &platform, &persisted, &human, &contact, &phone, &sms); err != nil {
			return nil, err
		}
		path, err := channelix.InteractionPath(row.ID, "")
		if err != nil {
			return nil, err
		}
		row.Path = path
		row.Verification = cap.Verification
		row.LiveProviders = cap.LiveProviders
		row.Retracted = retracted == 1
		row.AutoReach = false
		_ = autoReach
		row.ReceiptPlatform = platform == 1
		row.ReceiptPersisted = persisted == 1
		row.ReceiptHuman = human == 1
		row.ChannelContact = contact == 1
		row.PhoneMarketing = phone == 1
		row.SMSMarketing = sms == 1
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) loadChannelGrant(tenantID, provider, accountID string) (channelix.Grant, error) {
	var g channelix.Grant
	var caps, revoked sql.NullString
	err := s.DB.QueryRow(`SELECT app_id, subject_ns, message_ns, post_ns, capabilities, revoked_at
		FROM channel_grants WHERE tenant_id=? AND provider=? AND account_id=?`, tenantID, provider, accountID).
		Scan(&g.AppID, &g.SubjectNS, &g.MessageNS, &g.PostNS, &caps, &revoked)
	if err != nil {
		return g, err
	}
	g.TenantID, g.Provider, g.AccountID = tenantID, provider, accountID
	if caps.Valid && caps.String != "" {
		g.Capabilities = strings.Split(caps.String, ",")
	}
	g.Revoked = revoked.Valid && revoked.String != ""
	return g, nil
}

func (s *Store) loadChannelPrior(tenantID, provider, accountID, eventID string) (channelix.Stored, error) {
	var prior channelix.Stored
	var retracted int
	err := s.DB.QueryRow(`SELECT id, COALESCE(candidate_id,''), text_sha256, retracted
		FROM channel_interactions WHERE tenant_id=? AND provider=? AND account_id=? AND event_id=?`,
		tenantID, provider, accountID, eventID).Scan(&prior.InteractionID, &prior.CandidateID, &prior.TextSHA256, &retracted)
	prior.Retracted = retracted == 1
	return prior, err
}

func (s *Store) loadChannelFloor(tenantID, provider, accountID, subjectID string) (string, error) {
	var holder string
	err := s.DB.QueryRow(`SELECT holder FROM channel_reply_floor WHERE tenant_id=? AND provider=? AND account_id=? AND subject_id=?`,
		tenantID, provider, accountID, subjectID).Scan(&holder)
	return holder, err
}

func channelEventFrom(in ChannelEventIn) channelix.Event {
	return channelix.Event{
		TargetTenantID: in.TargetTenantID, Provider: in.Provider, AccountID: in.AccountID, AppID: in.AppID,
		SubjectNS: in.SubjectNS, MessageNS: in.MessageNS, PostNS: in.PostNS,
		EventID: in.EventID, Kind: in.Kind, SubjectID: in.SubjectID, Nickname: in.Nickname, Phone: in.Phone,
		Text: in.Text, ExplicitIntent: in.ExplicitIntent, Purpose: in.Purpose, Retracted: in.Retracted, Score: in.Score,
		PhoneMarketingConsent: in.PhoneMarketingConsent, SMSMarketingConsent: in.SMSMarketingConsent,
	}
}

func channelToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}
