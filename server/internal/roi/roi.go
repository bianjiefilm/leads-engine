// Package roi recalculates restricted source-chain statistics from cited
// facts (HUI-1696). It does not store or rewrite leads, costs, or revenues.
// Association is not a causal claim. Marketing-lift proof stays false.
package roi

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	RuleRestrictedSourceChainV1 = "restricted_source_chain_v1"
	StatusSourceStatsOnly       = "source_stats_only"
	StatusIncomplete            = "incomplete"
	StatusComplete              = "complete"

	disclaimer = "可关联不等于证明内容导致成交。没有实验引用就不写因果提升。本报告不是营销提升证明。"
	funnelNote = "漏斗阶段数字复用 HUI-1694 / HUI-1695，本报告不另算第三套漏斗。"
	roiRule    = "roi = 该收入口径净额 ÷ 已引用净成本；各口径不可相加。缺少成交、收款或成本时不给出 ROI。"

	causalNone       = "没有实验，不从可关联关系推算因果提升。"
	causalUnmeasured = "实验未给出可引用的提升测量，不写因果提升。"
	causalCited      = "实验引用的提升测量，不是由线索关联推算，也不是营销提升证明。"
	stageUnknown     = "引用里没有该阶段事件，未知不填 0。"
)

var (
	ErrCrossTenant       = errors.New("roi: cross-tenant fact refused")
	ErrAllocationExceeds = errors.New("roi: allocation sum exceeds original cost")
	ErrAllocationRule    = errors.New("roi: allocation requires rule version")
	ErrWindow            = errors.New("roi: window_end must be after window_start")
	ErrCategory          = errors.New("roi: business_category required")
	ErrTenant            = errors.New("roi: tenant required")
	ErrAttributionRule   = errors.New("roi: attribution rule must be restricted_source_chain_v1")
	ErrKind              = errors.New("roi: unknown kind")
	ErrAllocationTarget  = errors.New("roi: allocation target must be a counted production cost")
)

// Input is a citation bundle. Amounts are read, never written back.
type Input struct {
	TenantID         string
	BusinessCategory string
	WindowStart      time.Time
	WindowEnd        time.Time
	AttributionRule  string
	Links            []Link
	Events           []Event
	Costs            []CostRef
	Revenues         []RevenueRef
	Refunds          []Refund
	Allocations      []Allocation
	Experiment       *Experiment
}

type Link struct {
	ID, TenantID, Kind, ParentID, Ref, AssetID, CampaignID string
}

type Event struct {
	ID, TenantID, Kind, LinkID, Channel, MetricKind string
	OccurredAt                                      time.Time
	Metric                                          *float64
}

type CostRef struct {
	ID, TenantID, Kind, Authority, AssetID, CampaignID string
	AmountCents                                        int64
	OccurredAt                                         time.Time
}

type RevenueRef struct {
	ID, TenantID, Kind, Authority, LinkID string
	AmountCents                           int64
	OccurredAt                            time.Time
}

type Refund struct {
	ID, TenantID, TargetKind, TargetID string
	AmountCents                        int64
	OccurredAt                         time.Time
}

type Allocation struct {
	ID, TenantID, CostID, CampaignID, RuleVersion string
	AmountCents                                   int64
}

type Experiment struct {
	ID, TenantID string
	MeasuredLift *float64
}

type ChainNode struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	ParentID   string `json:"parent_id,omitempty"`
	Ref        string `json:"ref,omitempty"`
	AssetID    string `json:"asset_id,omitempty"`
	CampaignID string `json:"campaign_id,omitempty"`
	Gap        bool   `json:"gap"`
}

type MetricLine struct {
	EventID  string   `json:"event_id"`
	Channel  string   `json:"channel"`
	Kind     string   `json:"kind"`
	Value    *float64 `json:"value"`
	SampleAt string   `json:"sample_at"`
}

type StageLine struct {
	Key       string `json:"key"`
	Available bool   `json:"available"`
	Count     *int   `json:"count"`
	Reason    string `json:"reason,omitempty"`
}

type RevenueLine struct {
	Kind      string   `json:"kind"`
	Authority string   `json:"authority"`
	Cents     *int64   `json:"cents"`
	ROI       *float64 `json:"roi"`
}

type CostLine struct {
	Kind      string `json:"kind"`
	Authority string `json:"authority"`
	Cents     *int64 `json:"cents"`
}

type AllocationLine struct {
	CostID      string `json:"cost_id"`
	CampaignID  string `json:"campaign_id"`
	AmountCents int64  `json:"amount_cents"`
	RuleVersion string `json:"rule_version"`
}

type Report struct {
	TenantID                 string           `json:"tenant_id"`
	BusinessCategory         string           `json:"business_category"`
	WindowStart              string           `json:"window_start"`
	WindowEnd                string           `json:"window_end"`
	AttributionRule          string           `json:"attribution_rule"`
	Disclaimer               string           `json:"disclaimer"`
	MarketingLiftProof       bool             `json:"marketing_lift_proof"`
	FunnelReuse              string           `json:"funnel_reuse"`
	UnassociatedRatio        *float64         `json:"unassociated_ratio"`
	UnassociatedCount        int              `json:"unassociated_count"`
	AssociatedCount          int              `json:"associated_count"`
	CausalLiftAvailable      bool             `json:"causal_lift_available"`
	CausalLift               *float64         `json:"causal_lift"`
	CausalLiftSource         string           `json:"causal_lift_source,omitempty"`
	CausalReason             string           `json:"causal_reason"`
	SourceChain              []ChainNode      `json:"source_chain"`
	Metrics                  []MetricLine     `json:"metrics"`
	Stages                   []StageLine      `json:"stages"`
	Revenues                 []RevenueLine    `json:"revenues"`
	Costs                    []CostLine       `json:"costs"`
	ProductionCents          *int64           `json:"production_cents"`
	ProductionConflict       bool             `json:"production_conflict"`
	Allocations              []AllocationLine `json:"allocations"`
	AllocationRemainderCents *int64           `json:"allocation_remainder_cents"`
	AllocationConserved      bool             `json:"allocation_conserved"`
	ROIStatus                string           `json:"roi_status"`
	ROIRule                  string           `json:"roi_rule"`
}

var (
	linkRank = map[string]int{
		"source": 0, "campaign": 1, "store": 2, "tag": 3,
		"asset_version": 4, "submission": 5, "lead": 6, "opportunity": 7,
	}
	stageKeys  = []string{"browse", "authorized_lead", "first_processing", "won", "paid"}
	metricKind = map[string]bool{"impression": true, "like": true, "play": true, "completion_rate": true}
	eventKind  = map[string]bool{
		"browse": true, "authorized_lead": true, "first_processing": true, "won": true, "paid": true,
		"impression": true, "like": true, "play": true, "completion_rate": true,
		"submission": true, "lead": true, "opportunity": true,
	}
	costKind = map[string]bool{
		"production": true, "ad_spend": true, "reward": true, "generation": true,
		"storage": true, "channel": true, "human_support": true, "retry": true,
	}
	revenueKind = map[string]bool{
		"merchant_sales": true, "painuo_service_order": true, "platform_tool": true,
	}
	revenueOrder = []string{"merchant_sales", "painuo_service_order", "platform_tool"}
	costAuth     = map[string]bool{
		"billing": true, "ad_domain": true, "reward_domain": true, "manual": true, "platform_verified": true,
	}
	revenueAuth = map[string]bool{"manual": true, "platform_verified": true}
)

type countedCost struct {
	id, kind, authority, asset string
	original, net              int64
}

// Recalculate is a pure function of the citation bundle and the window.
// The same input always yields the same report or the same refusal.
func Recalculate(in Input) (Report, error) {
	if strings.TrimSpace(in.TenantID) == "" {
		return Report{}, ErrTenant
	}
	if strings.TrimSpace(in.BusinessCategory) == "" {
		return Report{}, ErrCategory
	}
	if !in.WindowEnd.After(in.WindowStart) {
		return Report{}, ErrWindow
	}
	rule := in.AttributionRule
	if rule == "" {
		rule = RuleRestrictedSourceChainV1
	}
	if rule != RuleRestrictedSourceChainV1 {
		return Report{}, ErrAttributionRule
	}
	if err := refuseForeign(in); err != nil {
		return Report{}, err
	}
	if err := knownKinds(in); err != nil {
		return Report{}, err
	}

	rep := Report{
		TenantID:            in.TenantID,
		BusinessCategory:    in.BusinessCategory,
		WindowStart:         in.WindowStart.UTC().Format(time.RFC3339),
		WindowEnd:           in.WindowEnd.UTC().Format(time.RFC3339),
		AttributionRule:     rule,
		Disclaimer:          disclaimer,
		MarketingLiftProof:  false,
		FunnelReuse:         funnelNote,
		ROIRule:             roiRule,
		SourceChain:         []ChainNode{},
		Metrics:             []MetricLine{},
		Stages:              []StageLine{},
		Revenues:            []RevenueLine{},
		Costs:               []CostLine{},
		Allocations:         []AllocationLine{},
		AllocationConserved: true,
	}
	applyExperiment(&rep, in.Experiment)

	links := map[string]Link{}
	var chain []Link
	for _, link := range in.Links {
		if _, ok := links[link.ID]; ok {
			continue
		}
		links[link.ID] = link
		chain = append(chain, link)
	}
	for _, link := range chain {
		rep.SourceChain = append(rep.SourceChain, ChainNode{
			ID: link.ID, Kind: link.Kind, ParentID: link.ParentID, Ref: link.Ref,
			AssetID: link.AssetID, CampaignID: link.CampaignID,
			Gap: link.ParentID != "" && !linkKnown(links, link.ParentID),
		})
	}

	events := dedupeEvents(in.Events, in.WindowStart, in.WindowEnd)
	stageCount := map[string]int{}
	for _, ev := range events {
		if _, ok := links[ev.LinkID]; ok && ev.LinkID != "" {
			rep.AssociatedCount++
		} else {
			rep.UnassociatedCount++
		}
		if slices.Contains(stageKeys, ev.Kind) {
			stageCount[ev.Kind]++
		}
		if metricKind[ev.Kind] || metricKind[ev.MetricKind] {
			kind := ev.MetricKind
			if kind == "" {
				kind = ev.Kind
			}
			rep.Metrics = append(rep.Metrics, MetricLine{
				EventID: ev.ID, Channel: ev.Channel, Kind: kind,
				Value: copyFloat(ev.Metric), SampleAt: ev.OccurredAt.UTC().Format(time.RFC3339),
			})
		}
	}
	sort.SliceStable(rep.Metrics, func(i, j int) bool { return rep.Metrics[i].EventID < rep.Metrics[j].EventID })
	totalEvents := rep.AssociatedCount + rep.UnassociatedCount
	if totalEvents > 0 {
		ratio := float64(rep.UnassociatedCount) / float64(totalEvents)
		rep.UnassociatedRatio = &ratio
	}
	for _, key := range stageKeys {
		n := stageCount[key]
		if n == 0 {
			rep.Stages = append(rep.Stages, StageLine{Key: key, Reason: stageUnknown})
			continue
		}
		count := n
		rep.Stages = append(rep.Stages, StageLine{Key: key, Available: true, Count: &count})
	}

	costs, conflict, err := countCosts(in.Costs, in.WindowStart, in.WindowEnd)
	if err != nil {
		return Report{}, err
	}
	rep.ProductionConflict = conflict
	refundBlocked := applyRefunds(costs, nil, in.Refunds, in.WindowStart, in.WindowEnd)
	revenues := countRevenues(in.Revenues, in.WindowStart, in.WindowEnd)
	refundBlocked = applyRefunds(nil, revenues, in.Refunds, in.WindowStart, in.WindowEnd) || refundBlocked

	_, allocs, remainder, err := allocationsOf(in.Allocations, costs)
	if err != nil {
		return Report{}, err
	}
	rep.Allocations = allocs
	rep.AllocationRemainderCents = remainder
	rep.AllocationConserved = true

	var production int64
	var sawProduction bool
	var netCost int64
	costKnown := !conflict && !refundBlocked
	grouped := map[string]*CostLine{}
	var groupOrder []string
	for _, cost := range costs {
		if cost.authority == "" || !costAuth[cost.authority] {
			costKnown = false
		}
		if cost.kind == "production" {
			production += cost.net
			sawProduction = true
		}
		netCost += cost.net
		key := cost.kind + "\x00" + cost.authority
		line, ok := grouped[key]
		if !ok {
			cents := cost.net
			line = &CostLine{Kind: cost.kind, Authority: cost.authority, Cents: &cents}
			grouped[key] = line
			groupOrder = append(groupOrder, key)
			continue
		}
		*line.Cents += cost.net
	}
	sort.SliceStable(groupOrder, func(i, j int) bool { return groupOrder[i] < groupOrder[j] })
	for _, key := range groupOrder {
		rep.Costs = append(rep.Costs, *grouped[key])
	}
	if sawProduction && !conflict {
		rep.ProductionCents = &production
	}
	if conflict {
		costKnown = false
	}

	revGrouped := map[string]*RevenueLine{}
	var revOrder []string
	for _, rev := range revenues {
		if rev.authority == "" || !revenueAuth[rev.authority] {
			refundBlocked = true
		}
		key := rev.kind + "\x00" + rev.authority
		line, ok := revGrouped[key]
		if !ok {
			cents := rev.net
			line = &RevenueLine{Kind: rev.kind, Authority: rev.authority, Cents: &cents}
			revGrouped[key] = line
			revOrder = append(revOrder, key)
			continue
		}
		*line.Cents += rev.net
	}
	sort.SliceStable(revOrder, func(i, j int) bool { return revOrder[i] < revOrder[j] })
	presentKind := map[string]bool{}
	for _, key := range revOrder {
		line := revGrouped[key]
		rep.Revenues = append(rep.Revenues, *line)
		presentKind[line.Kind] = true
	}
	for _, kind := range revenueOrder {
		if !presentKind[kind] {
			rep.Revenues = append(rep.Revenues, RevenueLine{Kind: kind})
		}
	}
	sort.SliceStable(rep.Revenues, func(i, j int) bool {
		if rankOf(revenueOrder, rep.Revenues[i].Kind) != rankOf(revenueOrder, rep.Revenues[j].Kind) {
			return rankOf(revenueOrder, rep.Revenues[i].Kind) < rankOf(revenueOrder, rep.Revenues[j].Kind)
		}
		return rep.Revenues[i].Authority < rep.Revenues[j].Authority
	})

	won := stageCount["won"] > 0
	hasMoney := len(costs) > 0 || len(revenues) > 0
	var paid bool
	for _, line := range rep.Revenues {
		if line.Cents != nil && *line.Cents > 0 {
			paid = true
		}
	}
	switch {
	case !hasMoney:
		rep.ROIStatus = StatusSourceStatsOnly
	case !won || !paid || !costKnown || netCost <= 0 || refundBlocked:
		rep.ROIStatus = StatusIncomplete
	default:
		rep.ROIStatus = StatusComplete
		for i := range rep.Revenues {
			line := &rep.Revenues[i]
			if line.Cents == nil || *line.Cents <= 0 {
				continue
			}
			ratio := float64(*line.Cents) / float64(netCost)
			line.ROI = &ratio
		}
	}
	return rep, nil
}

func applyExperiment(rep *Report, exp *Experiment) {
	rep.CausalReason = causalNone
	if exp == nil {
		return
	}
	if exp.MeasuredLift == nil {
		rep.CausalReason = causalUnmeasured
		return
	}
	lift := *exp.MeasuredLift
	rep.CausalLift = &lift
	rep.CausalLiftAvailable = true
	rep.CausalLiftSource = "experiment_citation"
	rep.CausalReason = causalCited
}

func refuseForeign(in Input) error {
	check := func(tenant string) error {
		if tenant != in.TenantID {
			return ErrCrossTenant
		}
		return nil
	}
	for _, row := range in.Links {
		if err := check(row.TenantID); err != nil {
			return err
		}
	}
	for _, row := range in.Events {
		if err := check(row.TenantID); err != nil {
			return err
		}
	}
	for _, row := range in.Costs {
		if err := check(row.TenantID); err != nil {
			return err
		}
	}
	for _, row := range in.Revenues {
		if err := check(row.TenantID); err != nil {
			return err
		}
	}
	for _, row := range in.Refunds {
		if err := check(row.TenantID); err != nil {
			return err
		}
	}
	for _, row := range in.Allocations {
		if err := check(row.TenantID); err != nil {
			return err
		}
	}
	if in.Experiment != nil {
		if err := check(in.Experiment.TenantID); err != nil {
			return err
		}
	}
	return nil
}

func knownKinds(in Input) error {
	for _, row := range in.Links {
		if _, ok := linkRank[row.Kind]; !ok {
			return ErrKind
		}
	}
	for _, row := range in.Events {
		if !eventKind[row.Kind] {
			return ErrKind
		}
		if row.MetricKind != "" && !metricKind[row.MetricKind] {
			return ErrKind
		}
	}
	for _, row := range in.Costs {
		if !costKind[row.Kind] || (row.Authority != "" && !costAuth[row.Authority]) {
			return ErrKind
		}
	}
	for _, row := range in.Revenues {
		if !revenueKind[row.Kind] || (row.Authority != "" && !revenueAuth[row.Authority]) {
			return ErrKind
		}
	}
	for _, row := range in.Refunds {
		if row.TargetKind != "cost" && row.TargetKind != "revenue" {
			return ErrKind
		}
	}
	return nil
}

func linkKnown(links map[string]Link, id string) bool {
	_, ok := links[id]
	return ok
}

func inWindow(at, start, end time.Time) bool {
	return !at.Before(start) && at.Before(end)
}

func dedupeEvents(events []Event, start, end time.Time) []Event {
	seen := map[string]bool{}
	var out []Event
	for _, ev := range events {
		if !inWindow(ev.OccurredAt, start, end) || seen[ev.ID] || ev.ID == "" {
			continue
		}
		seen[ev.ID] = true
		out = append(out, ev)
	}
	return out
}

func countCosts(costs []CostRef, start, end time.Time) ([]*countedCost, bool, error) {
	seen := map[string]bool{}
	var rows []*countedCost
	for _, cost := range costs {
		if !inWindow(cost.OccurredAt, start, end) || seen[cost.ID] || cost.ID == "" {
			continue
		}
		seen[cost.ID] = true
		rows = append(rows, &countedCost{
			id: cost.ID, kind: cost.Kind, authority: cost.Authority, asset: cost.AssetID,
			original: cost.AmountCents, net: cost.AmountCents,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	byAsset := map[string][]*countedCost{}
	var assets []string
	for _, row := range rows {
		if row.kind != "production" {
			continue
		}
		if _, ok := byAsset[row.asset]; !ok {
			assets = append(assets, row.asset)
		}
		byAsset[row.asset] = append(byAsset[row.asset], row)
	}
	drop := map[string]bool{}
	conflict := false
	for _, asset := range assets {
		group := byAsset[asset]
		if asset == "" {
			conflict = true
			for _, row := range group {
				drop[row.id] = true
			}
			continue
		}
		amount := group[0].original
		same := true
		for _, row := range group[1:] {
			if row.original != amount {
				same = false
			}
		}
		if !same {
			conflict = true
			for _, row := range group {
				drop[row.id] = true
			}
			continue
		}
		for _, row := range group[1:] {
			drop[row.id] = true
		}
	}
	var kept []*countedCost
	for _, row := range rows {
		if drop[row.id] {
			continue
		}
		kept = append(kept, row)
	}
	return kept, conflict, nil
}

type countedRev struct {
	id, kind, authority string
	net                 int64
}

func countRevenues(rows []RevenueRef, start, end time.Time) []*countedRev {
	seen := map[string]bool{}
	var out []*countedRev
	for _, row := range rows {
		if !inWindow(row.OccurredAt, start, end) || seen[row.ID] || row.ID == "" {
			continue
		}
		seen[row.ID] = true
		out = append(out, &countedRev{id: row.ID, kind: row.Kind, authority: row.Authority, net: row.AmountCents})
	}
	return out
}

func applyRefunds(costs []*countedCost, revenues []*countedRev, refunds []Refund, start, end time.Time) bool {
	seen := map[string]bool{}
	blocked := false
	costByID := map[string]*countedCost{}
	for _, cost := range costs {
		costByID[cost.id] = cost
	}
	revByID := map[string]*countedRev{}
	for _, rev := range revenues {
		revByID[rev.id] = rev
	}
	for _, refund := range refunds {
		if !inWindow(refund.OccurredAt, start, end) || seen[refund.ID] || refund.ID == "" {
			continue
		}
		seen[refund.ID] = true
		switch refund.TargetKind {
		case "cost":
			if costs == nil {
				continue
			}
			target, ok := costByID[refund.TargetID]
			if !ok {
				blocked = true
				continue
			}
			if refund.AmountCents > target.net {
				target.net = 0
				blocked = true
				continue
			}
			target.net -= refund.AmountCents
		case "revenue":
			if revenues == nil {
				continue
			}
			target, ok := revByID[refund.TargetID]
			if !ok {
				blocked = true
				continue
			}
			if refund.AmountCents > target.net {
				target.net = 0
				blocked = true
				continue
			}
			target.net -= refund.AmountCents
		}
	}
	return blocked
}

func allocationsOf(rows []Allocation, costs []*countedCost) (map[string]int64, []AllocationLine, *int64, error) {
	counted := map[string]*countedCost{}
	for _, cost := range costs {
		if cost.kind == "production" {
			counted[cost.id] = cost
		}
	}
	seen := map[string]bool{}
	used := map[string]int64{}
	var lines []AllocationLine
	for _, row := range rows {
		if seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		if strings.TrimSpace(row.RuleVersion) == "" {
			return nil, nil, nil, ErrAllocationRule
		}
		target, ok := counted[row.CostID]
		if !ok || target.kind != "production" {
			return nil, nil, nil, ErrAllocationTarget
		}
		if row.AmountCents < 0 {
			return nil, nil, nil, ErrAllocationExceeds
		}
		used[row.CostID] += row.AmountCents
		if used[row.CostID] > target.original {
			return nil, nil, nil, ErrAllocationExceeds
		}
		lines = append(lines, AllocationLine{
			CostID: row.CostID, CampaignID: row.CampaignID,
			AmountCents: row.AmountCents, RuleVersion: row.RuleVersion,
		})
	}
	if len(lines) == 0 {
		return used, []AllocationLine{}, nil, nil
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].CostID != lines[j].CostID {
			return lines[i].CostID < lines[j].CostID
		}
		return lines[i].CampaignID < lines[j].CampaignID
	})
	var remainder int64
	for id, amount := range used {
		remainder += counted[id].original - amount
	}
	return used, lines, &remainder, nil
}

func copyFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

func rankOf(order []string, kind string) int {
	for i, item := range order {
		if item == kind {
			return i
		}
	}
	return len(order)
}
