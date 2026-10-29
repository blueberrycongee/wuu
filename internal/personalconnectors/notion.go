package personalconnectors

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const notionVersion = "2025-09-03"

var notionID = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)
var providerID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func boundedProviderText(s string, max int) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsAny(s, "\x00\r")
}
func providerJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func providerCursor(s string) error {
	if !boundedProviderText(s, 4096) {
		return fault("invalid_cursor")
	}
	return nil
}

func buildNotion(c Connection, op string, args json.RawMessage) (Plan, error) {
	p := Plan{Headers: map[string]string{"Notion-Version": notionVersion}, RequiredScopes: []string{"read_content"}, Method: "GET"}
	base := "https://api.notion.com/v1/"
	switch op {
	case "notion.search":
		var a struct {
			Resource string `json:"resource"`
			Query    string `json:"query"`
			Cursor   string `json:"cursor"`
		}
		if err := decode(args, &a); err != nil {
			return p, err
		}
		if !notionID.MatchString(a.Resource) || !boundedProviderText(a.Query, 512) || providerCursor(a.Cursor) != nil {
			return p, fault("invalid_notion_search")
		}
		p.Resource = a.Resource
		p.Method = "POST"
		p.URL = base + "search"
		body := map[string]any{"query": a.Query, "page_size": 50}
		if a.Cursor != "" {
			body["start_cursor"] = a.Cursor
		}
		p.Body = providerJSON(body)
		// Search may return every page granted by Notion. Keep only the owner's
		// explicit local selection; do not imply full-text or descendant search.
		p.Filter = selectedNotionResults(c.Resources)
	case "notion.page", "notion.blocks", "notion.database", "notion.query":
		var a struct {
			PageID       string `json:"page_id"`
			BlockID      string `json:"block_id"`
			DatabaseID   string `json:"database_id"`
			DataSourceID string `json:"data_source_id"`
			Cursor       string `json:"cursor"`
		}
		if err := decode(args, &a); err != nil {
			return p, err
		}
		id := a.PageID
		endpoint := "pages/"
		switch op {
		case "notion.blocks":
			id = a.BlockID
			endpoint = "blocks/"
		case "notion.database":
			id = a.DatabaseID
			endpoint = "databases/"
		case "notion.query":
			id = a.DataSourceID
			endpoint = "data_sources/"
		}
		if !notionID.MatchString(id) || providerCursor(a.Cursor) != nil {
			return p, fault("invalid_notion_resource")
		}
		p.Resource = id
		p.URL = base + endpoint + id
		if op == "notion.blocks" {
			p.URL += "/children?page_size=50"
			if a.Cursor != "" {
				p.URL += "&start_cursor=" + url.QueryEscape(a.Cursor)
			}
		}
		if op == "notion.query" {
			p.Method = "POST"
			p.URL += "/query"
			body := map[string]any{"page_size": 50}
			if a.Cursor != "" {
				body["start_cursor"] = a.Cursor
			}
			p.Body = providerJSON(body)
		}
	case "notion.save_report":
		var a struct {
			PageID string `json:"page_id"`
			Text   string `json:"text"`
		}
		if err := decode(args, &a); err != nil {
			return p, err
		}
		if !notionID.MatchString(a.PageID) || strings.TrimSpace(a.Text) == "" || !boundedProviderText(a.Text, 100000) {
			return p, fault("invalid_notion_report")
		}
		// Append plain text only. Existing blocks and sharing permissions are never
		// touched. Split on Unicode boundaries to respect Notion rich-text limits.
		runes := []rune(a.Text)
		children := []any{}
		for len(runes) > 0 {
			n := len(runes)
			if n > 1800 {
				n = 1800
			}
			s := string(runes[:n])
			runes = runes[n:]
			children = append(children, map[string]any{"object": "block", "type": "paragraph", "paragraph": map[string]any{"rich_text": []any{map[string]any{"type": "text", "text": map[string]string{"content": s}}}}})
		}
		if len(children) > 100 {
			return p, fault("report_too_large")
		}
		p.Resource = a.PageID
		p.Method = "PATCH"
		p.URL = base + "blocks/" + a.PageID + "/children"
		p.Body = providerJSON(map[string]any{"children": children})
		p.Write = true
		p.RequiredScopes = []string{"read_content", "insert_content"}
		p.Verify = verifyNotionAppend(a.PageID, a.Text)
		p.Summary = "Append report to selected Notion page; preserve existing blocks and sharing"
	default:
		return p, fault("unsupported_operation")
	}
	return p, nil
}
func selectedNotionResults(resources []string) func([]byte) (json.RawMessage, error) {
	selected := map[string]bool{}
	for _, id := range resources {
		selected[strings.ToLower(strings.ReplaceAll(id, "-", ""))] = true
	}
	return func(data []byte) (json.RawMessage, error) {
		var body map[string]json.RawMessage
		if json.Unmarshal(data, &body) != nil {
			return nil, fault("invalid_response")
		}
		var items []map[string]json.RawMessage
		if json.Unmarshal(body["results"], &items) != nil {
			return nil, fault("invalid_response")
		}
		filtered := []map[string]json.RawMessage{}
		for _, item := range items {
			var id string
			_ = json.Unmarshal(item["id"], &id)
			if selected[strings.ToLower(strings.ReplaceAll(id, "-", ""))] {
				filtered = append(filtered, item)
			}
		}
		out := map[string]json.RawMessage{"results": providerJSON(filtered), "selection_scope": providerJSON("explicit_selected_ids_only")}
		for _, key := range []string{"has_more", "next_cursor"} {
			if value, ok := body[key]; ok {
				out[key] = value
			}
		}
		return providerJSON(out), nil
	}
}

// The write receipt is not evidence of a completed readback. Every newly
// appended block must be fetched and compared with the exact approved text.
func verifyNotionAppend(parent, text string) func(json.RawMessage) ([]Plan, error) {
	expected := []string{}
	runes := []rune(text)
	for len(runes) > 0 {
		n := len(runes)
		if n > 1800 {
			n = 1800
		}
		expected = append(expected, string(runes[:n]))
		runes = runes[n:]
	}
	return func(raw json.RawMessage) ([]Plan, error) {
		var response struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		}
		if json.Unmarshal(raw, &response) != nil || len(response.Results) != len(expected) {
			return nil, fault("write_verification_failed")
		}
		plans := []Plan{}
		seen := map[string]bool{}
		for i, block := range response.Results {
			if !notionID.MatchString(block.ID) || seen[block.ID] {
				return nil, fault("write_verification_failed")
			}
			seen[block.ID] = true
			id, want := block.ID, expected[i]
			plans = append(plans, Plan{Method: "GET", URL: "https://api.notion.com/v1/blocks/" + id, Headers: map[string]string{"Notion-Version": notionVersion}, RequiredScopes: []string{"read_content"}, Resource: parent, Filter: func(data []byte) (json.RawMessage, error) {
				var b struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Archived bool   `json:"archived"`
					InTrash  bool   `json:"in_trash"`
					Parent   struct {
						Type    string `json:"type"`
						PageID  string `json:"page_id"`
						BlockID string `json:"block_id"`
					} `json:"parent"`
					Paragraph struct {
						RichText []struct {
							PlainText string `json:"plain_text"`
							Text      struct {
								Content string `json:"content"`
							} `json:"text"`
						} `json:"rich_text"`
					} `json:"paragraph"`
				}
				if json.Unmarshal(data, &b) != nil || b.ID != id || b.Type != "paragraph" || b.Archived || b.InTrash {
					return nil, fault("write_verification_failed")
				}
				parentID := b.Parent.PageID
				if b.Parent.Type == "block_id" {
					parentID = b.Parent.BlockID
				} else if b.Parent.Type != "page_id" {
					return nil, fault("write_verification_failed")
				}
				if strings.ToLower(strings.ReplaceAll(parentID, "-", "")) != strings.ToLower(strings.ReplaceAll(parent, "-", "")) {
					return nil, fault("write_verification_failed")
				}
				got := ""
				for _, part := range b.Paragraph.RichText {
					got += part.Text.Content
				}
				if got != want {
					return nil, fault("write_verification_failed")
				}
				return providerJSON(map[string]string{"id": id, "verification": "read_back_verified"}), nil
			}})
		}
		return plans, nil
	}
}
