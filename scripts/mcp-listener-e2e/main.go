// Command mcp-listener-e2e exercises production MCP clients and transports
// against a separate loopback server process. All credentials are synthetic.
// Run: go run -race ./scripts/mcp-listener-e2e
// JSON receipts include the server-observed session, cursor and request counts.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/mcp"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}
type observed struct {
	Session string `json:"session"`
	Cursor  string `json:"cursor"`
}
type stats struct {
	Initializes int        `json:"initializes"`
	Gets        []observed `json:"gets"`
	Calls       int        `json:"calls"`
	Effects     int        `json:"effects"`
	Closed      int        `json:"closed"`
	BadHeaders  int        `json:"bad_headers"`
	EarlyGET    int        `json:"early_get"`
}
type fixture struct {
	mu          sync.Mutex
	scenario    string
	state       stats
	expired     bool
	initialized map[string]bool
	events      map[string]chan string
	gates       map[string]chan struct{}
}

func (f *fixture) gate(key string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gates[key] == nil {
		f.gates[key] = make(chan struct{})
	}
	return f.gates[key]
}
func (f *fixture) signal(key string) {
	ch := f.gate(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-ch:
	default:
		close(ch)
	}
}
func (f *fixture) event(session string) chan string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events[session] == nil {
		f.events[session] = make(chan string, 8)
	}
	return f.events[session]
}
func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	switch r.URL.Path {
	case "/wait":
		select {
		case <-f.gate(key):
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
		return
	case "/release":
		f.signal(key)
		w.WriteHeader(204)
		return
	case "/expire":
		f.mu.Lock()
		f.expired = true
		f.mu.Unlock()
		w.WriteHeader(204)
		return
	case "/event":
		f.event(key) <- r.URL.Query().Get("value")
		w.WriteHeader(204)
		return
	case "/stats":
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.state)
		return
	}
	sid := r.Header.Get("Mcp-Session-Id")
	if r.Method == "DELETE" {
		f.signal("deleted")
		w.WriteHeader(204)
		return
	}
	if r.Method == "GET" {
		f.mu.Lock()
		if !f.initialized[sid] {
			f.state.EarlyGET++
		}
		f.state.Gets = append(f.state.Gets, observed{sid, r.Header.Get("Last-Event-ID")})
		count := len(f.state.Gets)
		expired := f.expired
		f.mu.Unlock()
		f.signal(fmt.Sprintf("get-%d", count))
		if f.scenario == "stale-post" || (f.scenario == "unsupported" || f.scenario == "auth-unsupported") && sid == "s1" {
			w.WriteHeader(405)
			return
		}
		if (f.scenario == "auth-post-cursor" && expired && r.Header.Get("Authorization") != "Bearer restored") ||
			((f.scenario == "auth-recover" || f.scenario == "auth-stalled" || f.scenario == "auth-denied") && (f.scenario == "auth-denied" || r.Header.Get("Authorization") != "Bearer renewed")) {
			w.WriteHeader(401)
			if f.scenario == "auth-stalled" {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}
			return
		}
		if (f.scenario == "get-expiry" || f.scenario == "auth-reinitialize" || f.scenario == "auth-initialized") && sid == "s1" && expired {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		f.signal("open-" + sid)
		if f.scenario == "stale-get" && count == 1 {
			fmt.Fprint(w, "id: old-1\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/old-one\"}\n\nid: old-2\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"old-request\",\"method\":\"ping\"}\n\nid: old-3\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/old-three\"}\n\n")
			w.(http.Flusher).Flush()
			return
		}
		if f.scenario == "stale-get" && r.Header.Get("Last-Event-ID") == "old-3" {
			f.signal("old-events-buffered")
		}
		if f.scenario == "get-response-isolation" {
			select {
			case <-f.gate("post-open"):
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":20,\"result\":{\"source\":\"wrong-get\"}}\n\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/barrier\"}\n\n")
			w.(http.Flusher).Flush()
		}
		defer func() { f.mu.Lock(); f.state.Closed++; f.mu.Unlock(); f.signal("closed-" + sid) }()
		for {
			select {
			case data := <-f.event(sid):
				if data == "disconnect" {
					return
				}
				fmt.Fprintf(w, "id: cursor-%s\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n", sid)
				w.(http.Flusher).Flush()
				f.signal("sent-" + sid)
			case <-r.Context().Done():
				return
			}
		}
	}
	var q request
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		w.WriteHeader(400)
		return
	}
	reply := func(result string) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, q.ID, result)
	}
	switch q.Method {
	case "server/discover":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"legacy"}}`, q.ID)
	case "initialize":
		f.mu.Lock()
		expiredInit := f.expired
		f.mu.Unlock()
		if f.scenario == "auth-reinitialize" && expiredInit && r.Header.Get("Authorization") != "Bearer renewed" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		f.state.Initializes++
		n := f.state.Initializes
		f.expired = false
		f.mu.Unlock()
		if sid != "" || r.Header.Get("MCP-Protocol-Version") != "" || r.Header.Get("Last-Event-ID") != "" {
			f.mu.Lock()
			f.state.BadHeaders++
			f.mu.Unlock()
		}
		if n > 1 && (f.scenario == "concurrent" || f.scenario == "cancel-waiter" || f.scenario == "close-recovery") {
			f.signal("recovering")
			select {
			case <-f.gate("finish-init"):
			case <-r.Context().Done():
				f.signal("init-cancelled")
				return
			}
		}
		if n > 1 && f.scenario == "failed-recovery" {
			w.WriteHeader(503)
			return
		}
		if n > 1 && f.scenario == "bad-recovery" {
			q.ID = json.RawMessage(`"wrong"`)
		}
		w.Header().Set("Mcp-Session-Id", fmt.Sprintf("s%d", n))
		version := "2025-06-18"
		if n > 1 {
			switch f.scenario {
			case "bad-recovery-version":
				version = "unsupported"
			case "bad-recovery-empty-version":
				version = ""
			case "bad-recovery-stateless":
				version = mcp.PreferredProtocolVersion
			}
		}
		reply(fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"synthetic","version":"1"}}`, version))
	case "notifications/initialized":
		if f.scenario == "initialized-expiry" {
			f.mu.Lock()
			duplicate := f.initialized[sid]
			f.mu.Unlock()
			if sid == "s1" {
				w.WriteHeader(404)
				return
			}
			if duplicate {
				w.WriteHeader(400)
				return
			}
		}
		if f.scenario == "auth-initialized" && sid == "s2" && r.Header.Get("Authorization") != "Bearer renewed" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		f.initialized[sid] = true
		f.mu.Unlock()
		w.WriteHeader(202)
	case "tools/list":
		reply(fmt.Sprintf(`{"tools":[{"name":%q,"inputSchema":{"type":"object"}}]}`, "tool-"+sid))
	case "tools/call":
		if q.Params.Name == "stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			f.signal("post-open")
			select {
			case <-f.gate("release-post"):
			case <-r.Context().Done():
				return
			}
			if f.scenario == "stale-post" {
				fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/old-post\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"old-post-request\",\"method\":\"ping\"}\n\n")
			}
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"source\":\"origin-post\"}}\n\n", q.ID)
			w.(http.Flusher).Flush()
			return
		}
		if (f.scenario == "auth-post-cursor" && r.Header.Get("Authorization") != "Bearer restored") ||
			(f.scenario == "auth-unsupported" && r.Header.Get("Authorization") != "Bearer renewed") {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		f.state.Calls++
		count := f.state.Calls
		expired := f.expired || sid == "s1" && f.state.Initializes > 1
		f.mu.Unlock()
		if (f.scenario == "concurrent" || f.scenario == "cancel-waiter" || f.scenario == "close-recovery") && sid == "s1" {
			f.signal(fmt.Sprintf("call-%d", count))
			select {
			case <-f.gate("reject-calls"):
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(404)
			return
		}
		if expired && f.scenario != "auth-post-cursor" {
			w.WriteHeader(404)
			return
		}
		f.mu.Lock()
		f.state.Effects++
		f.mu.Unlock()
		reply(`{"content":[{"type":"text","text":"ok"}]}`)
	default:
		w.WriteHeader(202)
	}
}
func server() {
	var mu sync.Mutex
	fixtures := map[string]*fixture{}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println("http://" + l.Addr().String())
	http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("scenario")
		mu.Lock()
		f := fixtures[name]
		if f == nil {
			f = &fixture{initialized: map[string]bool{}, scenario: name, events: map[string]chan string{}, gates: map[string]chan struct{}{}}
			fixtures[name] = f
		}
		mu.Unlock()
		f.serve(w, r)
	}))
}
func action(base, scenario, path, key, value string) error {
	c := http.Client{Timeout: 2500 * time.Millisecond}
	resp, e := c.Get(base + path + "?scenario=" + url.QueryEscape(scenario) + "&key=" + url.QueryEscape(key) + "&value=" + url.QueryEscape(value))
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
func readStats(base, scenario string) stats {
	resp, e := http.Get(base + "/stats?scenario=" + scenario)
	if e != nil {
		panic(e)
	}
	defer resp.Body.Close()
	var s stats
	json.NewDecoder(resp.Body).Decode(&s)
	return s
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func run(base string) bool {
	all := true
	report := func(name string, pass bool, detail any) {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"scenario": name, "pass": pass, "detail": detail})
		all = all && pass
	}
	for _, scenario := range []string{"post-expiry", "get-expiry", "unsupported", "concurrent", "cancel-waiter", "close-recovery", "bad-recovery", "failed-recovery", "bad-recovery-version", "bad-recovery-empty-version", "bad-recovery-stateless"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c, e := mcp.ConnectStreamableHTTP(ctx, mcp.ServerConfig{Name: scenario, URL: base + "/mcp?scenario=" + scenario})
		must(e)
		closed := make(chan error, 1)
		c.SetConnectionClosedCallback(func(err error) { closed <- err })
		changed := make(chan struct{}, 10)
		c.SetToolsChangedCallback(func() { changed <- struct{}{} })
		must(action(base, scenario, "/wait", "get-1", ""))
		if scenario != "unsupported" {
			must(action(base, scenario, "/wait", "open-s1", ""))
			must(action(base, scenario, "/event", "s1", "notify"))
			select {
			case <-changed:
			case <-ctx.Done():
				panic("initial notification missing")
			}
		}
		switch scenario {
		case "concurrent", "cancel-waiter", "close-recovery":
			n := 8
			if scenario == "close-recovery" {
				n = 1
			}
			done := make(chan error, n)
			waiterCtx, cancelWaiter := context.WithCancel(ctx)
			for i := 0; i < n; i++ {
				callCtx := ctx
				if i == 0 && scenario == "cancel-waiter" {
					callCtx = waiterCtx
				}
				go func() { _, err := c.CallTool(callCtx, "effect", nil); done <- err }()
			}
			defer cancelWaiter()
			must(action(base, scenario, "/wait", "call-"+strconv.Itoa(n), ""))
			must(action(base, scenario, "/release", "reject-calls", ""))
			must(action(base, scenario, "/wait", "recovering", ""))
			if scenario == "close-recovery" {
				start := time.Now()
				must(c.Close())
				closed := action(base, scenario, "/wait", "init-cancelled", "") == nil
				report(scenario, closed && time.Since(start) < time.Second, readStats(base, scenario))
				cancel()
				continue
			}
			if scenario == "cancel-waiter" {
				cancelWaiter()
				report("cancelled-waiter-returns", errors.Is(<-done, context.Canceled), nil)
				n--
			}
			must(action(base, scenario, "/release", "finish-init", ""))
			good := true
			for i := 0; i < n; i++ {
				err := <-done
				good = good && err == nil
			}
			s := readStats(base, scenario)
			report(scenario+"-singleflight", good && s.Initializes == 2 && s.Effects == n, s)
		default:
			must(action(base, scenario, "/expire", "", ""))
			if scenario == "get-expiry" {
				must(action(base, scenario, "/event", "s1", "disconnect"))
			} else {
				_, e = c.CallTool(ctx, "effect", nil)
			}
			if scenario == "bad-recovery" || scenario == "failed-recovery" || scenario == "bad-recovery-version" || scenario == "bad-recovery-empty-version" || scenario == "bad-recovery-stateless" {
				var terminal error
				select {
				case terminal = <-closed:
				case <-ctx.Done():
				}
				s := readStats(base, scenario)
				report(scenario, e != nil && terminal != nil && c.ConnectionError() != nil && s.Effects == 0, s)
				c.Close()
				cancel()
				continue
			}
		}
		newListener := action(base, scenario, "/wait", "open-s2", "") == nil
		notified := false
		if newListener {
			must(action(base, scenario, "/event", "s2", "notify"))
			select {
			case <-changed:
				notified = true
			case <-time.After(time.Second):
			}
		}
		s := readStats(base, scenario)
		clean := len(s.Gets) > 0 && s.Gets[len(s.Gets)-1].Session == "s2" && s.Gets[len(s.Gets)-1].Cursor == ""
		oldClosed := scenario == "unsupported" || action(base, scenario, "/wait", "closed-s1", "") == nil
		catalog := c.Tools()
		catalogOK := len(catalog) == 1 && catalog[0].Name == "tool-s2"
		report(scenario, newListener && notified && clean && oldClosed && s.Initializes == 2 && catalogOK && s.EarlyGET == 0 && s.BadHeaders == 0, s)
		if scenario == "post-expiry" && newListener {
			must(action(base, scenario, "/expire", "", ""))
			_, err := c.CallTool(ctx, "effect", nil)
			opened := action(base, scenario, "/wait", "open-s3", "") == nil
			closed := action(base, scenario, "/wait", "closed-s2", "") == nil
			snapshot := readStats(base, scenario)
			report("repeat-session-rotation", err == nil && opened && closed && snapshot.Initializes == 3 && snapshot.Effects == 2 && snapshot.Gets[len(snapshot.Gets)-1].Cursor == "", snapshot)
		}
		must(c.Close())
		s = readStats(base, scenario)
		report(scenario+"-close", !newListener || action(base, scenario, "/wait", "closed-s2", "") == nil, s)
		cancel()
	}
	for _, scenario := range []string{"auth-recover", "auth-stalled", "auth-denied", "auth-reinitialize", "auth-initialized", "auth-post-cursor", "auth-unsupported"} {
		var refresh atomic.Int32
		provider := func(ctx context.Context, force bool) (string, error) {
			if force {
				refresh.Add(1)
			}
			if refresh.Load() > 1 {
				return "restored", nil
			}
			if refresh.Load() > 0 {
				return "renewed", nil
			}
			return "old", nil
		}
		tr := mcp.NewStreamableHTTPTransportWithAuth(base+"/mcp?scenario="+scenario, map[string]string{"Mcp-Session-Id": "stale", "MCP-Protocol-Version": "stale", "Last-Event-ID": "stale"}, provider)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-06-18"}`)}))
		_, e := tr.Receive(ctx)
		must(e)
		must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", Method: "notifications/initialized"}))
		if scenario == "auth-reinitialize" || scenario == "auth-initialized" {
			must(action(base, scenario, "/wait", "open-s1", ""))
			must(action(base, scenario, "/expire", "", ""))
			must(action(base, scenario, "/event", "s1", "disconnect"))
			opened := action(base, scenario, "/wait", "open-s2", "") == nil
			snapshot := readStats(base, scenario)
			report(scenario, opened && refresh.Load() == 1 && snapshot.Initializes == 2 && snapshot.EarlyGET == 0 && snapshot.BadHeaders == 0, snapshot)
			tr.Close()
			cancel()
			continue
		}
		if scenario == "auth-post-cursor" {
			must(action(base, scenario, "/wait", "open-s1", ""))
			must(action(base, scenario, "/event", "s1", "notify"))
			_, err := tr.Receive(ctx)
			must(err)
			must(action(base, scenario, "/expire", "", ""))
			must(action(base, scenario, "/event", "s1", "disconnect"))
			must(action(base, scenario, "/wait", "get-3", ""))
			must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: json.RawMessage(`{"name":"effect"}`)}))
			_, err = tr.Receive(ctx)
			must(err)
			opened := action(base, scenario, "/wait", "get-4", "") == nil
			snapshot := readStats(base, scenario)
			good := opened && refresh.Load() == 2 && len(snapshot.Gets) == 4 && snapshot.Gets[3].Cursor == "cursor-s1"
			report(scenario, good, snapshot)
			tr.Close()
			cancel()
			continue
		}
		if scenario == "auth-unsupported" {
			must(action(base, scenario, "/wait", "get-1", ""))
			must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: json.RawMessage(`{"name":"effect"}`)}))
			_, err := tr.Receive(ctx)
			must(err)
			time.Sleep(150 * time.Millisecond)
			snapshot := readStats(base, scenario)
			report(scenario, len(snapshot.Gets) == 1 && refresh.Load() == 1, snapshot)
			tr.Close()
			cancel()
			continue
		}
		second := action(base, scenario, "/wait", "get-2", "") == nil
		good := second && refresh.Load() == 1
		if scenario == "auth-recover" || scenario == "auth-stalled" {
			must(action(base, scenario, "/event", "s1", "notify"))
			msg, e := tr.Receive(ctx)
			good = good && e == nil && msg.Method == "notifications/tools/list_changed"
		} else {
			time.Sleep(150 * time.Millisecond)
		}
		s := readStats(base, scenario)
		report(scenario, good && len(s.Gets) == 2 && s.Gets[0].Cursor == "" && s.BadHeaders == 0 && s.EarlyGET == 0, map[string]any{"stats": s, "refreshes": refresh.Load()})
		tr.Close()
		cancel()
	}
	{
		scenario := "initialized-expiry"
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		c, err := mcp.ConnectStreamableHTTP(ctx, mcp.ServerConfig{Name: scenario, URL: base + "/mcp?scenario=" + scenario})
		if err == nil {
			must(action(base, scenario, "/wait", "open-s2", ""))
			must(c.Close())
		}
		s := readStats(base, scenario)
		report(scenario, err == nil && s.Initializes == 2 && s.EarlyGET == 0, map[string]any{"stats": s, "error": fmt.Sprint(err)})
		cancel()
	}
	for _, scenario := range []string{"stale-get", "stale-post", "get-response-isolation"} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		tr := mcp.NewStreamableHTTPTransport(base+"/mcp?scenario="+scenario, nil)
		must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-06-18"}`)}))
		_, err := tr.Receive(ctx)
		must(err)
		must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", Method: "notifications/initialized"}))
		if scenario == "stale-get" {
			_, err = tr.Receive(ctx)
			must(err)
			// A resumed GET carrying old-3 proves both old notifications and
			// the reverse request have reached the transport's buffered inbox.
			must(action(base, scenario, "/wait", "old-events-buffered", ""))
		} else {
			must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 20, Method: "tools/call", Params: json.RawMessage(`{"name":"stream"}`)}))
		}
		if scenario == "get-response-isolation" {
			msg, err := tr.Receive(ctx)
			must(err)
			report("get-cannot-settle-post", msg.Method == "notifications/barrier", msg)
		} else {
			must(action(base, scenario, "/expire", "", ""))
			must(tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 21, Method: "tools/call", Params: json.RawMessage(`{"name":"effect"}`)}))
			if scenario == "stale-post" {
				msg, err := tr.Receive(ctx)
				must(err)
				report("new-call-settles-during-old-post", msg.ID == 21 && msg.Method == "", msg)
			}
		}
		if scenario != "stale-get" {
			must(action(base, scenario, "/release", "release-post", ""))
		}
		msg, err := tr.Receive(ctx)
		must(err)
		wantID := int64(20)
		if scenario == "stale-get" {
			wantID = 21
		}
		report(scenario, msg.Method == "" && msg.ID == wantID, map[string]any{"message": msg, "stats": readStats(base, scenario)})
		must(tr.Close())
		cancel()
	}
	return all
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "server" {
		server()
		return
	}
	exe, e := os.Executable()
	must(e)
	p := exec.Command(exe, "server")
	stdout, e := p.StdoutPipe()
	must(e)
	p.Stderr = os.Stderr
	must(p.Start())
	defer func() { p.Process.Kill(); p.Wait() }()
	s := bufio.NewScanner(stdout)
	if !s.Scan() {
		panic("missing server address")
	}
	if !run(s.Text()) {
		os.Exit(1)
	}
}
