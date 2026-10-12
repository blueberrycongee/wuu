package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type historyFixture struct {
	Next    int                      `json:"next"`
	Threads map[string]fixtureThread `json:"threads"`
}

type fixtureThread struct {
	ID         string        `json:"id"`
	Subscribed bool          `json:"subscribed"`
	Turns      []fixtureTurn `json:"turns"`
}

type fixtureTurn struct {
	ID        string           `json:"id"`
	Status    string           `json:"status"`
	ItemsView string           `json:"itemsView"`
	Items     []map[string]any `json:"items"`
	Prompt    string           `json:"prompt"`
}

// The stateful mode is used by the real app-server E2E driver. Native history
// lives outside Wuu's store and survives process restarts, as the vendor CLI's
// history does. It never sends network requests or runs generated commands.
func handleHistory(req request) bool {
	path := os.Getenv("WUU_TEST_CODEX_HISTORY")
	if path == "" {
		return false
	}
	switch req.Method {
	case "thread/start", "thread/read", "thread/resume", "thread/fork", "thread/unsubscribe", "turn/start", "turn/interrupt":
	default:
		return false
	}
	var db historyFixture
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &db); err != nil {
			panic(err)
		}
	}
	if db.Threads == nil {
		db.Threads = make(map[string]fixtureThread)
	}
	var p struct {
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		LastTurnID   string `json:"lastTurnId"`
		BeforeTurnID string `json:"beforeTurnId"`
		Input        []struct {
			Text string `json:"text"`
		} `json:"input"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		panic(err)
	}
	var control struct {
		Mode   string `json:"mode"`
		Gate   string `json:"gate"`
		Marker string `json:"marker"`
	}
	if data, err := os.ReadFile(os.Getenv("WUU_TEST_CODEX_HISTORY_CONTROL")); err == nil {
		_ = json.Unmarshal(data, &control)
	}
	save := func() {
		data, _ := json.MarshalIndent(db, "", "  ")
		if err := os.WriteFile(path, data, 0600); err != nil {
			panic(err)
		}
	}
	thread, found := db.Threads[p.ThreadID]
	if req.Method != "thread/start" && !found {
		respondError(req.ID, -32602, "native thread not found")
		return true
	}
	switch req.Method {
	case "thread/start":
		db.Next++
		thread = fixtureThread{ID: fmt.Sprintf("native-%d", db.Next), Subscribed: true, Turns: []fixtureTurn{}}
		db.Threads[thread.ID] = thread
		save()
		respond(req.ID, map[string]any{"thread": thread, "model": "fixture", "cwd": "."})
	case "thread/read", "thread/resume":
		if control.Mode == "read-error" {
			respondError(req.ID, -32603, "injected native read failure")
			return true
		}
		if control.Mode == "settle-on-read" && len(thread.Turns) > 0 {
			thread.Turns[len(thread.Turns)-1].Status = "interrupted"
			db.Threads[thread.ID] = thread
			save()
		}
		if control.Mode == "incomplete" {
			thread.Turns = nil
		}
		if req.Method == "thread/resume" {
			thread.Subscribed = true
			db.Threads[thread.ID] = thread
			save()
		}
		respond(req.ID, map[string]any{"thread": thread, "model": "fixture", "cwd": "."})
	case "thread/unsubscribe":
		thread.Subscribed = false
		db.Threads[thread.ID] = thread
		save()
		respond(req.ID, map[string]any{})
	case "thread/fork":
		if control.Mode == "fork-error" {
			respondError(req.ID, -32603, "injected native fork failure")
			return true
		}
		if control.Mode == "alias-parent" || control.Mode == "alias-parent-whitespace" {
			if control.Mode == "alias-parent-whitespace" {
				thread.ID = " " + thread.ID + " "
			}
			respond(req.ID, map[string]any{"thread": thread})
			return true
		}
		keep := len(thread.Turns)
		if control.Mode != "ignore-cutoff" {
			for i, turn := range thread.Turns {
				if turn.ID == p.LastTurnID {
					keep = i + 1
				}
				if turn.ID == p.BeforeTurnID {
					keep = i
				}
			}
		}
		db.Next++
		fork := fixtureThread{ID: fmt.Sprintf("native-%d", db.Next), Subscribed: true, Turns: append([]fixtureTurn{}, thread.Turns[:keep]...)}
		if (control.Mode == "corrupt-item" || control.Mode == "corrupt-number") && keep > 0 {
			data, _ := json.Marshal(fork)
			fork = fixtureThread{}
			_ = json.Unmarshal(data, &fork)
			if control.Mode == "corrupt-item" {
				fork.Turns[0].Items[0]["id"] = "changed-native-item"
			} else {
				for _, item := range fork.Turns[0].Items {
					if item["type"] == "mcpToolCall" {
						item["result"] = map[string]any{"structuredContent": map[string]any{"counter": int64(9007199254740993)}}
					}
				}
			}
		}
		db.Threads[fork.ID] = fork
		save()
		waitForGate("WUU_TEST_CODEX_FORK_GATE")
		if control.Mode == "hold-fork" && control.Gate != "" {
			if err := os.WriteFile(control.Marker, []byte("entered"), 0600); err != nil {
				panic(err)
			}
			for {
				if _, err := os.Stat(control.Gate); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		respond(req.ID, map[string]any{"thread": fork, "model": "fixture", "cwd": "."})
	case "turn/start":
		if control.Mode == "reject-turn" {
			respondError(req.ID, -32602, "injected turn admission rejection")
			return true
		}
		var prompts []string
		for _, turn := range thread.Turns {
			prompts = append(prompts, turn.Prompt)
		}
		var input []string
		for _, part := range p.Input {
			input = append(input, part.Text)
		}
		prompt := strings.Join(input, "\n")
		db.Next++
		id := fmt.Sprintf("turn-%d", db.Next)
		text := "context:" + strings.Join(prompts, "|") + ";prompt:" + prompt
		items := []map[string]any{
			{"id": id + "-user", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": prompt}}},
			{"id": id + "-reason", "type": "reasoning", "summary": []string{"native hidden state"}, "content": []string{"retained only by the native engine"}},
			{"id": id + "-comment", "type": "agentMessage", "phase": "commentary", "text": "Inspecting " + prompt},
			{"id": id + "-tool", "type": "commandExecution", "status": "completed", "command": "printf fixture", "cwd": ".", "aggregatedOutput": "native-tool-result", "exitCode": 0},
			{"id": id + "-mcp", "type": "mcpToolCall", "status": "completed", "server": "fixture", "tool": "counter", "arguments": map[string]any{}, "result": map[string]any{"structuredContent": map[string]any{"counter": int64(9007199254740992)}}},
			{"id": id + "-answer", "type": "agentMessage", "phase": "final_answer", "text": text},
		}
		if control.Mode == "long-turn" {
			answer := items[len(items)-1]
			items = items[:len(items)-1]
			for i := 0; i < 300; i++ {
				items = append(items, map[string]any{"id": fmt.Sprintf("%s-tool-%d", id, i), "type": "commandExecution", "status": "completed", "command": "printf fixture", "cwd": ".", "aggregatedOutput": strings.Repeat("native-output-", 1024), "exitCode": 0})
			}
			items = append(items, answer)
		}
		status := "completed"
		if control.Mode == "hold-turn" || control.Mode == "no-interrupt-reply" || control.Mode == "disconnect-without-terminal" {
			status = "inProgress"
			items = items[:1]
		}
		if control.Mode == "failed-turn" {
			status = "failed"
			items = items[:1]
		}
		turn := fixtureTurn{ID: id, Status: status, ItemsView: "full", Items: items, Prompt: prompt}
		thread.Turns = append(thread.Turns, turn)
		db.Threads[thread.ID] = thread
		save()
		if control.Mode == "internal-error-after-admission" {
			respondError(req.ID, -32603, "injected failure after native admission")
			return true
		}
		if control.Mode != "terminal-empty-start-response" {
			respond(req.ID, map[string]any{"turn": map[string]any{"id": id}})
		}
		notify("turn/started", map[string]any{"threadId": thread.ID, "turn": map[string]any{"id": id}})
		for _, item := range items[1:] {
			notify("item/started", map[string]any{"threadId": thread.ID, "turnId": id, "item": item})
			notify("item/completed", map[string]any{"threadId": thread.ID, "turnId": id, "item": item})
		}
		if control.Mode == "disconnect-without-terminal" {
			notify("item/agentMessage/delta", map[string]any{"threadId": thread.ID, "turnId": id, "itemId": id + "-partial", "delta": "Retained partial output"})
			os.Exit(1)
		}
		if status != "inProgress" {
			notify("turn/completed", map[string]any{"threadId": thread.ID, "turn": map[string]any{"id": id, "status": status}})
		}
		if control.Mode == "terminal-empty-start-response" {
			respond(req.ID, map[string]any{"turn": map[string]any{"id": ""}})
		}
	case "turn/interrupt":
		if control.Mode == "no-interrupt-reply" {
			return true
		}
		for i := range thread.Turns {
			if thread.Turns[i].ID == p.TurnID {
				thread.Turns[i].Status = "interrupted"
			}
		}
		db.Threads[thread.ID] = thread
		save()
		respond(req.ID, map[string]any{})
		notify("turn/completed", map[string]any{"threadId": thread.ID, "turn": map[string]any{"id": p.TurnID, "status": "interrupted"}})
	}
	return true
}
