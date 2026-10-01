package codex

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type codexQuotaReply struct {
	PlanType  string `json:"plan_type"`
	RateLimit *struct {
		Primary   *codexQuotaPeriod `json:"primary_window"`
		Secondary *codexQuotaPeriod `json:"secondary_window"`
	} `json:"rate_limit"`
	Additional []struct {
		Name      string `json:"limit_name"`
		Feature   string `json:"metered_feature"`
		RateLimit *struct {
			Primary   *codexQuotaPeriod `json:"primary_window"`
			Secondary *codexQuotaPeriod `json:"secondary_window"`
		} `json:"rate_limit"`
	} `json:"additional_rate_limits"`
	ResetCredits *struct {
		AvailableCount *int `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
}

type codexQuotaPeriod struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *float64 `json:"limit_window_seconds"`
	ResetAt            *int64   `json:"reset_at"`
	ResetAfterSeconds  *float64 `json:"reset_after_seconds"`
}

// ReadQuota reads Codex's subscription usage endpoint without submitting an
// inference request.
func (c *Client) ReadQuota(ctx context.Context) (providers.Quota, error) {
	creds, credErr := c.auth.Credentials(ctx, false)
	account := codexQuotaAccount(c.baseURL, creds)
	quota := providers.Quota{Kind: "subscription", Account: account}
	if credErr != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	endpoint := strings.TrimSuffix(c.baseURL, "/codex") + "/wham/usage"
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Authorization", "Bearer "+creds.accessToken)
	req.Header.Set("Accept", "application/json")
	if creds.accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", creds.accountID)
	}
	body, err := providers.ReadQuotaJSON(c.httpClient, req)
	if err != nil {
		return quota, err
	}
	var reply codexQuotaReply
	if err := json.Unmarshal(body, &reply); err != nil || (reply.PlanType == "" && reply.RateLimit == nil && len(reply.Additional) == 0) {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	quota.Plan = reply.PlanType
	now := time.Now()
	if reply.RateLimit != nil {
		quota.Windows = appendCodexQuotaPeriods(quota.Windows, "primary", "Primary", reply.RateLimit.Primary, now)
		quota.Windows = appendCodexQuotaPeriods(quota.Windows, "secondary", "Secondary", reply.RateLimit.Secondary, now)
	}
	for i, additional := range reply.Additional {
		label := strings.TrimSpace(additional.Name)
		if label == "" {
			label = strings.TrimSpace(additional.Feature)
		}
		if label == "" {
			label = "Additional limit"
		}
		id := "additional-" + strings.TrimSpace(additional.Name)
		if id == "additional-" {
			id = "additional-" + strings.TrimSpace(additional.Feature)
		}
		if id == "additional-" {
			id = "additional-" + strconv.Itoa(i+1)
		}
		if additional.RateLimit != nil {
			quota.Windows = appendCodexQuotaPeriods(quota.Windows, id+"-primary", label+" primary", additional.RateLimit.Primary, now)
			quota.Windows = appendCodexQuotaPeriods(quota.Windows, id+"-secondary", label+" secondary", additional.RateLimit.Secondary, now)
		}
	}
	if reply.ResetCredits != nil && reply.ResetCredits.AvailableCount != nil {
		count := *reply.ResetCredits.AvailableCount
		quota.ResetCredits = &count
	}
	return quota, nil
}

func codexQuotaAccount(baseURL string, creds credentials) *providers.QuotaAccount {
	identity := strings.TrimSpace(creds.accountID)
	if identity == "" {
		identity = strings.TrimSpace(creds.accessToken)
	}
	source := creds.source
	if source == "" {
		source = "codex"
	}
	label := ""
	claims := jwtClaims(creds.accessToken)
	if profile, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
		label, _ = profile["email"].(string)
	}
	if label == "" {
		label, _ = claims["email"].(string)
	}
	return &providers.QuotaAccount{ID: providers.QuotaAccountID(source, baseURL, identity), Label: strings.TrimSpace(label), Source: source}
}

func appendCodexQuotaPeriods(windows []providers.QuotaWindow, prefix, label string, period *codexQuotaPeriod, now time.Time) []providers.QuotaWindow {
	if period == nil {
		return windows
	}
	window := providers.QuotaWindow{ID: prefix, Label: label, UsedPercent: period.UsedPercent}
	if period.LimitWindowSeconds != nil && *period.LimitWindowSeconds > 0 {
		window.WindowMinutes = int((*period.LimitWindowSeconds + 30) / 60)
	}
	if period.ResetAt != nil && *period.ResetAt > 0 {
		window.ResetsAt = time.Unix(*period.ResetAt, 0).UTC().Format(time.RFC3339)
	} else if period.ResetAfterSeconds != nil {
		window.ResetsAt = codexRelativeReset(now, *period.ResetAfterSeconds)
	}
	return append(windows, window)
}

func codexRelativeReset(now time.Time, seconds float64) string {
	maxSeconds := float64(1<<63-1) / float64(time.Second)
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > maxSeconds || seconds < -maxSeconds {
		return ""
	}
	return now.Add(time.Duration(seconds * float64(time.Second))).UTC().Format(time.RFC3339Nano)
}
