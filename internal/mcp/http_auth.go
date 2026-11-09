package mcp

import (
	"errors"
	"net/http"
	"net/url"
)

// Custom resource headers and OAuth credentials belong to the original origin.
// Clone the client so an injected or shared client keeps its own redirect policy.
func withSameOriginRedirects(client *http.Client) *http.Client {
	bounded := *client
	bounded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && !sameOrigin(via[0].URL.String(), req.URL.String()) {
			return errors.New("MCP credential-bearing request cannot redirect to another origin")
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &bounded
}

func sameOrigin(left, right string) bool {
	leftResource, rightResource := canonicalOAuthResource(left), canonicalOAuthResource(right)
	if leftResource == "" || rightResource == "" {
		return false
	}
	leftURL, _ := url.Parse(leftResource)
	rightURL, _ := url.Parse(rightResource)
	return leftURL.Scheme == rightURL.Scheme && leftURL.Host == rightURL.Host
}
