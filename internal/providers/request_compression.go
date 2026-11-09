package providers

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// This is an experimental cutoff, not a measured network-latency optimum.
const requestGzipMinBytes = 64 << 10

// PrepareRequestCompression optionally compresses a serialized model request.
// The experiment is off unless WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS lists
// the exact endpoint URL. Opting in asserts that the endpoint accepts request
// gzip; API-format compatibility alone is not enough. Call after custom headers
// are applied and before recording a submission. body must be the bytes used
// to construct req; token/cost estimates must continue to use that original size.
//
// It preserves replay bytes and the caller's HTTP timeout/transport, and returns
// a client that restricts compressed redirects to opted-in endpoints. It never
// sends a request, retries, or falls back after a remote failure.
func PrepareRequestCompression(req *http.Request, body []byte, client *http.Client) (*http.Client, error) {
	if len(body) < requestGzipMinBytes || req.Header.Get("Content-Encoding") != "" {
		return client, nil
	}
	endpoints := os.Getenv("WUU_EXPERIMENTAL_REQUEST_GZIP_ENDPOINTS")
	if !requestGzipEndpointEnabled(req.URL, endpoints) {
		return client, nil
	}

	var compressed bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	// Bound cancellation latency while compressing large histories or media.
	for remaining := body; len(remaining) > 0; {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		n := min(len(remaining), 32<<10)
		if _, err := writer.Write(remaining[:n]); err != nil {
			return nil, fmt.Errorf("compress request body: %w", err)
		}
		remaining = remaining[n:]
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish compressed request body: %w", err)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if compressed.Len() >= len(body) {
		return client, nil
	}

	wire := compressed.Bytes()
	req.Body = io.NopCloser(bytes.NewReader(wire))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(wire)), nil
	}
	req.ContentLength = int64(len(wire))
	req.Header.Set("Content-Encoding", "gzip")
	// net/http writes the authoritative ContentLength. A custom header may
	// still describe the uncompressed body and must not survive the change.
	req.Header.Del("Content-Length")

	compressedClient := *client
	compressedClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if client.CheckRedirect != nil {
			if err := client.CheckRedirect(next, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		// A caller's redirect hook may change the destination. Check the final
		// URL only after honoring its stop policy (including ErrUseLastResponse).
		if !requestGzipEndpointEnabled(next.URL, endpoints) {
			return requestCompressionRedirectError{}
		}
		return nil
	}
	return &compressedClient, nil
}

func requestGzipEndpointEnabled(endpoint *url.URL, configured string) bool {
	if configured == "" || endpoint.User != nil || endpoint.Fragment != "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return false
	}
	// Deliberately do not match hosts, prefixes, or normalized query strings.
	// A distinct path/query can select a backend with different capabilities.
	for _, allowed := range strings.Split(configured, ",") {
		if strings.TrimSpace(allowed) == endpoint.String() {
			return true
		}
	}
	return false
}

// A deterministic opt-in failure must not become a retryable network error
// when net/http wraps the redirect hook's error in url.Error.
type requestCompressionRedirectError struct{}

func (requestCompressionRedirectError) Error() string {
	return "compressed request redirect endpoint is not opted in"
}

func (requestCompressionRedirectError) InferenceRecoveryAction() RecoveryActionKind {
	return RecoveryStop
}
