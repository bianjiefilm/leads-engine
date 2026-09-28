package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/crmtenant"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
	"github.com/bianjiefilm/leads-engine/server/internal/subscription"
)

func (s *Server) handleSubscriptionGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	writeJSON(w, http.StatusOK, s.subscriptionView(c.Member.TenantID))
}

func (s *Server) handleSubscriptionCache(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Plan                     string `json:"plan"`
		Status                   string `json:"status"`
		WalletBalanceCents       int64  `json:"wallet_balance_cents"`
		OrgBillingAccountID      string `json:"org_billing_account_id"`
		AuthorizedPayerAccountID string `json:"authorized_payer_account_id"`
		CRMSubscriptionCents     int64  `json:"crm_subscription_cents"`
		MerchantDealCents        int64  `json:"merchant_deal_cents"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	switch in.Status {
	case "active", "expired", "missing", "downgraded":
	default:
		fail(w, http.StatusBadRequest, "bad_request", "status must be active, expired, missing or downgraded")
		return
	}
	if strings.TrimSpace(in.Plan) == "" || strings.TrimSpace(in.OrgBillingAccountID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "plan and org_billing_account_id are required")
		return
	}
	err := s.St.UpsertEntitlementCache(store.EntitlementCache{
		TenantID:                 c.Member.TenantID,
		Plan:                     in.Plan,
		Status:                   in.Status,
		WalletBalanceCents:       in.WalletBalanceCents,
		OrgBillingAccountID:      in.OrgBillingAccountID,
		AuthorizedPayerAccountID: in.AuthorizedPayerAccountID,
		CRMSubscriptionCents:     in.CRMSubscriptionCents,
		MerchantDealCents:        in.MerchantDealCents,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "entitlement cache update failed")
		return
	}
	writeJSON(w, http.StatusOK, s.subscriptionView(c.Member.TenantID))
}

func (s *Server) handleSubscriptionUsage(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	price := subscription.Price(in.Action)
	if price.Billable {
		fail(w, http.StatusBadRequest, "ai_quote_required", "this action is priced as AI usage and needs its own quote")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action": in.Action, "billable": false, "cents": 0, "meter": "", "live_charge": 0,
	})
}

func (s *Server) handleAIQuote(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	cache, _ := s.St.GetEntitlementCache(c.Member.TenantID)
	quotaKnown, quota, qerr := s.St.ChannelAIQuota(c.Member.TenantID)
	if qerr != nil {
		fail(w, http.StatusInternalServerError, "internal", "quota lookup failed")
		return
	}
	if !crmtenant.NewPaidAIAllowed(cache.Status, quotaKnown, quota) {
		kept := crmtenant.HistoryKept("contact")
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "paid_ai_stopped", "execute": false, "live_charge": int64(0),
			"history_retained": kept.Readable && !kept.Deleted && !kept.Hidden,
			"data_lost":        kept.Deleted || kept.Hidden,
		})
		return
	}
	var in struct {
		Action         string `json:"action"`
		PayerKind      string `json:"payer_kind"`
		PayerAccountID string `json:"payer_account_id"`
		ReturnTo       string `json:"return_to"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	cache, _ = s.St.GetEntitlementCache(c.Member.TenantID)
	price := subscription.Price(in.Action)
	decision := subscription.DecideAI(subscription.AIInput{
		Action: in.Action, PayerKind: in.PayerKind, PayerAccountID: in.PayerAccountID,
		OrgAccountID: cache.OrgBillingAccountID, AuthorizedAccount: cache.AuthorizedPayerAccountID,
		BalanceCents: cache.WalletBalanceCents, QuoteRevision: "pending", SeenRevision: "pending",
		ExplicitConfirm: true, ReturnTo: in.ReturnTo,
	})
	if decision.Reason == "payer_not_authorized" || decision.Reason == "not_ai_usage" {
		if decision.Reason == "not_ai_usage" {
			fail(w, http.StatusBadRequest, "not_ai_usage", "ordinary CRM work is not quoted here")
			return
		}
		writeJSON(w, http.StatusOK, decisionJSON("", "", decision))
		return
	}
	if !price.Billable {
		fail(w, http.StatusBadRequest, "not_ai_usage", "ordinary CRM work is not quoted here")
		return
	}
	// Force the balance check with a stable seen revision after the payer check.
	decision = subscription.DecideAI(subscription.AIInput{
		Action: in.Action, PayerKind: in.PayerKind, PayerAccountID: in.PayerAccountID,
		OrgAccountID: cache.OrgBillingAccountID, AuthorizedAccount: cache.AuthorizedPayerAccountID,
		BalanceCents: cache.WalletBalanceCents, QuoteRevision: "quoted", SeenRevision: "quoted",
		ExplicitConfirm: false, ReturnTo: in.ReturnTo,
	})
	if cache.WalletBalanceCents < price.Cents {
		decision = subscription.DecideAI(subscription.AIInput{
			Action: in.Action, PayerKind: in.PayerKind, PayerAccountID: in.PayerAccountID,
			OrgAccountID: cache.OrgBillingAccountID, AuthorizedAccount: cache.AuthorizedPayerAccountID,
			BalanceCents: cache.WalletBalanceCents, QuoteRevision: "quoted", SeenRevision: "quoted",
			ExplicitConfirm: true, ReturnTo: in.ReturnTo,
		})
	}
	row, err := s.St.InsertAIQuote(store.AIQuote{
		TenantID: c.Member.TenantID, Action: in.Action, PayerKind: in.PayerKind,
		PayerAccountID: in.PayerAccountID, QuoteCents: price.Cents, BalanceSeen: cache.WalletBalanceCents,
		ReturnTo: in.ReturnTo,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "quote create failed")
		return
	}
	writeJSON(w, http.StatusOK, decisionJSON(row.ID, row.Revision, decision))
}

func (s *Server) handleAIRequote(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	cache, _ := s.St.GetEntitlementCache(c.Member.TenantID)
	row, err := s.St.RequoteAI(c.Member.TenantID, r.PathValue("id"), cache.WalletBalanceCents)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "quote not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "requote failed")
		return
	}
	if row.Status == "committed" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"id": row.ID, "execute": false, "duplicate": true, "live_charge": 0, "side_effects": 0, "reason": "already_committed",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": row.ID, "revision": row.Revision, "execute": false, "live_charge": 0, "side_effects": 0,
		"reason": "reread", "reread": true, "return_to": row.ReturnTo, "quote_cents": row.QuoteCents,
	})
}

func (s *Server) handleAICommit(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		SeenRevision    string `json:"seen_revision"`
		ExplicitConfirm bool   `json:"explicit_confirm"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	row, err := s.St.GetAIQuote(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "quote not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "quote lookup failed")
		return
	}
	cache, _ := s.St.GetEntitlementCache(c.Member.TenantID)
	revision := row.Revision
	if cache.WalletBalanceCents != row.BalanceSeen {
		revision = row.Revision + "+funds"
	}
	decision := subscription.DecideAI(subscription.AIInput{
		Action: row.Action, PayerKind: row.PayerKind, PayerAccountID: row.PayerAccountID,
		OrgAccountID: cache.OrgBillingAccountID, AuthorizedAccount: cache.AuthorizedPayerAccountID,
		BalanceCents: cache.WalletBalanceCents, QuoteRevision: revision, SeenRevision: in.SeenRevision,
		ExplicitConfirm: in.ExplicitConfirm, AlreadyCommitted: row.Status == "committed", ReturnTo: row.ReturnTo,
	})
	if !decision.Execute {
		writeJSON(w, http.StatusConflict, decisionJSON(row.ID, row.Revision, decision))
		return
	}
	committed, err := s.St.CommitAIQuote(c.Member.TenantID, row.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "quote commit failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": committed.ID, "revision": committed.Revision, "execute": true, "reason": "quoted",
		"live_charge": committed.LiveCharge, "side_effects": committed.SideEffects, "return_to": committed.ReturnTo,
	})
}

func decisionJSON(id, revision string, d subscription.Decision) map[string]any {
	return map[string]any{
		"id": id, "revision": revision, "execute": d.Execute, "reason": d.Reason,
		"billing_center": d.BillingCenter, "return_to": d.ReturnTo, "reread": d.Reread,
		"duplicate": d.Duplicate, "live_charge": d.LiveCharge, "side_effects": int64(0),
	}
}

func (s *Server) subscriptionView(tenantID string) map[string]any {
	cache, err := s.St.GetEntitlementCache(tenantID)
	status := "unconfigured"
	if err == nil {
		status = cache.Status
	}
	aiCents, _ := s.St.SumCommittedAIQuoteCents(tenantID)
	ledgers := subscription.Ledgers(cache.CRMSubscriptionCents, aiCents, cache.MerchantDealCents, cache.WalletBalanceCents)
	foot, _ := s.St.IdentityFootprint(tenantID)
	opened := subscription.CRMOpened(status, cache.WalletBalanceCents)
	return map[string]any{
		"status":                     status,
		"plan":                       cache.Plan,
		"crm_opened":                 opened,
		"opened_by_wallet":           false,
		"data_lost":                  false,
		"history_retained":           true,
		"merchant_revenue_in_wallet": ledgers.MerchantRevenueInWallet,
		"live_charge":                0,
		"billing_integration":        "local_cache",
		"billing_pass":               "UNKNOWN",
		"production_authorized":      "NOT_AUTHORIZED",
		"ledgers": map[string]any{
			"crm_subscription_cents": ledgers.CRMSubscriptionCents,
			"ai_usage_cents":         ledgers.AIUsageCents,
			"merchant_deal_cents":    ledgers.MerchantDealCents,
			"wallet_cents":           ledgers.WalletCents,
		},
		"advanced": map[string]any{
			"auto_assign":      subscription.AdvancedAllowed(status, "auto_assign"),
			"analytics":        subscription.AdvancedAllowed(status, "analytics"),
			"advanced_channel": subscription.AdvancedAllowed(status, "advanced_channel"),
			"extra_form":       subscription.AdvancedAllowed(status, "extra_form"),
		},
		"identity": map[string]any{
			"contacts":                        foot.Contacts,
			"members":                         foot.Members,
			"platform_accounts_from_contacts": foot.PlatformAccountsFromContacts,
			"wallets_from_contacts":           foot.WalletsFromContacts,
		},
	}
}

func (s *Server) blockIfAdvancedExpired(w http.ResponseWriter, tenantID, feature string) bool {
	cache, err := s.St.GetEntitlementCache(tenantID)
	if err != nil || cache.Status != "expired" {
		return false
	}
	if subscription.AdvancedAllowed(cache.Status, feature) {
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error":            "entitlement_expired",
		"message":          "这项高级功能已随套餐到期停用，联系人、跟进和商机历史仍在",
		"data_lost":        false,
		"history_retained": true,
	})
	return true
}
