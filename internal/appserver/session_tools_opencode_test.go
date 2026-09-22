package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestPeerOpenCodeProcess(t *testing.T) {
	if !slices.Contains(os.Args, "wuu-peer-opencode") {
		return
	}
	var config struct {
		MCP map[string]struct {
			URL string `json:"url"`
		} `json:"mcp"`
	}
	if json.Unmarshal([]byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), &config) != nil {
		os.Exit(2)
	}
	endpoint := config.MCP["wuu_session_tools"].URL
	var mu sync.Mutex
	var final map[string]any
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "wuu" || password != os.Getenv("OPENCODE_SERVER_PASSWORD") {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch {
		case r.URL.Path == "/global/health":
			write(w, map[string]any{"healthy": true, "version": "1.0.0"})
		case r.URL.Path == "/session" || r.Method == http.MethodPatch:
			var input struct {
				Permission []struct {
					Action string `json:"action"`
				} `json:"permission"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Permission) != 1 || input.Permission[0].Action != "ask" {
				http.Error(w, "permissions missing", 400)
				return
			}
			write(w, map[string]string{"id": "ses_peer"})
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case r.URL.Path == "/session/ses_peer/message" && r.Method == http.MethodGet:
			mu.Lock()
			defer mu.Unlock()
			write(w, []any{final})
		case r.URL.Path == "/session/ses_peer/message" && r.Method == http.MethodPost:
			var prompt struct {
				MessageID, System string
				Parts             []struct {
					Text string `json:"text"`
				}
			}
			if json.NewDecoder(r.Body).Decode(&prompt) != nil || len(prompt.Parts) == 0 || prompt.System == "" {
				http.Error(w, "missing prompt or instructions", 400)
				return
			}
			text, err := peerEngineReply(endpoint, prompt.Parts[0].Text, "standard")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			final = map[string]any{
				"info":  map[string]any{"id": "msg_answer", "sessionID": "ses_peer", "role": "assistant", "parentID": prompt.MessageID, "finish": "stop", "time": map[string]int{"completed": 1}},
				"parts": []map[string]string{{"id": "prt_answer", "sessionID": "ses_peer", "messageID": "msg_answer", "type": "text", "text": text}},
			}
			write(w, final)
		case strings.HasSuffix(r.URL.Path, "/abort"):
			write(w, true)
		default:
			http.NotFound(w, r)
		}
	}))
	fmt.Println("opencode server listening on " + server.URL)
	select {}
}
