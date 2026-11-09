package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const anthropicQuotaEndpoint = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"

type anthropicQuotaReply struct {
	FiveHour       *anthropicQuotaWindow `json:"five_hour"`
	SevenDay       *anthropicQuotaWindow `json:"seven_day"`
	SevenDayOpus   *anthropicQuotaWindow `json:"seven_day_opus"`
	SevenDaySonnet *anthropicQuotaWindow `json:"seven_day_sonnet"`
	Limits         []struct {
		Kind     string   `json:"kind"`
		Percent  *float64 `json:"percent"`
		ResetsAt string   `json:"resets_at"`
		Scope    struct {
			Model struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
}

type anthropicQuotaWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

// ReadQuota reads usage for an OAuth-authenticated Anthropic account. API-key
// clients do not have this subscription endpoint and return unsupported.
func (c *Client) ReadQuota(ctx context.Context) (providers.Quota, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	base.Path, base.RawPath, base.RawQuery, base.Fragment = "/api/oauth/usage", "", "cedar_ember=1&skip_spend=1", ""
	endpoint := base.String()
	identity := strings.TrimSpace(c.authToken)
	if identity == "" {
		identity = strings.TrimSpace(c.apiKey)
	}
	account := &providers.QuotaAccount{ID: providers.QuotaAccountID("anthropic-oauth", base.Scheme+"://"+base.Host, identity), Source: "anthropic-oauth"}
	quota := providers.Quota{Kind: "subscription", Account: account}
	if strings.TrimSpace(c.authToken) == "" {
		return quota, providers.NewQuotaError(providers.QuotaErrorUnsupported)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	req.Header.Set("Authorization", "Bearer "+c.authToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	body, err := providers.ReadQuotaJSON(c.httpClient, req)
	if err != nil {
		return quota, err
	}
	return parseAnthropicQuota(body, quota)
}

// ReadSelectedQuota reads the Claude CLI account exposed by the captured
// credential provider. It never refreshes or mutates that external login.
func ReadSelectedQuota(ctx context.Context, selected credentialstore.Store) (providers.Quota, error) {
	return readSelectedQuota(ctx, selected, nil)
}
func readSelectedQuota(ctx context.Context, selected credentialstore.Store, client *http.Client) (providers.Quota, error) {
	credentials, err := authstorage.ReadCLI(ctx, selected, "claude")
	return readLocalQuotaWithCredentials(ctx, credentials, err, client)
}

func readLocalQuotaWithCredentials(ctx context.Context, credentials authstorage.Credentials, credentialErr error, client *http.Client) (providers.Quota, error) {
	account := &providers.QuotaAccount{ID: providers.QuotaAccountID("claude-code", "https://api.anthropic.com", credentials.AccessToken), Source: "claude-code"}
	quota := providers.Quota{Kind: "subscription", Account: account, Plan: credentials.SubscriptionType}
	if credentialErr != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return quota, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicQuotaEndpoint, nil)
	if err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	body, err := providers.ReadQuotaJSON(client, req)
	if err != nil {
		return quota, err
	}
	return parseAnthropicQuota(body, quota)
}

func parseAnthropicQuota(body []byte, quota providers.Quota) (providers.Quota, error) {
	var reply anthropicQuotaReply
	if json.Unmarshal(body, &reply) != nil || (reply.FiveHour == nil && reply.SevenDay == nil && reply.SevenDayOpus == nil && reply.SevenDaySonnet == nil && reply.Limits == nil) {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	quota.Windows = appendAnthropicWindow(quota.Windows, "five-hour", "5 hours", 5*60, reply.FiveHour)
	quota.Windows = appendAnthropicWindow(quota.Windows, "seven-day", "7 days", 7*24*60, reply.SevenDay)
	quota.Windows = appendAnthropicWindow(quota.Windows, "seven-day-opus", "7 days (Opus)", 7*24*60, reply.SevenDayOpus)
	quota.Windows = appendAnthropicWindow(quota.Windows, "seven-day-sonnet", "7 days (Sonnet)", 7*24*60, reply.SevenDaySonnet)
	for i, limit := range reply.Limits {
		id := strings.TrimSpace(limit.Kind)
		if id == "" {
			id = "limit"
		}
		model := strings.TrimSpace(limit.Scope.Model.DisplayName)
		label := strings.TrimSpace(limit.Kind)
		if label == "" {
			label = model
		}
		if label == "" {
			label = "Limit"
		}
		quota.Windows = append(quota.Windows, providers.QuotaWindow{
			ID:          id + "-" + strconv.Itoa(i+1),
			Label:       label,
			UsedPercent: limit.Percent,
			ResetsAt:    quotaTimestamp(limit.ResetsAt),
			Model:       model,
			Scope:       strings.TrimSpace(limit.Kind),
		})
	}
	return quota, nil
}

func appendAnthropicWindow(windows []providers.QuotaWindow, id, label string, minutes int, value *anthropicQuotaWindow) []providers.QuotaWindow {
	if value == nil {
		return windows
	}
	return append(windows, providers.QuotaWindow{ID: id, Label: label, WindowMinutes: minutes, UsedPercent: value.Utilization, ResetsAt: quotaTimestamp(value.ResetsAt)})
}

func quotaTimestamp(value string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return ""
}
