package host

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/account"
)

type userHostSyncTransport struct {
	mu              sync.Mutex
	historyRequests int
}

func (tr *userHostSyncTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	body := `{"enabled":true,"generation":"fixture"}`
	if req.URL.Path != "/v1/account/history/settings" {
		tr.historyRequests++
		body = `{"enabled":true,"generation":"fixture","entries":[],"cursor":"0","more":false}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
func TestUserHostIsExcludedFromCodingConversationPublisher(t *testing.T) {
	tr := &userHostSyncTransport{}
	original := http.DefaultTransport
	http.DefaultTransport = tr
	defer func() { http.DefaultTransport = original }()
	store, err := LoadOrCreateStore(t.TempDir()+"/host.json", "synthetic isolated host")
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	h := &Host{store: store, appServer: func(ctx context.Context, in io.Reader, out io.Writer) error {
		scan := bufio.NewScanner(in)
		for scan.Scan() {
			var req struct {
				ID     string
				Method string
			}
			if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
				return err
			}
			methods = append(methods, req.Method)
			result := any(map[string]any{"threads": []any{}})
			if req.Method == "initialize" {
				result = map[string]any{"user_space_id": "local", "product_domain": "personal"}
			}
			if err := json.NewEncoder(out).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
				return err
			}
		}
		return scan.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = h.publishConversationPass(ctx, account.Credentials{Server: "https://synthetic.invalid", Token: "synthetic"})
	if err == nil || !strings.Contains(err.Error(), "user host") {
		t.Fatalf("scoped exporter did not stop explicitly: %v", err)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.historyRequests != 0 {
		t.Fatalf("Personal host accessed Coding account history %d times", tr.historyRequests)
	}
	if len(methods) != 1 || methods[0] != "initialize" {
		t.Fatalf("Personal host reached Coding RPCs: %v", methods)
	}
}
func TestConversationRPCUsesDeviceAwareEmbeddingWithoutRuntime(t *testing.T) {
	called := make(chan appserver.PushRegistrar, 1)
	h := &Host{appServerForDevice: func(ctx context.Context, in io.Reader, out io.Writer, registrar appserver.PushRegistrar) error {
		called <- registrar
		scan := bufio.NewScanner(in)
		if !scan.Scan() {
			return scan.Err()
		}
		var req struct{ ID string }
		_ = json.Unmarshal(scan.Bytes(), &req)
		return json.NewEncoder(out).Encode(map[string]any{"id": req.ID, "result": map[string]any{}})
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rpc, close := h.conversationRPC(ctx)
	defer close()
	err := rpc("initialize", "", nil, nil)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	select {
	case registrar := <-called:
		if registrar != nil {
			t.Fatal("publisher borrowed device registration authority")
		}
	default:
		t.Fatal("device-aware execution authority was ignored")
	}
}
