package personalconnectors

import (
	"encoding/json"
	"net/url"
	"strings"
)

func buildFeishu(c Connection, op string, args json.RawMessage) (Plan, error) {
	p := Plan{Method: "GET", RequiredScopes: []string{"im:message:readonly"}}
	if c.Domain != "work" {
		return p, fault("work_domain_required")
	}
	var a struct {
		ChatID        string `json:"chat_id"`
		Query         string `json:"query"`
		MentionUserID string `json:"mention_user_id"`
		Cursor        string `json:"cursor"`
	}
	if err := decode(args, &a); err != nil {
		return p, err
	}
	if !providerID.MatchString(a.ChatID) || providerCursor(a.Cursor) != nil || !boundedProviderText(a.Query, 512) {
		return p, fault("invalid_feishu_query")
	}
	if op != "feishu.history" && op != "feishu.search" && op != "feishu.mentions" {
		return p, fault("unsupported_operation")
	}
	if op == "feishu.search" && strings.TrimSpace(a.Query) == "" {
		return p, fault("query_required")
	}
	if op == "feishu.mentions" && !providerID.MatchString(a.MentionUserID) {
		return p, fault("mention_user_required")
	}
	v := url.Values{"container_id_type": {"chat"}, "container_id": {a.ChatID}, "page_size": {"50"}, "sort_type": {"ByCreateTimeDesc"}}
	if a.Cursor != "" {
		v.Set("page_token", a.Cursor)
	}
	p.Resource = a.ChatID
	p.URL = "https://open.feishu.cn/open-apis/im/v1/messages?" + v.Encode()
	p.Filter = func(data []byte) (json.RawMessage, error) {
		var envelope struct {
			Code *int `json:"code"`
			Data struct {
				Items     []map[string]json.RawMessage `json:"items"`
				HasMore   bool                         `json:"has_more"`
				PageToken string                       `json:"page_token"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Code == nil {
			return nil, fault("invalid_response")
		}
		if *envelope.Code != 0 {
			return nil, fault("provider_denied")
		}
		out := []map[string]json.RawMessage{}
		for _, m := range envelope.Data.Items {
			if op == "feishu.search" {
				var body struct {
					Content string `json:"content"`
				}
				_ = json.Unmarshal(m["body"], &body)
				if !strings.Contains(strings.ToLower(body.Content), strings.ToLower(a.Query)) {
					continue
				}
			}
			if op == "feishu.mentions" {
				var mentions []struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(m["mentions"], &mentions) != nil {
					continue
				}
				found := false
				for _, mention := range mentions {
					if mention.ID == a.MentionUserID {
						found = true
					}
				}
				if !found {
					continue
				}
			}
			out = append(out, m)
		}
		if providerCursor(envelope.Data.PageToken) != nil {
			return nil, fault("invalid_response")
		}
		return providerJSON(map[string]any{"messages": out, "has_more": envelope.Data.HasMore, "next_cursor": envelope.Data.PageToken, "domain": "work", "chat_id": a.ChatID, "search_scope": "selected_chat_current_page", "content_trust": "untrusted_external_data"}), nil
	}
	return p, nil
}
