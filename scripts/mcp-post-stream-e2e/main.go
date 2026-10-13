// Command mcp-post-stream-e2e exercises the production MCP client against a
// separate loopback HTTP server process using synthetic requests only.
// Run from the repository root:
//
//	go run -race ./scripts/mcp-post-stream-e2e
//
// JSON receipts on stdout record each result and the server's invocation counts.
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
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/mcp"
)

type wire struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}
type replySession struct {
	ID      string `json:"id"`
	Session string `json:"session"`
}

type fixture struct {
	mu            sync.Mutex
	gates         map[string]chan struct{}
	calls         map[string]int
	targetIDs     map[string]json.RawMessage
	reverse       []string
	replySessions []replySession
	replyActive   int
	replyMax      int
	initializes   int
	heldIDs       map[string]string
	events        chan string
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
	c := f.gate(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-c:
	default:
		close(c)
	}
}
func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/wait":
		select {
		case <-f.gate(r.URL.Query().Get("key")):
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
		return
	case "/release":
		f.signal(r.URL.Query().Get("key"))
		w.WriteHeader(204)
		return
	case "/notify":
		f.events <- `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`
		w.WriteHeader(204)
		return
	case "/stats":
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"calls": f.calls, "reverse": f.reverse, "reply_active": f.replyActive, "reply_max": f.replyMax, "initializes": f.initializes, "reply_sessions": f.replySessions})
		return
	}
	if r.Method == "DELETE" {
		w.WriteHeader(204)
		return
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		f.signal("get-open")
		for {
			select {
			case s := <-f.events:
				fmt.Fprintf(w, "data: %s\n\n", s)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
	var q wire
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		w.WriteHeader(400)
		return
	}
	if q.Method == "" {
		var id string
		_ = json.Unmarshal(q.ID, &id)
		f.mu.Lock()
		f.replySessions = append(f.replySessions, replySession{ID: string(q.ID), Session: r.Header.Get("Mcp-Session-Id")})
		if heldID := f.heldIDs[string(q.ID)]; heldID != "" {
			id = heldID
		}
		f.mu.Unlock()
		if id == "session-auth-retry" {
			f.mu.Lock()
			f.reverse = append(f.reverse, string(q.ID))
			f.mu.Unlock()
			f.signal("reply-open-" + id)
			select {
			case <-f.gate("release-auth-reply"):
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(id, "held-") || id == "expired-reply" {
			f.mu.Lock()
			f.reverse = append(f.reverse, string(q.ID))
			f.replyActive++
			if f.replyActive > f.replyMax {
				f.replyMax = f.replyActive
			}
			f.mu.Unlock()
			defer func() { f.mu.Lock(); f.replyActive--; f.mu.Unlock(); f.signal("reply-closed-" + id) }()
			if id == "expired-reply" {
				w.WriteHeader(404)
				return
			}
			if strings.Contains(id, "body") {
				w.WriteHeader(202)
				w.(http.Flusher).Flush()
			}
			f.signal("reply-open-" + id)
			select {
			case <-r.Context().Done():
			case <-f.gate("release-replies"):
			case <-f.gate("release-reply-" + id):
			}
			return
		}
		f.mu.Lock()
		f.reverse = append(f.reverse, string(q.ID))
		f.mu.Unlock()
		f.signal("reverse-" + string(q.ID))
		w.WriteHeader(202)
		return
	}
	switch q.Method {
	case "server/discover":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"legacy"}}`, q.ID)
		return
	case "initialize":
		f.mu.Lock()
		f.initializes++
		session := fmt.Sprintf("synthetic-%d", f.initializes)
		f.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", session)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"loopback","version":"1"}}}`, q.ID)
		return
	case "tools/list":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"probe","inputSchema":{"type":"object"}}]}}`, q.ID)
		return
	case "tools/call":
	default:
		w.WriteHeader(202)
		return
	}
	name := q.Params.Name
	if name == "refresh-session" && r.Header.Get("Authorization") != "Bearer synthetic-refreshed" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if name == "replace-session" && r.Header.Get("Mcp-Session-Id") == "synthetic-1" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	f.calls[name]++
	if strings.HasPrefix(name, "cross-target-") {
		f.targetIDs[strings.TrimPrefix(name, "cross-target-")] = append(json.RawMessage(nil), q.ID...)
	}
	f.mu.Unlock()
	if name == "cross-origin-json" {
		f.mu.Lock()
		target := append(json.RawMessage(nil), f.targetIDs["json"]...)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"wrong-origin"}]}}`, target)
		return
	}
	if name == "json-cancel" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{")
		w.(http.Flusher).Flush()
		f.signal("open-" + name)
		<-r.Context().Done()
		f.signal("closed-" + name)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, ": open\n\n")
	w.(http.Flusher).Flush()
	f.signal("open-" + name)
	result := func() {
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n\n", q.ID)
		w.(http.Flusher).Flush()
	}
	if name == "replace-session" || name == "refresh-session" {
		result()
		return
	}
	if name == "session-barrier" {
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"current-session-probe\",\"method\":\"ping\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-f.gate(`reverse-"current-session-probe"`):
			result()
		case <-r.Context().Done():
		}
		return
	}
	if strings.HasPrefix(name, "reply-session-") {
		defer f.signal("closed-" + name)
		emit := func(message string) bool {
			if strings.Contains(name, "queued-get") {
				select {
				case f.events <- message:
					return true
				case <-r.Context().Done():
					return false
				}
			}
			fmt.Fprintf(w, "data: %s\n\n", message)
			w.(http.Flusher).Flush()
			return true
		}
		if name == "reply-session-auth-retry" {
			emit(`{"jsonrpc":"2.0","id":"session-auth-retry","method":"ping"}`)
		} else {
			for i := 0; i < 5; i++ {
				id := fmt.Sprintf("held-session-%d", i)
				if i == 4 {
					id = "queued-old-session"
				}
				if !emit(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"method":"ping"}`, id)) {
					return
				}
				if i < 4 {
					select {
					case <-f.gate("reply-open-" + id):
					case <-r.Context().Done():
						return
					}
				}
			}
			emit(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
		}
		<-r.Context().Done()
		return
	}
	if strings.HasPrefix(name, "reply-") {
		defer f.signal("closed-" + name)
		id := "held-" + strings.TrimPrefix(name, "reply-")
		if name == "reply-expired" {
			id = "expired-reply"
		}
		if strings.HasPrefix(name, "reply-server-cancel-") {
			rawID := map[string]string{"zero": "0", "empty": `""`, "string-zero": `"0"`, "collision": string(q.ID)}[strings.TrimPrefix(name, "reply-server-cancel-")]
			f.mu.Lock()
			f.heldIDs[rawID] = id
			f.mu.Unlock()
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"method\":\"ping\"}\n\n", rawID)
			w.(http.Flusher).Flush()
			select {
			case <-f.gate("reply-open-" + id):
			case <-r.Context().Done():
				return
			}
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/cancelled\",\"params\":{\"requestId\":%s}}\n\n", rawID)
			w.(http.Flusher).Flush()
			select {
			case <-f.gate("reply-closed-" + id):
				result()
			case <-r.Context().Done():
			}
			return
		}
		count := 1
		if name == "reply-overload" || name == "reply-unsolicited" {
			count = 128
		}
		if name == "reply-queued" {
			count = 20
		}
		for i := 0; i < count; i++ {
			itemID := id
			if count > 1 {
				itemID += "-" + strconv.Itoa(i)
			}
			if name == "reply-unsolicited" {
				select {
				case f.events <- fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"method":"ping"}`, itemID):
				case <-r.Context().Done():
					return
				}
				continue
			}
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"method\":\"ping\"}\n\n", itemID)
			w.(http.Flusher).Flush()
			if count == 1 || (name == "reply-queued" && i < 4) {
				select {
				case <-f.gate("reply-open-" + itemID):
				case <-r.Context().Done():
					return
				}
			}
		}
		if name == "reply-body-complete" {
			result()
		}
		if name == "reply-queued" {
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
		return
	}
	if strings.HasPrefix(name, "cross-target-") {
		select {
		case <-f.gate("release-" + name):
			result()
		case <-r.Context().Done():
		}
		return
	}
	if name == "cross-origin-batch" || name == "cross-origin-after-result" {
		format := strings.TrimPrefix(name, "cross-origin-")
		f.mu.Lock()
		target := append(json.RawMessage(nil), f.targetIDs[format]...)
		f.mu.Unlock()
		if format == "batch" {
			fmt.Fprintf(w, "data: [{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{}},{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"wrong-origin\"}]}}]\n\n", q.ID, target)
		} else {
			result()
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"wrong-origin\"}]}}\n\n", target)
		}
		w.(http.Flusher).Flush()
		return
	}
	if name == "cross-origin-sse" {
		f.mu.Lock()
		target := append(json.RawMessage(nil), f.targetIDs["sse"]...)
		f.mu.Unlock()
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"wrong-origin\"}]}}\n\n", target)
		fmt.Fprint(w, "data: {broken\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		f.signal("closed-" + name)
		return
	}
	if strings.HasPrefix(name, "race-") {
		select {
		case <-f.gate("release-" + name):
			result()
			<-r.Context().Done()
		case <-r.Context().Done():
		}
		f.signal("closed-" + name)
		return
	}
	switch name {
	case "flood":
		for i := 0; i < 128; i++ {
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
		}
		w.(http.Flusher).Flush()
		f.signal("flood-ready")
	case "eof", "concurrent-eof":
		return
	case "invalid-version", "null-message", "missing-id", "missing-result", "batch":
		payload := map[string]string{
			"invalid-version": fmt.Sprintf(`{"jsonrpc":"1.0","id":%s,"result":{}}`, q.ID),
			"null-message":    `null`,
			"missing-id":      `{"jsonrpc":"2.0","result":{}}`,
			"missing-result":  fmt.Sprintf(`{"jsonrpc":"2.0","id":%s}`, q.ID),
			"batch":           fmt.Sprintf(`[{"jsonrpc":"2.0","id":%s,"result":{}}]`, q.ID),
		}[name]
		fmt.Fprintf(w, "data: %s\n\n", payload)
		w.(http.Flusher).Flush()
	case "malformed":
		fmt.Fprint(w, "data: {broken\n\n")
		w.(http.Flusher).Flush()
	case "oversized":
		fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("x", 4*1024*1024+1))
		w.(http.Flusher).Flush()
	case "success-eof", "healthy":
		result()
		return
	case "success-open":
		result()
	case "error-open":
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32042,\"message\":\"synthetic tool failure\"}}\n\n", q.ID)
		w.(http.Flusher).Flush()
	case "concurrent-healthy":
		select {
		case <-f.gate("release-healthy"):
			result()
			return
		case <-r.Context().Done():
			return
		}
	case "reverse":
		for _, id := range []string{`"reverse-string"`, string(q.ID), "0", `""`, `"0"`, `"汉字\\quote"`, `"` + string(q.ID) + `"`} {
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"method\":\"ping\"}\n\n", id)
			w.(http.Flusher).Flush()
			select {
			case <-f.gate("reverse-" + id):
			case <-r.Context().Done():
				return
			}
		}
		result()
		return
	}
	<-r.Context().Done()
	f.signal("closed-" + name)
}
func server() {
	f := &fixture{gates: map[string]chan struct{}{}, calls: map[string]int{}, targetIDs: map[string]json.RawMessage{}, events: make(chan string, 16), heldIDs: map[string]string{}}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		panic(e)
	}
	fmt.Println("http://" + l.Addr().String())
	http.Serve(l, http.HandlerFunc(f.serve))
}
func httpAction(base, path string) error {
	c := &http.Client{Timeout: time.Second}
	r, e := c.Get(base + path)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 204 {
		return fmt.Errorf("HTTP %d", r.StatusCode)
	}
	return nil
}

type receipt struct {
	Name         string `json:"name"`
	Error        string `json:"error,omitempty"`
	Milliseconds int64  `json:"milliseconds,omitempty"`
	Closed       bool   `json:"closed,omitempty"`
	Healthy      bool   `json:"healthy,omitempty"`
	GETHealthy   bool   `json:"get_healthy,omitempty"`
	Pass         bool   `json:"pass"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "server" {
		server()
		return
	}
	if !run() {
		os.Exit(1)
	}
}

func run() bool {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	p := exec.Command(exe, "server")
	stdout, e := p.StdoutPipe()
	if e != nil {
		panic(e)
	}
	p.Stderr = os.Stderr
	if e = p.Start(); e != nil {
		panic(e)
	}
	defer func() { p.Process.Kill(); p.Wait() }()
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() {
		panic("no server address")
	}
	base := scan.Text()
	c, e := mcp.ConnectStreamableHTTP(context.Background(), mcp.ServerConfig{Name: "synthetic", URL: base})
	if e != nil {
		panic(e)
	}
	defer c.Close()
	changed := make(chan struct{}, 16)
	c.SetToolsChangedCallback(func() { changed <- struct{}{} })
	if e = httpAction(base, "/wait?key=get-open"); e != nil {
		panic(e)
	}
	all := true
	rs := []receipt{}
	for _, name := range []string{"eof", "invalid-version", "null-message", "missing-id", "missing-result", "batch", "malformed", "oversized", "success-eof", "success-open", "error-open", "cancel-open", "reverse", "concurrent-eof"} {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		done := make(chan error, 1)
		start := time.Now()
		healthyDone := make(chan error, 1)
		if name == "concurrent-eof" {
			go func() { _, err := c.CallTool(ctx, "concurrent-healthy", json.RawMessage(`{}`)); healthyDone <- err }()
			if e = httpAction(base, "/wait?key=open-concurrent-healthy"); e != nil {
				panic(e)
			}
		}
		go func() { _, err := c.CallTool(ctx, name, json.RawMessage(`{}`)); done <- err }()
		if name == "cancel-open" {
			if e = httpAction(base, "/wait?key=open-cancel-open"); e != nil {
				panic(e)
			}
			cancel()
		}
		err := <-done
		ms := time.Since(start).Milliseconds()
		r := receipt{Name: name, Milliseconds: ms, Pass: true, Closed: true}
		if err != nil {
			r.Error = err.Error()
		}
		switch name {
		case "eof", "concurrent-eof", "malformed", "oversized", "invalid-version", "null-message", "missing-id", "missing-result", "batch":
			r.Pass = err != nil && !errors.Is(err, context.DeadlineExceeded) && ms < 1000
		case "error-open":
			r.Pass = err != nil && strings.Contains(err.Error(), "synthetic tool failure")
		case "cancel-open":
			r.Pass = errors.Is(err, context.Canceled)
		default:
			r.Pass = err == nil
		}
		if name == "success-open" || name == "error-open" || name == "cancel-open" || name == "malformed" || name == "oversized" {
			r.Closed = httpAction(base, "/wait?key="+url.QueryEscape("closed-"+name)) == nil
			r.Pass = r.Pass && r.Closed
		}
		if name == "concurrent-eof" {
			httpAction(base, "/release?key=release-healthy")
			r.Pass = r.Pass && (<-healthyDone == nil)
		}
		cancel()
		hc, hcancel := context.WithTimeout(context.Background(), time.Second)
		_, he := c.CallTool(hc, "healthy", json.RawMessage(`{}`))
		hcancel()
		r.Healthy = he == nil && c.ConnectionError() == nil
		httpAction(base, "/notify")
		select {
		case <-changed:
			r.GETHealthy = true
		case <-time.After(time.Second):
		}
		r.Pass = r.Pass && r.Healthy && r.GETHealthy
		all = all && r.Pass
		rs = append(rs, r)
	}

	for _, format := range []string{"sse", "json", "batch", "after-result"} {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		targetName := "cross-target-" + format
		targetDone := make(chan error, 1)
		go func() {
			result, err := c.CallTool(ctx, targetName, json.RawMessage(`{}`))
			if err == nil && (len(result.Content) != 1 || result.Content[0].Text != "ok") {
				err = fmt.Errorf("unrelated call accepted a result from the wrong POST: %+v", result)
			}
			targetDone <- err
		}()
		if e = httpAction(base, "/wait?key=open-"+targetName); e != nil {
			panic(e)
		}
		_, originErr := c.CallTool(ctx, "cross-origin-"+format, json.RawMessage(`{}`))
		releaseErr := httpAction(base, "/release?key=release-"+targetName)
		targetErr := <-targetDone
		originOK := originErr != nil && !errors.Is(originErr, context.DeadlineExceeded)
		if format == "after-result" {
			originOK = originErr == nil
		}
		pass := originOK && releaseErr == nil && targetErr == nil && c.ConnectionError() == nil
		rs = append(rs, receipt{Name: "cross-post-id-" + format, Error: fmt.Sprintf("origin: %v; target: %v", originErr, targetErr), Pass: pass})
		all = all && pass
		cancel()
	}
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("race-%d", i)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := c.CallTool(ctx, name, json.RawMessage(`{}`)); done <- err }()
		if e = httpAction(base, "/wait?key=open-"+name); e != nil {
			panic(e)
		}
		start := make(chan struct{})
		sent := make(chan error, 1)
		go func() { <-start; cancel() }()
		go func() { <-start; sent <- httpAction(base, "/release?key=release-"+name) }()
		close(start)
		err := <-done
		pass := (err == nil || errors.Is(err, context.Canceled)) && <-sent == nil && httpAction(base, "/wait?key=closed-"+name) == nil
		rs = append(rs, receipt{Name: name, Pass: pass})
		all = all && pass
	}
	{
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := c.CallTool(ctx, "json-cancel", json.RawMessage(`{}`)); done <- err }()
		if e = httpAction(base, "/wait?key=open-json-cancel"); e != nil {
			panic(e)
		}
		cancel()
		err := <-done
		pass := errors.Is(err, context.Canceled) && httpAction(base, "/wait?key=closed-json-cancel") == nil
		rs = append(rs, receipt{Name: "json-body-cancel", Pass: pass})
		all = all && pass
	}
	{
		tr := mcp.NewStreamableHTTPTransport(base, nil)
		ctx, cancel := context.WithCancel(context.Background())
		err := tr.Send(ctx, mcp.Request{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: json.RawMessage(`{"name":"flood"}`)})
		if e = httpAction(base, "/wait?key=flood-ready"); e != nil {
			panic(e)
		}
		cancel()
		done := make(chan error, 1)
		go func() { done <- tr.Close() }()
		pass := err == nil && httpAction(base, "/wait?key=closed-flood") == nil
		select {
		case e := <-done:
			pass = pass && e == nil
		case <-time.After(time.Second):
			pass = false
		}
		rs = append(rs, receipt{Name: "full-inbox-cancel-close", Pass: pass})
		all = all && pass
	}
	{
		c2, e := mcp.ConnectStreamableHTTP(context.Background(), mcp.ServerConfig{Name: "close-client", URL: base})
		if e != nil {
			panic(e)
		}
		done := make(chan error, 2)
		for _, name := range []string{"close-open-1", "close-open-2"} {
			go func(name string) { _, e := c2.CallTool(context.Background(), name, json.RawMessage(`{}`)); done <- e }(name)
			if e = httpAction(base, "/wait?key=open-"+name); e != nil {
				panic(e)
			}
		}
		closed := make(chan error, 1)
		go func() { closed <- c2.Close() }()
		pass := true
		select {
		case e := <-closed:
			pass = e == nil
		case <-time.After(time.Second):
			pass = false
		}
		for _, name := range []string{"close-open-1", "close-open-2"} {
			select {
			case e := <-done:
				pass = pass && e != nil
			case <-time.After(time.Second):
				pass = false
			}
			pass = pass && httpAction(base, "/wait?key=closed-"+name) == nil
		}
		rs = append(rs, receipt{Name: "close-two-pending-calls", Pass: pass})
		all = all && pass
	}
	for _, id := range []string{`0`, `""`, `"0"`, `"caller-id"`} {
		for _, method := range []string{"eof", "success-open", "error-open"} {
			tr := mcp.NewStreamableHTTPTransport(base, nil)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			var req mcp.Request
			if e := json.Unmarshal([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":%q}}`, id, method)), &req); e != nil {
				panic(e)
			}
			err := tr.Send(ctx, req)
			got, readErr := tr.Receive(ctx)
			out, _ := json.Marshal(got)
			var msg struct {
				ID json.RawMessage `json:"id"`
			}
			json.Unmarshal(out, &msg)
			pass := err == nil && readErr == nil && string(msg.ID) == id && ((method == "success-open") == (got.Error == nil))
			rs = append(rs, receipt{Name: "direct-id-" + id + "-" + method, Pass: pass})
			all = all && pass
			cancel()
			tr.Close()
		}
	}

	response, e := http.Get(base + "/stats")
	if e != nil {
		panic(e)
	}
	var stats struct {
		Calls   map[string]int `json:"calls"`
		Reverse []string       `json:"reverse"`
	}
	json.NewDecoder(response.Body).Decode(&stats)
	response.Body.Close()
	for k, v := range stats.Calls {
		expected := 1
		switch k {
		case "healthy":
			expected = 14
		case "eof", "success-open", "error-open":
			expected = 5
		}
		if v != expected {
			all = false
		}
	}
	all = all && len(stats.Reverse) == 7
	replyReceipts, replyPass := runReverseReplies()
	all = all && replyPass
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{"pass": all, "receipts": rs, "server_receipts": stats, "reverse_reply_receipts": replyReceipts})
	return all
}

// Each scenario drives the production Client over a separate HTTP server process.
// Gates prove dispatch, cancellation and teardown without sleeping or server keys.
func runReverseReplies() ([]map[string]any, bool) {
	receipts := []map[string]any{}
	all := true
	for _, name := range []string{"reply-body-cancel", "reply-headers-cancel", "reply-body-complete", "reply-body-timeout", "reply-queued", "reply-overload", "reply-expired", "reply-headers-close", "reply-server-cancel-zero", "reply-server-cancel-empty", "reply-server-cancel-string-zero", "reply-server-cancel-collision", "reply-unsolicited", "reply-session-queued-post", "reply-session-queued-get", "reply-session-auth-retry", "reply-session-queued-get-refresh"} {
		record := runReverseReply(name)
		all = all && record["pass"].(bool)
		receipts = append(receipts, record)
	}
	return receipts, all
}

func runReverseReply(name string) map[string]any {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	server := exec.Command(exe, "server")
	stdout, err := server.StdoutPipe()
	if err != nil {
		panic(err)
	}
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		panic(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		panic("no reverse fixture address")
	}
	base := scanner.Text()
	var client *mcp.Client
	if name == "reply-session-auth-retry" || name == "reply-session-queued-get-refresh" {
		transport := mcp.NewStreamableHTTPTransportWithAuth(base, nil, func(_ context.Context, forceRefresh bool) (string, error) {
			if forceRefresh {
				return "synthetic-refreshed", nil
			}
			return "synthetic-token", nil
		})
		client, err = mcp.Connect(context.Background(), name, transport)
	} else {
		client, err = mcp.ConnectStreamableHTTP(context.Background(), mcp.ServerConfig{Name: name, URL: base})
	}
	if err != nil {
		panic(err)
	}
	defer client.Close()
	if err := httpAction(base, "/wait?key=get-open"); err != nil {
		panic(err)
	}
	changed := make(chan struct{}, 8)
	client.SetToolsChangedCallback(func() { changed <- struct{}{} })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	origin := make(chan error, 1)
	go func() { _, err := client.CallTool(ctx, name, json.RawMessage(`{}`)); origin <- err }()
	pass := true
	var originErr error
	var healthyErr error
	closed := false
	started := time.Now()
	switch name {
	case "reply-session-queued-post", "reply-session-queued-get", "reply-session-auth-retry", "reply-session-queued-get-refresh":
		if name == "reply-session-auth-retry" {
			pass = httpAction(base, "/wait?key=reply-open-session-auth-retry") == nil
		} else {
			select {
			case <-changed:
			case <-time.After(time.Second):
				pass = false
			}
		}
		replaceCtx, stop := context.WithTimeout(context.Background(), time.Second)
		method := "replace-session"
		if name == "reply-session-queued-get-refresh" {
			method = "refresh-session"
		}
		_, replaceErr := client.CallTool(replaceCtx, method, json.RawMessage(`{}`))
		stop()
		pass = pass && replaceErr == nil
		if name == "reply-session-auth-retry" {
			httpAction(base, "/release?key=release-auth-reply")
			select {
			case originErr = <-origin:
				pass = pass && originErr != nil
			case <-time.After(time.Second):
				pass = false
			}
		} else {
			// Keep three workers occupied. The remaining worker must finish the
			// queued old-session reply before it can answer the new probe.
			httpAction(base, "/release?key=release-reply-held-session-0")
		}
		// The new-session reverse round trip is also a queue-drain barrier.
		barrierCtx, stop := context.WithTimeout(context.Background(), time.Second)
		_, healthyErr = client.CallTool(barrierCtx, "session-barrier", json.RawMessage(`{}`))
		stop()
		pass = pass && healthyErr == nil
		cancel()
		if originErr == nil {
			originErr = <-origin
		}
		for i := 0; name != "reply-session-auth-retry" && i < 4; i++ {
			// GET replies have no originating call to cancel them.
			httpAction(base, "/release?key=release-reply-held-session-"+strconv.Itoa(i))
			pass = httpAction(base, "/wait?key=reply-closed-held-session-"+strconv.Itoa(i)) == nil && pass
		}
	case "reply-body-cancel", "reply-headers-cancel", "reply-headers-close":
		id := "held-" + strings.TrimPrefix(name, "reply-")
		pass = httpAction(base, "/wait?key=reply-open-"+id) == nil
		// A healthy call and GET notification must pass while the reply POST is held.
		healthyCtx, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_, healthyErr = client.CallTool(healthyCtx, "healthy", json.RawMessage(`{}`))
		stop()
		httpAction(base, "/notify")
		notified := false
		select {
		case <-changed:
			notified = true
		case <-time.After(time.Second):
		}
		pass = pass && healthyErr == nil && notified
		if name == "reply-headers-close" {
			done := make(chan error, 1)
			go func() { done <- client.Close() }()
			select {
			case err := <-done:
				closed = err == nil
			case <-time.After(time.Second):
			}
		} else {
			cancel()
		}
		select {
		case originErr = <-origin:
		case <-time.After(time.Second):
			originErr = errors.New("origin did not settle")
		}
		pass = pass && originErr != nil && httpAction(base, "/wait?key=reply-closed-"+id) == nil
		if name == "reply-headers-close" {
			pass = pass && closed
		} else {
			pass = pass && errors.Is(originErr, context.Canceled)
		}
	case "reply-queued":
		select {
		case <-changed:
		case <-time.After(time.Second):
			pass = false
		}
		cancel()
		originErr = <-origin
		pass = pass && errors.Is(originErr, context.Canceled)
		for i := 0; i < 4; i++ {
			pass = httpAction(base, "/wait?key=reply-closed-held-queued-"+strconv.Itoa(i)) == nil && pass
		}
	default:
		originErr = <-origin
		if strings.HasPrefix(name, "reply-server-cancel-") {
			pass = originErr == nil
		} else if name == "reply-body-complete" {
			pass = originErr == nil && httpAction(base, "/wait?key=reply-closed-held-body-complete") == nil
		} else {
			pass = originErr != nil && !errors.Is(originErr, context.DeadlineExceeded)
			if name == "reply-overload" || name == "reply-unsolicited" {
				pass = pass && strings.Contains(originErr.Error(), "capacity")
			}
		}
	}
	pass = httpAction(base, "/wait?key=closed-"+name) == nil && pass
	// Release only after assertions, so a red baseline can still cleanly exit.
	httpAction(base, "/release?key=release-replies")
	if name != "reply-headers-close" && name != "reply-unsolicited" {
		healthyCtx, stop := context.WithTimeout(context.Background(), time.Second)
		_, err = client.CallTool(healthyCtx, "healthy", json.RawMessage(`{}`))
		stop()
		pass = pass && err == nil && client.ConnectionError() == nil
	}
	if name == "reply-unsolicited" {
		pass = pass && client.ConnectionError() != nil
		client.Close()
	}
	response, err := http.Get(base + "/stats")
	if err != nil {
		panic(err)
	}
	var stats struct {
		Calls       map[string]int `json:"calls"`
		Reverse     []string       `json:"reverse"`
		Active      int            `json:"reply_active"`
		Maximum     int            `json:"reply_max"`
		Initializes int            `json:"initializes"`
		Sessions    []replySession `json:"reply_sessions"`
	}
	json.NewDecoder(response.Body).Decode(&stats)
	response.Body.Close()
	expectedInitializes := 1
	if strings.HasPrefix(name, "reply-session-") {
		expectedInitializes = 2
		expectedReplies := 5
		probeSession := "synthetic-2"
		if name == "reply-session-queued-get-refresh" {
			expectedInitializes = 1
			expectedReplies = 6
			probeSession = "synthetic-1"
		}
		if name == "reply-session-auth-retry" {
			expectedReplies = 2
		}
		pass = pass && len(stats.Reverse) == expectedReplies && len(stats.Sessions) == expectedReplies
		for _, reply := range stats.Sessions {
			if reply.ID == `"current-session-probe"` {
				pass = pass && reply.Session == probeSession
			} else {
				pass = pass && reply.Session == "synthetic-1" && (name == "reply-session-queued-get-refresh" || reply.ID != `"queued-old-session"`)
			}
		}
	}
	pass = pass && stats.Calls[name] == 1 && stats.Active == 0 && stats.Maximum <= 4 && stats.Initializes == expectedInitializes
	if name == "reply-queued" {
		pass = pass && len(stats.Reverse) == 4
	}
	if name == "reply-expired" {
		pass = pass && len(stats.Reverse) == 1
	}
	return map[string]any{"name": name, "pass": pass, "origin_error": fmt.Sprint(originErr), "healthy_while_held": fmt.Sprint(healthyErr), "milliseconds": time.Since(started).Milliseconds(), "server_receipts": stats}
}
