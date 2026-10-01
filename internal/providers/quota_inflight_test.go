package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Observe when a caller is waiting without advancing clocks or delaying the
// response. The context otherwise has the ordinary cancellation contract.
type quotaObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *quotaObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type quotaReadResult struct {
	body []byte
	err  error
}

func quotaTestRequest(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer synthetic-account-one")
	req.Header.Set("ChatGPT-Account-ID", "synthetic-account-one")
	return req
}

func quotaTestRead(client *http.Client, req *http.Request) <-chan quotaReadResult {
	done := make(chan quotaReadResult, 1)
	go func() { body, err := ReadQuotaJSON(client, req); done <- quotaReadResult{body, err} }()
	return done
}

func quotaAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("quota read did not progress")
	}
	var zero T
	return zero
}

func TestReadQuotaJSONSharesOnlyInflightResolvedRequests(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}, 3), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		_, _ = io.WriteString(w, `{"value":1}`)
	}))
	t.Cleanup(func() { unblock(); server.Close() })
	first := quotaTestRead(&http.Client{}, quotaTestRequest(t, context.Background(), server.URL))
	quotaAwait(t, entered)
	observed := &quotaObservedContext{Context: context.Background(), observed: make(chan struct{})}
	// Different client instances match the construction used by providerfactory.
	second := quotaTestRead(&http.Client{}, quotaTestRequest(t, observed, server.URL))
	quotaAwait(t, observed.observed)
	unblock()
	one, two := quotaAwait(t, first), quotaAwait(t, second)
	if one.err != nil || two.err != nil {
		t.Fatalf("reads: %v, %v", one.err, two.err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("identical concurrent quota reads made %d requests, want 1", got)
	}
	one.body[0] = '!'
	if string(two.body) != `{"value":1}` {
		t.Fatal("callers share mutable response storage")
	}
	if _, err := ReadQuotaJSON(&http.Client{}, quotaTestRequest(t, context.Background(), server.URL)); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("completed response was cached: requests=%d", got)
	}
}

func TestReadQuotaJSONSeparatesIdentityAndClientPolicy(t *testing.T) {
	for _, variation := range []string{"credential", "account", "endpoint", "host", "header", "timeout", "cookie-jar", "transport", "redirect", "legacy-cancel"} {
		t.Run(variation, func(t *testing.T) {
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-release
				_, _ = io.WriteString(w, `{}`)
			}))
			defer func() { unblock(); server.Close() }()
			left, right := quotaTestRequest(t, context.Background(), server.URL), quotaTestRequest(t, context.Background(), server.URL)
			oneClient, twoClient := &http.Client{}, &http.Client{}
			switch variation {
			case "credential":
				right.Header.Set("Authorization", "Bearer synthetic-account-two")
			case "account":
				right.Header.Set("ChatGPT-Account-ID", "synthetic-account-two")
			case "endpoint":
				right.URL.Path = "/different"
			case "host":
				right.Host = "different.example"
			case "header":
				right.Header.Set("X-Quota-Configuration", "different")
			case "timeout":
				twoClient.Timeout = time.Minute
			case "cookie-jar":
				oneClient.Jar, _ = cookiejar.New(nil)
				twoClient.Jar, _ = cookiejar.New(nil)
			case "transport":
				oneClient.Transport = http.DefaultTransport
				twoClient.Transport = http.DefaultTransport
			case "legacy-cancel":
				left.Cancel = make(chan struct{})
				right.Cancel = make(chan struct{})
			case "redirect":
				oneClient.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
				twoClient.CheckRedirect = oneClient.CheckRedirect
			}
			first := quotaTestRead(oneClient, left)
			quotaAwait(t, entered)
			second := quotaTestRead(twoClient, right)
			quotaAwait(t, entered)
			unblock()
			if a, b := quotaAwait(t, first), quotaAwait(t, second); a.err != nil || b.err != nil {
				t.Fatalf("reads: %v, %v", a.err, b.err)
			}
		})
	}
}

func TestReadQuotaJSONCallerCancellationKeepsOtherWaiter(t *testing.T) {
	entered, release, canceled := make(chan struct{}, 2), make(chan struct{}), make(chan struct{}, 2)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			canceled <- struct{}{}
		case <-release:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer func() { unblock(); server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := quotaTestRead(nil, quotaTestRequest(t, ctx, server.URL))
	quotaAwait(t, entered)
	observed := &quotaObservedContext{Context: context.Background(), observed: make(chan struct{})}
	second := quotaTestRead(nil, quotaTestRequest(t, observed, server.URL))
	quotaAwait(t, observed.observed)
	cancel()
	if result := quotaAwait(t, first); result.err == nil {
		t.Fatal("canceled caller returned success")
	}
	unblock()
	if result := quotaAwait(t, second); result.err != nil {
		t.Fatalf("first caller canceled another waiter: %v", result.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("waiters did not share request: %d", calls.Load())
	}
	select {
	case <-canceled:
		t.Fatal("shared request was canceled with a valid waiter")
	default:
	}
}

func TestReadQuotaJSONAllCanceledThenFreshRead(t *testing.T) {
	entered, canceled := make(chan struct{}, 2), make(chan struct{}, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-r.Context().Done()
			canceled <- struct{}{}
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := quotaTestRead(nil, quotaTestRequest(t, ctx, server.URL))
	quotaAwait(t, entered)
	cancel()
	if result := quotaAwait(t, first); result.err == nil {
		t.Fatal("canceled read succeeded")
	}
	quotaAwait(t, canceled)
	if _, err := ReadQuotaJSON(nil, quotaTestRequest(t, context.Background(), server.URL)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("fresh read reused an abandoned request")
	}
}

func TestReadQuotaJSONFailureDoesNotCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	if _, err := ReadQuotaJSON(nil, quotaTestRequest(t, context.Background(), server.URL)); err == nil {
		t.Fatal("failure returned success")
	}
	if _, err := ReadQuotaJSON(nil, quotaTestRequest(t, context.Background(), server.URL)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("failed read was cached")
	}
}

func TestReadQuotaJSONOwnsClientAfterFirstCallerReturns(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			entered <- struct{}{}
			<-release
			http.Redirect(w, r, "/done", http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer func() { unblock(); server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{}
	first := quotaTestRead(client, quotaTestRequest(t, ctx, server.URL))
	quotaAwait(t, entered)
	observed := &quotaObservedContext{Context: context.Background(), observed: make(chan struct{})}
	second := quotaTestRead(&http.Client{}, quotaTestRequest(t, observed, server.URL))
	quotaAwait(t, observed.observed)
	cancel()
	if result := quotaAwait(t, first); result.err == nil {
		t.Fatal("canceled first caller succeeded")
	}
	// The first call is over and its owner may reuse its client. The pending
	// shared read must retain its own policy until the other waiter finishes.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("new client policy") }
	unblock()
	if result := quotaAwait(t, second); result.err != nil || string(result.body) != `{}` {
		t.Fatalf("returned caller still owns the shared client's policy: %+v", result)
	}
}
