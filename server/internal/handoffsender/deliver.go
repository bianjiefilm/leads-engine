// deliver.go — the delivery client toward the registered receiver intake.
//
// 纪律:URL 与令牌只来自部署配置/接缝注入,绝无任何客户端可传 URL 的字段;
// 重试永远重发**存储的原始字节**(同键同内容),结构上不存在改写已确认内容
// 的路径。响应语义对齐 O2 接收端:201 created / 200 duplicate / 409
// HANDOFF_ID_CONFLICT / 422 code=机器拒绝码 / 404 不可见。
package handoffsender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const intakeTokenHeader = "X-Internal-Token"

// DeliveryResult is the restricted status projection the receiver returns.
// 投递成功 ≠ 成交/已支付:这只是「接单侧建了草稿」的事实投影。
type DeliveryResult struct {
	HandoffID  string   `json:"handoff_id"`
	DraftRef   string   `json:"draft_ref"`
	Status     string   `json:"status"`
	Duplicate  bool     `json:"duplicate"`
	Dirty      bool     `json:"dirty"`
	Revoked    bool     `json:"revoked"`
	SourceApp  string   `json:"source_app"`
	SourceKind string   `json:"source_kind"`
	SourceRef  string   `json:"source_ref"`
	Missing    []string `json:"missing"`
	Scopes     []string `json:"scopes"`
}

// Sentinel delivery outcomes (typed, safe for errors.Is).
var (
	// ErrReceiverConflict = receiver answered 409 HANDOFF_ID_CONFLICT (同键
	// 异内容):本地快照绝不覆盖,调用方显式处置。
	ErrReceiverConflict = errors.New("handoffsender: receiver conflict (same key, different content)")
	// ErrReceiverRejected = receiver answered 422 with a machine code.
	ErrReceiverRejected = errors.New("handoffsender: receiver rejected the handoff")
	// ErrTransport = network/timeout/non-contract status: retryable, the
	// stored snapshot is kept for a later retry.
	ErrTransport = errors.New("handoffsender: delivery transport failure")
)

// ReceiverError carries the receiver's machine rejection code (422).
type ReceiverError struct {
	Code   string
	Reason string
}

func (e *ReceiverError) Error() string { return "receiver rejected: " + e.Code }

// Deliverer posts stored snapshot bytes to the receiver intake.
type Deliverer struct {
	// IntakeURL is the receiver handoff endpoint (deployment config; empty =
	// not configured = every delivery fails closed).
	IntakeURL string
	// Token is the receiver internal token.
	Token string
	// Client is the HTTP client (injectable for tests; default 10s timeout).
	Client *http.Client
}

func (d *Deliverer) httpClient() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Deliver sends the EXACT stored bytes; no re-serialization, ever.
func (d *Deliverer) Deliver(ctx context.Context, doc []byte) (*DeliveryResult, error) {
	if strings.TrimSpace(d.IntakeURL) == "" || strings.TrimSpace(d.Token) == "" {
		return nil, fmt.Errorf("%w: receiver intake is not configured (fail-closed)", ErrTransport)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.IntakeURL, bytes.NewReader(doc))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(intakeTokenHeader, d.Token)
	res, err := d.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, MaxDocBytes))
	switch {
	case res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusOK:
		var out struct {
			Result *DeliveryResult `json:"result"`
		}
		if err := json.Unmarshal(body, &out); err != nil || out.Result == nil {
			return nil, fmt.Errorf("%w: unreadable receiver result (HTTP %d)", ErrTransport, res.StatusCode)
		}
		return out.Result, nil
	case res.StatusCode == http.StatusConflict:
		return nil, ErrReceiverConflict
	case res.StatusCode == http.StatusUnprocessableEntity:
		var out struct {
			Code   string `json:"code"`
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(body, &out)
		return nil, &ReceiverError{Code: out.Code, Reason: out.Reason}
	default:
		return nil, fmt.Errorf("%w: receiver answered HTTP %d", ErrTransport, res.StatusCode)
	}
}

// TargetProjection is the receiver-side restricted read-only projection
// (status + draft reference + edge annotations; O2 /internal/eco/leads/projection).
type TargetProjection struct {
	HandoffID string `json:"handoff_id"`
	DraftRef  string `json:"draft_ref"`
	Status    string `json:"status"`
	Dirty     bool   `json:"dirty"`
	Revoked   bool   `json:"revoked"`
	SourceApp string `json:"source_app"`
	SourceRef string `json:"source_ref"`
	UpdatedAt int64  `json:"updated_at"`
}

// ErrProjectionNotFound means the receiver has no such handoff (404; e.g.
// delivery never succeeded).
var ErrProjectionNotFound = errors.New("handoffsender: receiver has no such handoff")

// FetchProjection queries the receiver's live projection for one handoff.
func (d *Deliverer) FetchProjection(ctx context.Context, handoffID string) (*TargetProjection, error) {
	if strings.TrimSpace(d.IntakeURL) == "" || strings.TrimSpace(d.Token) == "" {
		return nil, fmt.Errorf("%w: receiver intake is not configured (fail-closed)", ErrTransport)
	}
	// 投影查询按引用进 body(对齐 O2 凭据面纪律,引用不进 URL)。
	payload, _ := json.Marshal(map[string]string{"handoff_id": handoffID})
	projURL := strings.Replace(d.IntakeURL, "/handoffs", "/projection", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, projURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(intakeTokenHeader, d.Token)
	res, err := d.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, MaxDocBytes))
	switch {
	case res.StatusCode == http.StatusOK:
		var out struct {
			Projection *TargetProjection `json:"projection"`
		}
		if err := json.Unmarshal(body, &out); err != nil || out.Projection == nil {
			return nil, fmt.Errorf("%w: unreadable projection (HTTP %d)", ErrTransport, res.StatusCode)
		}
		return out.Projection, nil
	case res.StatusCode == http.StatusNotFound:
		return nil, ErrProjectionNotFound
	default:
		return nil, fmt.Errorf("%w: projection answered HTTP %d", ErrTransport, res.StatusCode)
	}
}
