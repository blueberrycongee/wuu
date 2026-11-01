package personalconnectors

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

type drainingHTTPSCallback struct {
	callback         func(string) error
	stopped, release chan struct{}
}

func (f *drainingHTTPSCallback) Register(_ context.Context, _ string, callback func(string) error) (func(), error) {
	f.callback = callback
	return func() { close(f.stopped); <-f.release }, nil
}

// Provider shutdown cannot release its credential dependency while an OAuth
// callback or its host callback registration is still unwinding.
func TestBrokerCloseDrainsOAuthCallbackAndHTTPSTransport(t *testing.T) {
	for _, transportFirst := range []bool{false, true} {
		name := "callback-first"
		if transportFirst {
			name = "transport-first"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := &memoryStore{values: map[string][]byte{}}
				_, err := ImportClient(context.Background(), store, strings.NewReader(`{"provider":"gmail","client_id":"fixture","redirect_uri":"https://callback.example.invalid/oauth/personal-connectors/callback"}`))
				if err != nil {
					t.Fatal(err)
				}
				relay := &drainingHTTPSCallback{stopped: make(chan struct{}), release: make(chan struct{})}
				entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				releaseCallback := sync.OnceFunc(func() { close(release) })
				releaseTransport := sync.OnceFunc(func() { close(relay.release) })
				defer releaseCallback()
				defer releaseTransport()
				broker := New(Options{StateDir: t.TempDir(), Store: store, HTTPSCallbacks: relay, HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
					close(entered)
					<-r.Context().Done()
					close(canceled)
					<-release
					return nil, r.Context().Err()
				})}})
				actor := Actor{"personal", "space", "owner-ui:live"}
				request := ConnectRequest{Provider: "gmail", ConnectionID: "gmail", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Resources: []string{"me"}}
				attempt, err := broker.Start(context.Background(), actor, request)
				if err != nil {
					t.Fatal(err)
				}
				authorization, _ := url.Parse(attempt.AuthorizationURL)
				callbackDone := make(chan struct{})
				go func() {
					_ = relay.callback(authorization.Query().Get("redirect_uri") + "?state=" + url.QueryEscape(authorization.Query().Get("state")) + "&code=fixture")
					close(callbackDone)
				}()
				<-entered
				closed := make(chan struct{})
				go func() { _ = broker.Close(); close(closed) }()
				<-canceled
				<-relay.stopped
				synctest.Wait()
				select {
				case <-closed:
					t.Fatal("Close returned before OAuth and transport cleanup")
				default:
				}
				if _, err = broker.Start(context.Background(), actor, request); err == nil {
					t.Fatal("closed broker accepted a new OAuth attempt")
				}
				// Another generation remains independent while the old one drains.
				replacement := New(Options{StateDir: t.TempDir(), Store: store})
				defer replacement.Close()
				if replacement.lifetime.Err() != nil {
					t.Fatal("old close canceled replacement")
				}
				if transportFirst {
					releaseTransport()
				} else {
					releaseCallback()
					<-callbackDone
				}
				synctest.Wait()
				select {
				case <-closed:
					t.Fatal("Close returned while one cleanup participant remained")
				default:
				}
				if transportFirst {
					releaseCallback()
					<-callbackDone
				} else {
					releaseTransport()
				}
				<-closed
				if err = broker.Close(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
