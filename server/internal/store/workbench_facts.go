package store

import (
	"database/sql"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/workbench"
)

// DeskFacts is the tenant-scoped evidence the sales desk classifies.
// Plaintext phones stay on timeline events and never on the queue rows.
type DeskFacts struct {
	Leads  []workbench.LeadView
	Opps   []workbench.OpportunityView
	Desk   []workbench.ReceptionView
	Events map[string][]workbench.TimelineEvent
}

type followSnap struct {
	contactID string
	leadID    string
	note      string
	next      string
	done      string
	createdAt string
}

// LoadDeskFacts reads leads, opportunities, follow-ups, consents, provenance
// and reception rows for one tenant. It does not charge or send anything.
func (s *Store) LoadDeskFacts(tenantID string) (DeskFacts, error) {
	leads, contacts, err := s.loadDeskLeads(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	follows, err := s.loadDeskFollowUps(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	consents, err := s.loadDeskConsents(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	forms, err := s.loadDeskForms(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	submitted, err := s.loadDeskSubmitted(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	opps, err := s.loadDeskOpps(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	hist, err := s.loadDeskStageHistory(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}
	desk, err := s.loadDeskReception(tenantID)
	if err != nil {
		return DeskFacts{}, err
	}

	openNext := map[string]string{}
	completed := map[string]bool{}
	for _, f := range follows {
		if f.done == "" && f.next != "" {
			if cur, ok := openNext[f.contactID]; !ok || f.next < cur {
				openNext[f.contactID] = f.next
			}
		}
		if f.done != "" {
			completed[f.contactID] = true
		}
	}
	for i := range leads {
		lead := &leads[i]
		contactID := lead.ContactID
		if next, ok := openNext[contactID]; ok {
			lead.HasOpenFollowUp = true
			lead.ManualNextAt = next
		}
		lead.HasCompletedFollowUp = completed[contactID]
		applyConsentFacts(lead, consents[contactID])
		if form, ok := forms[lead.ID]; ok {
			lead.SourceForm = form.key
			lead.SourceAt = form.at
			lead.SubmittedAt = form.at
		}
		if at, ok := submitted[lead.ID]; ok && lead.SubmittedAt == "" {
			lead.SubmittedAt = at
		}
		if lead.SourceAt == "" {
			lead.SourceAt = lead.CreatedAt
		}
		if lead.SourceChannel == "" {
			lead.SourceChannel = sourceChannelLabel(contacts[contactID].sourceType)
		}
	}
	for i := range opps {
		if _, ok := openNext[opps[i].ContactID]; ok {
			opps[i].HasManualNext = true
		}
	}
	for i := range desk {
		if desk[i].LeadID == "" {
			continue
		}
		for _, lead := range leads {
			if lead.ID == desk[i].LeadID && lead.Purpose != "" {
				desk[i].Purpose = lead.Purpose
			}
		}
	}
	events := map[string][]workbench.TimelineEvent{}
	for _, lead := range leads {
		contact := contacts[lead.ContactID]
		var ev []workbench.TimelineEvent
		ev = append(ev, workbench.TimelineEvent{
			At: lead.SourceAt, Kind: "source",
			Summary:          sourceSummary(lead),
			ContactPlaintext: contact.phone,
		})
		if lead.Assignee != "" {
			at := lead.UpdatedAt
			if at == "" {
				at = lead.CreatedAt
			}
			ev = append(ev, workbench.TimelineEvent{At: at, Kind: "assignment", Summary: "已分配"})
		}
		for _, f := range follows {
			if f.contactID != lead.ContactID {
				continue
			}
			ev = append(ev, workbench.TimelineEvent{At: f.createdAt, Kind: "follow_up", Summary: f.note})
		}
		for _, row := range hist[lead.ContactID] {
			ev = append(ev, workbench.TimelineEvent{At: row.at, Kind: "opportunity", Summary: row.summary})
		}
		for _, row := range consents[lead.ContactID] {
			summary := "授权 " + row.channel
			at := row.createdAt
			if row.revokedAt != "" {
				summary = "撤销授权 " + row.channel
				at = row.revokedAt
			}
			ev = append(ev, workbench.TimelineEvent{At: at, Kind: "consent", Summary: summary})
		}
		events[lead.ID] = ev
	}
	return DeskFacts{Leads: leads, Opps: opps, Desk: desk, Events: events}, nil
}

type contactSnap struct {
	phone      string
	sourceType string
	consent    string
}

type formSnap struct {
	key string
	at  string
}

type consentSnap struct {
	channel   string
	purpose   string
	marketing bool
	revokedAt string
	createdAt string
}

type histSnap struct {
	at      string
	summary string
}

func (s *Store) loadDeskLeads(tenantID string) ([]workbench.LeadView, map[string]contactSnap, error) {
	rows, err := s.DB.Query(`
		SELECT l.id, l.contact_id, l.status, l.filter_reason, COALESCE(l.assigned_member_id,''),
		       l.created_at, l.updated_at,
		       c.phone, c.source_type, c.consent_status,
		       COALESCE(sr.source_app,''), COALESCE(sr.source_ref,''), COALESCE(sr.created_at,'')
		FROM leads l
		JOIN contacts c ON c.id = l.contact_id AND c.tenant_id = l.tenant_id AND c.deleted_at IS NULL
		LEFT JOIN source_refs sr ON sr.id = l.source_ref_id AND sr.tenant_id = l.tenant_id
		WHERE l.tenant_id=?
		ORDER BY l.created_at, l.id`, tenantID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var leads []workbench.LeadView
	contacts := map[string]contactSnap{}
	for rows.Next() {
		var lead workbench.LeadView
		var phone, sourceType, consent, app, ref, refAt string
		if err := rows.Scan(&lead.ID, &lead.ContactID, &lead.Status, &lead.FilterReason, &lead.Assignee,
			&lead.CreatedAt, &lead.UpdatedAt, &phone, &sourceType, &consent, &app, &ref, &refAt); err != nil {
			return nil, nil, err
		}
		lead.TenantID = tenantID
		lead.SourceActivity = ref
		if refAt != "" {
			lead.SourceAt = refAt
		}
		if app != "" && lead.SourceChannel == "" {
			lead.SourceChannel = app
		}
		contacts[lead.ContactID] = contactSnap{phone: phone, sourceType: sourceType, consent: consent}
		leads = append(leads, lead)
	}
	return leads, contacts, rows.Err()
}

func (s *Store) loadDeskFollowUps(tenantID string) ([]followSnap, error) {
	rows, err := s.DB.Query(`
		SELECT contact_id, COALESCE(lead_id,''), note, COALESCE(next_follow_up_at,''), COALESCE(completed_at,''), created_at
		FROM follow_ups WHERE tenant_id=?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []followSnap
	for rows.Next() {
		var f followSnap
		if err := rows.Scan(&f.contactID, &f.leadID, &f.note, &f.next, &f.done, &f.createdAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) loadDeskConsents(tenantID string) (map[string][]consentSnap, error) {
	rows, err := s.DB.Query(`
		SELECT contact_id, source_channel, purpose, marketing_allowed, COALESCE(revoked_at,''), created_at
		FROM contact_consents WHERE tenant_id=?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]consentSnap{}
	for rows.Next() {
		var contactID string
		var snap consentSnap
		var marketing int
		if err := rows.Scan(&contactID, &snap.channel, &snap.purpose, &marketing, &snap.revokedAt, &snap.createdAt); err != nil {
			return nil, err
		}
		snap.marketing = marketing == 1
		out[contactID] = append(out[contactID], snap)
	}
	return out, rows.Err()
}

func (s *Store) loadDeskForms(tenantID string) (map[string]formSnap, error) {
	rows, err := s.DB.Query(`
		SELECT fs.lead_id, f.form_key, fs.created_at
		FROM form_submissions fs
		JOIN forms f ON f.id = fs.form_id
		WHERE fs.tenant_id=?
		ORDER BY fs.created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]formSnap{}
	for rows.Next() {
		var leadID string
		var snap formSnap
		if err := rows.Scan(&leadID, &snap.key, &snap.at); err != nil {
			return nil, err
		}
		if _, ok := out[leadID]; !ok {
			out[leadID] = snap
		}
	}
	return out, rows.Err()
}

func (s *Store) loadDeskSubmitted(tenantID string) (map[string]string, error) {
	out := map[string]string{}
	queries := []string{
		`SELECT lead_id, MIN(created_at) FROM lead_intake_events WHERE tenant_id=? GROUP BY lead_id`,
		`SELECT lead_id, MIN(created_at) FROM notify_inbox WHERE tenant_id=? AND lead_id IS NOT NULL GROUP BY lead_id`,
	}
	for _, q := range queries {
		rows, err := s.DB.Query(q, tenantID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, at string
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return nil, err
			}
			if cur, ok := out[id]; !ok || at < cur {
				out[id] = at
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func (s *Store) loadDeskOpps(tenantID string) ([]workbench.OpportunityView, error) {
	rows, err := s.DB.Query(`
		SELECT id, contact_id, stage, business_category, COALESCE(assigned_member_id,''),
		       amount_cents, updated_at
		FROM opportunities WHERE tenant_id=?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workbench.OpportunityView
	for rows.Next() {
		var opp workbench.OpportunityView
		var amount sql.NullInt64
		if err := rows.Scan(&opp.ID, &opp.ContactID, &opp.Stage, &opp.Category, &opp.Assignee, &amount, &opp.UpdatedAt); err != nil {
			return nil, err
		}
		opp.TenantID = tenantID
		opp.AmountKind = "customer_deal"
		if amount.Valid {
			v := amount.Int64
			opp.AmountCents = &v
		}
		out = append(out, opp)
	}
	return out, rows.Err()
}

func (s *Store) loadDeskStageHistory(tenantID string) (map[string][]histSnap, error) {
	rows, err := s.DB.Query(`
		SELECT o.contact_id, h.changed_at, h.from_stage, h.to_stage
		FROM opportunity_stage_history h
		JOIN opportunities o ON o.id = h.opportunity_id AND o.tenant_id = h.tenant_id
		WHERE h.tenant_id=?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]histSnap{}
	for rows.Next() {
		var contactID, at, from, to string
		if err := rows.Scan(&contactID, &at, &from, &to); err != nil {
			return nil, err
		}
		summary := "商机 " + to
		if from != "" {
			summary = "商机 " + from + " → " + to
		}
		out[contactID] = append(out[contactID], histSnap{at: at, summary: summary})
	}
	return out, rows.Err()
}

func (s *Store) loadDeskReception(tenantID string) ([]workbench.ReceptionView, error) {
	rows, err := s.DB.Query(`
		SELECT id, COALESCE(lead_id,''), COALESCE(owner_member_id,''), COALESCE(pending_reason,''), mode
		FROM reception_sessions WHERE tenant_id=? AND status='open'`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workbench.ReceptionView
	for rows.Next() {
		var item workbench.ReceptionView
		var mode string
		if err := rows.Scan(&item.SessionID, &item.LeadID, &item.Assignee, &item.PendingReason, &mode); err != nil {
			return nil, err
		}
		item.TenantID = tenantID
		item.WaitingReply = item.PendingReason == "waiting_reply" || item.PendingReason == "awaiting_customer"
		item.HumanTodo = mode == "human" || item.PendingReason == "clarify_or_handoff" || item.PendingReason == "awaiting_approval" || item.PendingReason == "model_unavailable" || item.PendingReason == "human_takeover"
		out = append(out, item)
	}
	return out, rows.Err()
}

func applyConsentFacts(lead *workbench.LeadView, rows []consentSnap) {
	for _, row := range rows {
		if row.revokedAt != "" {
			continue
		}
		channel := strings.TrimSpace(row.channel)
		switch channel {
		case "sms", "phone", "voice", "tel":
			if row.marketing {
				lead.MarketingSMSOrPhone = true
			}
		default:
			if channel != "" {
				lead.ChannelIdentity = channel
				lead.ChannelReplyAllowed = true
				if lead.SourceChannel == "" || lead.SourceChannel == "手工录入" || lead.SourceChannel == "表单" || lead.SourceChannel == "碰一碰" {
					lead.SourceChannel = channel
				}
			}
		}
		if workbench.RefuseSalesPush(row.purpose) {
			lead.Purpose = row.purpose
		}
	}
}

func sourceChannelLabel(sourceType string) string {
	switch sourceType {
	case "manual":
		return "手工录入"
	case "form":
		return "表单"
	case "touch_campaign":
		return "碰一碰"
	default:
		return sourceType
	}
}

func sourceSummary(lead workbench.LeadView) string {
	parts := []string{}
	if lead.SourceForm != "" {
		parts = append(parts, "表单 "+lead.SourceForm)
	}
	if lead.SourceActivity != "" {
		parts = append(parts, "活动 "+lead.SourceActivity)
	}
	if lead.SourceChannel != "" {
		parts = append(parts, lead.SourceChannel)
	}
	if len(parts) == 0 {
		return "来源"
	}
	return strings.Join(parts, " · ")
}

// CompleteOpenFollowUps closes every still-open follow-up on the contact.
// The first completion stamp is kept.
func (s *Store) CompleteOpenFollowUps(tenantID, contactID string) error {
	rows, err := s.DB.Query(`SELECT id FROM follow_ups WHERE tenant_id=? AND contact_id=? AND completed_at IS NULL`, tenantID, contactID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, _, _, err := s.CompleteFollowUp(id, tenantID); err != nil {
			return err
		}
	}
	return nil
}
