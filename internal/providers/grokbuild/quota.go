package grokbuild

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

type grokQuotaReply struct {
	Config *struct {
		CreditUsagePercent json.RawMessage `json:"creditUsagePercent"`
		CurrentPeriod      *struct {
			Type string `json:"type"`
			End  string `json:"end"`
		} `json:"currentPeriod"`
		BillingPeriodEnd string `json:"billingPeriodEnd"`
		OnDemandCap      struct {
			Val json.RawMessage `json:"val"`
		} `json:"onDemandCap"`
		OnDemandUsed struct {
			Val json.RawMessage `json:"val"`
		} `json:"onDemandUsed"`
	} `json:"config"`
}

// ReadQuota reads the allowance endpoint of the configured Grok Build CLI
// proxy. This intentionally uses Grok Build's CLI bearer, not browser-account
// SuperGrok credentials.
func (c *Client) ReadQuota(ctx context.Context) (providers.Quota, error) {
	token, tokenErr := c.auth.token()
	source := "configured"
	if c.auth.reuseCLI && c.auth.explicitToken == "" {
		source = "grok-cli"
	}
	account := &providers.QuotaAccount{ID: providers.QuotaAccountID(source, c.baseURL, token), Source: source}
	quota := providers.Quota{Kind: "subscription", Account: account}
	if tokenErr != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/billing"
	parsed.RawPath = ""
	parsed.RawQuery = "format=credits"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	body, err := providers.ReadQuotaJSON(c.httpClient, req)
	if err != nil {
		return quota, err
	}
	var reply grokQuotaReply
	if err := json.Unmarshal(body, &reply); err != nil || reply.Config == nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	cfg := reply.Config
	periodName, periodMinutes := grokQuotaPeriod(cfg.CurrentPeriod)
	reset := strings.TrimSpace(cfg.BillingPeriodEnd)
	if cfg.CurrentPeriod != nil && strings.TrimSpace(cfg.CurrentPeriod.End) != "" {
		reset = strings.TrimSpace(cfg.CurrentPeriod.End)
	}
	window := providers.QuotaWindow{ID: "subscription", Label: periodName, WindowMinutes: periodMinutes, UsedPercent: quotaFloat(cfg.CreditUsagePercent)}
	window.ResetsAt = quotaReset(reset)
	quota.Windows = append(quota.Windows, window)

	cap, capOK := quotaNumber(cfg.OnDemandCap.Val)
	used, usedOK := quotaNumber(cfg.OnDemandUsed.Val)
	if capOK && cap > 0 {
		window := providers.QuotaWindow{ID: "on-demand", Label: "On-demand", ResetsAt: window.ResetsAt}
		if usedOK {
			percent := used / cap * 100
			window.UsedPercent = &percent
		}
		quota.Windows = append(quota.Windows, window)
	}
	return quota, nil
}

func grokQuotaPeriod(period *struct {
	Type string `json:"type"`
	End  string `json:"end"`
}) (string, int) {
	if period == nil {
		return "Allowance", 0
	}
	switch strings.TrimPrefix(strings.ToUpper(period.Type), "USAGE_PERIOD_TYPE_") {
	case "DAILY":
		return "Daily", 24 * 60
	case "WEEKLY":
		return "Weekly", 7 * 24 * 60
	case "MONTHLY":
		return "Monthly", 0
	default:
		return "Allowance", 0
	}
}

func quotaFloat(raw json.RawMessage) *float64 {
	if value, ok := quotaNumber(raw); ok {
		return &value
	}
	return nil
}

func quotaNumber(raw json.RawMessage) (float64, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, false
	}
	var value float64
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func quotaReset(value string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return ""
}
