package externalengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

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
