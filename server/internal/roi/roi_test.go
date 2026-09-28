package roi

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func at(t *testing.T, raw string) time.Time {
	t.Helper()
	got, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func baseInput(t *testing.T) Input {
	t.Helper()
	return Input{
		TenantID:         "tnt_1",
		BusinessCategory: "merchant_customer",
		WindowStart:      at(t, "2026-01-01T00:00:00Z"),
		WindowEnd:        at(t, "2026-02-01T00:00:00Z"),
	}
}

func chainLinks() []Link {
	return []Link{
		{ID: "src", TenantID: "tnt_1", Kind: "source", Ref: "touch"},
		{ID: "cmp", TenantID: "tnt_1", Kind: "campaign", ParentID: "src", CampaignID: "cmp", Ref: "spring"},
		{ID: "store", TenantID: "tnt_1", Kind: "store", ParentID: "cmp", Ref: "shop-1"},
		{ID: "tag", TenantID: "tnt_1", Kind: "tag", ParentID: "store", Ref: "new"},
		{ID: "asset", TenantID: "tnt_1", Kind: "asset_version", ParentID: "tag", AssetID: "asset-1", Ref: "v3"},
		{ID: "sub", TenantID: "tnt_1", Kind: "submission", ParentID: "asset", Ref: "form-9"},
		{ID: "lead", TenantID: "tnt_1", Kind: "lead", ParentID: "sub", Ref: "lead-9"},
		{ID: "opp", TenantID: "tnt_1", Kind: "opportunity", ParentID: "lead", Ref: "opp-9"},
	}
}

func findStage(t *testing.T, rep Report, key string) StageLine {
	t.Helper()
	for _, st := range rep.Stages {
		if st.Key == key {
			return st
		}
	}
	t.Fatalf("missing stage %s", key)
	return StageLine{}
}

func findRevenue(t *testing.T, rep Report, kind, authority string) RevenueLine {
	t.Helper()
	for _, line := range rep.Revenues {
		if line.Kind == kind && line.Authority == authority {
			return line
		}
	}
	t.Fatalf("missing revenue %s/%s in %+v", kind, authority, rep.Revenues)
	return RevenueLine{}
}

func centsOf(t *testing.T, p *int64) int64 {
	t.Helper()
	if p == nil {
		t.Fatal("cents is nil")
	}
	return *p
}

func TestRestrictedSourceChainKeepsGaps(t *testing.T) {
	in := baseInput(t)
	in.Links = append(chainLinks(), Link{
		ID: "orphan", TenantID: "tnt_1", Kind: "lead", ParentID: "missing-parent", Ref: "同昵称",
	})
	in.Events = []Event{
		{ID: "e-linked", TenantID: "tnt_1", Kind: "browse", LinkID: "lead", OccurredAt: at(t, "2026-01-05T00:00:00Z")},
		{ID: "e-nick", TenantID: "tnt_1", Kind: "browse", LinkID: "", Channel: "同昵称", OccurredAt: at(t, "2026-01-06T00:00:00Z")},
		{ID: "e-miss", TenantID: "tnt_1", Kind: "authorized_lead", LinkID: "no-such-link", OccurredAt: at(t, "2026-01-07T00:00:00Z")},
	}
	rep, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.AttributionRule != RuleRestrictedSourceChainV1 {
		t.Fatalf("rule = %s", rep.AttributionRule)
	}
	want := []string{"src", "cmp", "store", "tag", "asset", "sub", "lead", "opp", "orphan"}
	if len(rep.SourceChain) != len(want) {
		t.Fatalf("chain = %+v", rep.SourceChain)
	}
	for i, id := range want {
		if rep.SourceChain[i].ID != id {
			t.Fatalf("chain[%d] = %s, want %s", i, rep.SourceChain[i].ID, id)
		}
	}
	if rep.SourceChain[len(rep.SourceChain)-1].Gap != true {
		t.Fatal("missing parent must stay a gap, not a guessed link")
	}
	for _, node := range rep.SourceChain[:len(rep.SourceChain)-1] {
		if node.Gap {
			t.Fatalf("complete link %s marked gap", node.ID)
		}
	}
	if rep.UnassociatedCount != 2 || rep.AssociatedCount != 1 {
		t.Fatalf("assoc=%d unassoc=%d", rep.AssociatedCount, rep.UnassociatedCount)
	}
	if rep.UnassociatedRatio == nil || math.Abs(*rep.UnassociatedRatio-2.0/3.0) > 1e-9 {
		t.Fatalf("ratio = %v", rep.UnassociatedRatio)
	}
	if rep.MarketingLiftProof {
		t.Fatal("association must not be marked as marketing-lift proof")
	}
}

func TestNoCausalLiftWithoutExperiment(t *testing.T) {
	in := baseInput(t)
	in.Links = chainLinks()
	in.Events = []Event{{
		ID: "won-1", TenantID: "tnt_1", Kind: "won", LinkID: "opp", OccurredAt: at(t, "2026-01-20T00:00:00Z"),
	}}
	in.Costs = []CostRef{{
		ID: "prod", TenantID: "tnt_1", Kind: "production", Authority: "billing",
		AssetID: "asset-1", AmountCents: 10000, OccurredAt: at(t, "2026-01-02T00:00:00Z"),
	}}
	in.Revenues = []RevenueRef{{
		ID: "rev", TenantID: "tnt_1", Kind: "merchant_sales", Authority: "platform_verified",
		AmountCents: 20000, LinkID: "opp", OccurredAt: at(t, "2026-01-21T00:00:00Z"),
	}}
	rep, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CausalLiftAvailable || rep.CausalLift != nil {
		t.Fatalf("causal = %v %v", rep.CausalLiftAvailable, rep.CausalLift)
	}
	if rep.CausalReason == "" || !containsAll(rep.CausalReason, "没有实验") {
		t.Fatalf("reason = %s", rep.CausalReason)
	}
	if !containsAll(rep.Disclaimer, "可关联", "不是营销提升证明") {
		t.Fatalf("disclaimer = %s", rep.Disclaimer)
	}

	in.Experiment = &Experiment{ID: "exp-1", TenantID: "tnt_1"}
	rep, err = Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CausalLift != nil || rep.CausalLiftAvailable {
		t.Fatal("an experiment without a measured lift still is not a causal number")
	}

	lift := 0.12
	in.Experiment.MeasuredLift = &lift
	rep, err = Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.CausalLiftAvailable || rep.CausalLift == nil || math.Abs(*rep.CausalLift-0.12) > 1e-9 {
		t.Fatalf("cited lift = %v %v", rep.CausalLiftAvailable, rep.CausalLift)
	}
	if rep.CausalLiftSource != "experiment_citation" || rep.MarketingLiftProof {
		t.Fatalf("source=%s proof=%v", rep.CausalLiftSource, rep.MarketingLiftProof)
	}
}

func TestRevenuesStayOnSeparateLines(t *testing.T) {
	in := baseInput(t)
	in.Events = []Event{{
		ID: "won-1", TenantID: "tnt_1", Kind: "won", LinkID: "opp", OccurredAt: at(t, "2026-01-20T00:00:00Z"),
	}}
	in.Links = chainLinks()
	in.Costs = []CostRef{{
		ID: "prod", TenantID: "tnt_1", Kind: "production", Authority: "billing",
		AssetID: "asset-1", AmountCents: 10000, OccurredAt: at(t, "2026-01-02T00:00:00Z"),
	}}
	in.Revenues = []RevenueRef{
		{ID: "m", TenantID: "tnt_1", Kind: "merchant_sales", Authority: "manual", AmountCents: 11100, OccurredAt: at(t, "2026-01-21T00:00:00Z")},
		{ID: "p", TenantID: "tnt_1", Kind: "painuo_service_order", Authority: "platform_verified", AmountCents: 22200, OccurredAt: at(t, "2026-01-21T00:00:00Z")},
		{ID: "t", TenantID: "tnt_1", Kind: "platform_tool", Authority: "platform_verified", AmountCents: 33300, OccurredAt: at(t, "2026-01-21T00:00:00Z")},
	}
	rep, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if centsOf(t, findRevenue(t, rep, "merchant_sales", "manual").Cents) != 11100 {
		t.Fatal(rep.Revenues)
	}
	if centsOf(t, findRevenue(t, rep, "painuo_service_order", "platform_verified").Cents) != 22200 {
		t.Fatal(rep.Revenues)
	}
	if centsOf(t, findRevenue(t, rep, "platform_tool", "platform_verified").Cents) != 33300 {
		t.Fatal(rep.Revenues)
	}
	raw, _ := json.Marshal(rep)
	if containsAll(string(raw), "66600") {
		t.Fatalf("revenues were added together: %s", raw)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"total", "total_roi", "total_revenue_cents", "blended_roi"} {
		if _, ok := obj[banned]; ok {
			t.Fatalf("report has %s", banned)
		}
	}
}

func TestMissingFactsDoNotShowCompleteROI(t *testing.T) {
	onlySource := baseInput(t)
	onlySource.Links = chainLinks()
	onlySource.Events = []Event{{
		ID: "b", TenantID: "tnt_1", Kind: "browse", LinkID: "lead", OccurredAt: at(t, "2026-01-04T00:00:00Z"),
	}}
	rep, err := Recalculate(onlySource)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ROIStatus != StatusSourceStatsOnly || roiShown(rep) {
		t.Fatalf("source-only status=%s revenues=%+v", rep.ROIStatus, rep.Revenues)
	}

	withMoney := onlySource
	withMoney.Costs = []CostRef{{
		ID: "prod", TenantID: "tnt_1", Kind: "production", Authority: "billing",
		AssetID: "asset-1", AmountCents: 10000, OccurredAt: at(t, "2026-01-02T00:00:00Z"),
	}}
	withMoney.Revenues = []RevenueRef{{
		ID: "rev", TenantID: "tnt_1", Kind: "merchant_sales", Authority: "manual",
		AmountCents: 20000, OccurredAt: at(t, "2026-01-21T00:00:00Z"),
	}}
	rep, err = Recalculate(withMoney)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ROIStatus != StatusIncomplete || roiShown(rep) {
		t.Fatal("won missing must stay incomplete")
	}

	noPay := withMoney
	noPay.Events = append(noPay.Events, Event{
		ID: "won-1", TenantID: "tnt_1", Kind: "won", LinkID: "opp", OccurredAt: at(t, "2026-01-20T00:00:00Z"),
	})
	noPay.Revenues = nil
	rep, err = Recalculate(noPay)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ROIStatus != StatusIncomplete || roiShown(rep) {
		t.Fatal("payment missing must stay incomplete")
	}

	noCost := withMoney
	noCost.Costs = nil
	noCost.Events = append(noCost.Events, Event{
		ID: "won-1", TenantID: "tnt_1", Kind: "won", LinkID: "opp", OccurredAt: at(t, "2026-01-20T00:00:00Z"),
	})
	rep, err = Recalculate(noCost)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ROIStatus != StatusIncomplete || roiShown(rep) {
		t.Fatal("cost missing must stay incomplete")
	}

	full := withMoney
	full.Events = append(full.Events, Event{
		ID: "won-1", TenantID: "tnt_1", Kind: "won", LinkID: "opp", OccurredAt: at(t, "2026-01-20T00:00:00Z"),
	})
	rep, err = Recalculate(full)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ROIStatus != StatusComplete {
		t.Fatalf("status = %s", rep.ROIStatus)
	}
	line := findRevenue(t, rep, "merchant_sales", "manual")
	if line.ROI == nil || math.Abs(*line.ROI-2) > 1e-9 {
		t.Fatalf("roi = %v", line.ROI)
	}
	if findRevenue(t, rep, "painuo_service_order", "").ROI != nil || findRevenue(t, rep, "platform_tool", "").ROI != nil {
		t.Fatal("streams without payment must not borrow another stream's ROI")
	}
	if !containsAll(rep.FunnelReuse, "HUI-1694", "HUI-1695") {
		t.Fatalf("funnel reuse = %s", rep.FunnelReuse)
	}
	for _, banned := range []string{"lead_form", "profile_created", "exposure"} {
		for _, st := range rep.Stages {
			if st.Key == banned {
				t.Fatalf("third funnel stage %s", banned)
			}
		}
	}
}

func TestUnknownMetricsStayNull(t *testing.T) {
	in := baseInput(t)
	play := 10.0
	rate := 0.4
	likes := 4.0
	in.Events = []Event{
		{ID: "imp", TenantID: "tnt_1", Kind: "impression", Channel: "douyin", MetricKind: "impression", OccurredAt: at(t, "2026-01-03T00:00:00Z")},
		{ID: "like", TenantID: "tnt_1", Kind: "like", Channel: "douyin", MetricKind: "like", Metric: &likes, OccurredAt: at(t, "2026-01-03T01:00:00Z")},
		{ID: "play", TenantID: "tnt_1", Kind: "play", Channel: "wechat", MetricKind: "play", Metric: &play, OccurredAt: at(t, "2026-01-03T02:00:00Z")},
		{ID: "rate", TenantID: "tnt_1", Kind: "completion_rate", Channel: "douyin", MetricKind: "completion_rate", Metric: &rate, OccurredAt: at(t, "2026-01-03T03:00:00Z")},
	}
	rep, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	var sawUnknown bool
	for _, line := range rep.Metrics {
		if line.Kind == "impression" {
			if line.Value != nil {
				t.Fatalf("unknown impression filled as %v", *line.Value)
			}
			sawUnknown = true
		}
	}
	if !sawUnknown {
		t.Fatal("unknown impression line missing")
	}
	if len(rep.Metrics) != 4 {
		t.Fatalf("metrics collapsed: %+v", rep.Metrics)
	}
	raw, _ := json.Marshal(rep.Metrics)
	if containsAll(string(raw), "14") || containsAll(string(raw), "10.4") {
		t.Fatalf("metrics added: %s", raw)
	}
}

func TestProductionCostOnceAndAllocationConservation(t *testing.T) {
	in := baseInput(t)
	in.Links = chainLinks()
	in.Costs = []CostRef{
		{ID: "p2", TenantID: "tnt_1", Kind: "production", Authority: "billing", AssetID: "asset-1", CampaignID: "cmp-b", AmountCents: 8000, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "p1", TenantID: "tnt_1", Kind: "production", Authority: "billing", AssetID: "asset-1", CampaignID: "cmp-a", AmountCents: 8000, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "retry", TenantID: "tnt_1", Kind: "retry", Authority: "billing", AmountCents: 100, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "store", TenantID: "tnt_1", Kind: "storage", Authority: "billing", AmountCents: 20, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "chan", TenantID: "tnt_1", Kind: "channel", Authority: "ad_domain", AmountCents: 30, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "human", TenantID: "tnt_1", Kind: "human_support", Authority: "manual", AmountCents: 40, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "gen", TenantID: "tnt_1", Kind: "generation", Authority: "billing", AmountCents: 50, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "ad", TenantID: "tnt_1", Kind: "ad_spend", Authority: "ad_domain", AmountCents: 60, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "reward", TenantID: "tnt_1", Kind: "reward", Authority: "reward_domain", AmountCents: 70, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
	}
	before := in.Costs[0].AmountCents
	rep, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if in.Costs[0].AmountCents != before {
		t.Fatal("recalculate rewrote a cost fact")
	}
	if centsOf(t, rep.ProductionCents) != 8000 {
		t.Fatalf("production = %v", rep.ProductionCents)
	}
	seen := map[string]int64{}
	for _, line := range rep.Costs {
		seen[line.Kind] = centsOf(t, line.Cents)
	}
	for _, kind := range []string{"retry", "storage", "channel", "human_support", "generation", "ad_spend", "reward", "production"} {
		if seen[kind] == 0 {
			t.Fatalf("missing separate cost %s in %+v", kind, rep.Costs)
		}
	}
	if seen["production"] != 8000 || seen["retry"] != 100 {
		t.Fatalf("retry substituted production: %+v", seen)
	}

	conflict := in
	conflict.Costs = []CostRef{
		{ID: "a", TenantID: "tnt_1", Kind: "production", Authority: "billing", AssetID: "asset-1", AmountCents: 8000, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
		{ID: "b", TenantID: "tnt_1", Kind: "production", Authority: "billing", AssetID: "asset-1", AmountCents: 9000, OccurredAt: at(t, "2026-01-02T00:00:00Z")},
	}
	rep, err = Recalculate(conflict)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ProductionConflict || rep.ProductionCents != nil || rep.ROIStatus == StatusComplete {
		t.Fatalf("conflict report = %+v", rep)
	}

	split := baseInput(t)
	split.Costs = []CostRef{{
		ID: "p1", TenantID: "tnt_1", Kind: "production", Authority: "billing",
		AssetID: "asset-1", AmountCents: 10000, OccurredAt: at(t, "2026-01-02T00:00:00Z"),
	}}
	split.Allocations = []Allocation{
		{ID: "al1", TenantID: "tnt_1", CostID: "p1", CampaignID: "cmp-a", AmountCents: 4000, RuleVersion: "alloc-v1"},
		{ID: "al2", TenantID: "tnt_1", CostID: "p1", CampaignID: "cmp-b", AmountCents: 6000, RuleVersion: "alloc-v1"},
	}
	rep, err = Recalculate(split)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.AllocationConserved || centsOf(t, rep.ProductionCents) != 10000 {
		t.Fatalf("alloc report production=%v conserved=%v", rep.ProductionCents, rep.AllocationConserved)
	}
	if rep.AllocationRemainderCents == nil || *rep.AllocationRemainderCents != 0 || len(rep.Allocations) != 2 {
		t.Fatalf("allocations = %+v remainder=%v", rep.Allocations, rep.AllocationRemainderCents)
	}

	partial := split
	partial.Allocations = []Allocation{
		{ID: "al1", TenantID: "tnt_1", CostID: "p1", CampaignID: "cmp-a", AmountCents: 4000, RuleVersion: "alloc-v1"},
	}
	rep, err = Recalculate(partial)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.AllocationConserved || centsOf(t, rep.AllocationRemainderCents) != 6000 || centsOf(t, rep.ProductionCents) != 10000 {
		t.Fatalf("partial = production %v remainder %v", rep.ProductionCents, rep.AllocationRemainderCents)
	}

	over := split
	over.Allocations = []Allocation{
		{ID: "al1", TenantID: "tnt_1", CostID: "p1", CampaignID: "cmp-a", AmountCents: 7000, RuleVersion: "alloc-v1"},
		{ID: "al2", TenantID: "tnt_1", CostID: "p1", CampaignID: "cmp-b", AmountCents: 4000, RuleVersion: "alloc-v1"},
	}
	if _, err = Recalculate(over); !errors.Is(err, ErrAllocationExceeds) {
		t.Fatalf("err = %v", err)
	}
	if _, err = Recalculate(over); !errors.Is(err, ErrAllocationExceeds) {
		t.Fatal("exceeding allocation was not stable on recalculation")
	}

	missingRule := split
	missingRule.Allocations = []Allocation{
		{ID: "al1", TenantID: "tnt_1", CostID: "p1", CampaignID: "cmp-a", AmountCents: 1000},
	}
	if _, err = Recalculate(missingRule); !errors.Is(err, ErrAllocationRule) {
		t.Fatalf("err = %v", err)
	}
}

func TestRefundsDuplicatesLateEventsAndCrossTenant(t *testing.T) {
	in := baseInput(t)
	in.Links = chainLinks()
	in.Events = []Event{
		{ID: "b1", TenantID: "tnt_1", Kind: "browse", LinkID: "lead", OccurredAt: at(t, "2026-01-04T00:00:00Z")},
		{ID: "b1", TenantID: "tnt_1", Kind: "browse", LinkID: "lead", OccurredAt: at(t, "2026-01-04T00:00:00Z")},
		{ID: "won-1", TenantID: "tnt_1", Kind: "won", LinkID: "opp", OccurredAt: at(t, "2026-01-20T00:00:00Z")},
	}
	in.Costs = []CostRef{{
		ID: "prod", TenantID: "tnt_1", Kind: "production", Authority: "billing",
		AssetID: "asset-1", AmountCents: 10000, OccurredAt: at(t, "2026-01-02T00:00:00Z"),
	}}
	in.Revenues = []RevenueRef{{
		ID: "rev", TenantID: "tnt_1", Kind: "merchant_sales", Authority: "platform_verified",
		AmountCents: 30000, OccurredAt: at(t, "2026-01-21T00:00:00Z"),
	}}
	in.Refunds = []Refund{
		{ID: "rf1", TenantID: "tnt_1", TargetKind: "revenue", TargetID: "rev", AmountCents: 5000, OccurredAt: at(t, "2026-01-22T00:00:00Z")},
		{ID: "rf1", TenantID: "tnt_1", TargetKind: "revenue", TargetID: "rev", AmountCents: 5000, OccurredAt: at(t, "2026-01-22T00:00:00Z")},
	}
	rep, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if findStage(t, rep, "browse").Count == nil || *findStage(t, rep, "browse").Count != 1 {
		t.Fatalf("duplicate browse counted as %+v", findStage(t, rep, "browse"))
	}
	if centsOf(t, findRevenue(t, rep, "merchant_sales", "platform_verified").Cents) != 25000 {
		t.Fatalf("refund = %+v", rep.Revenues)
	}
	again, err := Recalculate(in)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(rep)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("recalculation diverged")
	}

	late := in
	late.Events = append(late.Events, Event{
		ID: "late", TenantID: "tnt_1", Kind: "first_processing", LinkID: "lead", OccurredAt: at(t, "2026-01-25T00:00:00Z"),
	})
	rep, err = Recalculate(late)
	if err != nil {
		t.Fatal(err)
	}
	st := findStage(t, rep, "first_processing")
	if st.Count == nil || *st.Count != 1 {
		t.Fatalf("late in-window event missing: %+v", st)
	}
	outside := in
	outside.Events = append(outside.Events, Event{
		ID: "early", TenantID: "tnt_1", Kind: "first_processing", LinkID: "lead", OccurredAt: at(t, "2025-12-01T00:00:00Z"),
	})
	rep, err = Recalculate(outside)
	if err != nil {
		t.Fatal(err)
	}
	if findStage(t, rep, "first_processing").Available {
		t.Fatal("event outside the window was counted")
	}

	lateRefund := in
	lateRefund.Refunds = []Refund{{
		ID: "rf-late", TenantID: "tnt_1", TargetKind: "revenue", TargetID: "rev", AmountCents: 1000, OccurredAt: at(t, "2026-03-01T00:00:00Z"),
	}}
	rep, err = Recalculate(lateRefund)
	if err != nil {
		t.Fatal(err)
	}
	if centsOf(t, findRevenue(t, rep, "merchant_sales", "platform_verified").Cents) != 30000 {
		t.Fatal("refund outside the window changed this window")
	}

	foreign := in
	foreign.Events = append(foreign.Events, Event{
		ID: "other", TenantID: "tnt_2", Kind: "paid", OccurredAt: at(t, "2026-01-08T00:00:00Z"),
	})
	if _, err = Recalculate(foreign); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("err = %v", err)
	}
	if _, err = Recalculate(foreign); !errors.Is(err, ErrCrossTenant) {
		t.Fatal("cross-tenant rejection was not stable")
	}
}

func roiShown(rep Report) bool {
	for _, line := range rep.Revenues {
		if line.ROI != nil {
			return true
		}
	}
	return false
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (s == part || len(s) > 0 && indexOf(s, part) >= 0))
}

func indexOf(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
