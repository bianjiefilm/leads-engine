package subscription

import "testing"

func TestCRMContactDoesNotMintPlatformAccount(t *testing.T) {
	for i := 0; i < 100; i++ {
		got := PlatformSideEffect(ContactIdentity{
			Phone: "1380000" + pad4(i),
			Email: "c@shop.example",
		})
		if got.Register || got.Bind || got.Merge || got.Principals != 0 || got.Wallets != 0 {
			t.Fatalf("contact %d minted a platform identity: %+v", i, got)
		}
	}
}

func TestSamePhoneOrEmailDoesNotBindPrincipal(t *testing.T) {
	got := PlatformSideEffect(ContactIdentity{
		Phone:          "13812345678",
		Email:          "owner@shop.example",
		KnownPrincipal: "usr_owner",
		KnownPhone:     "13812345678",
		KnownEmail:     "owner@shop.example",
	})
	if got.Register || got.Bind || got.Merge || got.Principals != 0 || got.Wallets != 0 {
		t.Fatalf("matching phone/email bound a platform user: %+v", got)
	}
}

func TestWalletBalanceDoesNotOpenCRM(t *testing.T) {
	if CRMOpened("expired", 50_000_00) {
		t.Fatal("wallet balance opened an expired CRM plan")
	}
	if CRMOpened("missing", 1) {
		t.Fatal("wallet balance opened a missing CRM plan")
	}
	if !CRMOpened("active", 0) {
		t.Fatal("an active plan with an empty wallet must stay open")
	}
}

func TestExpiryKeepsHistoryAndLimitsAdvanced(t *testing.T) {
	for _, kind := range []string{"contact", "follow_up", "opportunity"} {
		got := HistoryAfterExpiry(kind)
		if !got.Readable || got.Deleted || got.ReportedLost {
			t.Fatalf("%s history = %+v", kind, got)
		}
	}
	for _, feature := range []string{"auto_assign", "analytics", "advanced_channel", "extra_form"} {
		if AdvancedAllowed("expired", feature) {
			t.Fatalf("expired plan still allowed %s", feature)
		}
	}
	if !AdvancedAllowed("active", "analytics") {
		t.Fatal("active plan must allow analytics")
	}
}

func TestOrdinaryCRMIsNotMetered(t *testing.T) {
	for _, action := range []string{"ingest", "view", "manual_follow_up"} {
		got := Price(action)
		if got.Billable || got.Cents != 0 || got.Meter != "" {
			t.Fatalf("%s priced as %+v", action, got)
		}
	}
}

func TestAIActionsAreQuotedSeparatelyAndNeverAutoRun(t *testing.T) {
	score := Price("ai_score")
	outbound := Price("ai_outbound")
	if !score.Billable || score.Meter != "ai_usage" || score.Cents != 100 {
		t.Fatalf("score quote = %+v", score)
	}
	if !outbound.Billable || outbound.Meter != "ai_usage" || outbound.Cents != 500 || outbound.Cents == score.Cents {
		t.Fatalf("outbound quote = %+v", outbound)
	}
	if AutoExecute("ai_outbound", 1_000_000) || AutoExecute("ai_message", 1_000_000) {
		t.Fatal("a funded wallet auto-ran an outbound or a message")
	}
}

func TestAIPayerMustBeOrgOrAuthorized(t *testing.T) {
	org := "ba_org"
	allowed := "ba_agent_payer"
	if !PayerAllowed("organization", org, org, "") {
		t.Fatal("organization billing account was rejected")
	}
	if !PayerAllowed("authorized", allowed, org, allowed) {
		t.Fatal("authorized payer was rejected")
	}
	if PayerAllowed("personal", "ba_person", org, allowed) || PayerAllowed("organization", "ba_other", org, allowed) || PayerAllowed("authorized", "ba_stranger", org, allowed) {
		t.Fatal("an unrelated payer was accepted")
	}
}

func TestInsufficientBalanceOnlyOffersRechargeReturn(t *testing.T) {
	got := DecideAI(AIInput{
		Action: "ai_score", PayerKind: "organization", PayerAccountID: "ba_org", OrgAccountID: "ba_org",
		BalanceCents: 10, QuoteRevision: "r1", SeenRevision: "r1", ExplicitConfirm: true,
		ReturnTo: "/leads/lead_1",
	})
	if got.Execute || got.LiveCharge != 0 || got.Reason != "insufficient_balance" || got.BillingCenter != "billing_center" || got.ReturnTo != "/leads/lead_1" {
		t.Fatalf("insufficient = %+v", got)
	}
}

func TestResumeRereadsQuoteAndDoesNotRepeat(t *testing.T) {
	stale := DecideAI(AIInput{
		Action: "ai_outbound", PayerKind: "organization", PayerAccountID: "ba_org", OrgAccountID: "ba_org",
		BalanceCents: 10_000, QuoteRevision: "r2", SeenRevision: "r1", ExplicitConfirm: true,
		ReturnTo: "/leads/lead_1",
	})
	if stale.Execute || !stale.Reread || stale.LiveCharge != 0 {
		t.Fatalf("stale resume = %+v", stale)
	}
	once := DecideAI(AIInput{
		Action: "ai_score", PayerKind: "organization", PayerAccountID: "ba_org", OrgAccountID: "ba_org",
		BalanceCents: 10_000, QuoteRevision: "r2", SeenRevision: "r2", ExplicitConfirm: true,
	})
	if !once.Execute || once.LiveCharge != 0 || once.Reread {
		t.Fatalf("fresh confirm = %+v", once)
	}
	again := DecideAI(AIInput{
		Action: "ai_score", PayerKind: "organization", PayerAccountID: "ba_org", OrgAccountID: "ba_org",
		BalanceCents: 10_000, QuoteRevision: "r2", SeenRevision: "r2", ExplicitConfirm: true, AlreadyCommitted: true,
	})
	if again.Execute || !again.Duplicate || again.LiveCharge != 0 {
		t.Fatalf("repeat = %+v", again)
	}
}

func TestAuthorizedVisitorNeedsNoPlatformAccount(t *testing.T) {
	if !VisitorLeadAllowed(true, "") {
		t.Fatal("authorized visitor without a platform account was refused")
	}
	if VisitorLeadAllowed(false, "") {
		t.Fatal("unauthorized visitor was accepted")
	}
}

func TestLedgersStayApart(t *testing.T) {
	got := Ledgers(12_00, 3_00, 80_000_00, 50_000_00)
	if got.CRMSubscriptionCents != 12_00 || got.AIUsageCents != 3_00 || got.MerchantDealCents != 80_000_00 {
		t.Fatalf("ledgers = %+v", got)
	}
	if got.WalletCents != 50_000_00 || got.MerchantRevenueInWallet {
		t.Fatalf("merchant revenue entered the wallet: %+v", got)
	}
}

func pad4(n int) string {
	digits := "0000"
	raw := ""
	if n == 0 {
		raw = "0"
	} else {
		v := n
		for v > 0 {
			raw = string(rune('0'+v%10)) + raw
			v /= 10
		}
	}
	if len(raw) >= 4 {
		return raw
	}
	return digits[:4-len(raw)] + raw
}
