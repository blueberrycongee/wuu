package personalhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestUnavailableHostRejectsForgedAuthorityAndNeverClaimsProtection(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	host := New(binding, Options{Authorize: authorizeFixture})
	defer host.Close()
	for _, operation := range []string{"calendar.status", "calendar.start", "calendar.cancel", "calendar.select", "calendar.disconnect", "review.list", "review.resolve", "review.revoke"} {
		if _, err := host.Call(context.Background(), operation, json.RawMessage(`{"owner":true}`), ""); err == nil {
			t.Fatalf("forged owner admitted for %s", operation)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"calendar.status", "review.list"} {
		value, err := host.Call(owner, operation, json.RawMessage(`{}`), "")
		if err != nil || !strings.Contains(string(value), `"unavailable"`) {
			t.Fatalf("not unavailable: %s %v", value, err)
		}
	}
	for _, operation := range []string{"review.propose", "review.assess", "review.execute", "review.recover", "review.resolve", "calendar.start"} {
		if _, err := host.Call(owner, operation, json.RawMessage(`{}`), ""); err == nil {
			t.Fatalf("unconfigured operation exposed: %s", operation)
		}
	}
	cancel()
	if _, err := host.Call(context.WithoutCancel(owner), "calendar.status", json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("closed owner transport retained control")
	}
	if _, err := host.Call(context.Background(), "calendar.read.status", json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("read without host-resolved session admitted")
	}
}

type cancellationFixture struct {
	Calendar
	failure error
}

func (f *cancellationFixture) Start(context.Context, personalcalendar.Actor) (personalcalendar.AuthorizationAttempt, error) {
	return personalcalendar.AuthorizationAttempt{ID: "attempt", AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth", State: "pending"}, nil
}
func (f *cancellationFixture) Cancel(personalcalendar.Actor, string) error { return f.failure }
func (f *cancellationFixture) Close() error                                { return f.failure }

func TestOpenerFailurePreservesCancellationOutcomeAndCloseError(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	for _, code := range []string{"credential_cleanup_failed", "authorization_already_completed"} {
		t.Run(code, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
			if err != nil {
				t.Fatal(err)
			}
			h := New(binding, Options{Authorize: authorizeFixture, Calendar: &cancellationFixture{failure: &personalcalendar.Failure{Code: code}}, OpenAuthorizationURL: func(context.Context, string) error { return errors.New("synthetic browser failure") }})
			_, err = h.Call(owner, "calendar.start", nil, "")
			var failure *personalcalendar.Failure
			if !errors.As(err, &failure) || failure.Code != code {
				t.Fatalf("lost cancel outcome: %v", err)
			}
			if err = h.Close(); !errors.As(err, &failure) || failure.Code != code {
				t.Fatalf("lost close outcome: %v", err)
			}
			if err = h.Close(); !errors.As(err, &failure) || failure.Code != code {
				t.Fatalf("repeat close lost outcome: %v", err)
			}
		})
	}
}

// The old Cancel can finish after a second owner establishes a new grant.
// Its outcome is a fact about the old attempt, never the new connection state.
type lateCancellationFixture struct {
	Calendar
	mu         sync.Mutex
	starts     int
	connection personalcalendar.Connection
	release    chan struct{}
	code       string
}

func (f *lateCancellationFixture) Start(context.Context, personalcalendar.Actor) (personalcalendar.AuthorizationAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	id := "old-attempt"
	if f.starts > 1 {
		id = "new-attempt"
		f.connection = personalcalendar.Connection{ID: "new-connection", State: "connected", ErrorCode: "current-generation-notice", SelectedCalendars: []string{"new-calendar"}}
	}
	return personalcalendar.AuthorizationAttempt{ID: id, State: "pending", AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth"}, nil
}
func (f *lateCancellationFixture) Cancel(_ personalcalendar.Actor, id string) error {
	if id != "old-attempt" {
		return nil
	}
	<-f.release
	f.mu.Lock()
	defer f.mu.Unlock()
	return &personalcalendar.Failure{Code: f.code}
}
func (f *lateCancellationFixture) Status(context.Context, personalcalendar.Actor) (personalcalendar.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connection, nil
}
func (f *lateCancellationFixture) Disconnect(context.Context, personalcalendar.Actor, string, bool) (personalcalendar.DisconnectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connection = personalcalendar.Connection{State: "disconnected"}
	return personalcalendar.DisconnectResult{LocalDeleted: true}, nil
}
func (f *lateCancellationFixture) Close() error {
	return &personalcalendar.Failure{Code: "credential_cleanup_failed"}
}

func TestLateOwnerCancellationCannotOverwriteNewConnection(t *testing.T) {
	for _, code := range []string{"credential_cleanup_failed", "authorization_already_completed"} {
		for _, late := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/late=%v", code, late), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
					owner := func() (context.Context, context.CancelFunc) {
						ctx, cancel := context.WithCancel(context.Background())
						proof, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
						if err != nil {
							t.Fatal(err)
						}
						return proof, cancel
					}
					old, oldCancel := owner()
					defer oldCancel()
					next, nextCancel := owner()
					defer nextCancel()
					f := &lateCancellationFixture{release: make(chan struct{}), code: code}
					h := New(binding, Options{Authorize: authorizeFixture, Calendar: f, OpenAuthorizationURL: func(context.Context, string) error { return nil }})
					if _, err := h.Call(old, "calendar.start", nil, ""); err != nil {
						t.Fatal(err)
					}
					oldCancel()
					synctest.Wait()
					if !late {
						close(f.release)
						synctest.Wait()
					}
					if _, err := h.Call(next, "calendar.start", nil, ""); err != nil {
						t.Fatal(err)
					}
					if late {
						close(f.release)
						synctest.Wait()
					}
					raw, err := h.Call(next, "calendar.status", nil, "")
					if err != nil {
						t.Fatal(err)
					}
					var got struct {
						personalcalendar.Connection
						Diagnostics []struct {
							AttemptID string `json:"attempt_id"`
							Code      string `json:"error_code"`
						} `json:"authorization_diagnostics"`
					}
					if err = json.Unmarshal(raw, &got); err != nil {
						t.Fatal(err)
					}
					if got.ID != "new-connection" || got.State != "connected" || got.ErrorCode != "current-generation-notice" {
						t.Fatalf("old result corrupted new connection: %s", raw)
					}
					if len(got.Diagnostics) != 1 || got.Diagnostics[0].AttemptID != "old-attempt" || got.Diagnostics[0].Code != code {
						t.Fatalf("old attempt fact lost: %s", raw)
					}
					if code == "credential_cleanup_failed" {
						f.mu.Lock()
						f.code = "attempt_scope_mismatch"
						f.mu.Unlock()
						_, _ = h.Call(next, "calendar.cancel", json.RawMessage(`{"attempt_id":"old-attempt"}`), "")
						raw, err = h.Call(next, "calendar.status", nil, "")
						if err != nil || !strings.Contains(string(raw), "credential_cleanup_failed") {
							t.Fatalf("later attempt lookup hid unresolved cleanup: %s %v", raw, err)
						}
					}
					read, err := h.Call(context.Background(), "calendar.read.status", nil, "verified-session")
					if err != nil || strings.Contains(string(read), "authorization_diagnostics") {
						t.Fatalf("owner diagnostic leaked to read: %s %v", read, err)
					}
					if _, err = h.Call(next, "calendar.disconnect", json.RawMessage(`{"connection_id":"new-connection"}`), ""); err != nil {
						t.Fatal(err)
					}
					raw, err = h.Call(next, "calendar.status", nil, "")
					if err != nil || !strings.Contains(string(raw), code) {
						t.Fatalf("new disconnect erased old unresolved attempt: %s %v", raw, err)
					}
					for range 2 {
						var failure *personalcalendar.Failure
						if err = h.Close(); !errors.As(err, &failure) || failure.Code != "credential_cleanup_failed" {
							t.Fatalf("close error lost: %v", err)
						}
					}
				})
			})
		}
	}
}
