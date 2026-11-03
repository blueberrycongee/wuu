package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

// Cover late discovery, explicit paths, and the existing standalone server
// through the same inventory, authentication, and turn paths used by clients.
func TestAntigravityDiscoveryAndTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture launcher requires a POSIX shell")
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node, _ := exec.LookPath("node")
	for _, tc := range []struct {
		name, executable, expectedArgs string
		late, override, standalone     bool
	}{
		{name: "new CLI after startup", executable: "agy", expectedArgs: "acp", late: true},
		{name: "explicit CLI path", executable: "agy", expectedArgs: "acp", override: true},
		{name: "standalone server takes precedence", executable: "agy_acp_server", standalone: true},
		{name: "explicit wrapper path", executable: "custom-acp-wrapper", override: true, standalone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			pathDirs := []string{binDir, "/usr/bin", "/bin"}
			if node != "" {
				pathDirs = append(pathDirs, filepath.Dir(node))
			}
			t.Setenv("PATH", strings.Join(pathDirs, string(os.PathListSeparator)))
			t.Setenv("WUU_ANTIGRAVITY_BINARY", "")
			t.Setenv("WUU_TEST_ANTIGRAVITY_BINARY", testBinary)
			t.Setenv("WUU_TEST_ANTIGRAVITY_FIXTURE", "1")
			expectedArgs := tc.expectedArgs
			if tc.standalone && runtime.GOOS == "linux" {
				expectedArgs = "--uid="
			}
			t.Setenv("WUU_TEST_ANTIGRAVITY_ARGS", expectedArgs)
			writeFixture := func(name string) string {
				t.Helper()
				path := filepath.Join(binDir, name)
				launcher := "#!/bin/sh\nexec \"$WUU_TEST_ANTIGRAVITY_BINARY\" -test.run=^TestAntigravityACPFixture$ -- \"$@\"\n"
				if err := os.WriteFile(path, []byte(launcher), 0755); err != nil {
					t.Fatal(err)
				}
				return path
			}
			binary := filepath.Join(binDir, tc.executable)
			if !tc.late {
				writeFixture(tc.executable)
			}
			if tc.standalone {
				writeFixture("agy")
			}
			disabled := false
			off := &config.EngineBinaryConfig{Enabled: &disabled}
			engines := &config.EnginesConfig{Codex: off, Claude: off, Cursor: off, Devin: off, DSH: off, Grok: off, Hermes: off, Pi: off, OpenCode: off}
			if tc.override {
				engines.Antigravity = &config.EngineBinaryConfig{BinaryPath: binary}
			}
			rt := newEngineAdminRuntime(t, config.Config{Engines: engines})
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if tc.late {
				for _, engine := range srv.engineInventory() {
					if engine.ID == "antigravity" && (engine.Enabled || engine.BinaryOK) {
						t.Fatalf("Antigravity was available before installation: %+v", engine)
					}
				}
				writeFixture(tc.executable)
			}
			if err := srv.handleLine(ctx, []byte(`{"id":"inventory","method":"engine/list","params":{"refresh_models":true}}`)); err != nil {
				t.Fatal(err)
			}
			response := waitForResponseByID(t, out, "inventory")
			if response["error"] != nil {
				t.Fatalf("engine/list: %+v", response)
			}
			inventory := remarshal[EngineListResult](t, response["result"])
			var antigravity EngineInfo
			for _, engine := range inventory.Engines {
				if engine.ID == "antigravity" {
					antigravity = engine
				}
			}
			if !antigravity.Enabled || !antigravity.BinaryOK || antigravity.BinaryPath != binary || len(antigravity.Models) != 1 || antigravity.Models[0].ID != "fixture-model" {
				t.Fatalf("discovered Antigravity = %+v", antigravity)
			}
			if err := srv.handleLine(ctx, []byte(`{"id":"auth","method":"engine/auth/methods","params":{"engine_id":"antigravity"}}`)); err != nil {
				t.Fatal(err)
			}
			auth := waitForResponseByID(t, out, "auth")
			if auth["error"] != nil || len(remarshal[pluginprotocol.EngineAuthResult](t, auth["result"]).Methods) != 1 {
				t.Fatalf("auth methods: %+v", auth)
			}
			if err := srv.handleLine(ctx, []byte(`{"id":"authenticate","method":"engine/authenticate","params":{"engine_id":"antigravity","method_id":"fixture"}}`)); err != nil {
				t.Fatal(err)
			}
			auth = waitForResponseByID(t, out, "authenticate")
			if auth["error"] != nil || !remarshal[pluginprotocol.EngineAuthResult](t, auth["result"]).Authenticated {
				t.Fatalf("authentication: %+v", auth)
			}
			if err := srv.handleLine(ctx, []byte(`{"id":"thread","method":"thread/start","params":{"engine":"antigravity"}}`)); err != nil {
				t.Fatal(err)
			}
			threadResponse := waitForResponseByID(t, out, "thread")
			if threadResponse["error"] != nil {
				t.Fatalf("thread/start: %+v", threadResponse)
			}
			threadID := remarshal[ThreadStartResult](t, threadResponse["result"]).Thread.ID
			for turn := range 2 {
				out.Reset()
				request := fmt.Sprintf(`{"id":"turn-%d","method":"turn/start","params":{"thread_id":%q,"prompt":"hello"}}`, turn, threadID)
				if err := srv.handleLine(ctx, []byte(request)); err != nil {
					t.Fatal(err)
				}
				messages := waitForTurnCompletedForThread(t, out, threadID)
				completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
				if completed.Content != "Hello from Antigravity." {
					t.Fatalf("turn %d content = %q", turn, completed.Content)
				}
			}
			if err := srv.handleLine(ctx, []byte(`{"id":"disable","method":"engine/update","params":{"antigravity":{"enabled":false}}}`)); err != nil {
				t.Fatal(err)
			}
			disable := waitForResponseByID(t, out, "disable")
			if disable["error"] != nil {
				t.Fatalf("disable: %+v", disable)
			}
			for _, engine := range remarshal[EngineListResult](t, disable["result"]).Engines {
				if engine.ID == "antigravity" && engine.Enabled {
					t.Fatalf("disabled Antigravity remained available: %+v", engine)
				}
			}
		})
	}
}

// The fixture rejects incorrect arguments and serves only synthetic ACP data.
func TestAntigravityACPFixture(t *testing.T) {
	if os.Getenv("WUU_TEST_ANTIGRAVITY_FIXTURE") != "1" {
		return
	}
	if len(os.Args) < 3 || strings.Join(os.Args[3:], " ") != os.Getenv("WUU_TEST_ANTIGRAVITY_ARGS") {
		fmt.Fprintln(os.Stderr, "unexpected Antigravity launch arguments")
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		result := any(map[string]any{})
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}, "authMethods": []any{map[string]string{"id": "fixture", "name": "Fixture", "type": "agent"}}}
		case "authenticate":
		case "session/new":
			result = map[string]any{"sessionId": "fixture-session", "models": map[string]any{"currentModelId": "fixture-model", "availableModels": []any{map[string]string{"modelId": "fixture-model", "name": "Fixture model"}}}}
		case "session/load":
			var params struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.SessionID != "fixture-session" {
				os.Exit(2)
			}
			result = map[string]any{"models": map[string]any{"currentModelId": "fixture-model", "availableModels": []any{map[string]string{"modelId": "fixture-model", "name": "Fixture model"}}}}
		case "session/prompt":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Hello from Antigravity."}}}})
			result = map[string]string{"stopReason": "end_turn"}
		default:
			os.Exit(2)
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	os.Exit(0)
}
