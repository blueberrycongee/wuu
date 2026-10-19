package appserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/externalengine"
)

func TestGrokLocalLoginUsesExistingAuthRouteWithoutReturningCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic executable uses a POSIX shell")
	}
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	binary := filepath.Join(t.TempDir(), "grok with spaces")
	const script = `#!/bin/sh
[ "$#" = 3 ] && [ "$1" = --no-auto-update ] && [ "$2" = login ] && [ "$3" = --oauth ] || exit 2
printf 'synthetic-secret-output\n'
printf 'synthetic-secret-error\n' >&2
printf '{"https://accounts.x.ai/sign-in":{"access_token":"synthetic-secret-token"}}' > "$GROK_HOME/auth.json"
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WUU_GROK_BINARY", binary)
	out := &lockedBuffer{}
	s := New(newTestRuntime(t, &fakeClient{}), out)
	defer s.Close()
	if err := s.handleLine(context.Background(), []byte(`{"id":"login","method":"engine/authenticate","params":{"engine_id":"grok","method_id":"local-cli"}}`)); err != nil {
		t.Fatal(err)
	}
	response := waitForResponseByID(t, out, "login")
	result := remarshal[externalengine.AuthResult](t, response["result"])
	if response["error"] != nil || !result.Authenticated || len(result.Methods) != 0 {
		t.Fatalf("login response: %+v", response)
	}
	if strings.Contains(out.String(), "synthetic-secret") {
		t.Fatalf("login leaked output or credentials: %s", out.String())
	}
}

func TestRemoteDeviceCannotStartLocalCLILogin(t *testing.T) {
	out := &lockedBuffer{}
	// Even a device transport with no push registrar remains remote.
	request := strings.NewReader("{\"id\":\"login\",\"method\":\"engine/authenticate\",\"params\":{\"engine_id\":\"grok\",\"method_id\":\"local-cli\"}}\n")
	if err := RunStdioForDevice(context.Background(), newTestRuntime(t, &fakeClient{}), request, out, nil); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "login")
	if response["error"] == nil || !strings.Contains(toString(response["error"]), "local desktop") {
		t.Fatalf("remote login was not rejected before resolution: %+v", response)
	}
}

func TestEngineAuthCancellationAndShutdownOwnAdmittedWork(t *testing.T) {
	s := &Server{}
	ctx, release, err := s.beginEngineAuth(context.Background(), "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.beginEngineAuth(context.Background(), "cursor"); err == nil {
		t.Fatal("duplicate sign-in admitted")
	}
	other, releaseOther, err := s.beginEngineAuth(context.Background(), "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOther()
	s.cancelEngineAuth("cursor")
	if !errors.Is(ctx.Err(), context.Canceled) || other.Err() != nil {
		t.Fatal("cancellation did not target the selected engine")
	}
	release()
	ctx, release, err = s.beginEngineAuth(context.Background(), "cursor")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	s.Close()
	if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(other.Err(), context.Canceled) {
		t.Fatal("shutdown did not cancel all admitted sign-in operations")
	}
	if _, _, err := s.beginEngineAuth(context.Background(), "devin"); !errors.Is(err, errServerClosed) {
		t.Fatalf("sign-in admitted after shutdown: %v", err)
	}
}
