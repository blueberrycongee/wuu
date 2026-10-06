package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/toolresult"

	"golang.org/x/net/html"
)

const (
	webFetchMaxBytes = 1 * 1024 * 1024 // 1MB
	webFetchTimeout  = 30 * time.Second
)

type webEvidence struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	Source               string `json:"source"`
	SourceTier           string `json:"source_tier"`
	RetrievedAt          string `json:"retrieved_at"`
	VersionMatchedToRepo string `json:"version_matched_to_repo"`
}

type webPackageContext struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Ecosystem string `json:"ecosystem"`
}

func newWebEvidence(kind, source, sourceTier string, retrievedAt time.Time) webEvidence {
	retrievedAt = retrievedAt.UTC()
	sum := sha256.Sum256([]byte(kind + "\x00" + source + "\x00" + retrievedAt.Format(time.RFC3339Nano)))
	return webEvidence{
		ID:                   "web_" + hex.EncodeToString(sum[:])[:16],
		Kind:                 kind,
		Source:               source,
		SourceTier:           sourceTier,
		RetrievedAt:          retrievedAt.Format(time.RFC3339Nano),
		VersionMatchedToRepo: "unknown",
	}
}

func applyWebEvidenceVersionContext(evidence *webEvidence, versionHint string, pkg webPackageContext) {
	if evidence == nil {
		return
	}
	version := webVersionMatchedToRepo(versionHint, pkg)
	if version != "" {
		evidence.VersionMatchedToRepo = version
	}
}

func webVersionMatchedToRepo(versionHint string, pkg webPackageContext) string {
	versionHint = strings.TrimSpace(versionHint)
	if versionHint != "" {
		return compactWebEvidenceValue(versionHint)
	}
	name := strings.TrimSpace(pkg.Name)
	version := strings.TrimSpace(pkg.Version)
	ecosystem := strings.TrimSpace(pkg.Ecosystem)
	if name == "" && version == "" && ecosystem == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if ecosystem != "" {
		parts = append(parts, ecosystem)
	}
	label := name
	if version != "" {
		if label != "" {
			label += "@" + version
		} else {
			label = version
		}
	}
	if label != "" {
		parts = append(parts, label)
	}
	return compactWebEvidenceValue(strings.Join(parts, " "))
}

func compactWebEvidenceValue(value string) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	const maxRunes = 160
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

// isBlockedIP returns true if the IP should not be reachable from web_fetch.
// Covers loopback (127/8, ::1), RFC1918 private space, link-local
// (169.254/16 — includes AWS/GCP/Azure metadata endpoints), unspecified
// (0.0.0.0, ::), and multicast. Callers also reject non-http(s) schemes.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast()
}

// validateFetchURL rejects URLs that target internal addresses or use
// schemes other than http/https. Called on the original URL and on every
// redirect target.
func validateFetchURL(u *url.URL, allowInternal bool) error {
	if u == nil {
		return fmt.Errorf("blocked: missing URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("blocked: unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("blocked: missing host")
	}
	if ip := net.ParseIP(host); ip != nil && !allowInternal && isBlockedIP(ip) {
		return fmt.Errorf("blocked: internal address %s", ip)
	}
	return nil
}

// safeDialContext refuses connections to internal addresses. This catches
// DNS rebinding and redirects whose host resolves to a private IP, which
// validateFetchURL alone cannot detect.
func guardedDialContext(allowInternal bool) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if allowInternal {
		return dialer.DialContext
	}
	return safeDialContext
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("blocked: no addresses for %q", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("blocked: %q resolves to internal address %s", host, ip)
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

func safeHTTPClient(allowInternal bool) *http.Client {
	return &http.Client{
		Timeout: webFetchTimeout,
		Transport: &http.Transport{
			DialContext:           guardedDialContext(allowInternal),
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateFetchURL(req.URL, allowInternal)
		},
	}
}

var browserUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
}

var webAcceptLanguages = []string{
	"en-US,en;q=0.9",
	"en-US,en;q=0.9,es;q=0.8",
	"en-GB,en;q=0.9,en-US;q=0.8",
	"en-US,en;q=0.5",
}

var collapseNewlines = regexp.MustCompile(`\n{3,}`)

// skipTags are HTML elements whose content is noise for text extraction.
var skipTags = map[string]bool{
	"script":   true,
	"style":    true,
	"nav":      true,
	"header":   true,
	"footer":   true,
	"aside":    true,
	"noscript": true,
	"iframe":   true,
	"svg":      true,
}

func randomUA() string {
	return browserUserAgents[rand.IntN(len(browserUserAgents))]
}

func randomAcceptLang() string {
	return webAcceptLanguages[rand.IntN(len(webAcceptLanguages))]
}

// ---------------------------------------------------------------------------
// web_search
// ---------------------------------------------------------------------------

func webSearchExecute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Query          string            `json:"query"`
		VersionHint    string            `json:"version_hint"`
		PackageContext webPackageContext `json:"package_context"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("web_search requires query")
	}
	maxResults := 10
	evidence := newWebEvidence("search", "https://lite.duckduckgo.com/lite/", "search_index", time.Now())
	applyWebEvidenceVersionContext(&evidence, args.VersionHint, args.PackageContext)

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	results, err := duckDuckGoSearch(ctx, args.Query, maxResults)
	if err != nil {
		return mustJSON(map[string]any{
			"action":   "web_search",
			"query":    args.Query,
			"evidence": evidence,
			"error":    fmt.Sprintf("search failed: %s", err),
		})
	}

	return mustJSON(map[string]any{
		"action":   "web_search",
		"query":    args.Query,
		"evidence": evidence,
		"results":  results,
	})
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func duckDuckGoSearch(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	searchURL := "https://lite.duckduckgo.com/lite/"

	form := url.Values{}
	form.Set("q", query)

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", randomUA())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", randomAcceptLang())
	req.Header.Set("Accept-Encoding", "identity")

	client := safeHTTPClient(false)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return parseDDGResults(string(body), maxResults)
}

func parseDDGResults(htmlContent string, maxResults int) ([]searchResult, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	var results []searchResult
	var current *searchResult

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == html.ElementNode {
			if n.Data == "a" && nodeHasClass(n, "result-link") {
				// Flush previous result.
				if current != nil && current.URL != "" {
					results = append(results, *current)
					if len(results) >= maxResults {
						return
					}
				}
				current = &searchResult{Title: extractText(n)}
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						current.URL = cleanDDGURL(attr.Val)
						break
					}
				}
			}
			if n.Data == "td" && nodeHasClass(n, "result-snippet") && current != nil {
				current.Snippet = extractText(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if len(results) >= maxResults {
				return
			}
			traverse(c)
		}
	}
	traverse(doc)

	// Flush last result.
	if current != nil && current.URL != "" && len(results) < maxResults {
		results = append(results, *current)
	}

	return results, nil
}

func nodeHasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			return slices.Contains(strings.Fields(attr.Val), class)
		}
	}
	return false
}

func extractText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func cleanDDGURL(raw string) string {
	if strings.HasPrefix(raw, "//duckduckgo.com/l/?uddg=") {
		if _, after, ok := strings.Cut(raw, "uddg="); ok {
			encoded := after
			if idx := strings.Index(encoded, "&"); idx != -1 {
				encoded = encoded[:idx]
			}
			if decoded, err := url.QueryUnescape(encoded); err == nil {
				return decoded
			}
		}
	}
	return raw
}

// ---------------------------------------------------------------------------
// web_fetch
// ---------------------------------------------------------------------------

func webFetchExecute(ctx context.Context, argsJSON string, allowInternal bool) (string, error) {
	var args struct {
		URL            string            `json:"url"`
		VersionHint    string            `json:"version_hint"`
		PackageContext webPackageContext `json:"package_context"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.URL) == "" {
		return "", fmt.Errorf("web_fetch requires url")
	}
	source := strings.TrimSpace(args.URL)
	evidence := newWebEvidence("fetch", source, "web_page", time.Now())
	applyWebEvidenceVersionContext(&evidence, args.VersionHint, args.PackageContext)

	parsed, parseErr := url.Parse(source)
	if parseErr != nil {
		return mustJSON(map[string]any{
			"action":   "web_fetch",
			"url":      args.URL,
			"evidence": evidence,
			"error":    fmt.Sprintf("invalid URL: %s", parseErr),
		})
	}
	if err := validateFetchURL(parsed, allowInternal); err != nil {
		return mustJSON(map[string]any{
			"action":   "web_fetch",
			"url":      args.URL,
			"evidence": evidence,
			"error":    err.Error(),
		})
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", parsed.String(), nil)
	if err != nil {
		return mustJSON(map[string]any{
			"action":   "web_fetch",
			"url":      args.URL,
			"evidence": evidence,
			"error":    fmt.Sprintf("invalid URL: %s", err),
		})
	}
	req.Header.Set("User-Agent", randomUA())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", randomAcceptLang())

	client := safeHTTPClient(allowInternal)
	resp, err := client.Do(req)
	if err != nil {
		return mustJSON(map[string]any{
			"action":   "web_fetch",
			"url":      args.URL,
			"evidence": evidence,
			"error":    fmt.Sprintf("fetch failed: %s", err),
		})
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return mustJSON(map[string]any{
			"action":      "web_fetch",
			"url":         args.URL,
			"evidence":    evidence,
			"status_code": resp.StatusCode,
			"error":       fmt.Sprintf("HTTP %d", resp.StatusCode),
		})
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
	if err != nil {
		return mustJSON(map[string]any{
			"action":   "web_fetch",
			"url":      args.URL,
			"evidence": evidence,
			"error":    fmt.Sprintf("read body: %s", err),
		})
	}

	truncated := len(body) > webFetchMaxBytes
	if truncated {
		body = body[:webFetchMaxBytes]
	}

	contentType := resp.Header.Get("Content-Type")
	content := string(body)

	switch {
	case strings.Contains(contentType, "text/html"):
		content = htmlToText(content)
	case strings.Contains(contentType, "json"):
		content = prettyJSON(content)
	}

	envelope := map[string]any{
		"action": "web_fetch", "url": args.URL, "evidence": evidence,
		"content_type": contentType, "content": content, "size": len(body), "truncated": truncated,
	}
	encoded, err := mustJSON(envelope)
	if err != nil || len(encoded) <= toolresult.MaxStructuredJSONSize {
		return encoded, err
	}
	// The transport bounds serialized text, not the downloaded body. Metadata,
	// JSON escaping and pretty-printing must fit without turning a fetch into an error.
	envelope["truncated"] = true
	low, high := 0, len(content)
	for low < high {
		middle := (low + high + 1) / 2
		keep := middle
		for keep > 0 && keep < len(content) && !utf8.RuneStart(content[keep]) {
			keep--
		}
		envelope["content"] = content[:keep]
		candidate, _ := mustJSON(envelope)
		if len(candidate) <= toolresult.MaxStructuredJSONSize {
			low = middle
		} else {
			high = middle - 1
		}
	}
	for low > 0 && low < len(content) && !utf8.RuneStart(content[low]) {
		low--
	}
	envelope["content"] = content[:low]
	return mustJSON(envelope)
}

// htmlToText strips noisy elements and extracts readable text from HTML.
func htmlToText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteByte('\n')
			}
		}
		// Add extra newline after block elements for readability.
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6",
				"li", "tr", "blockquote", "pre", "section", "article":
				defer func() { sb.WriteByte('\n') }()
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	result := sb.String()
	result = collapseNewlines.ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

// prettyJSON attempts to format JSON; returns original on failure.
func prettyJSON(raw string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return buf.String()
}
