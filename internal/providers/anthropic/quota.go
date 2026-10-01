package anthropic

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

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

// ReadLocalQuota reads an external Claude Code login without refreshing,
// rewriting, or otherwise taking ownership of its credential.
func ReadLocalQuota(ctx context.Context, home string) (providers.Quota, error) {
	return readLocalQuota(ctx, home, nil)
}

func readLocalQuota(ctx context.Context, home string, client *http.Client) (providers.Quota, error) {
	credentials, err := readClaudeQuotaCredential(ctx, home)
	return readLocalQuotaWithCredentials(ctx, credentials, err, client)
}

func readLocalQuotaWithCredentials(ctx context.Context, credentials claudeQuotaCredential, credentialErr error, client *http.Client) (providers.Quota, error) {
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

type claudeQuotaCredential struct {
	AccessToken      string `json:"accessToken"`
	SubscriptionType string `json:"subscriptionType"`
}

func readClaudeQuotaCredential(ctx context.Context, home string) (claudeQuotaCredential, error) {
	configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if configDir == "" {
		home = strings.TrimSpace(home)
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return claudeQuotaCredential{}, err
			}
		}
		configDir = filepath.Join(home, ".claude")
	}
	path := filepath.Join(configDir, ".credentials.json")
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		body, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
		if readErr != nil || len(body) > 1<<20 {
			return claudeQuotaCredential{}, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
		}
		var payload struct {
			OAuth claudeQuotaCredential `json:"claudeAiOauth"`
		}
		if json.Unmarshal(body, &payload) != nil || strings.TrimSpace(payload.OAuth.AccessToken) == "" {
			return claudeQuotaCredential{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
		}
		return payload.OAuth, nil
	}
	if !os.IsNotExist(err) {
		return claudeQuotaCredential{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	if runtime.GOOS != "darwin" || strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")) != "" {
		return claudeQuotaCredential{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	// The default Keychain login does not belong to a custom or isolated home.
	current, err := user.Current()
	if err != nil || filepath.Clean(home) != filepath.Clean(current.HomeDir) {
		return claudeQuotaCredential{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
	}
	return readClaudeQuotaKeychain(ctx)
}

func readClaudeQuotaKeychain(parent context.Context) (claudeQuotaCredential, error) {
	account := os.Getenv("USER")
	if account == "" {
		if current, err := user.Current(); err == nil {
			account = current.Username
		}
	}
	if !quotaKeychainAccountPattern.MatchString(account) {
		account = "claude-code-user"
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	for _, args := range [][]string{{"-a", account}, nil} {
		commandArgs := append([]string{"find-generic-password", "-s", "Claude Code-credentials", "-w"}, args...)
		cmd := exec.CommandContext(ctx, "security", commandArgs...)
		var output quotaCommandBuffer
		cmd.Stdout = &output
		if err := cmd.Run(); err != nil || output.exceeded {
			continue
		}
		data := strings.TrimSpace(string(output.data))
		if decoded, err := hex.DecodeString(data); err == nil && json.Valid(decoded) && !strings.HasPrefix(data, "{") {
			data = string(decoded)
		}
		var payload struct {
			OAuth claudeQuotaCredential `json:"claudeAiOauth"`
		}
		if json.Unmarshal([]byte(data), &payload) == nil && strings.TrimSpace(payload.OAuth.AccessToken) != "" {
			return payload.OAuth, nil
		}
	}
	return claudeQuotaCredential{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
}

type quotaCommandBuffer struct {
	data     []byte
	exceeded bool
}

func (b *quotaCommandBuffer) Write(data []byte) (int, error) {
	if len(b.data)+len(data) > 1<<20 {
		b.exceeded = true
		return 0, io.ErrShortWrite
	}
	b.data = append(b.data, data...)
	return len(data), nil
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

var quotaKeychainAccountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
