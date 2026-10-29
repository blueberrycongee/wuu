package personalconnectors

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

func buildDrive(c Connection, op string, args json.RawMessage) (Plan, error) {
	scope := "https://www.googleapis.com/auth/drive.file"
	if !slices.Contains(c.Scopes, scope) {
		return Plan{}, fault("selected_file_scope_required")
	}
	p := Plan{Method: "GET", RequiredScopes: []string{scope}}
	base := "https://www.googleapis.com/drive/v3/files"
	switch op {
	case "drive.search":
		var a struct {
			Resource string `json:"resource"`
			Query    string `json:"query"`
			Cursor   string `json:"cursor"`
		}
		if err := decode(args, &a); err != nil {
			return p, err
		}
		if !providerID.MatchString(a.Resource) || !boundedProviderText(a.Query, 512) || providerCursor(a.Cursor) != nil {
			return p, fault("invalid_drive_search")
		}
		q := "trashed = false"
		if a.Query != "" {
			q += " and name contains '" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(a.Query) + "'"
		}
		v := url.Values{"q": {q}, "pageSize": {"50"}, "fields": {"nextPageToken,incompleteSearch,files(id,name,mimeType,modifiedTime,size,webViewLink)"}, "spaces": {"drive"}}
		if a.Cursor != "" {
			v.Set("pageToken", a.Cursor)
		}
		p.Resource = a.Resource
		p.URL = base + "?" + v.Encode()
		selected := slices.Clone(c.Resources)
		p.Filter = func(data []byte) (json.RawMessage, error) {
			var body map[string]json.RawMessage
			if json.Unmarshal(data, &body) != nil {
				return nil, fault("invalid_response")
			}
			var files []map[string]json.RawMessage
			if json.Unmarshal(body["files"], &files) != nil {
				return nil, fault("invalid_response")
			}
			out := []map[string]json.RawMessage{}
			for _, f := range files {
				var id string
				_ = json.Unmarshal(f["id"], &id)
				if slices.Contains(selected, id) {
					out = append(out, f)
				}
			}
			result := map[string]json.RawMessage{"files": providerJSON(out), "selection_scope": providerJSON("explicit_selected_files_only")}
			for _, key := range []string{"nextPageToken", "incompleteSearch"} {
				if value, ok := body[key]; ok {
					result[key] = value
				}
			}
			return providerJSON(result), nil
		}
	case "drive.file", "drive.export", "drive.pdf":
		var a struct {
			FileID string `json:"file_id"`
		}
		if err := decode(args, &a); err != nil {
			return p, err
		}
		if !providerID.MatchString(a.FileID) {
			return p, fault("invalid_drive_file")
		}
		p.Resource = a.FileID
		p.URL = base + "/" + a.FileID
		switch op {
		case "drive.file":
			p.URL += "?fields=id,name,mimeType,size,modifiedTime,webViewLink,capabilities(canDownload)"
		case "drive.export":
			p.URL += "/export?mimeType=text%2Fplain"
			p.ResponseKind = "text"
		case "drive.pdf":
			p.URL += "?alt=media"
			p.ResponseKind = "pdf"
		}
	default:
		return p, fault("unsupported_operation")
	}
	return p, nil
}
