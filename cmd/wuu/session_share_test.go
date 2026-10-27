package main

import (
	"context"
	"encoding/json"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"testing"
)

// The supported command uses the same authenticated app-server route, not a
// store-opening export shortcut or a silently substituted sharing provider.
func TestSessionShareCommandUsesProjectedEndpoint(t *testing.T) {
	old := debugAppServerClientOverride
	defer func() { debugAppServerClientOverride = old }()
	var gotMethod string
	var gotParams json.RawMessage
	debugAppServerClientOverride = func(context.Context, debugAppServerOptions) (debugAppServerClient, error) {
		return &shareCommandClient{call: func(method string, params any) error {
			gotMethod = method
			gotParams = params.(json.RawMessage)
			return nil
		}}, nil
	}
	if err := runSessionShare([]string{"read", `{"thread_id":"s","share_id":"g"}`}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "session/share/read" || string(gotParams) != `{"thread_id":"s","share_id":"g"}` {
		t.Fatal(gotMethod, string(gotParams))
	}
	if err := runSessionShare([]string{"turn/start", `{}`}); err == nil {
		t.Fatal("arbitrary command accepted")
	}
}

type shareCommandClient struct{ call func(string, any) error }

func (c *shareCommandClient) Call(_ context.Context, method string, params any, result any) error {
	if err := c.call(method, params); err != nil {
		return err
	}
	*(result.(*json.RawMessage)) = json.RawMessage(`{"version":1,"session_id":"s","items":[]}`)
	return nil
}
func (c *shareCommandClient) Shutdown(context.Context) error { return nil }

func (c *shareCommandClient) Notifications() <-chan wuuexec.Notification { return nil }
