package platformtask

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	phraseCapability = "reception.phrase"
	phraseAmountCNY  = 0.01
	phraseCostCents  = 1
)

// Client calls the platform task API. It never logs the token or the body.
type Client struct {
	BaseURL   string
	Token     string
	AccountID string
	AppID     string
	HTTP      *http.Client
}

// New returns nil unless base URL, token, and account id are all set.
func New(baseURL, token, accountID, appID string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	accountID = strings.TrimSpace(accountID)
	appID = strings.TrimSpace(appID)
	if baseURL == "" || token == "" || accountID == "" {
		return nil
	}
	if appID == "" {
		appID = "leads-engine"
	}
	return &Client{
		BaseURL:   baseURL,
		Token:     token,
		AccountID: accountID,
		AppID:     appID,
		HTTP:      &http.Client{Timeout: 2 * time.Second},
	}
}

type phraseBody struct {
	AppID          string         `json:"app_id"`
	AccountID      string         `json:"account_id"`
	ProjectID      string         `json:"project_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	Capability     string         `json:"capability"`
	Params         map[string]any `json:"params"`
	Billing        struct {
		Mode      string  `json:"mode"`
		AmountCNY float64 `json:"amount_cny"`
	} `json:"billing"`
}

type taskSummary struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// Phrase submits one grounded answer and polls until the task ends.
// Only SUCCEEDED with a task id is recorded, at one cent. Timeouts stay not_completed.
func (c *Client) Phrase(ctx context.Context, in PhraseInput) PhraseResult {
	fallback := PhraseResult{BillingVerdict: "not_completed"}
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return fallback
	}
	timeout := 2 * time.Second
	if c.HTTP != nil && c.HTTP.Timeout > 0 {
		timeout = c.HTTP.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ids := in.CitationIDs
	if ids == nil {
		ids = []string{}
	}
	payload := phraseBody{
		AppID: c.AppID, AccountID: c.AccountID, IdempotencyKey: in.IdempotencyKey,
		Capability: phraseCapability,
		Params: map[string]any{
			"language":     in.Language,
			"body":         in.Body,
			"citation_ids": ids,
		},
	}
	payload.Billing.Mode = "hold"
	payload.Billing.AmountCNY = phraseAmountCNY
	raw, err := json.Marshal(payload)
	if err != nil {
		return fallback
	}
	sum, err := c.post(ctx, raw)
	if err != nil {
		return fallback
	}
	for !terminal(sum.Status) {
		if err := sleep(ctx, 15*time.Millisecond); err != nil {
			return PhraseResult{TaskID: sum.TaskID, BillingVerdict: "not_completed"}
		}
		next, err := c.get(ctx, sum.TaskID)
		if err != nil {
			return PhraseResult{TaskID: sum.TaskID, BillingVerdict: "not_completed"}
		}
		sum = next
	}
	if sum.Status == "SUCCEEDED" && strings.TrimSpace(sum.TaskID) != "" {
		return PhraseResult{TaskID: sum.TaskID, CostCents: phraseCostCents, BillingVerdict: "recorded"}
	}
	return PhraseResult{TaskID: sum.TaskID, BillingVerdict: "not_completed"}
}

func terminal(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "CANCELED":
		return true
	default:
		return false
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) post(ctx context.Context, raw []byte) (taskSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/internal/v1/tasks/"+phraseCapability, bytes.NewReader(raw))
	if err != nil {
		return taskSummary{}, err
	}
	c.authorize(req)
	return c.do(req)
}

func (c *Client) get(ctx context.Context, taskID string) (taskSummary, error) {
	u, err := url.Parse(c.BaseURL + "/internal/v1/tasks/" + url.PathEscape(taskID))
	if err != nil {
		return taskSummary{}, err
	}
	q := u.Query()
	q.Set("app_id", c.AppID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return taskSummary{}, err
	}
	c.authorize(req)
	return c.do(req)
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-ID", c.AppID)
	req.Header.Set("X-PilotSeaView-Internal-Token", c.Token)
}

func (c *Client) do(req *http.Request) (taskSummary, error) {
	ht := c.HTTP
	if ht == nil {
		ht = http.DefaultClient
	}
	res, err := ht.Do(req)
	if err != nil {
		return taskSummary{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return taskSummary{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return taskSummary{}, errStatus
	}
	var sum taskSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		return taskSummary{}, err
	}
	return sum, nil
}

var errStatus = errString("task status")

type errString string

func (e errString) Error() string { return string(e) }
