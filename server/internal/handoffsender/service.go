// service.go — the confirmation orchestration: fingerprint idempotency,
// version progression, delivery bookkeeping and the revoke guard.
//
// 幂等四边缘(发送端侧,与 O2 接收端四边缘对偶):
//
//	① 双击/并发同确认 → 同指纹命中同一快照 → 同 handoff_id 同引用;
//	② 投递失败/超时 → 快照保留(local_status=delivery_failed)→ retry 原样
//	   重发同字节 → 接收端 200 duplicate=true 同草稿引用;
//	③ 内容变更 → 新指纹 → 新版本新 handoff_id(新快照走新确认,绝不覆盖
//	   已确认内容);旧版本显式 superseded,不可静默重发;
//	④ 撤销守卫:接单侧 status=draft(未接受)才可撤;非 draft → 明确引导
//	   走接单侧变更流程;接单侧不可达 → fail-closed 拒绝盲撤。
package handoffsender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/appregistry"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// Handoff local statuses (migration 0004 CHECK).
const (
	StatusConfirmed      = "confirmed"
	StatusDelivered      = "delivered"
	StatusDeliveryFailed = "delivery_failed"
	StatusRevoked        = "revoked"
)

// Service-level errors (typed, handler-mappable).
var (
	// ErrSuperseded: an older snapshot must not be silently re-delivered.
	ErrSuperseded = errors.New("handoffsender: snapshot superseded by a newer confirmation")
	// ErrAlreadyRevoked: a revoked handoff must not be re-delivered or
	// re-confirmed into; the user starts a fresh confirmation.
	ErrAlreadyRevoked = errors.New("handoffsender: handoff is revoked")
	// ErrAcceptedByTarget: the target left draft status; changes go through
	// the target's own change flow (never a silent overwrite).
	ErrAcceptedByTarget = errors.New("handoffsender: accepted by target; change via the target product flow")
	// ErrCannotConfirmTargetState: fail-closed revoke guard.
	ErrCannotConfirmTargetState = errors.New("handoffsender: cannot confirm target state (fail-closed)")
	// ErrNotConfigured: the eco handoff deployment facts are missing.
	ErrNotConfigured = errors.New("handoffsender: eco handoff is not configured")
	// ErrHandoffNotFound: no snapshot for the requested reference.
	ErrHandoffNotFound = errors.New("handoffsender: no handoff snapshot")
)

// Config carries the deployment-injected eco handoff facts. Nothing here is
// client-supplied, ever.
type Config struct {
	// TargetAppID is the receiver app id (validated against the registry).
	TargetAppID string
	// TenantScope is the deployment-co-configured exact scope value.
	TenantScope string
	// IntakeURL / Token configure the delivery channel.
	IntakeURL string
	Token     string
	// ProofSalt optionally salts the PROVISIONAL binding digest.
	ProofSalt string
}

// Complete reports whether every required deployment fact is present.
func (c Config) Complete() bool {
	return c.TargetAppID != "" && c.TenantScope != "" && c.IntakeURL != "" && c.Token != ""
}

// Service orchestrates confirm/retry/refresh/revoke over the store and the
// receiver delivery client.
type Service struct {
	St       *store.Store
	Registry *appregistry.Registry
	Deliver  *Deliverer
	Cfg      Config
	// Now is the clock seam (tests pin it; nil = UTC now).
	Now func() time.Time
	// NewID mints the stable handoff id (seam for deterministic tests; nil =
	// random hex).
	NewID func() (string, error)
}

// NewService wires the deployment facts into a ready Service. The deliverer
// is built from the same config (URL/token never come from requests).
func NewService(st *store.Store, reg *appregistry.Registry, cfg Config) *Service {
	return &Service{
		St:       st,
		Registry: reg,
		Deliver:  &Deliverer{IntakeURL: cfg.IntakeURL, Token: cfg.Token},
		Cfg:      cfg,
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// ConfirmResult reports one confirm/retry outcome. Delivered=false with a
// snapshot still means the snapshot was persisted (delivery can be retried).
type ConfirmResult struct {
	Handoff   *store.OpportunityHandoff
	Duplicate bool   // same fingerprint snapshot already existed
	Delivered bool   // this call completed a delivery
	Note      string // human-readable outcome note (guarded copy)
}

// Confirm runs the two-step contract: fingerprint lookup → idempotent reuse
// or new-version snapshot → build+validate → persist → deliver.
func (s *Service) Confirm(ctx context.Context, in ConfirmedInput, principalID, actorIssuer, confirmedBy string) (*ConfirmResult, error) {
	if !s.Cfg.Complete() {
		return nil, ErrNotConfigured
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	// Registry facts are the single source of identity truth.
	self, ok := s.Registry.Self()
	if !ok || !self.Enabled {
		return nil, fmt.Errorf("%w: self app is not registered/enabled", ErrNotConfigured)
	}
	if !s.Registry.HasCapability(self.AppID, CapabilityHandoff) {
		return nil, fmt.Errorf("%w: capability %s is not registered for %s", ErrNotConfigured, CapabilityHandoff, self.AppID)
	}
	if _, ok := s.Registry.App(s.Cfg.TargetAppID); !ok {
		return nil, fmt.Errorf("target app %s is not registered in the app registry", s.Cfg.TargetAppID)
	}
	if _, ok := s.Registry.ResolveTarget(self.AppID, ReturnTargetID, "receipt"); !ok {
		return nil, fmt.Errorf("return target %s is not registered for %s", ReturnTargetID, self.AppID)
	}
	if !TenantScopeMatches(s.Cfg.TenantScope, s.Cfg.TenantScope) {
		// Unreachable by construction (both sides same value); kept as the
		// explicit fail-closed statement that an empty scope never ships.
		return nil, fmt.Errorf("tenant_scope must be non-empty (fail-closed)")
	}

	fingerprint, err := in.Fingerprint()
	if err != nil {
		return nil, err
	}

	// ① 指纹命中 → 幂等复用(不新建、不覆盖、不改字节)。
	existing, err := s.St.OpportunityHandoffByFingerprint(in.OpportunityID, tenantOf(in), fingerprint)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.LocalStatus == StatusRevoked {
			return nil, ErrAlreadyRevoked
		}
		res := &ConfirmResult{Handoff: existing, Duplicate: true}
		if existing.LocalStatus != StatusDelivered {
			err := s.deliver(ctx, existing)
			switch {
			case err == nil:
				updated, uerr := s.St.OpportunityHandoffByHandoffID(existing.HandoffID, existing.TenantID)
				if uerr != nil {
					return nil, uerr
				}
				res.Handoff = updated
				res.Delivered = true
				return res, nil
			case errors.Is(err, ErrTransport):
				res.Note = "已恢复既有交接快照;投递暂未完成,可重试(同一 handoff 引用)。"
				return res, nil
			default:
				return nil, err
			}
		}
		res.Note = "同一内容已确认过:返回既有交接引用(幂等,未新建)。"
		return res, nil
	}

	// ② 新内容 → 新版本新快照(新确认新 handoff_id)。
	version, err := s.St.NextOpportunityHandoffVersion(in.OpportunityID, tenantOf(in))
	if err != nil {
		return nil, err
	}
	newID, err := s.newHandoffID()
	if err != nil {
		return nil, err
	}
	doc, err := BuildDocument(in, fingerprint, newID, version, BuildParams{
		SourceApp:   self.AppID,
		TargetApp:   s.Cfg.TargetAppID,
		TenantScope: s.Cfg.TenantScope,
		PrincipalID: principalID,
		ActorIssuer: actorIssuer,
		ProofSalt:   s.Cfg.ProofSalt,
		Now:         s.now(),
	})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(doc)
	row := store.OpportunityHandoff{
		ID:             "ohf_" + newID[len("handoff-"):],
		OpportunityID:  in.OpportunityID,
		SourceVersion:  version,
		Fingerprint:    fingerprint,
		HandoffID:      newID,
		DocJSON:        string(doc),
		DocSHA256:      hex.EncodeToString(sum[:]),
		SourceRevision: fingerprint,
		PrincipalID:    principalID,
		ActorIssuer:    actorIssuer,
		ActorSubject:   principalID,
		TargetApp:      s.Cfg.TargetAppID,
		ReturnTargetID: ReturnTargetID,
		LocalStatus:    StatusConfirmed,
		ConfirmedBy:    confirmedBy,
	}
	row.ConfirmedAt = s.now().Format(time.RFC3339Nano)
	row.UpdatedAt = row.ConfirmedAt
	// TenantID is set by the caller context (the opportunity's tenant); the
	// store layer requires it scoped — taken from the confirmed opportunity.
	row.TenantID = tenantOf(in)
	if err := s.St.InsertOpportunityHandoff(row); err != nil {
		// 并发双击撞 UNIQUE(opportunity_id, fingerprint):输方回读既有快照,
		// 返回同引用(原子幂等;SQLite 单写者下输方必已提交)。
		loser, gerr := s.St.OpportunityHandoffByFingerprint(in.OpportunityID, tenantOf(in), fingerprint)
		if gerr == nil && loser != nil {
			return &ConfirmResult{Handoff: loser, Duplicate: true,
				Note: "同一内容已由并行确认建立:返回同一交接引用(幂等)。"}, nil
		}
		return nil, err
	}
	res := &ConfirmResult{Handoff: &row}
	err = s.deliver(ctx, &row)
	switch {
	case err == nil:
		updated, uerr := s.St.OpportunityHandoffByHandoffID(newID, row.TenantID)
		if uerr != nil {
			return nil, uerr
		}
		res.Handoff = updated
		res.Delivered = true
		return res, nil
	case errors.Is(err, ErrTransport):
		// 快照已保存,投递可重试:确认事实不丢失(票面:目标暂不可用→可保存
		// 草稿+重试)。
		res.Note = "交接快照已保存;投递暂未完成,可重试(同一 handoff 引用),确认事实不丢失。"
		return res, nil
	default:
		return nil, err
	}
}

// Retry re-delivers the LATEST snapshot's exact stored bytes. A specific
// (older) handoff_id can only be answered with an explicit superseded
// refusal — old versions are never silently re-sent.
func (s *Service) Retry(ctx context.Context, opportunityID, tenantID, requestedHandoffID string) (*ConfirmResult, error) {
	latest, err := s.St.LatestOpportunityHandoff(opportunityID, tenantID)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, ErrHandoffNotFound
	}
	if requestedHandoffID != "" && requestedHandoffID != latest.HandoffID {
		return nil, ErrSuperseded
	}
	if latest.LocalStatus == StatusRevoked {
		return nil, ErrAlreadyRevoked
	}
	err = s.deliver(ctx, latest)
	switch {
	case err == nil:
		updated, uerr := s.St.OpportunityHandoffByHandoffID(latest.HandoffID, tenantID)
		if uerr != nil {
			return nil, uerr
		}
		return &ConfirmResult{Handoff: updated, Delivered: true,
			Note: "已按原快照字节重发(同键同内容):接单侧幂等返回原草稿引用。"}, nil
	case errors.Is(err, ErrTransport):
		// 网络层失败:快照已保留,保持可重试(200 + 注记,非错误终态)。
		return &ConfirmResult{Handoff: latest,
			Note: "投递仍未完成:快照已保留,可再次重试(同一 handoff 引用)。"}, nil
	default:
		// 409 同键异内容 / 422 显式拒绝:结构性问题必须显式上浮,不吞错。
		return nil, err
	}
}

// Refresh pulls the receiver's live projection and updates the stored one.
func (s *Service) Refresh(ctx context.Context, opportunityID, tenantID string) (*store.OpportunityHandoff, error) {
	latest, err := s.St.LatestOpportunityHandoff(opportunityID, tenantID)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, ErrHandoffNotFound
	}
	if latest.LocalStatus == StatusDelivered || latest.LocalStatus == StatusDeliveryFailed || latest.LocalStatus == StatusConfirmed {
		proj, err := s.Deliver.FetchProjection(ctx, latest.HandoffID)
		switch {
		case err == nil:
			latest.TargetStatus = proj.Status
			latest.TargetDirty = proj.Dirty
			latest.TargetRevoked = proj.Revoked
			latest.DraftRef = proj.DraftRef
			if err := s.St.UpdateOpportunityHandoffDelivery(latest.ID, latest.LocalStatus, latest.DraftRef,
				latest.TargetStatus, latest.TargetDirty, latest.TargetRevoked, latest.LastHTTPStatus, latest.LastError); err != nil {
				return nil, err
			}
		case errors.Is(err, ErrProjectionNotFound):
			// 接单侧查无:从未成功投递(快照仍在,可重试)。
		case errors.Is(err, ErrTransport):
			return latest, fmt.Errorf("receiver unreachable: %w", err)
		default:
			return latest, err
		}
	}
	return latest, nil
}

// Revoke runs the acceptance guard: only a target still in draft (or a
// target that never received the handoff) may be revoked. Anything else is
// an explicit refusal pointing at the target change flow. 接单侧不可达时
// fail-closed:不做盲撤。
func (s *Service) Revoke(ctx context.Context, opportunityID, tenantID string) (*store.OpportunityHandoff, error) {
	latest, err := s.St.LatestOpportunityHandoff(opportunityID, tenantID)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, ErrHandoffNotFound
	}
	if latest.LocalStatus == StatusRevoked {
		return latest, nil // idempotent
	}
	if latest.TargetStatus != "" && latest.TargetStatus != "draft" {
		// 已接受(离开草稿):变更必须走接单侧变更流程,不偷偷覆盖。
		return latest, ErrAcceptedByTarget
	}
	if latest.LocalStatus == StatusDelivered || latest.LocalStatus == StatusConfirmed {
		proj, err := s.Deliver.FetchProjection(ctx, latest.HandoffID)
		switch {
		case err == nil:
			if proj.Revoked {
				// 接单侧已标注撤销:本地收敛为已撤。
			} else if proj.Status != "" && proj.Status != "draft" {
				return latest, ErrAcceptedByTarget
			} else if proj.Dirty {
				// 接单侧草稿被人工触碰:视同已接受编辑,不盲撤。
				return latest, ErrAcceptedByTarget
			}
		case errors.Is(err, ErrProjectionNotFound):
			// 接单侧查无 → 从未成功接收,可撤。
		default:
			return latest, ErrCannotConfirmTargetState
		}
	}
	changed, err := s.St.MarkOpportunityHandoffRevoked(latest.ID, tenantID)
	if err != nil {
		return nil, err
	}
	_ = changed
	updated, err := s.St.OpportunityHandoffByHandoffID(latest.HandoffID, tenantID)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// deliver attempts one delivery and books the outcome on the snapshot.
func (s *Service) deliver(ctx context.Context, h *store.OpportunityHandoff) error {
	res, err := s.Deliver.Deliver(ctx, []byte(h.DocJSON))
	httpStatus := statusOf(err)
	switch {
	case err == nil:
		h.LocalStatus = StatusDelivered
		h.DraftRef = res.DraftRef
		h.TargetStatus = res.Status
		h.TargetDirty = res.Dirty
		h.TargetRevoked = res.Revoked
		h.LastError = ""
	case errors.Is(err, ErrReceiverConflict) || errors.Is(err, ErrReceiverRejected):
		// 结构性拒绝:快照保留原样(绝不覆盖),显式记录,等待人工处置。
		h.LastError = err.Error()
	default: // transport
		h.LocalStatus = StatusDeliveryFailed
		h.LastError = err.Error()
	}
	h.LastHTTPStatus = httpStatus
	if uerr := s.St.UpdateOpportunityHandoffDelivery(h.ID, h.LocalStatus, h.DraftRef,
		h.TargetStatus, h.TargetDirty, h.TargetRevoked, h.LastHTTPStatus, h.LastError); uerr != nil {
		return uerr
	}
	return err
}

func statusOf(err error) *int64 {
	if err == nil {
		v := int64(201)
		return &v
	}
	var re *ReceiverError
	switch {
	case errors.Is(err, ErrReceiverConflict):
		v := int64(409)
		return &v
	case errors.Is(err, ErrReceiverRejected):
		v := int64(422)
		return &v
	case errors.As(err, &re):
		v := int64(422)
		return &v
	default:
		return nil
	}
}

func (s *Service) newHandoffID() (string, error) {
	if s.NewID != nil {
		return s.NewID()
	}
	return NewHandoffID()
}

// tenantOf reads the tenant id carried on the confirmed input (set by
// the handler from the caller's membership; excluded from the
// fingerprint by construction).
func tenantOf(in ConfirmedInput) string { return in.TenantID }
