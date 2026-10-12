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
type fixture struct {
	mu        sync.Mutex
	gates     map[string]chan struct{}
	calls     map[string]int
	targetIDs map[string]json.RawMessage
	reverse   []string
	events    chan string
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
		json.NewEncoder(w).Encode(map[string]any{"calls": f.calls, "reverse": f.reverse})
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
		w.Header().Set("Mcp-Session-Id", "synthetic")
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
	f := &fixture{gates: map[string]chan struct{}{}, calls: map[string]int{}, targetIDs: map[string]json.RawMessage{}, events: make(chan string, 16)}
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
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{"pass": all, "receipts": rs, "server_receipts": stats})
	return all
}
