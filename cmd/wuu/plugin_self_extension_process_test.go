//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Exercise the public protocol and CLI from inside a real agent execution. In
// particular, publication must not wait for the very turn running its command.
// WUU_SELF_EXTENSION_BINARY selects a real built Wuu binary and exercises the
// host plugin_manager bridge; the default uses the existing CLI test helper.
// WUU_SELF_EXTENSION_EVIDENCE preserves the synthetic protocol transcripts.
func TestAgentSelfExtensionProcess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	root := t.TempDir()
	home := filepath.Join(root, "state")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "self-extension.ts")
	binary := strings.TrimSpace(os.Getenv("WUU_SELF_EXTENSION_BINARY"))
	useManager := binary != ""
	if !useManager {
		var err error
		binary, err = os.Executable()
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("plugin publication path: manager=%t executable=%s", useManager, binary)
	shellQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	cli := "WUU_EXEC_PROCESS_TEST=1 " + shellQuote(binary) + " -test.run=^TestExecProcessHelper$ -- plugin "
	publish := cli + "dev --watch=false " + shellQuote(source)
	fixture := func(version string) string {
		return fmt.Sprintf(`import { readFileSync } from "node:fs";
import type { RuntimePlugin } from "@wuu/plugin-sdk";
const version = %q;
export default {
 initialize() { return { tools: [{ id: "read", description: "Self extension %s", input_schema: { type: "object" } }] }; },
 executeTool() {
  const disk = readFileSync(new URL(import.meta.url), "utf8").match(/const version = "([^"]+)"/)[1];
  return { result: { content: [{ type: "text", text: "memory:" + version + ";disk:" + disk }] } };
 }
} satisfies RuntimePlugin;
`, version, version)
	}
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	writeAndPublish := func(contents string) string {
		write := "await tools.write_file(" + encode(map[string]string{"path": source, "content": contents}) + "); "
		if useManager {
			return write + "try { const receipt = await tools.plugin_manager(" + encode(map[string]string{"action": "apply", "path": source}) + "); text(\"MANAGER_RECEIPT:\" + receipt.model_text); } catch (error) { text(String(error)); }"
		}
		return write + "text(await tools.bash(" + encode(map[string]string{"command": publish}) + "));"
	}
	var mu sync.Mutex
	calls := map[string]int{}
	var problems []string
	var providerTranscript bytes.Buffer
	toolPattern := regexp.MustCompile(`plugin_local_[a-z0-9_]+_read_[a-f0-9]{16}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Messages []struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"tool_call_id"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("provider request: %v", err)
			http.Error(w, "invalid request", 400)
			return
		}
		stage, surface, result := "", string(request.Tools), ""
		for _, message := range request.Messages {
			if message.Role == "user" {
				var content string
				_ = json.Unmarshal(message.Content, &content)
				if strings.HasPrefix(content, "acceptance:") {
					stage = strings.TrimPrefix(content, "acceptance:")
				}
			}
			if message.Role == "system" {
				surface += string(message.Content)
			}
			if message.Role == "tool" {
				if err := json.Unmarshal(message.Content, &result); err != nil {
					result = string(message.Content)
				}
			}
		}
		// Session title generation shares the provider but has no tools or
		// acceptance turn. Keep that auxiliary request out of turn assertions.
		if stage == "" && (len(request.Tools) == 0 || string(request.Tools) == "[]") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Self extension acceptance\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		mu.Lock()
		defer mu.Unlock()
		calls[stage]++
		call := calls[stage]
		fmt.Fprintf(&providerTranscript, "stage=%s call=%d request=%s\n", stage, call, body)
		fail := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
		name := toolPattern.FindString(surface)
		if stage == "install" || stage == "disabled" || stage == "discover-manager" {
			if name != "" {
				fail("%s advertised plugin tool %s", stage, name)
			}
		} else if stage != "disable-manager" && name == "" {
			fail("%s did not advertise the installed plugin tool", stage)
		}
		if stage == "update" && strings.Contains(surface, "Self extension v2") {
			fail("active update turn adopted v2 before completion")
		}
		code := ""
		directTool := ""
		if call == 1 {
			switch stage {
			case "discover-manager":
				if strings.Contains(string(request.Tools), `"name":"tool_search"`) {
					directTool = "tool_search"
				} else {
					code = `text(await tools.plugin_manager({action:"status"}));`
				}
			case "disable-manager":
				code = "const receipt = await tools.plugin_manager(" + encode(map[string]string{"action": "disable", "id": singleFilePluginID(source)}) + "); text(\"MANAGER_RECEIPT:\" + receipt.model_text);"
			case "install":
				code = writeAndPublish(fixture("v1"))
			case "read-v1", "read-v2", "after-invalid":
				code = "text(await tools." + name + "({}));"
			case "update":
				code = writeAndPublish(fixture("v2")) + " text(await tools." + name + "({}));"
			case "invalid":
				code = writeAndPublish("export default {") + " text(await tools." + name + "({}));"
			case "disabled":
			default:
				fail("unexpected acceptance stage %q", stage)
			}
		} else {
			if useManager && (stage == "install" || stage == "update" || stage == "disable-manager") {
				var receipt struct {
					Action  string `json:"action"`
					Plugins []struct {
						ID        string `json:"id"`
						Published string `json:"published_fingerprint"`
						Current   string `json:"current_conversation_fingerprint"`
						Enabled   bool   `json:"desired_enabled"`
						Trusted   bool   `json:"trusted"`
						Pending   bool   `json:"adoption_pending"`
					} `json:"plugins"`
				}
				for _, line := range strings.Split(result, "\n") {
					if strings.HasPrefix(line, "MANAGER_RECEIPT:") {
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "MANAGER_RECEIPT:")), &receipt); err != nil {
							fail("invalid manager receipt: %v", err)
						}
					}
				}
				if len(receipt.Plugins) != 1 || receipt.Plugins[0].ID != singleFilePluginID(source) {
					fail("%s missing exact plugin receipt: %s", stage, result)
				} else {
					item := receipt.Plugins[0]
					if item.Published == "" || !item.Trusted {
						fail("%s missing published trusted generation: %+v", stage, item)
					}
					if stage == "disable-manager" {
						if receipt.Action != "disable" || item.Enabled {
							fail("disable receipt did not disable plugin: %+v", receipt)
						}
					} else if receipt.Action != "apply" || !item.Enabled || !item.Pending || item.Published == item.Current {
						fail("%s receipt did not distinguish published and pinned generation: %+v", stage, receipt)
					}
					if stage == "install" && item.Current != "" {
						fail("install claimed same-turn adoption: %+v", item)
					}
					if stage == "update" && item.Current == "" {
						fail("update lost pinned generation identity: %+v", item)
					}
				}
			}
			if stage == "discover-manager" && !strings.Contains(result, "plugin_manager") && !strings.Contains(result, `"action":"status"`) {
				fail("manager discovery failed: %s", result)
			}
			switch stage {
			case "install", "update":
				if !strings.Contains(result, "published generation") {
					fail("%s publication failed inside active turn: %s", stage, result)
				}
			}
			switch stage {
			case "read-v1", "update":
				if !strings.Contains(result, "memory:v1;disk:v1") {
					fail("%s lost pinned v1 source: %s", stage, result)
				}
			case "read-v2", "invalid", "after-invalid":
				if !strings.Contains(result, "memory:v2;disk:v2") {
					fail("%s lost last-good v2: %s", stage, result)
				}
			}
			if stage == "invalid" && (!strings.Contains(result, "previous generation preserved") || strings.Contains(result, "published generation")) {
				fail("invalid update did not fail cleanly: %s", result)
			}
			t.Logf("stage=%s tool result=%s", stage, result)
		}
		delta := map[string]any{"role": "assistant", "content": "acceptance complete"}
		finish := "stop"
		if directTool != "" {
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": stage + "-call", "type": "function", "function": map[string]any{"name": directTool, "arguments": encode(map[string]string{"query": "select:plugin_manager"})}}}}
			finish = "tool_calls"
		} else if code != "" {
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": stage + "-call", "type": "function", "function": map[string]any{"name": "run_code", "arguments": encode(map[string]string{"code": code})}}}}
			finish = "tool_calls"
		}
		payload := encode(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	defer server.Close()
	configPath := filepath.Join(home, "config.json")
	config := fmt.Sprintf(`{"default_provider":"test","providers":{"test":{"type":"openai-compatible","base_url":%q,"api_key":"synthetic","model":"gpt-test"}},"agent":{"permission_mode":"unconfined"},"skills":{"enabled":false}}`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"app-server", "--workdir", root, "--config", configPath}
	if !useManager {
		args = append([]string{"-test.run=^TestExecProcessHelper$", "--"}, args...)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "WUU_HOME=" + home}
	if !useManager {
		cmd.Env = append(cmd.Env, "WUU_EXEC_PROCESS_TEST=1")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if t.Failed() {
			t.Logf("app-server stderr:\n%s", &stderr)
		}
	}()
	messages := make(chan map[string]any, 256)
	var transcript bytes.Buffer
	var transcriptMu sync.Mutex
	go func() {
		defer close(messages)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			transcriptMu.Lock()
			transcript.Write(line)
			transcript.WriteByte('\n')
			transcriptMu.Unlock()
			var message map[string]any
			if json.Unmarshal(line, &message) == nil {
				select {
				case messages <- message:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	defer func() {
		if dir := os.Getenv("WUU_SELF_EXTENSION_EVIDENCE"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Errorf("evidence directory: %v", err)
				return
			}
			transcriptMu.Lock()
			err := os.WriteFile(filepath.Join(dir, "app-server.jsonl"), transcript.Bytes(), 0600)
			transcriptMu.Unlock()
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			err = os.WriteFile(filepath.Join(dir, "provider.log"), providerTranscript.Bytes(), 0600)
			mu.Unlock()
			if err != nil {
				t.Error(err)
			}
			t.Logf("Evidence: %s", dir)
		}
	}()
	wait := func(match func(map[string]any) bool) map[string]any {
		for {
			select {
			case message, ok := <-messages:
				if !ok {
					t.Fatal("app-server closed before expected protocol event")
				}
				if match(message) {
					return message
				}
			case <-ctx.Done():
				t.Fatalf("waiting for protocol event: %v", ctx.Err())
			}
		}
	}
	request := func(id, method string, params any) map[string]any {
		raw := encode(map[string]any{"id": id, "method": method, "params": params})
		for {
			if _, err := fmt.Fprintln(stdin, raw); err != nil {
				t.Fatal(err)
			}
			response := wait(func(m map[string]any) bool { return m["id"] == id })
			if response["error"] == nil {
				return response
			}
			// The filesystem watcher may already be activating the next
			// generation. Admission advertises a retryable busy response.
			if method != "turn/start" || !strings.Contains(fmt.Sprint(response["error"]), "execution is owned by another app-server") {
				t.Fatalf("%s: %v", method, response["error"])
			}
			select {
			case <-time.After(20 * time.Millisecond):
			case <-ctx.Done():
				t.Fatalf("generation never became ready for %s: %v", id, ctx.Err())
			}
		}
	}
	request("initialize", "initialize", map[string]any{"protocol_version": "wuu-app-server/v0.1", "client": map[string]string{"name": "self-extension-acceptance"}})
	created := request("thread", "thread/start", map[string]any{})
	threadID := created["result"].(map[string]any)["thread"].(map[string]any)["id"].(string)
	runTurn := func(stage string) {
		request(stage, "turn/start", map[string]string{"thread_id": threadID, "prompt": "acceptance:" + stage})
		terminal := wait(func(m map[string]any) bool { return m["method"] == "turn/completed" })
		params := terminal["params"].(map[string]any)
		if params["thread_id"] != threadID || params["turn"].(map[string]any)["status"] != "completed" {
			t.Fatalf("stage %s did not complete successfully: %v", stage, terminal)
		}
		mu.Lock()
		failures := strings.Join(problems, "\n")
		count := calls[stage]
		mu.Unlock()
		if failures != "" {
			t.Fatal(failures)
		}
		wantCalls := 2
		if stage == "disabled" {
			wantCalls = 1
		}
		if count != wantCalls {
			t.Fatalf("stage %s made %d provider calls, want %d", stage, count, wantCalls)
		}
		t.Logf("PASS stage=%s thread=%s provider_calls=%d", stage, threadID, count)
	}
	if useManager {
		runTurn("discover-manager")
	}
	for _, stage := range []string{"install", "read-v1", "update", "read-v2", "invalid", "after-invalid"} {
		runTurn(stage)
	}
	if useManager {
		runTurn("disable-manager")
	} else {
		disable := exec.CommandContext(ctx, binary, "-test.run=^TestExecProcessHelper$", "--", "plugin", "disable", singleFilePluginID(source))
		disable.Env, disable.Dir = cmd.Env, root
		if output, err := disable.CombinedOutput(); err != nil {
			t.Fatalf("disable: %v\n%s", err, output)
		}
	}
	runTurn("disabled")
	request("shutdown", "shutdown", map[string]any{})
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("app-server shutdown: %v", err)
	}
}
