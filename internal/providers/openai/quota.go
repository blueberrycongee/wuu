package openai

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type quotaEndpoint struct {
	source string
	path   string
	kind   string
	auth   string
}

// ReadQuota exposes provider-specific account balances and plan windows only
// for known quota endpoints. Arbitrary OpenAI-compatible services are never
// queried speculatively.
func (c *Client) ReadQuota(ctx context.Context) (providers.Quota, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported)
	}
	endpoint, ok := detectQuotaEndpoint(base)
	identity := strings.TrimSpace(c.apiKey)
	source := "openai-compatible"
	endpointScope := c.baseURL
	if ok {
		source = endpoint.source
		endpointScope = base.Scheme + "://" + base.Host
	}
	account := &providers.QuotaAccount{ID: providers.QuotaAccountID(source, endpointScope, identity), Source: source}
	quota := providers.Quota{Account: account}
	if !ok {
		return quota, providers.NewQuotaError(providers.QuotaErrorUnsupported)
	}
	base.Path, base.RawPath, base.RawQuery, base.Fragment = endpoint.path, "", "", ""
	quota.Kind = endpoint.kind
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	for name, value := range c.headers {
		if strings.EqualFold(name, "Authorization") {
			continue
		}
		req.Header.Set(name, value)
	}
	if endpoint.auth == "bare" {
		req.Header.Set("Authorization", c.apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	body, err := providers.ReadQuotaJSON(c.httpClient, req)
	if err != nil {
		return quota, err
	}
	switch endpoint.source {
	case "deepseek":
		err = parseDeepSeekQuota(body, &quota)
	case "openrouter":
		err = parseOpenRouterQuota(body, &quota)
	case "kimi-code":
		err = parseKimiQuota(body, &quota)
	case "zhipu":
		err = parseZhipuQuota(body, &quota)
	}
	if err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	return quota, nil
}

func detectQuotaEndpoint(base *url.URL) (quotaEndpoint, bool) {
	if !strings.EqualFold(base.Scheme, "https") {
		return quotaEndpoint{}, false
	}
	host := strings.ToLower(strings.TrimSuffix(base.Hostname(), "."))
	switch host {
	case "api.deepseek.com":
		return quotaEndpoint{source: "deepseek", path: "/user/balance", kind: "balance", auth: "bearer"}, true
	case "openrouter.ai", "api.openrouter.ai":
		return quotaEndpoint{source: "openrouter", path: "/api/v1/credits", kind: "balance", auth: "bearer"}, true
	case "api.kimi.com", "api.kimi.ai":
		if strings.HasPrefix(strings.TrimRight(base.Path, "/"), "/coding") && (len(strings.TrimRight(base.Path, "/")) == len("/coding") || strings.TrimRight(base.Path, "/")[len("/coding")] == '/') {
			return quotaEndpoint{source: "kimi-code", path: "/coding/v1/usages", kind: "plan", auth: "bearer"}, true
		}
	case "open.bigmodel.cn", "api.z.ai":
		return quotaEndpoint{source: "zhipu", path: "/api/monitor/usage/quota/limit", kind: "plan", auth: "bare"}, true
	}
	return quotaEndpoint{}, false
}

func parseDeepSeekQuota(body []byte, quota *providers.Quota) error {
	var response struct {
		BalanceInfos []struct {
			Currency     string          `json:"currency"`
			TotalBalance json.RawMessage `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &response); err != nil || len(response.BalanceInfos) == 0 {
		return errQuotaInvalid
	}
	for _, balance := range response.BalanceInfos {
		amount, ok := quotaDecimal(balance.TotalBalance)
		if !ok || strings.TrimSpace(balance.Currency) == "" {
			return errQuotaInvalid
		}
		quota.Balances = append(quota.Balances, providers.QuotaBalance{Currency: strings.TrimSpace(balance.Currency), Amount: amount})
	}
	return nil
}

func parseOpenRouterQuota(body []byte, quota *providers.Quota) error {
	var response struct {
		Data *struct {
			TotalCredits json.RawMessage `json:"total_credits"`
			TotalUsage   json.RawMessage `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Data == nil {
		return errQuotaInvalid
	}
	credits, okCredits := quotaDecimal(response.Data.TotalCredits)
	usage, okUsage := quotaDecimal(response.Data.TotalUsage)
	if !okCredits || !okUsage {
		return errQuotaInvalid
	}
	amount, ok := subtractQuotaDecimals(credits, usage)
	if !ok {
		return errQuotaInvalid
	}
	quota.Balances = []providers.QuotaBalance{{Currency: "USD", Amount: amount}}
	return nil
}

func parseKimiQuota(body []byte, quota *providers.Quota) error {
	var response struct {
		Usage  json.RawMessage `json:"usage"`
		Limits []struct {
			Window json.RawMessage `json:"window"`
			Detail json.RawMessage `json:"detail"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	for i, row := range response.Limits {
		windowValues, err := decodeQuotaObject(row.Window)
		if err != nil {
			return err
		}
		detail := row.Detail
		if len(detail) == 0 || string(detail) == "null" {
			detail = row.Window
		}
		values, err := decodeQuotaObject(detail)
		if err != nil {
			return err
		}
		window, ok := kimiQuotaWindow("limit-"+strconv.Itoa(i+1), windowValues, values)
		if ok {
			quota.Windows = append(quota.Windows, window)
		}
	}
	usage, err := decodeQuotaObject(response.Usage)
	if err != nil {
		return err
	}
	if window, ok := kimiQuotaWindow("usage", map[string]any{"duration": float64(7), "timeUnit": "TIME_UNIT_DAY"}, usage); ok {
		window.Label = "7 days"
		quota.Windows = append(quota.Windows, window)
	}
	if len(quota.Windows) == 0 {
		return errQuotaInvalid
	}
	return nil
}

func kimiQuotaWindow(id string, window, detail map[string]any) (providers.QuotaWindow, bool) {
	limit, limitOK := quotaNumberValue(detail["limit"])
	if !limitOK || limit <= 0 {
		return providers.QuotaWindow{}, false
	}
	percent, percentOK := quotaNumberValue(detail["used"])
	if percentOK {
		percent = percent / limit * 100
	} else if remaining, ok := quotaNumberValue(detail["remaining"]); ok {
		percent = (limit - remaining) / limit * 100
		percentOK = true
	}
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		percentOK = false
	}
	quotaWindow := providers.QuotaWindow{ID: id, Label: kimiQuotaWindowLabel(window)}
	if percentOK {
		quotaWindow.UsedPercent = &percent
	}
	quotaWindow.WindowMinutes = kimiQuotaWindowMinutes(window)
	for _, key := range []string{"resetTime", "resetAt", "reset_at", "reset_time"} {
		if value, ok := detail[key].(string); ok {
			quotaWindow.ResetsAt = quotaTimestamp(value)
			if quotaWindow.ResetsAt != "" {
				break
			}
		}
	}
	return quotaWindow, true
}

func kimiQuotaWindowMinutes(window map[string]any) int {
	duration, _ := quotaNumberValue(window["duration"])
	unit, _ := window["timeUnit"].(string)
	switch {
	case strings.Contains(unit, "MINUTE"):
		return int(duration)
	case strings.Contains(unit, "HOUR"):
		return int(duration * 60)
	case strings.Contains(unit, "DAY"):
		return int(duration * 24 * 60)
	default:
		return 0
	}
}

func kimiQuotaWindowLabel(window map[string]any) string {
	minutes := kimiQuotaWindowMinutes(window)
	if minutes == 0 {
		return "Allowance"
	}
	if minutes%(24*60) == 0 {
		return strconv.Itoa(minutes/(24*60)) + " days"
	}
	if minutes%60 == 0 {
		return strconv.Itoa(minutes/60) + " hours"
	}
	return strconv.Itoa(minutes) + " minutes"
}

func parseZhipuQuota(body []byte, quota *providers.Quota) error {
	var response struct {
		Success *bool `json:"success"`
		Data    *struct {
			Level  string `json:"level"`
			Limits []struct {
				Type          string          `json:"type"`
				Unit          json.RawMessage `json:"unit"`
				Number        json.RawMessage `json:"number"`
				Percentage    *float64        `json:"percentage"`
				NextResetTime json.RawMessage `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Data == nil || response.Success != nil && !*response.Success {
		return errQuotaInvalid
	}
	quota.Plan = strings.TrimSpace(response.Data.Level)
	for i, limit := range response.Data.Limits {
		unit, _ := quotaNumberValue(limit.Unit)
		number, _ := quotaNumberValue(limit.Number)
		window := providers.QuotaWindow{
			ID:          strings.ToLower(strings.TrimSpace(limit.Type)) + "-" + strconv.Itoa(i+1),
			Label:       zhipuQuotaLabel(limit.Type, unit, number),
			UsedPercent: limit.Percentage,
		}
		if strings.EqualFold(limit.Type, "TIME_LIMIT") {
			window.Scope = "tools"
		}
		window.WindowMinutes = zhipuQuotaMinutes(limit.Type, unit, number)
		window.ResetsAt = zhipuQuotaReset(limit.NextResetTime)
		quota.Windows = append(quota.Windows, window)
	}
	return nil
}

func zhipuQuotaMinutes(kind string, unit, number float64) int {
	if strings.EqualFold(kind, "TIME_LIMIT") {
		return 0
	}
	switch int(unit) {
	case 3:
		if number > 0 {
			return int(number * 60)
		}
		return 5 * 60
	case 6:
		return 7 * 24 * 60
	default:
		return 0
	}
}

func zhipuQuotaLabel(kind string, unit, number float64) string {
	if strings.EqualFold(kind, "TIME_LIMIT") {
		return "Tool usage"
	}
	minutes := zhipuQuotaMinutes(kind, unit, number)
	if minutes > 0 {
		return kimiQuotaWindowLabel(map[string]any{"duration": float64(minutes), "timeUnit": "TIME_UNIT_MINUTE"})
	}
	return strings.TrimSpace(kind)
}

func zhipuQuotaReset(raw json.RawMessage) string {
	if value, ok := quotaNumberValue(raw); ok && value > 0 {
		return time.UnixMilli(int64(value)).UTC().Format(time.RFC3339)
	}
	return ""
}

func decodeQuotaObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func quotaNumberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case json.RawMessage:
		var text string
		if json.Unmarshal(number, &text) == nil {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
		}
		var parsed float64
		if json.Unmarshal(number, &parsed) == nil {
			return parsed, true
		}
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	}
	return 0, false
}

func quotaDecimal(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var value string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return "", false
		}
	} else {
		value = string(raw)
	}
	value = strings.TrimSpace(value)
	if !quotaDecimalPattern.MatchString(value) {
		return "", false
	}
	return value, true
}

func subtractQuotaDecimals(left, right string) (string, bool) {
	leftInt, leftScale, ok := decimalInteger(left)
	if !ok {
		return "", false
	}
	rightInt, rightScale, ok := decimalInteger(right)
	if !ok {
		return "", false
	}
	scale := leftScale
	if rightScale > scale {
		scale = rightScale
	}
	leftInt.Mul(leftInt, pow10(scale-leftScale))
	rightInt.Mul(rightInt, pow10(scale-rightScale))
	leftInt.Sub(leftInt, rightInt)
	negative := leftInt.Sign() < 0
	leftInt.Abs(leftInt)
	digits := leftInt.String()
	if scale > 0 {
		for len(digits) <= scale {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative && leftInt.Sign() != 0 {
		digits = "-" + digits
	}
	return digits, true
}

func decimalInteger(value string) (*big.Int, int, bool) {
	exponent := 0
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		parsedExponent, err := strconv.Atoi(value[index+1:])
		if err != nil || parsedExponent > 10000 || parsedExponent < -10000 {
			return nil, 0, false
		}
		exponent = parsedExponent
		value = value[:index]
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	parts := strings.SplitN(unsigned, ".", 2)
	scale := 0
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		scale = len(fraction)
	}
	scale -= exponent
	digits := strings.TrimLeft(parts[0]+fraction, "0")
	if digits == "" {
		digits = "0"
	}
	if scale < 0 {
		digits += strings.Repeat("0", -scale)
		scale = 0
	}
	if scale > 10000 {
		return nil, 0, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(digits, 10); !ok {
		return nil, 0, false
	}
	if negative {
		n.Neg(n)
	}
	return n, scale, true
}

func pow10(power int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
}

func quotaTimestamp(value string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return ""
}

var (
	quotaDecimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
	errQuotaInvalid     = errors.New("invalid quota response")
)
