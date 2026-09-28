// Package subscription is the local read model for CRM entitlement and AI
// usage quotes. It does not call a payment provider and never moves money.
package subscription

// ContactIdentity is a CRM row. It is not a platform principal.
type ContactIdentity struct {
	Phone          string
	Email          string
	KnownPrincipal string
	KnownPhone     string
	KnownEmail     string
}

// SideEffect is what creating or matching a contact may do to platform identity.
type SideEffect struct {
	Register   bool
	Bind       bool
	Merge      bool
	Principals int
	Wallets    int
}

// PlatformSideEffect is always empty. A shared phone or email does not
// register, bind, or merge a platform user, and it does not open a wallet.
func PlatformSideEffect(ContactIdentity) SideEffect { return SideEffect{} }

// CRMOpened is true only for an active plan. Wallet balance is ignored.
func CRMOpened(planStatus string, _ int64) bool { return planStatus == "active" }

// History is the expiry policy for a stored CRM record.
type History struct {
	Readable     bool
	Deleted      bool
	ReportedLost bool
}

// HistoryAfterExpiry keeps contacts, follow-ups, and opportunities readable.
func HistoryAfterExpiry(kind string) History {
	switch kind {
	case "contact", "follow_up", "opportunity":
		return History{Readable: true}
	default:
		return History{}
	}
}

// AdvancedAllowed is the package gate. Expiry limits the feature and does
// not describe the underlying rows as missing.
func AdvancedAllowed(planStatus, feature string) bool {
	switch feature {
	case "auto_assign", "analytics", "advanced_channel", "extra_form":
		return planStatus == "active"
	default:
		return false
	}
}

// Charge is the local price of one action. Ordinary CRM work is not metered.
type Charge struct {
	Billable bool
	Cents    int64
	Meter    string
}

// Price quotes AI actions on their own meter. Ingest, view, and a manual
// follow-up stay at zero.
func Price(action string) Charge {
	switch action {
	case "ai_score":
		return Charge{Billable: true, Cents: 100, Meter: "ai_usage"}
	case "ai_summary":
		return Charge{Billable: true, Cents: 80, Meter: "ai_usage"}
	case "ai_reception":
		return Charge{Billable: true, Cents: 200, Meter: "ai_usage"}
	case "ai_outbound":
		return Charge{Billable: true, Cents: 500, Meter: "ai_usage"}
	case "ai_message":
		return Charge{Billable: true, Cents: 50, Meter: "ai_usage"}
	default:
		return Charge{}
	}
}

// AutoExecute is never true. A positive balance does not place a call or
// send a message.
func AutoExecute(string, int64) bool { return false }

// PayerAllowed accepts the current organization's billing account, or the
// one payer account that organization has authorized.
func PayerAllowed(kind, payerAccount, orgAccount, authorizedAccount string) bool {
	if payerAccount == "" || orgAccount == "" {
		return false
	}
	switch kind {
	case "organization":
		return payerAccount == orgAccount
	case "authorized":
		return authorizedAccount != "" && payerAccount == authorizedAccount && payerAccount != orgAccount
	default:
		return false
	}
}

// AIInput is one quote or commit attempt. Live charge stays zero here.
type AIInput struct {
	Action            string
	PayerKind         string
	PayerAccountID    string
	OrgAccountID      string
	AuthorizedAccount string
	BalanceCents      int64
	QuoteRevision     string
	SeenRevision      string
	ExplicitConfirm   bool
	AlreadyCommitted  bool
	ReturnTo          string
}

// Decision is the local outcome. LiveCharge is always zero: this package
// does not debit a platform wallet.
type Decision struct {
	Execute       bool
	Reason        string
	BillingCenter string
	ReturnTo      string
	Reread        bool
	Duplicate     bool
	LiveCharge    int64
}

// DecideAI quotes a billable AI action. Insufficient balance only points at
// the billing center and the original return path. A stale revision must be
// read again. A committed action is not run a second time.
func DecideAI(in AIInput) Decision {
	price := Price(in.Action)
	if !price.Billable {
		return Decision{Reason: "not_ai_usage"}
	}
	if !PayerAllowed(in.PayerKind, in.PayerAccountID, in.OrgAccountID, in.AuthorizedAccount) {
		return Decision{Reason: "payer_not_authorized"}
	}
	if in.AlreadyCommitted {
		return Decision{Reason: "already_committed", Duplicate: true}
	}
	if in.SeenRevision == "" || in.SeenRevision != in.QuoteRevision {
		return Decision{Reason: "quote_stale", Reread: true, ReturnTo: in.ReturnTo}
	}
	if !in.ExplicitConfirm || AutoExecute(in.Action, in.BalanceCents) {
		return Decision{Reason: "confirmation_required", ReturnTo: in.ReturnTo}
	}
	if in.BalanceCents < price.Cents {
		return Decision{
			Reason:        "insufficient_balance",
			BillingCenter: "billing_center",
			ReturnTo:      in.ReturnTo,
		}
	}
	return Decision{Execute: true, Reason: "quoted"}
}

// VisitorLeadAllowed accepts an authorized marketing visitor who has no
// platform account. Authorization is the credential, not a principal id.
func VisitorLeadAllowed(authorized bool, _ string) bool {
	return authorized
}

// Ledger is the three-way split shown to a merchant.
type Ledger struct {
	CRMSubscriptionCents    int64
	AIUsageCents            int64
	MerchantDealCents       int64
	WalletCents             int64
	MerchantRevenueInWallet bool
}

// Ledgers keeps CRM subscription, AI usage, and merchant revenue apart.
// Merchant revenue is never copied into the wallet figure.
func Ledgers(crmCents, aiCents, dealCents, walletCents int64) Ledger {
	return Ledger{
		CRMSubscriptionCents:    crmCents,
		AIUsageCents:            aiCents,
		MerchantDealCents:       dealCents,
		WalletCents:             walletCents,
		MerchantRevenueInWallet: false,
	}
}
