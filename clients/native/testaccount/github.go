package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Substitute only the external provider inside this disposable process. The
// production OAuth handler still validates cookies, PKCE, enrollment and replay.
func fakeGitHub() *sync.Map {
	challenges := &sync.Map{}
	local := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.String() {
		case "https://github.com/login/oauth/access_token":
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			expected, ok := challenges.LoadAndDelete(r.Form.Get("code"))
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || expected != base64.RawURLEncoding.EncodeToString(hash[:]) || r.Form.Get("client_secret") != "fixture-only-secret" {
				return nil, fmt.Errorf("invalid fixture PKCE or provider credentials")
			}
			body = `{"access_token":"fixture-provider-token"}`
		case "https://api.github.com/user":
			if r.Header.Get("Authorization") != "Bearer fixture-provider-token" {
				return nil, fmt.Errorf("missing provider token")
			}
			body = `{"id":12345,"login":"native-oauth-test"}`
		default:
			if r.URL.Scheme != "http" || r.URL.Hostname() != "127.0.0.1" {
				return nil, fmt.Errorf("external network disabled in fixture")
			}
			return local.RoundTrip(r)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return challenges
}

func authorizeBrowser(server, id string, denied bool, challenges *sync.Map) error {
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := browser.Get(server + "/v1/account/github/authorize?state=" + url.QueryEscape(id))
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		return fmt.Errorf("authorize: %d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return err
	}
	challenges.Store(id, location.Query().Get("code_challenge"))
	query := url.Values{"state": {id}, "code": {id}}
	if denied {
		query.Set("error", "access_denied")
	}
	response, err = browser.Get(server + "/v1/account/github/callback?" + query.Encode())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("callback: %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), "wuu://account/github") {
		return fmt.Errorf("missing native return link")
	}
	return nil
}
