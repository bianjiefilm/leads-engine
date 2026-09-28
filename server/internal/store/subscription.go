package store

import "database/sql"

// EntitlementCache is the local observation of a CRM plan. Wallet balance is
// stored beside it and is not an entitlement.
type EntitlementCache struct {
	TenantID                 string
	Plan                     string
	Status                   string
	WalletBalanceCents       int64
	OrgBillingAccountID      string
	AuthorizedPayerAccountID string
	CRMSubscriptionCents     int64
	MerchantDealCents        int64
	UpdatedAt                string
}

func (s *Store) UpsertEntitlementCache(row EntitlementCache) error {
	row.UpdatedAt = now()
	_, err := s.DB.Exec(`
		INSERT INTO crm_entitlement_cache(
			tenant_id, plan, status, wallet_balance_cents, org_billing_account_id,
			authorized_payer_account_id, crm_subscription_cents, merchant_deal_cents, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			plan=excluded.plan,
			status=excluded.status,
			wallet_balance_cents=excluded.wallet_balance_cents,
			org_billing_account_id=excluded.org_billing_account_id,
			authorized_payer_account_id=excluded.authorized_payer_account_id,
			crm_subscription_cents=excluded.crm_subscription_cents,
			merchant_deal_cents=excluded.merchant_deal_cents,
			updated_at=excluded.updated_at`,
		row.TenantID, row.Plan, row.Status, row.WalletBalanceCents, row.OrgBillingAccountID,
		row.AuthorizedPayerAccountID, row.CRMSubscriptionCents, row.MerchantDealCents, row.UpdatedAt)
	return err
}

func (s *Store) GetEntitlementCache(tenantID string) (EntitlementCache, error) {
	var row EntitlementCache
	err := s.DB.QueryRow(`
		SELECT tenant_id, plan, status, wallet_balance_cents, org_billing_account_id,
		       authorized_payer_account_id, crm_subscription_cents, merchant_deal_cents, updated_at
		FROM crm_entitlement_cache WHERE tenant_id=?`, tenantID).Scan(
		&row.TenantID, &row.Plan, &row.Status, &row.WalletBalanceCents, &row.OrgBillingAccountID,
		&row.AuthorizedPayerAccountID, &row.CRMSubscriptionCents, &row.MerchantDealCents, &row.UpdatedAt)
	return row, err
}

// AIQuote is a local price reservation. It cannot charge or send.
type AIQuote struct {
	ID             string
	TenantID       string
	Action         string
	PayerKind      string
	PayerAccountID string
	QuoteCents     int64
	Revision       string
	BalanceSeen    int64
	Status         string
	ReturnTo       string
	LiveCharge     int64
	SideEffects    int64
	CreatedAt      string
	CommittedAt    string
}

func (s *Store) InsertAIQuote(row AIQuote) (AIQuote, error) {
	row.ID = newID("aiq_")
	row.Revision = newID("rev_")
	row.Status = "quoted"
	row.LiveCharge = 0
	row.SideEffects = 0
	row.CreatedAt = now()
	_, err := s.DB.Exec(`
		INSERT INTO ai_usage_quotes(
			id, tenant_id, action, payer_kind, payer_account_id, quote_cents, revision,
			balance_seen, status, return_to, live_charge, side_effects, created_at, committed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,0,0,?,?)`,
		row.ID, row.TenantID, row.Action, row.PayerKind, row.PayerAccountID, row.QuoteCents, row.Revision,
		row.BalanceSeen, row.Status, row.ReturnTo, row.CreatedAt, "")
	return row, err
}

func (s *Store) GetAIQuote(tenantID, id string) (AIQuote, error) {
	var row AIQuote
	err := s.DB.QueryRow(`
		SELECT id, tenant_id, action, payer_kind, payer_account_id, quote_cents, revision,
		       balance_seen, status, return_to, live_charge, side_effects, created_at, committed_at
		FROM ai_usage_quotes WHERE tenant_id=? AND id=?`, tenantID, id).Scan(
		&row.ID, &row.TenantID, &row.Action, &row.PayerKind, &row.PayerAccountID, &row.QuoteCents, &row.Revision,
		&row.BalanceSeen, &row.Status, &row.ReturnTo, &row.LiveCharge, &row.SideEffects, &row.CreatedAt, &row.CommittedAt)
	return row, err
}

// RequoteAI updates the observed balance and revision. A committed quote is left alone.
func (s *Store) RequoteAI(tenantID, id string, balance int64) (AIQuote, error) {
	row, err := s.GetAIQuote(tenantID, id)
	if err != nil {
		return AIQuote{}, err
	}
	if row.Status == "committed" {
		return row, nil
	}
	row.Revision = newID("rev_")
	row.BalanceSeen = balance
	_, err = s.DB.Exec(`UPDATE ai_usage_quotes SET revision=?, balance_seen=? WHERE tenant_id=? AND id=? AND status='quoted'`,
		row.Revision, row.BalanceSeen, tenantID, id)
	return row, err
}

// CommitAIQuote marks one quoted row committed. live_charge stays 0.
func (s *Store) CommitAIQuote(tenantID, id string) (AIQuote, error) {
	_, err := s.DB.Exec(`UPDATE ai_usage_quotes SET status='committed', committed_at=?, live_charge=0, side_effects=0
		WHERE tenant_id=? AND id=? AND status='quoted'`, now(), tenantID, id)
	if err != nil {
		return AIQuote{}, err
	}
	return s.GetAIQuote(tenantID, id)
}

func (s *Store) SumCommittedAIQuoteCents(tenantID string) (int64, error) {
	var n sql.NullInt64
	err := s.DB.QueryRow(`SELECT COALESCE(SUM(quote_cents),0) FROM ai_usage_quotes WHERE tenant_id=? AND status='committed'`, tenantID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n.Int64, nil
}

// IdentityFootprint counts CRM contacts against platform membership.
// WalletsFromContacts is always zero: this schema has no wallet ledger.
type IdentityFootprint struct {
	Contacts                     int
	Members                      int
	PlatformAccountsFromContacts int
	WalletsFromContacts          int
}

func (s *Store) IdentityFootprint(tenantID string) (IdentityFootprint, error) {
	var out IdentityFootprint
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM contacts WHERE tenant_id=? AND deleted_at IS NULL`, tenantID).Scan(&out.Contacts); err != nil {
		return out, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM members WHERE tenant_id=?`, tenantID).Scan(&out.Members); err != nil {
		return out, err
	}
	err := s.DB.QueryRow(`
		SELECT COUNT(1) FROM members m
		WHERE m.tenant_id=? AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.tenant_id=m.tenant_id AND c.deleted_at IS NULL
			  AND (c.phone=m.principal_ref OR lower(c.email)=lower(m.principal_ref))
		)`, tenantID).Scan(&out.PlatformAccountsFromContacts)
	return out, err
}
