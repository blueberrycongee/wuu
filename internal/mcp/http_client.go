package mcp

import "net/http"

// toolHTTPClient keeps a separate connection pool so a tool can wait for its
// first byte for its full CallTool budget. The connection/listener client keeps
// its header timeout; CallTool's context bounds every phase of the tool POST.
// Copy the client as well as its transport to preserve redirect/auth policy.
func toolHTTPClient(base *http.Client) *http.Client {
	client := *base
	transport := base.Transport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 0
	client.Transport = transport
	return &client
}
