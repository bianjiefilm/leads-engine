package notifyingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Record is the authorized minimal submission fetched from a registered source.
// Contact fields exist only in this response, which is written into the leads
// database and nowhere else.
type Record struct {
	SubmissionRef     string `json:"submission_ref"`
	TenantID          string `json:"tenant_id"`
	Name              string `json:"name"`
	Phone             string `json:"phone"`
	Email             string `json:"email"`
	Wechat            string `json:"wechat"`
	ChannelSubjectRef string `json:"channel_subject_ref"`
	// Authorization is granted | revoked | none.
	Authorization string `json:"authorization"`
	// Purpose is lead_submission, or a non-lead purpose such as browse.
	Purpose  string `json:"purpose"`
	Consents struct {
		FormAuthorized bool   `json:"form_authorized"`
		ChannelReply   bool   `json:"channel_reply"`
		MarketingPhone bool   `json:"marketing_phone"`
		MarketingSMS   bool   `json:"marketing_sms"`
		NoticeVersion  string `json:"notice_version"`
	} `json:"consents"`
}

// FetchRecord GETs the deployment-registered source. baseURL is never taken
// from the event. Redirects are refused.
func FetchRecord(ctx context.Context, baseURL, token, tenantID, submissionRef string) (Record, error) {
	if strings.TrimSpace(token) == "" {
		return Record{}, errorsNew("fetch token is empty")
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/internal/v1/lead-records/" + url.PathEscape(submissionRef))
	if err != nil {
		return Record{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Record{}, err
	}
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-Leads-Tenant", tenantID)
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errorsNew("redirects are disabled")
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return Record{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return Record{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Record{}, fmt.Errorf("source record status %d", res.StatusCode)
	}
	var rec Record
	if err := json.Unmarshal(body, &rec); err != nil {
		return Record{}, err
	}
	if rec.SubmissionRef != submissionRef || rec.TenantID != tenantID {
		return Record{}, errorsNew("source record tenant or submission_ref mismatch")
	}
	return rec, nil
}

func errorsNew(s string) error { return fmt.Errorf("%s", s) }
