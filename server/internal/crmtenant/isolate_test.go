package crmtenant

import "testing"

func TestBrandDoesNotReplaceTenant(t *testing.T) {
	got, err := AuthorityTenant("tnt_a", "brand_white", "brand_white")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tnt_a" {
		t.Fatalf("tenant = %s, want tnt_a", got)
	}
	if _, err := AuthorityTenant("tnt_a", "tnt_b", "brand_white"); err == nil {
		t.Fatal("a body tenant from another workspace was accepted")
	}
	if _, err := AuthorityTenant("", "brand_white", "brand_white"); err == nil {
		t.Fatal("a brand id was accepted as the only tenant")
	}
}

func TestTargetTenantIgnoresSourceAndCampaign(t *testing.T) {
	got, err := TargetTenant("tnt_a", "camp_alias", "camp_alias", "camp_alias", "camp_alias")
	if err != nil || got != "tnt_a" {
		t.Fatalf("alias tenant = %q err=%v", got, err)
	}
	got, err = TargetTenant("tnt_a", "", "brand_white", "tnt_b", "camp_from_b")
	if err != nil || got != "tnt_a" {
		t.Fatalf("source tag changed tenant: %q err=%v", got, err)
	}
	if _, err := TargetTenant("tnt_a", " tnt_b ", "brand_white", "src", "camp"); err == nil {
		t.Fatal("a foreign body tenant was accepted")
	}
	if _, err := TargetTenant("  ", "brand_white", "brand_white", "src", "camp"); err == nil {
		t.Fatal("an empty member tenant was filled from brand or campaign")
	}
}

func TestSamePhoneDoesNotCrossMergeOrBind(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b PhonePlace
	}{
		{"two tenants", PhonePlace{TenantID: "tnt_a", BrandID: "brand_self", Phone: "13800001111"}, PhonePlace{TenantID: "tnt_b", BrandID: "brand_self", Phone: "13800001111"}},
		{"two brands", PhonePlace{TenantID: "tnt_a", BrandID: "brand_self", Phone: "13800001111"}, PhonePlace{TenantID: "tnt_b", BrandID: "brand_white", Phone: "13800001111"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if AllowContactMerge(tc.a, tc.b) {
				t.Fatal("merge was allowed")
			}
			if BindPlatformPrincipal(tc.a.Phone) {
				t.Fatal("phone bound a platform principal")
			}
		})
	}
}

func TestBrandRenameKeepsContactHistory(t *testing.T) {
	before := ContactAnchor{
		TenantID:       "tnt_a",
		BrandID:        "brand_white",
		ConsentSource:  "sub-1",
		ConsentChannel: "landing",
		OpportunityID:  "opp_1",
		Stage:          "open",
	}
	after := AfterBrandRename(before, "新品牌名")
	if after.TenantID != before.TenantID || after.BrandID != before.BrandID ||
		after.ConsentSource != before.ConsentSource || after.ConsentChannel != before.ConsentChannel ||
		after.OpportunityID != before.OpportunityID || after.Stage != before.Stage {
		t.Fatalf("rename rewrote history: %+v", after)
	}
	if after.BrandDisplay != "新品牌名" {
		t.Fatalf("display = %q", after.BrandDisplay)
	}
}

func TestChannelAdminNeedsIndependentDelegation(t *testing.T) {
	if ChannelAdminPlaintext(false, false) {
		t.Fatal("channel admin saw plaintext without support or operation")
	}
	if !ChannelAdminPlaintext(true, false) || !ChannelAdminPlaintext(false, true) {
		t.Fatal("an active support or operation delegation was ignored")
	}
	if ChannelAdminPlaintext(false, false) {
		t.Fatal("revoked flags were treated as active")
	}
}

func TestExportDropsForeignRowsPricesAndTokens(t *testing.T) {
	raw := ExportBundle{
		TenantID: "tnt_a",
		Contacts: []map[string]string{
			{"tenant_id": "tnt_a", "phone": "13800001111"},
			{"tenant_id": "tnt_b", "phone": "13900002222"},
		},
		Extra: map[string]string{
			"purchase_price_cents": "8800",
			"internal_token":       "sekret",
			"brand_id":             "brand_white",
		},
	}
	got := SanitizeExport(raw)
	if len(got.Contacts) != 1 || got.Contacts[0]["phone"] != "13800001111" {
		t.Fatalf("contacts = %+v", got.Contacts)
	}
	if _, ok := got.Extra["purchase_price_cents"]; ok {
		t.Fatal("export kept the channel purchase price")
	}
	if _, ok := got.Extra["internal_token"]; ok {
		t.Fatal("export kept an internal token")
	}
	if got.Extra["brand_id"] != "brand_white" {
		t.Fatal("safe brand reference was dropped")
	}
}

func TestDownloadRequiresFreshAuthorization(t *testing.T) {
	job := ExportJob{TenantID: "tnt_a", Requester: "usr_owner_a", Expired: false}
	if DownloadAllowed(job, "tnt_a", "usr_owner_a", false) {
		t.Fatal("download succeeded without the confirm step")
	}
	if !DownloadAllowed(job, "tnt_a", "usr_owner_a", true) {
		t.Fatal("owner download was refused")
	}
	if DownloadAllowed(job, "tnt_b", "usr_owner_a", true) || DownloadAllowed(job, "tnt_a", "usr_owner_b", true) {
		t.Fatal("another tenant or requester downloaded the job")
	}
	job.Expired = true
	if DownloadAllowed(job, "tnt_a", "usr_owner_a", true) {
		t.Fatal("expired job downloaded")
	}
}

func TestDowngradeStopsNewAIButKeepsHistory(t *testing.T) {
	if NewPaidAIAllowed("downgraded", false, 0) {
		t.Fatal("downgraded plan started a new paid AI action")
	}
	if NewPaidAIAllowed("active", true, 0) {
		t.Fatal("empty channel quota started a new paid AI action")
	}
	if !NewPaidAIAllowed("active", false, 0) {
		t.Fatal("an active plan with no channel quota row was blocked")
	}
	for _, kind := range []string{"contact", "lead", "opportunity", "follow_up"} {
		kept := HistoryKept(kind)
		if !kept.Readable || kept.Deleted || kept.Hidden {
			t.Fatalf("%s history = %+v", kind, kept)
		}
	}
}

func TestPauseAndResumeDoNotRestoreRevocations(t *testing.T) {
	tenant, brand := IndependentPause("suspended", "active")
	if !tenant || brand {
		t.Fatalf("pauses coupled: tenant=%v brand=%v", tenant, brand)
	}
	tenant, brand = IndependentPause("active", "suspended")
	if tenant || !brand {
		t.Fatalf("brand pause moved the tenant: tenant=%v brand=%v", tenant, brand)
	}
	member, consent, delegation := ResumeRestores(true, true, true)
	if member || consent || delegation {
		t.Fatal("resume restored a revoked membership, consent, or delegation")
	}
	if ReplayMarketing(true) {
		t.Fatal("replaying an old event restored marketing permission")
	}
}
