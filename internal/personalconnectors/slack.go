package personalconnectors

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

func buildSlack(c Connection, op string, args json.RawMessage) (Plan, error) {
	p := Plan{Method: "GET"}
	if c.Domain != "work" {
		return p, fault("work_domain_required")
	}
	var a struct {
		ChannelID     string `json:"channel_id"`
		Query         string `json:"query"`
		MentionUserID string `json:"mention_user_id"`
		Cursor        string `json:"cursor"`
	}
	if err := decode(args, &a); err != nil {
		return p, err
	}
	if !providerID.MatchString(a.ChannelID) || providerCursor(a.Cursor) != nil || !boundedProviderText(a.Query, 512) {
		return p, fault("invalid_slack_query")
	}
	if op != "slack.history" && op != "slack.search" && op != "slack.mentions" {
		return p, fault("unsupported_operation")
	}
	if op == "slack.search" && strings.TrimSpace(a.Query) == "" {
		return p, fault("query_required")
	}
	if op == "slack.mentions" && !providerID.MatchString(a.MentionUserID) {
		return p, fault("mention_user_required")
	}
	for _, scope := range []string{"channels:history", "groups:history", "im:history", "mpim:history"} {
		if slices.Contains(c.Scopes, scope) {
			p.RequiredScopes = []string{scope}
			break
		}
	}
	if len(p.RequiredScopes) == 0 {
		return p, fault("history_scope_required")
	}
	v := url.Values{"channel": {a.ChannelID}, "limit": {"15"}}
	if a.Cursor != "" {
		v.Set("cursor", a.Cursor)
	}
	p.Resource = a.ChannelID
	p.URL = "https://slack.com/api/conversations.history?" + v.Encode()
	// Deliberately search only the selected conversation's returned page. Slack
	// global search can widen scope with query operators and requires user tokens.
	p.Filter = func(data []byte) (json.RawMessage, error) {
		var body map[string]json.RawMessage
		if json.Unmarshal(data, &body) != nil {
			return nil, fault("invalid_response")
		}
		var ok bool
		if json.Unmarshal(body["ok"], &ok) != nil || !ok {
			return nil, fault("provider_denied")
		}
		var messages []map[string]json.RawMessage
		if json.Unmarshal(body["messages"], &messages) != nil {
			return nil, fault("invalid_response")
		}
		out := []map[string]json.RawMessage{}
		for _, m := range messages {
			var text string
			_ = json.Unmarshal(m["text"], &text)
			if op == "slack.search" && !strings.Contains(strings.ToLower(text), strings.ToLower(a.Query)) {
				continue
			}
			if op == "slack.mentions" && !strings.Contains(text, "<@"+a.MentionUserID+">") {
				continue
			}
			out = append(out, m)
		}
		result := map[string]any{"messages": out, "domain": "work", "channel_id": a.ChannelID, "search_scope": "selected_channel_current_page", "content_trust": "untrusted_external_data"}
		for _, k := range []string{"has_more", "response_metadata"} {
			if x, ok := body[k]; ok {
				result[k] = x
			}
		}
		return providerJSON(result), nil
	}
	return p, nil
}
