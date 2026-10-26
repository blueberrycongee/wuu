// Command testhost serves the real relay and execution host for native client tests.
// All identities and state are disposable. It never reads the user's Wuu configuration.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/clients/native/testsupport"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type provider struct{ release chan struct{} }

func (p *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer native-fixture-only" {
		http.Error(w, "synthetic fixture credential required", http.StatusUnauthorized)
		return
	}
	var request struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch request.Model {
	case "native-fixture-title":
	case "native-test", "alternate":
		select {
		case <-p.release:
		case <-r.Context().Done():
			return
		}
	default:
		http.Error(w, "unexpected fixture model", http.StatusBadRequest)
		return
	}
	if !request.Stream {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"native-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"native transport verified"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"id\":\"native-fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"native transport verified\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"native-fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	root, err := os.MkdirTemp("", "wuu-native-test-")
	must(err)
	defer os.RemoveAll(root)
	must(os.Setenv("HOME", root))
	must(os.Setenv("WUU_HOME", filepath.Join(root, ".wuu")))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	registry, err := relay.OpenRegistry("")
	must(err)
	server := httptest.NewServer(relay.New(relay.Options{Registry: registry}).Handler())
	defer server.Close()
	store, err := host.LoadOrCreateStore(filepath.Join(root, "host.json"), "Native test computer")
	must(err)
	phone, err := secure.NewIdentity()
	must(err)
	b64 := base64.RawURLEncoding.EncodeToString
	must(store.AddDevice(phone.Public(), "Native test phone", time.Now()))
	must(registry.AddDevice(b64(store.Identity().Public()), b64(phone.Public()), "Native test phone", time.Now()))
	gate := &provider{release: make(chan struct{}, 8)}
	modelServer := httptest.NewServer(gate)
	defer modelServer.Close()
	must(testsupport.WriteLoopbackConfig(filepath.Join(root, "config.json"), "native-test", modelServer.URL+"/v1"))
	cfg, _, err := config.LoadPath(filepath.Join(root, "config.json"))
	must(err)
	bindings := appserver.NewRuntimeBindings()
	factories := append(bindings.NativeProviders(), runtime.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) {
			return credentialstore.NewFileStore(filepath.Join(root, "credentials.json")), nil
		}), nil
	}})
	rt, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: root, ConfigPath: filepath.Join(root, "config.json"), ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg, NativeProviders: factories})
	must(err)
	defer rt.Cleanup()

	ready := make(chan struct{}, 1)
	// Persisted history uses the same reconstruction and paging as an old conversation.
	var records []session.HistoryRecord
	for i := 0; i < 43; i++ {
		reply := fmt.Sprintf("reply %d", i)
		if i == 42 {
			reply = strings.Repeat("长消息🌱\n", 20_000)
		}
		records = append(records, session.HistoryRecord{Role: "user", Content: fmt.Sprintf("message %d", i)},
			session.HistoryRecord{Role: "assistant", Content: reply})
	}
	paged, err := rt.Sessions.CreateInitialized(session.Session{ID: session.NewID(), CWD: root, Title: "Long history"}, records)
	must(err)
	toolHistory, err := rt.Sessions.CreateInitialized(session.Session{ID: session.NewID(), CWD: root, Title: "Tool history"}, []session.HistoryRecord{
		{Role: "user", Content: "inspect fixture"},
		{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"large-tool","name":"read_file","arguments":"{\"path\":\"fixture.txt\"}"}]`)},
		{Role: "tool", Name: "read_file", ToolCallID: "large-tool", Content: strings.Repeat("工具结果🌱\n", 20_000)},
		{Role: "assistant", Content: "inspected"},
	})
	must(err)
	h, err := host.New(host.Options{Runtime: rt, Store: store, AppServerOptions: []appserver.ServerOption{appserver.WithRuntimeBindings(bindings)},
		RelayURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/connect",
		Pairing: &host.PairingConfig{Once: true, OnURI: func(string) {
			select {
			case ready <- struct{}{}:
			default:
			}
		}},
	})
	must(err)
	go func() {
		if err := h.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, err)
			cancel()
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		panic(ctx.Err())
	}
	var imageData bytes.Buffer
	bitmap := image.NewRGBA(image.Rect(0, 0, 256, 256))
	pixels := rand.New(rand.NewSource(42))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			bitmap.Set(x, y, color.RGBA{R: uint8(pixels.Intn(256)), G: uint8(pixels.Intn(256)), B: uint8(pixels.Intn(256)), A: 255})
		}
	}
	must(png.Encode(&imageData, bitmap))
	// Publish through the real runtime so remote reads exercise managed ownership.
	artifactID := session.NewID()
	must(os.WriteFile(filepath.Join(root, "published.png"), imageData.Bytes(), 0600))
	artifactRuntime, err := rt.NewThreadRuntimeForRoot(artifactID, root)
	must(err)
	artifactContext := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: artifactID, ExecutionID: "publish-fixture"})
	published, err := artifactRuntime.StreamRunner.Tools.(agent.RichToolExecutor).ExecuteResult(artifactContext,
		providers.ToolCall{ID: "publish", Name: "present_artifact", Arguments: `{"path":"published.png"}`})
	must(err)
	publishedJSON, err := json.Marshal(published)
	must(err)
	_, err = rt.Sessions.CreateInitialized(session.Session{ID: artifactID, CWD: root, Title: "Published image"}, []session.HistoryRecord{
		{Role: "user", Content: "publish the fixture"},
		{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"publish","name":"present_artifact","arguments":"{\"path\":\"published.png\"}"}]`)},
		{Role: "tool", Name: "present_artifact", ToolCallID: "publish", ToolResult: publishedJSON},
		{Role: "assistant", Content: "published"},
	})
	must(err)
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>", "<< /Length 0 >>\nstream\nendstream"}
	offsets := []int{}
	for i, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprint(&pdf, "xref\n0 5\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	must(json.NewEncoder(os.Stdout).Encode(map[string]string{
		"server": server.URL, "token": "test-only", "username": "native-test",
		"pub": b64(phone.Public()), "deviceSeed": b64(phone.Seed()), "seed": b64(phone.Seed()),
		"host": b64(store.Identity().Public()), "workspace": root,
		"paged_thread":    paged.ID,
		"tool_thread":     toolHistory.ID,
		"artifact_thread": artifactID,
		"image":           base64.StdEncoding.EncodeToString(imageData.Bytes()), "pdf": base64.StdEncoding.EncodeToString(pdf.Bytes()),
	}))
	commands := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			commands <- scanner.Text()
		}
		close(commands)
	}()
	for {
		select {
		case command, ok := <-commands:
			if !ok || command == "quit" {
				return
			}
			if command == "release" {
				gate.release <- struct{}{}
			}
		case <-ctx.Done():
			return
		}
	}
}
