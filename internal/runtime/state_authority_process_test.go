package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Independent SDK process -> HTTP -> independently implemented durable backend.
// No native storage provider is selected. Failure cases include unknown replies,
// stale CAS, changed intent, restart/transfer, disabled/missing/offline provider,
// and accidental local storage of business payloads.
func TestProviderAuthorityHTTPProcess(t *testing.T) {
	fixture := os.Getenv("WUU_PROVIDER_AUTHORITY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/state-authority-acceptance/run.mjs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	startBackend := func(file string) (string, func()) {
		t.Helper()
		command := exec.CommandContext(ctx, node, "backend.ts", file)
		command.Dir = fixture
		stdout, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(stdout)
		if !lines.Scan() {
			t.Fatalf("backend readiness: %v %s", lines.Err(), stderr.String())
		}
		var ready struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(lines.Bytes(), &ready); err != nil || ready.URL == "" {
			t.Fatalf("backend readiness: %s %v", lines.Bytes(), err)
		}
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		}
		t.Cleanup(stop)
		return ready.URL, stop
	}
	backendDir := t.TempDir()
	endpoint, stopBackend := startBackend(filepath.Join(backendDir, "remote.json"))
	home := t.TempDir()
	makeHost := func(endpoint, home string, nativeDomain ...bool) (*pluginhost.Host, *persistence.Client, error) {
		provider := testRuntimePlugin("independent.http-documents")
		provider.Root = fixture
		provider.Runtime.Command = node
		provider.Runtime.Args = []string{"provider.ts"}
		provider.Runtime.Env = map[string]string{"DOCUMENTS_ENDPOINT": endpoint}
		consumer := &authorityConsumerFixture{id: "independent.documents-user", state: pluginhost.StatePrepared}
		var client *persistence.Client
		factory := NativeProviderFactory{ID: consumer.ID(), Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			client = persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				result, err := input.Registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
				if err != nil {
					return nil, err
				}
				return result, nil
			})
			return consumer, nil
		}}
		modules := []string{provider.ID, consumer.ID()}
		if len(nativeDomain) != 0 && nativeDomain[0] {
			modules = append(modules, session.DefaultModule, sessionStateConsumer)
		}
		host, _, err := buildPluginHost([]pluginpkg.Plugin{provider}, t.TempDir(), "", home, t.TempDir(), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "", factory)
		if err != nil {
			return nil, nil, err
		}
		t.Cleanup(func() { _ = host.Close(context.Background()) })
		if err := host.Activate(ctx); err != nil {
			return nil, nil, err
		}
		return host, client, nil
	}
	host, client, err := makeHost(endpoint, home)
	if err != nil {
		t.Fatal(err)
	}
	if selected, ok := host.ServiceRegistry().ProviderPluginID(persistence.ServiceName, 1); !ok || selected != "independent.http-documents" {
		t.Fatalf("selected backend=%q %v", selected, ok)
	}
	tx := authorityWrite("unknown-response", 0, "remote-only-sensitive-payload")
	if _, err := client.Commit(ctx, tx); err == nil {
		t.Fatal("unknown response was hidden")
	}
	first, err := client.Commit(ctx, tx)
	if err != nil || first.Revision != 1 {
		t.Fatalf("exact retry=%+v %v", first, err)
	}
	changed := tx
	changed.Writes = append([]persistence.Write(nil), tx.Writes...)
	changed.Writes[0].Value = json.RawMessage(`"different"`)
	if _, err := client.Commit(ctx, changed); !errors.Is(err, persistence.ErrOperationConflict) {
		t.Fatalf("different-intent retry: %v", err)
	}
	stale := authorityWrite("stale", 0, "no")
	if _, err := client.Commit(ctx, stale); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	verifyNoLocalData := func(home string) {
		t.Helper()
		if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if err == nil && strings.Contains(string(raw), "remote-only-sensitive-payload") {
				t.Errorf("business data copied locally: %s", path)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	verifyNoLocalData(home)
	if _, err := os.Stat(filepath.Join(home, "state-authority", "head.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local intent journal created: %v", err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stopBackend()
	endpoint, stopBackend = startBackend(filepath.Join(backendDir, "remote.json"))
	host, client, err = makeHost(endpoint, home)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := client.Commit(ctx, tx); err != nil || receipt != first {
		t.Fatalf("restart receipt=%+v %v", receipt, err)
	}
	freshHome := t.TempDir()
	other, otherClient, err := makeHost(endpoint, freshHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherClient.Commit(ctx, authorityWrite("second-reader-process", 1, "next")); err != nil {
		t.Fatal(err)
	}
	view, err := client.Read(ctx, persistence.Query{Namespace: "business"})
	if err != nil || view.Revision != 2 {
		t.Fatalf("provider advances were fenced by local revision: %+v %v", view, err)
	}
	verifyNoLocalData(freshHome)
	if err := other.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Transfer both data and operation receipts while the source writers are
	// stopped. The backend retains its logical identity, not its old endpoint.
	snapshot, err := client.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := client.Authority(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stopBackend()
	target, targetStop := startBackend(filepath.Join(t.TempDir(), "target.json"))
	transfer, _ := json.Marshal(struct {
		StoreID  string               `json:"store_id"`
		Snapshot persistence.Snapshot `json:"snapshot"`
	}{authority.StoreID, snapshot})
	response, err := http.Post(target+"/admin/import", "application/json", bytes.NewReader(transfer))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("transfer=%s", body)
	}
	host, client, err = makeHost(target, home)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := client.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := persistence.SnapshotCheckpoint(snapshot)
	after, err := persistence.SnapshotCheckpoint(copied)
	if err != nil || before != after {
		t.Fatalf("verified migration=%+v %+v %v", before, after, err)
	}
	if receipt, err := client.Commit(ctx, tx); err != nil || receipt != first {
		t.Fatalf("migrated receipt=%+v %v", receipt, err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	host, client, err = makeHost(target, home, true)
	if err != nil {
		t.Fatal(err)
	}
	sessions := func(host *pluginhost.Host) *session.Store {
		return session.NewClient(ctx, func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := host.CallService(ctx, sessionStateConsumer, session.ServiceName, method, raw)
			if err != nil {
				return nil, err
			}
			return result, nil
		})
	}
	if _, err := sessions(host).Create("remote-native-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions(host).UpdateTitle("remote-native-session", "remote-only-sensitive-payload"); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	host, client, err = makeHost(target, home, true)
	if err != nil {
		t.Fatal(err)
	}
	restored, found, err := sessions(host).Find("remote-native-session")
	if err != nil || !found || restored.Title != "remote-only-sensitive-payload" {
		t.Fatalf("native domain remote reopen: %+v %v %v", restored, found, err)
	}
	verifyNoLocalData(home)
	// Generated handoffs retain their exact generation through the selected
	// out-of-process document service and local derived artifacts.
	reportDir := t.TempDir()
	store := harness.NewDocumentStore(client, "generated-report-scope", reportDir)
	firstReport := harness.Report{ID: "generated-first", TaskID: "worker", Kind: harness.ReportKindFinalText, Summary: "first generation", ReportPath: filepath.Join(reportDir, "first", "report.md"), SubmittedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	nextReport := harness.Report{ID: "generated-next", TaskID: "worker", Kind: harness.ReportKindFinalText, Summary: "next generation", ReportPath: filepath.Join(reportDir, "next", "report.md"), SubmittedAt: firstReport.SubmittedAt.Add(time.Second)}
	if _, err := store.SubmitGeneratedReport(firstReport); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(firstReport.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitGeneratedReport(nextReport); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitGeneratedReport(firstReport); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.ReportForTask("worker")
	if err != nil || !ok || latest.ID != nextReport.ID {
		t.Fatalf("latest generated=%+v ok=%v err=%v", latest, ok, err)
	}
	oldData, err := os.ReadFile(firstReport.ReportPath)
	if err != nil || !bytes.Equal(oldData, original) {
		t.Fatalf("prior generated artifact changed: %v", err)
	}
	nextData, err := os.ReadFile(nextReport.ReportPath)
	if err != nil || !strings.Contains(string(nextData), "next generation") {
		t.Fatalf("next generated artifact=%q %v", nextData, err)
	}
	targetStop()
	if _, err := client.Read(ctx, persistence.Query{Namespace: "business"}); err == nil {
		t.Fatal("offline backend fell back")
	}
	if _, ok := host.RetirePlugin(ctx, "independent.http-documents", errors.New("disabled")); !ok {
		t.Fatal("provider not retired")
	}
	if _, err := client.Read(ctx, persistence.Query{Namespace: "business"}); err == nil {
		t.Fatal("disabled backend remained usable")
	}
	verifyNoLocalData(home)
	if _, _, err := makeHost(target, home); err == nil {
		t.Fatal("unavailable selected provider admitted")
	}
	t.Log("public SDK HTTP authority: default removed; native session create/reopen; unknown reply exact retry; stale/intent conflicts; remote restart; fresh-home bootstrap; concurrent storage revision; verified stopped transfer; disabled/offline fail-closed; no local business journal")
}
