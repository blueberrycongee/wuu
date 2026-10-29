package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestPluginRegistryDownloadDoesNotBlockStdioAndCloses(t *testing.T) {
	for _, method := range []string{MethodPluginPackageInspect, MethodPluginPackageInstall} {
		t.Run(method, func(t *testing.T) {
			entered, canceled := make(chan struct{}), make(chan struct{})
			var enterOnce, cancelOnce sync.Once
			registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				enterOnce.Do(func() { close(entered) })
				<-r.Context().Done()
				cancelOnce.Do(func() { close(canceled) })
			}))
			t.Cleanup(registry.Close)
			defer registry.CloseClientConnections()
			oldTransport := http.DefaultTransport
			http.DefaultTransport = registry.Client().Transport
			t.Cleanup(func() { http.DefaultTransport = oldTransport })
			rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "independent turn completed"}})
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			callPluginPackageRPC(t, srv, "thread", MethodThreadStart, ThreadStartParams{})
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "thread")["result"]).Thread.ID
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close(); _ = reader.Close() })
			scannerDone := make(chan error, 1)
			go func() { scannerDone <- runStdioScanner(context.Background(), srv, reader) }()
			request, err := json.Marshal(map[string]any{"id": "download", "method": method, "params": PluginPackageInstallParams{Path: "npm:fixture-plugin@1.0.0", Registry: registry.URL}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintln(writer, string(request)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("registry request never began")
			}
			// Closing stdin after these requests proves the serial scanner reached
			// independent work while the registry still withholds its response.
			requests := fmt.Sprintf("{\"id\":\"turn\",\"method\":\"turn/start\",\"params\":{\"thread_id\":%q,\"prompt\":\"continue without the registry\"}}\n{\"id\":\"read\",\"method\":\"thread/list\",\"params\":{}}\n", threadID)
			writeDone := make(chan error, 1)
			go func() { _, err := io.Copy(writer, strings.NewReader(requests)); _ = writer.Close(); writeDone <- err }()
			select {
			case err := <-scannerDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("registry download blocked the stdio scanner")
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"turn", "read"} {
				if response := responseByID(t, parseOutput(t, out.String()), id); response["error"] != nil {
					t.Fatalf("%s failed: %+v", id, response)
				}
			}
			waitForTurnCompletedCountForThread(t, out, threadID, 1)
			closed := make(chan struct{})
			go func() { srv.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("Close did not cancel and drain the registry request")
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("registry did not observe canceled download")
			}
			if response := responseByID(t, parseOutput(t, out.String()), "download"); response["error"] == nil {
				t.Fatalf("canceled download reported success: %+v", response)
			}
		})
	}
}
