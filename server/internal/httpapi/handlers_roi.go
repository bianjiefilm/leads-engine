// HUI-1696 受限来源链复算。FEATURE_ROI 默认 off 时路由不注册。
// 请求体是引用，响应是复算结果。本处理器不写线索、费用或收入。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/roi"
)

type roiLinkIn struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	Kind       string `json:"kind"`
	ParentID   string `json:"parent_id"`
	Ref        string `json:"ref"`
	AssetID    string `json:"asset_id"`
	CampaignID string `json:"campaign_id"`
}

type roiEventIn struct {
	ID         string   `json:"id"`
	TenantID   string   `json:"tenant_id"`
	Kind       string   `json:"kind"`
	LinkID     string   `json:"link_id"`
	Channel    string   `json:"channel"`
	MetricKind string   `json:"metric_kind"`
	OccurredAt string   `json:"occurred_at"`
	Metric     *float64 `json:"metric"`
}

type roiCostIn struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Kind        string `json:"kind"`
	Authority   string `json:"authority"`
	AssetID     string `json:"asset_id"`
	CampaignID  string `json:"campaign_id"`
	AmountCents int64  `json:"amount_cents"`
	OccurredAt  string `json:"occurred_at"`
}

type roiRevenueIn struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Kind        string `json:"kind"`
	Authority   string `json:"authority"`
	LinkID      string `json:"link_id"`
	AmountCents int64  `json:"amount_cents"`
	OccurredAt  string `json:"occurred_at"`
}

type roiRefundIn struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	TargetKind  string `json:"target_kind"`
	TargetID    string `json:"target_id"`
	AmountCents int64  `json:"amount_cents"`
	OccurredAt  string `json:"occurred_at"`
}

type roiAllocationIn struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	CostID      string `json:"cost_id"`
	CampaignID  string `json:"campaign_id"`
	RuleVersion string `json:"rule_version"`
	AmountCents int64  `json:"amount_cents"`
}

type roiExperimentIn struct {
	ID           string   `json:"id"`
	TenantID     string   `json:"tenant_id"`
	MeasuredLift *float64 `json:"measured_lift"`
}

type roiRequest struct {
	BusinessCategory string            `json:"business_category"`
	WindowStart      string            `json:"window_start"`
	WindowEnd        string            `json:"window_end"`
	AttributionRule  string            `json:"attribution_rule"`
	Links            []roiLinkIn       `json:"links"`
	Events           []roiEventIn      `json:"events"`
	Costs            []roiCostIn       `json:"costs"`
	Revenues         []roiRevenueIn    `json:"revenues"`
	Refunds          []roiRefundIn     `json:"refunds"`
	Allocations      []roiAllocationIn `json:"allocations"`
	Experiment       *roiExperimentIn  `json:"experiment"`
}

func (s *Server) handleROIRecalculate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var body roiRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "roi citation bundle must be JSON")
		return
	}
	in, err := body.input(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "window_start and window_end must be RFC3339")
		return
	}
	rep, err := roi.Recalculate(in)
	if err != nil {
		writeROIError(w, err)
		return
	}
	s.Log.Printf("roi tenant=%s status=%s by=%s", c.Member.TenantID, rep.ROIStatus, c.Member.ID)
	writeJSON(w, http.StatusOK, rep)
}

func writeROIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, roi.ErrCrossTenant):
		fail(w, http.StatusForbidden, "cross_tenant", "cross-tenant citation refused")
	case errors.Is(err, roi.ErrAllocationExceeds):
		fail(w, http.StatusBadRequest, "allocation_exceeds", err.Error())
	case errors.Is(err, roi.ErrAllocationRule):
		fail(w, http.StatusBadRequest, "allocation_rule", err.Error())
	case errors.Is(err, roi.ErrAllocationTarget):
		fail(w, http.StatusBadRequest, "allocation_target", err.Error())
	default:
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

func (body roiRequest) input(tenant string) (roi.Input, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(body.WindowStart))
	if err != nil {
		return roi.Input{}, err
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(body.WindowEnd))
	if err != nil {
		return roi.Input{}, err
	}
	in := roi.Input{
		TenantID:         tenant,
		BusinessCategory: body.BusinessCategory,
		WindowStart:      start,
		WindowEnd:        end,
		AttributionRule:  body.AttributionRule,
	}
	for _, row := range body.Links {
		in.Links = append(in.Links, roi.Link{
			ID: row.ID, TenantID: citedTenant(tenant, row.TenantID), Kind: row.Kind,
			ParentID: row.ParentID, Ref: row.Ref, AssetID: row.AssetID, CampaignID: row.CampaignID,
		})
	}
	for _, row := range body.Events {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(row.OccurredAt))
		if err != nil {
			return roi.Input{}, err
		}
		in.Events = append(in.Events, roi.Event{
			ID: row.ID, TenantID: citedTenant(tenant, row.TenantID), Kind: row.Kind,
			LinkID: row.LinkID, Channel: row.Channel, MetricKind: row.MetricKind,
			OccurredAt: at, Metric: row.Metric,
		})
	}
	for _, row := range body.Costs {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(row.OccurredAt))
		if err != nil {
			return roi.Input{}, err
		}
		in.Costs = append(in.Costs, roi.CostRef{
			ID: row.ID, TenantID: citedTenant(tenant, row.TenantID), Kind: row.Kind,
			Authority: row.Authority, AssetID: row.AssetID, CampaignID: row.CampaignID,
			AmountCents: row.AmountCents, OccurredAt: at,
		})
	}
	for _, row := range body.Revenues {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(row.OccurredAt))
		if err != nil {
			return roi.Input{}, err
		}
		in.Revenues = append(in.Revenues, roi.RevenueRef{
			ID: row.ID, TenantID: citedTenant(tenant, row.TenantID), Kind: row.Kind,
			Authority: row.Authority, LinkID: row.LinkID, AmountCents: row.AmountCents, OccurredAt: at,
		})
	}
	for _, row := range body.Refunds {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(row.OccurredAt))
		if err != nil {
			return roi.Input{}, err
		}
		in.Refunds = append(in.Refunds, roi.Refund{
			ID: row.ID, TenantID: citedTenant(tenant, row.TenantID), TargetKind: row.TargetKind,
			TargetID: row.TargetID, AmountCents: row.AmountCents, OccurredAt: at,
		})
	}
	for _, row := range body.Allocations {
		in.Allocations = append(in.Allocations, roi.Allocation{
			ID: row.ID, TenantID: citedTenant(tenant, row.TenantID), CostID: row.CostID,
			CampaignID: row.CampaignID, RuleVersion: row.RuleVersion, AmountCents: row.AmountCents,
		})
	}
	if body.Experiment != nil {
		in.Experiment = &roi.Experiment{
			ID: body.Experiment.ID, TenantID: citedTenant(tenant, body.Experiment.TenantID),
			MeasuredLift: body.Experiment.MeasuredLift,
		}
	}
	return in, nil
}

func citedTenant(caller, cited string) string {
	if strings.TrimSpace(cited) == "" {
		return caller
	}
	return cited
}
