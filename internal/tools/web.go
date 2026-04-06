package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	webFetchMaxBytes = 1 * 1024 * 1024 // 1MB
	webFetchTimeout  = 30 * time.Second
)

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

func (t *Toolkit) webSearch(argsJSON string) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("web_search requires query")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 20 {
		maxResults = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), webFetchTimeout)
	defer cancel()

	results, err := duckDuckGoSearch(ctx, args.Query, maxResults)
	if err != nil {
		return mustJSON(map[string]any{
			"error": fmt.Sprintf("search failed: %s", err),
		})
	}

	return mustJSON(map[string]any{
		"query":   args.Query,
		"results": results,
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

	client := &http.Client{Timeout: webFetchTimeout}
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

func (t *Toolkit) webFetch(argsJSON string) (string, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.URL) == "" {
		return "", fmt.Errorf("web_fetch requires url")
	}

	ctx, cancel := context.WithTimeout(context.Background(), webFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", args.URL, nil)
	if err != nil {
		return mustJSON(map[string]any{
			"error": fmt.Sprintf("invalid URL: %s", err),
		})
	}
	req.Header.Set("User-Agent", randomUA())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", randomAcceptLang())

	client := &http.Client{Timeout: webFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return mustJSON(map[string]any{
			"url":   args.URL,
			"error": fmt.Sprintf("fetch failed: %s", err),
		})
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return mustJSON(map[string]any{
			"url":         args.URL,
			"status_code": resp.StatusCode,
			"error":       fmt.Sprintf("HTTP %d", resp.StatusCode),
		})
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
	if err != nil {
		return mustJSON(map[string]any{
			"url":   args.URL,
			"error": fmt.Sprintf("read body: %s", err),
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

	return mustJSON(map[string]any{
		"url":          args.URL,
		"content_type": contentType,
		"content":      content,
		"size":         len(body),
		"truncated":    truncated,
	})
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
