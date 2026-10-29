package personalconnectors

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var githubRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

func buildGitHub(c Connection, op string, args json.RawMessage) (Plan, error) {
	var a struct {
		Repository string `json:"repository"`
		Number     int    `json:"number"`
		Ref        string `json:"ref"`
		Page       int    `json:"page"`
	}
	p := Plan{Method: "GET", Headers: map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}}
	if err := decode(args, &a); err != nil {
		return p, err
	}
	if !githubRepo.MatchString(a.Repository) || strings.Contains(a.Repository, "..") || a.Page < 0 || a.Page > 1000 {
		return p, fault("invalid_github_resource")
	}
	if a.Page == 0 {
		a.Page = 1
	}
	p.Resource = a.Repository
	p.URL = "https://api.github.com/repos/" + a.Repository
	switch op {
	case "github.pr", "github.issue":
		if a.Number < 1 || a.Number > 100000000 {
			return p, fault("invalid_issue_number")
		}
		if op == "github.pr" {
			p.URL += fmt.Sprintf("/pulls/%d", a.Number)
			p.RequiredScopes = []string{"pull_requests:read"}
		} else {
			p.URL += fmt.Sprintf("/issues/%d", a.Number)
			p.RequiredScopes = []string{"issues:read"}
		}
	case "github.checks":
		if a.Ref == "" || !boundedProviderText(a.Ref, 200) || strings.ContainsAny(a.Ref, "\n?#") || strings.Contains(a.Ref, "..") {
			return p, fault("invalid_git_ref")
		}
		p.URL += "/commits/" + url.PathEscape(a.Ref) + fmt.Sprintf("/check-runs?per_page=50&page=%d", a.Page)
		p.RequiredScopes = []string{"checks:read"}
	case "github.releases":
		p.URL += fmt.Sprintf("/releases?per_page=50&page=%d", a.Page)
		p.RequiredScopes = []string{"contents:read"}
	default:
		return p, fault("unsupported_operation")
	}
	return p, nil
}
