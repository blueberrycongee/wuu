package externalengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/grokbuildspec"
)

func localLoginFixture(t *testing.T, scenario string) *Engine {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the synthetic executable launcher uses a POSIX shell")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	t.Setenv("WUU_TEST_LOGIN_HELPER", binary)
	t.Setenv("WUU_TEST_LOGIN_SCENARIO", scenario)
	launcher := filepath.Join(t.TempDir(), "grok; literal executable")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec \"$WUU_TEST_LOGIN_HELPER\" -test.run=^TestGrokLocalCLIHelper$ -- \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry, _ := enginecatalog.Lookup("grok")
	return New(entry, launcher, t.TempDir())
}

func TestLocalCLILoginHasFixedCommandAndSanitizedOutcomes(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "missing-credential", "unsupported-credential"} {
		t.Run(scenario, func(t *testing.T) {
			e := localLoginFixture(t, scenario)
			result, err := e.Authenticate(context.Background(), "local-cli")
			if scenario == "success" {
				if err != nil || !result.Authenticated || len(result.Methods) != 0 {
					t.Fatalf("login did not finish: %+v %v", result, err)
				}
			} else if err == nil || result.Authenticated {
				t.Fatalf("failed login reported success: %+v %v", result, err)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("login exposed raw output: %v", err)
			}
			args, readErr := os.ReadFile(filepath.Join(os.Getenv("GROK_HOME"), "args"))
			if readErr != nil || string(args) != "--no-auto-update\nlogin\n--oauth" {
				t.Fatalf("login command was not fixed argv: %q %v", args, readErr)
			}
		})
	}
}

func TestLocalCLILoginRejectsUnsupportedEngineAndMissingExecutable(t *testing.T) {
	e := localLoginFixture(t, "success")
	e.entry.ID = "cursor"
	result, err := e.Authenticate(context.Background(), "local-cli")
	if err == nil || result.Authenticated {
		t.Fatalf("unsupported local login succeeded: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("GROK_HOME"), "args")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported engine launched a command: %v", err)
	}
	e.entry.ID = "grok"
	e.binary = filepath.Join(t.TempDir(), "missing-synthetic-secret")
	result, err = e.Authenticate(context.Background(), "local-cli")
	if err == nil || result.Authenticated || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("missing executable result was not safe: %+v %v", result, err)
	}
}

func TestLocalCLILoginCancellationAndDeadlineReapProcess(t *testing.T) {
	for _, timedOut := range []bool{false, true} {
		t.Run(fmt.Sprint(timedOut), func(t *testing.T) {
			e := localLoginFixture(t, "wait")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			t.Setenv("WUU_TEST_LOGIN_ADDRESS", listener.Addr().String())
			timeout := 5 * time.Second
			if timedOut {
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				result, err := e.Authenticate(ctx, "local-cli")
				if result.Authenticated {
					done <- errors.New("cancelled login reported success")
					return
				}
				done <- err
			}()
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if !timedOut {
				cancel()
			}
			want := context.Canceled
			if timedOut {
				want = context.DeadlineExceeded
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("interrupted login = %v, want %v", err, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("interrupted login did not finish")
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("login process retained its connection: %v", err)
			}
		})
	}
}

func TestGrokLocalCLIHelper(t *testing.T) {
	if os.Getenv("WUU_TEST_LOGIN_HELPER") == "" || len(os.Args) < 4 || os.Args[2] != "--" {
		return
	}
	home := os.Getenv("GROK_HOME")
	args := strings.Join(os.Args[3:], "\n")
	if os.WriteFile(filepath.Join(home, "args"), []byte(args), 0o600) != nil || args != "--no-auto-update\nlogin\n--oauth" {
		os.Exit(4)
	}
	fmt.Fprintln(os.Stdout, "synthetic-secret-stdout")
	fmt.Fprintln(os.Stderr, "synthetic-secret-stderr")
	switch os.Getenv("WUU_TEST_LOGIN_SCENARIO") {
	case "success", "unsupported-credential":
		scope := grokbuildspec.OAuthCredentialScope
		if os.Getenv("WUU_TEST_LOGIN_SCENARIO") == "unsupported-credential" {
			scope = "https://unsupported.example.test"
		}
		data, _ := json.Marshal(map[string]any{scope: map[string]string{"access_token": "synthetic-secret-token"}})
		if os.WriteFile(filepath.Join(home, "auth.json"), data, 0o600) != nil {
			os.Exit(5)
		}
	case "failure":
		os.Exit(7)
	case "wait":
		conn, err := net.Dial("tcp", os.Getenv("WUU_TEST_LOGIN_ADDRESS"))
		if err != nil {
			os.Exit(6)
		}
		_, _ = io.Copy(io.Discard, conn)
	}
	os.Exit(0)
}

func TestACPAuthenticationRequiresAdvertisedExplicitChoice(t *testing.T) {
	for _, method := range []string{"", "browser", "terminal", "unknown", "denied", "eof"} {
		t.Run(method, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "authenticate-called")
			e := New(enginecatalog.Entry{Protocol: "acp", Args: []string{"-test.run=^TestACPAuthHelper$", "--", "wuu-auth-helper", marker}}, binary, t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := e.Authenticate(ctx, method)
			if method == "" || method == "browser" {
				if err != nil || result.Authenticated != (method == "browser") || len(result.Methods) != 3 {
					t.Fatalf("authentication result: %+v %v", result, err)
				}
			} else if err == nil || result.Authenticated {
				t.Fatalf("unsupported/failed login succeeded: %+v %v", result, err)
			}
			called, readErr := os.ReadFile(marker)
			if method == "" || method == "terminal" || method == "unknown" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("discovery or unsupported choice triggered login: %q %v", called, readErr)
				}
			} else if readErr != nil || string(called) != method {
				t.Fatalf("explicit choice not forwarded: %q %v", called, readErr)
			}
		})
	}
}

func TestACPAuthHelper(t *testing.T) {
	args := os.Args
	if len(args) < 2 || args[len(args)-2] != "wuu-auth-helper" {
		return
	}
	marker := args[len(args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
		switch msg.Method {
		case "initialize":
			reply["result"] = map[string]any{"protocolVersion": 1, "authMethods": []AuthMethod{
				{ID: "browser", Name: "Browser"}, {ID: "terminal", Name: "Terminal", Type: "terminal"},
				{ID: "denied", Name: "Denied", Type: "agent"}, {ID: "eof", Name: "EOF"},
			}}
		case "authenticate":
			var params struct {
				MethodID string `json:"methodId"`
			}
			if json.Unmarshal(msg.Params, &params) != nil || os.WriteFile(marker, []byte(params.MethodID), 0o600) != nil {
				os.Exit(3)
			}
			if params.MethodID == "eof" {
				os.Exit(0)
			}
			if params.MethodID == "denied" {
				reply["error"] = &rpcError{Code: -32000, Message: "login refused"}
			} else {
				reply["result"] = map[string]any{}
			}
		default:
			// Sign-in must not create a chat session or start a prompt.
			os.Exit(4)
		}
		_ = json.NewEncoder(os.Stdout).Encode(reply)
	}
	os.Exit(0)
}
