package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This opt-in fixture models loaded-thread rejoin and sticky turn overrides.
// Its file probe verifies the adapter's workspace scope, not native OS sandbox
// enforcement or the engine's independent temporary-directory permissions.
type workspaceProbe struct {
	CWD       string `json:"cwd"`
	Turns     int    `json:"turns"`
	loaded    bool
	statePath string
}

func newWorkspaceProbe() *workspaceProbe {
	path := os.Getenv("WUU_TEST_CODEX_WORKSPACE_STATE")
	if path == "" {
		return nil
	}
	probe := &workspaceProbe{statePath: path}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, probe); err != nil {
			panic(err)
		}
	}
	return probe
}

func (p *workspaceProbe) handle(req request) bool {
	var params struct {
		ThreadID string `json:"threadId"`
		CWD      string `json:"cwd"`
		Input    []struct {
			Text string `json:"text"`
		} `json:"input"`
		SandboxPolicy struct {
			Type          string   `json:"type"`
			WritableRoots []string `json:"writableRoots"`
		} `json:"sandboxPolicy"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return false
	}
	switch req.Method {
	case "thread/start", "thread/resume":
		if !p.loaded {
			p.CWD = params.CWD
			p.loaded = true
		}
		// Like a loaded native thread with subscribers, warm resume does not
		// apply CWD/configuration overrides; turn/start must carry the update.
		respond(req.ID, map[string]any{"thread": map[string]any{"id": "workspace-native"}, "model": "gpt-6-astra", "cwd": p.CWD})
		return true
	case "turn/start":
		if params.CWD != "" {
			p.CWD = params.CWD
		}
		if params.ThreadID != "workspace-native" {
			panic("native identity changed")
		}
		var probe struct {
			Paths []string `json:"paths"`
		}
		if len(params.Input) != 1 || json.Unmarshal([]byte(params.Input[0].Text), &probe) != nil {
			panic("invalid workspace probe")
		}
		p.Turns++
		marker, err := os.ReadFile(filepath.Join(p.CWD, "README.md"))
		if err != nil {
			panic(err)
		}
		writes := make(map[string]bool)
		roots := append([]string{p.CWD}, params.SandboxPolicy.WritableRoots...)
		for _, path := range probe.Paths {
			allowed := params.SandboxPolicy.Type == "dangerFullAccess"
			if params.SandboxPolicy.Type == "workspaceWrite" {
				parent, err := filepath.EvalSymlinks(filepath.Dir(path))
				if err != nil {
					panic(err)
				}
				for _, root := range roots {
					root, err = filepath.EvalSymlinks(root)
					if err != nil {
						panic(err)
					}
					rel, err := filepath.Rel(root, parent)
					if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
						allowed = true
					}
				}
			}
			writes[path] = allowed
			if allowed {
				if err := os.WriteFile(path, []byte(fmt.Sprint(p.Turns)), 0600); err != nil {
					panic(err)
				}
			}
		}
		state, _ := json.Marshal(p)
		if err := os.WriteFile(p.statePath, state, 0600); err != nil {
			panic(err)
		}
		content, _ := json.Marshal(map[string]any{"cwd": p.CWD, "turns": p.Turns, "marker": string(marker), "writes": writes, "policy": params.SandboxPolicy.Type, "writableRoots": params.SandboxPolicy.WritableRoots})
		turn := fmt.Sprintf("workspace-turn-%d", p.Turns)
		respond(req.ID, map[string]any{"turn": map[string]any{"id": turn}})
		notify("item/agentMessage/delta", map[string]any{"threadId": params.ThreadID, "turnId": turn, "itemId": "probe", "delta": string(content)})
		notify("item/completed", map[string]any{"threadId": params.ThreadID, "turnId": turn, "item": map[string]any{"id": "probe", "type": "agentMessage", "text": string(content), "phase": "final_answer"}})
		notify("turn/completed", map[string]any{"threadId": params.ThreadID, "turn": map[string]any{"id": turn, "status": "completed"}})
		return true
	default:
		return false
	}
}
