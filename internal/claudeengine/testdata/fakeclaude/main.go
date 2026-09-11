// Command fakeclaude is a minimal claude CLI stub speaking the headless
// stream-json protocol over stdio. It exists only for claudeengine tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func send(v any) {
	line, _ := json.Marshal(v)
	fmt.Fprintln(os.Stdout, string(line))
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("2.1.226 (fake)")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--close-stdout-and-hang" {
		_ = os.Stdout.Close()
		for {
			time.Sleep(time.Hour)
		}
	}
	// Resume mode: emit init with the requested session id.
	resumeID := ""
	for i, arg := range os.Args {
		if arg == "--resume" && i+1 < len(os.Args) {
			resumeID = os.Args[i+1]
		}
	}
	if resumeID == "" {
		resumeID = "fake-session-1"
	}
	send(map[string]any{
		"type":    "system",
		"subtype": "init",
		"message": map[string]any{
			"session_id":          resumeID,
			"claude_code_version": "2.1.226 (fake)",
			"model":               "claude-sonnet-4",
		},
	})

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var envelope struct {
			Type            string  `json:"type"`
			ParentToolUseID *string `json:"parent_tool_use_id"`
			Message         struct {
				Content any `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if path := os.Getenv("WUU_TEST_CLAUDE_INPUT"); path != "" {
			if err := os.WriteFile(path, []byte(line), 0600); err != nil {
				panic(err)
			}
			sendResult(false, "Input captured")
			continue
		}
		if strings.Contains(line, "wait_forever") {
			send(map[string]any{
				"type": "stream_event",
				"event": map[string]any{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]any{"type": "text_delta", "text": "partial"},
				},
			})
			for {
				time.Sleep(time.Hour)
			}
		}
		// Detect tool_result content and answer with the tool outcome.
		if blocks, ok := envelope.Message.Content.([]any); ok {
			for _, raw := range blocks {
				block, _ := raw.(map[string]any)
				if block["type"] == "tool_result" {
					toolID, _ := block["tool_use_id"].(string)
					text, _ := block["content"].(string)
					send(map[string]any{
						"type": "assistant",
						"message": map[string]any{
							"role":    "assistant",
							"content": []any{map[string]any{"type": "text", "text": "Ran tool " + toolID + ": " + text}},
						},
					})
					sendResult(false, "Ran tool "+toolID+": "+text)
					continue
				}
			}
			continue
		}
		// Plain user prompt: stream a thinking delta, a text delta, usage,
		// then the result.
		send(map[string]any{
			"type":    "system",
			"subtype": "hook_started",
			"message": map[string]any{"hook_event_name": "PreToolUse"},
		})
		send(map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":    "message_start",
				"message": map[string]any{"model": "claude-sonnet-4"},
			},
		})
		send(map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "thinking_delta", "thinking": "let me think..."},
			},
		})
		send(map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 1,
				"delta": map[string]any{"type": "text_delta", "text": "Hello from claude."},
			},
		})
		send(map[string]any{
			"type": "assistant",
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "let me think..."},
				map[string]any{"type": "text", "text": "Hello from claude."},
			}},
		})
		send(map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "message_delta",
				"usage": map[string]any{"input_tokens": 80, "output_tokens": 12, "cache_read_input_tokens": 30},
			},
		})
		sendResult(false, "Hello from claude.")
	}
}

func sendResult(isError bool, result string) {
	payload := map[string]any{
		"type":        "result",
		"subtype":     "success",
		"is_error":    isError,
		"stop_reason": "end_turn",
		"result":      result,
		"usage":       map[string]any{"input_tokens": 80, "output_tokens": 12, "cache_read_input_tokens": 30},
	}
	if isError {
		payload["error"] = map[string]any{"message": "fake failure"}
	}
	send(payload)
}
