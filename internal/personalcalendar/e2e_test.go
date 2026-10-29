package personalcalendar

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The optional E2E test requires repository-local Node dependencies. Every
// Google request is intercepted; the only actual socket is the loopback callback.
func TestSignedOAuthToCalendarToolE2E(t *testing.T) {
	if os.Getenv("WUU_CALENDAR_E2E") != "1" {
		t.Skip("set WUU_CALENDAR_E2E=1 after npm --prefix plugins ci --ignore-scripts")
	}
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	output := os.Getenv("WUU_CALENDAR_E2E_OUTPUT")
	if output == "" {
		output = t.TempDir()
	}
	if e = os.MkdirAll(output, 0700); e != nil {
		t.Fatal(e)
	}
	private, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{"kid": "fixture-key", "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(private.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes())}}})
	var offset atomic.Int64
	now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	var mu sync.Mutex
	mode, nonce := "ok", ""
	eventCalls, refreshCalls := 0, 0
	store := &memoryStore{values: map[string][]byte{}}
	clientID := "fixture.apps.googleusercontent.com"
	broker := newBroker(Config{ClientID: clientID}, store, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Host == "oauth2.googleapis.com" && r.URL.Path == "/token" {
			if e := r.ParseForm(); e != nil {
				return nil, e
			}
			token := map[string]any{"access_token": "synthetic-go-only-access", "refresh_token": "synthetic-go-only-refresh", "token_type": "Bearer", "expires_in": 3600, "scope": strings.Join(Scopes(), " ")}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshCalls++
			} else {
				payload, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "aud": clientID, "sub": "fixture-sub", "nonce": nonce, "iat": now().Unix(), "exp": now().Add(time.Hour).Unix()})
				signed := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture-key"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
				hash := sha256.Sum256([]byte(signed))
				signature, e := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, hash[:])
				if e != nil {
					return nil, e
				}
				token["id_token"] = signed + "." + base64.RawURLEncoding.EncodeToString(signature)
			}
			data, _ := json.Marshal(token)
			return response(200, string(data)), nil
		}
		if r.URL.Host != "www.googleapis.com" {
			return nil, errors.New("fixture rejected non-Google target")
		}
		if r.URL.Path == "/oauth2/v3/certs" {
			return response(200, string(jwks)), nil
		}
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-go-only-access" {
			return nil, errors.New("fixture read-only boundary failed")
		}
		if r.URL.Path == "/calendar/v3/users/me/calendarList" {
			return response(200, `{"items":[{"id":"fixture@example.invalid","summary":"Fixture calendar","accessRole":"owner","primary":true,"timeZone":"America/New_York"}]}`), nil
		}
		if !strings.HasSuffix(r.URL.Path, "/events") {
			return nil, errors.New("fixture rejected unknown endpoint")
		}
		eventCalls++
		if mode == "rate-limit-once" {
			mode = "ok"
			resp := response(429, `{}`)
			resp.Header.Set("Retry-After", "0")
			return resp, nil
		}
		if mode == "timeout-once" {
			mode = "ok"
			return nil, context.DeadlineExceeded
		}
		if mode == "revoked" {
			return response(401, `{"error":{"code":401}}`), nil
		}
		event := map[string]any{"id": "instance", "status": "confirmed", "summary": "Fixture meeting", "start": map[string]string{"dateTime": "2026-11-01T09:00:00-05:00"}, "end": map[string]string{"dateTime": "2026-11-01T10:00:00-05:00"}, "recurringEventId": "series", "originalStartTime": map[string]string{"dateTime": "2026-11-01T08:00:00-05:00"}}
		if mode == "recurring-cancelled" {
			event["status"] = "cancelled"
		}
		page := map[string]any{"timeZone": "America/New_York"}
		if r.URL.Query().Get("pageToken") == "" {
			page["items"] = []any{map[string]any{"id": "day", "status": "confirmed", "summary": "Fixture day", "start": map[string]string{"date": "2026-11-01"}, "end": map[string]string{"date": "2026-11-02"}}, event}
			page["nextPageToken"] = "fixture-page2"
		} else {
			page["items"] = []any{event, map[string]any{"id": "deleted", "status": "cancelled", "recurringEventId": "series", "originalStartTime": map[string]string{"dateTime": "2026-11-01T15:00:00-05:00"}}}
		}
		body, _ := json.Marshal(page)
		return response(200, string(body)), nil
	})}, t.TempDir()+"/activation")
	broker.now = now
	broker.wait = func(context.Context, time.Duration) error { return nil }
	defer broker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "internal/personalcalendar/testdata/bridge.mjs")
	command.Dir = root
	command.Env = append(os.Environ(), "WUU_CALENDAR_E2E_REPORT="+filepath.Join(output, "report.json"))
	stdin, e := command.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := command.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	encoder := json.NewEncoder(stdin)
	finished := false
	var methods []string
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				ConnectionID string     `json:"connection_id"`
				CalendarIDs  []string   `json:"calendar_ids"`
				Query        EventQuery `json:"query"`
				Mode         string     `json:"mode"`
			} `json:"params"`
		}
		if e = json.Unmarshal(scanner.Bytes(), &request); e != nil {
			t.Fatal(e)
		}
		methods = append(methods, request.Method)
		var result any
		var failure error
		switch request.Method {
		case "fixture.connect":
			var attempt AuthorizationAttempt
			attempt, failure = broker.Start(ctx, actor)
			if failure == nil {
				u, _ := url.Parse(attempt.AuthorizationURL)
				mu.Lock()
				nonce = u.Query().Get("nonce")
				mu.Unlock()
				var response *http.Response
				response, failure = (&http.Client{Timeout: 5 * time.Second}).Get(u.Query().Get("redirect_uri") + "?state=" + u.Query().Get("state") + "&code=fixture-code")
				if failure == nil {
					_ = response.Body.Close()
					if response.StatusCode != 200 {
						failure = errors.New("loopback failed")
					}
				}
			}
			if failure == nil {
				result, failure = broker.Status(ctx, actor)
			}
		case "authorize":
			result, failure = broker.Authorize(ctx, actor)
		case "status":
			result, failure = broker.Status(ctx, actor)
		case "calendars":
			result, failure = broker.Calendars(ctx, actor, request.Params.ConnectionID)
		case "select":
			failure = broker.Select(ctx, actor, request.Params.ConnectionID, request.Params.CalendarIDs)
		case "eventsPage":
			result, failure = broker.EventsPage(ctx, actor, request.Params.ConnectionID, request.Params.Query)
		case "disconnect":
			result, failure = broker.Disconnect(ctx, actor, request.Params.ConnectionID, false)
		case "fixture.mode":
			mu.Lock()
			mode = request.Params.Mode
			mu.Unlock()
		case "fixture.expire":
			offset.Store(int64(2 * time.Hour))
		case "fixture.stats":
			mu.Lock()
			result = map[string]int{"event_calls": eventCalls, "refresh_calls": refreshCalls}
			mu.Unlock()
		case "fixture.finished":
			finished = true
		default:
			failure = errors.New("fixture method denied")
		}
		reply := map[string]any{"id": request.ID, "result": result}
		if failure != nil {
			var safe *Failure
			if !errors.As(failure, &safe) {
				safe = &Failure{Code: "fixture_error"}
			}
			reply["error"] = safe
		}
		if e = encoder.Encode(reply); e != nil {
			t.Fatal(e)
		}
	}
	_ = stdin.Close()
	if e = scanner.Err(); e != nil {
		t.Fatal(e)
	}
	if e = command.Wait(); e != nil {
		t.Fatalf("bridge failed: %v\n%s", e, stderr.String())
	}
	if !finished {
		t.Fatal("fixture did not complete")
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh count %d", refreshCalls)
	}
	if len(store.values) != 0 {
		t.Fatal("local credential survived disconnect")
	}
	audit, _ := json.MarshalIndent(map[string]any{"passed": true, "google_network_requests": 0, "system_keychain_access": false, "event_http_attempts": eventCalls, "refreshes": refreshCalls, "host_methods": methods}, "", "  ")
	if e = os.WriteFile(filepath.Join(output, "audit.json"), audit, 0600); e != nil {
		t.Fatal(e)
	}
	t.Log("signed OAuth → loopback → Go broker → Node adapter → existing CAS → model tool passed; Google transport and credential store were synthetic")
}
