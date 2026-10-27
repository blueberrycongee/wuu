package personalconnectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPollBaselineChangeRestartAndReconnect(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "session"}
	body := `{"threads":[{"id":"one"}]}`
	calls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	b := New(Options{Store: s, HTTPClient: client})
	defer b.Close()
	g := Grant{Connection: Connection{ID: "gmail", Provider: "gmail", AccountID: "subject", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}
	if e := b.RegisterVerified(ctx, a, g); e != nil {
		t.Fatal(e)
	}
	q := PollRequest{"inbox", "gmail", "gmail.search", json.RawMessage(`{"query":"in:inbox"}`)}
	baseline, e := b.Poll(ctx, a, q)
	if e != nil || baseline.State != "baseline" || baseline.Sequence != 0 || baseline.ChangeID != "" {
		t.Fatalf("baseline %#v %v", baseline, e)
	}
	body = `{"threads":[{"id":"two"}]}`
	change, e := b.Poll(ctx, a, q)
	if e != nil || change.State != "changed" || change.Sequence != 1 || change.ChangeID == "" {
		t.Fatalf("change %#v %v", change, e)
	}
	restarted := New(Options{Store: s, HTTPClient: client})
	defer restarted.Close()
	same, e := restarted.Poll(ctx, a, q)
	if e != nil || same.State != "unchanged" || same.Sequence != 1 || same.ChangeID != "" {
		t.Fatalf("repeat %#v %v", same, e)
	}
	if e = restarted.Disconnect(ctx, a, "gmail"); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.Poll(ctx, a, q); e == nil {
		t.Fatal("disconnected poll")
	}
	if e = restarted.RegisterVerified(ctx, a, g); e != nil {
		t.Fatal(e)
	}
	reset, e := restarted.Poll(ctx, a, q)
	if e != nil || reset.State != "baseline" || reset.Sequence != 1 || reset.ChangeID != "" {
		t.Fatalf("reconnect %#v %v", reset, e)
	}
	q.Arguments = json.RawMessage(`{"query":"different"}`)
	before := calls
	if _, e = restarted.Poll(ctx, a, q); e == nil || calls != before {
		t.Fatal("silent source definition change")
	}
	for _, raw := range s.values {
		if strings.Contains(string(raw), `"threads"`) {
			t.Fatal("poll persisted external content")
		}
	}
}
func TestPollRejectsWritesAndUnapprovedWorkWithoutHTTP(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	b := New(Options{Store: s})
	defer b.Close()
	g := Grant{Connection: Connection{ID: "work", Provider: "slack", AccountID: "T:U", Domain: "work", Scopes: []string{"channels:history"}, Resources: []string{"C123"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}
	if e := b.RegisterVerified(ctx, a, g); e != nil {
		t.Fatal(e)
	}
	if _, e := b.Poll(ctx, a, PollRequest{"work", "work", "slack.history", json.RawMessage(`{"channel_id":"C123"}`)}); code(e) != "confirmation_required" {
		t.Fatalf("work unapproved %v", e)
	}
	if _, e := b.Poll(ctx, a, PollRequest{"send", "work", "gmail.send", json.RawMessage(`{}`)}); code(e) != "poll_operation_not_supported" {
		t.Fatalf("write poll %v", e)
	}
}

func TestPollConcurrentOldResponseDoesNotRollBackNewerSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	response := func(body string) *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	}
	baseline := New(Options{Store: s, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(`{"threads":[]}`), nil })}})
	defer baseline.Close()
	g := Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "subject", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}
	if e := baseline.RegisterVerified(ctx, a, g); e != nil {
		t.Fatal(e)
	}
	q := PollRequest{"inbox", "g", "gmail.search", json.RawMessage(`{}`)}
	if _, e := baseline.Poll(ctx, a, q); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	old := New(Options{Store: s, HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		select {
		case <-release:
			return response(`{"threads":[{"id":"old"}]}`), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}})
	defer old.Close()
	fresh := New(Options{Store: s, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(`{"threads":[{"id":"new"}]}`), nil })}})
	defer fresh.Close()
	done := make(chan error, 1)
	go func() { _, e := old.Poll(ctx, a, q); done <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("old poll did not start")
	}
	next, e := fresh.Poll(ctx, a, q)
	if e != nil || next.State != "changed" || next.Sequence != 1 {
		t.Fatalf("newer %#v %v", next, e)
	}
	close(release)
	if e = <-done; code(e) != "poll_superseded" {
		t.Fatalf("old response not fenced: %v", e)
	}
	again, e := fresh.Poll(ctx, a, q)
	if e != nil || again.State != "unchanged" || again.Sequence != 1 {
		t.Fatalf("snapshot rolled back %#v %v", again, e)
	}
}
func TestPollFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx := context.Background()
	s := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "s"}
	status := 200
	body := `{"threads":[]}`
	b := New(Options{Store: s, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	defer b.Close()
	if e := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "subject", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}}, AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	q := PollRequest{"inbox", "g", "gmail.search", json.RawMessage(`{}`)}
	if _, e := b.Poll(ctx, a, q); e != nil {
		t.Fatal(e)
	}
	status = 503
	if _, e := b.Poll(ctx, a, q); e == nil {
		t.Fatal("failed request committed")
	}
	status = 200
	body = `{"threads":[{"id":"new"}]}`
	next, e := b.Poll(ctx, a, q)
	if e != nil || next.State != "changed" || next.Sequence != 1 {
		t.Fatalf("failed read advanced checkpoint %#v %v", next, e)
	}
}

func TestPollCheckpointsAndChangesAreSessionBound(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{values: map[string][]byte{}}
	a := Actor{"personal", "space", "one"}
	other := Actor{"personal", "space", "two"}
	body := `{"threads":[{"id":"first"}]}`
	b := New(Options{Store: store, HTTPClient: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	defer b.Close()
	grant := Grant{Connection: Connection{ID: "g", Provider: "gmail", AccountID: "subject", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}}, AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)}
	if err := b.RegisterVerified(ctx, a, grant); err != nil {
		t.Fatal(err)
	}
	query := PollRequest{"inbox", "g", "gmail.search", json.RawMessage(`{}`)}
	for _, actor := range []Actor{a, other} {
		first, err := b.Poll(ctx, actor, query)
		if err != nil || first.State != "baseline" {
			t.Fatalf("session inherited another baseline: %+v %v", first, err)
		}
	}
	body = `{"threads":[{"id":"second"}]}`
	first, err := b.Poll(ctx, a, query)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Poll(ctx, other, query)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "changed" || second.State != "changed" || first.ChangeID == second.ChangeID {
		t.Fatalf("sessions shared change identity: %+v %+v", first, second)
	}
}
