package personalcalendar

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopClientConfigurationStaysInHostStore(t *testing.T) {
	s := &memoryStore{values: map[string][]byte{}}
	ctx := context.Background()
	id := "synthetic.apps.googleusercontent.com"
	dir := filepath.Join(t.TempDir(), "calendar")
	if _, err := NewDesktop(DesktopOptions{Store: s, StateDir: dir}); err == nil {
		t.Fatal("empty client became configured")
	}
	if len(s.values) != 0 {
		t.Fatal("unconfigured constructor wrote credentials")
	}
	if _, err := NewDesktop(DesktopOptions{ClientID: id, Store: s, StateDir: dir}); err == nil {
		t.Fatal("missing secure client accepted")
	}
	got, err := ImportDesktopClient(ctx, s, strings.NewReader(`{"client_id":"synthetic.apps.googleusercontent.com","client_secret":"synthetic-only-secret"}`))
	if err != nil || got != id {
		t.Fatalf("import %q %v", got, err)
	}
	b, err := NewDesktop(DesktopOptions{ClientID: id, Store: s, StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.config.ClientSecret != "synthetic-only-secret" || b.activationDir != filepath.Join(dir, "activation-v1") {
		t.Fatal("host client or state root lost")
	}
	for _, raw := range []string{`{"client_id":"https://attacker.invalid","client_secret":"secret"}`, `{"client_id":"synthetic.apps.googleusercontent.com","client_secret":"secret","owner":true}`, `{"client_id":"synthetic.apps.googleusercontent.com"} {}`} {
		_, err := ImportDesktopClient(ctx, s, strings.NewReader(raw))
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe config error: %v", err)
		}
	}
	_, err = NewDesktop(DesktopOptions{ClientID: "other.apps.googleusercontent.com", Store: s, StateDir: dir})
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "oauth_client_not_configured" {
		t.Fatalf("missing config: %v", err)
	}
}
