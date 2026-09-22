package codexengine

import "context"

// RateLimitWindow is an account allowance, not a token count or context size.
// Optional fields remain pointers so absent values cannot look like unused quota.
type RateLimitWindow struct {
	UsedPercent   *float64 `json:"usedPercent"`
	WindowMinutes *int     `json:"windowDurationMins"`
	ResetsAt      *int64   `json:"resetsAt"`
}

type RateLimitSnapshot struct {
	LimitName string           `json:"limitName"`
	Primary   *RateLimitWindow `json:"primary"`
	Secondary *RateLimitWindow `json:"secondary"`
}

type RateLimitsResponse struct {
	RateLimits RateLimitSnapshot            `json:"rateLimits"`
	ByLimitID  map[string]RateLimitSnapshot `json:"rateLimitsByLimitId"`
}

// ReadRateLimits uses the installed CLI's account without importing credentials.
// Older CLIs or API-key accounts can reject this optional request.
func (h *Host) ReadRateLimits(ctx context.Context) (RateLimitsResponse, error) {
	client, err := h.Acquire(ctx)
	if err != nil {
		return RateLimitsResponse{}, err
	}
	defer h.Release()
	var result RateLimitsResponse
	err = client.Request(ctx, MethodAccountRateLimits, struct{}{}, &result)
	return result, err
}
