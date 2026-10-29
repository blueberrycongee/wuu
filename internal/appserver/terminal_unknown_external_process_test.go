package appserver

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const unknownExternalRoot = "unknown-external-root"
const unknownExternalParticipant = "unknown-external-participant"

type unknownExternalClient struct{ logPath string }

func (c unknownExternalClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming call")
}
func (c unknownExternalClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	f, err := os.OpenFile(c.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = f.WriteString("model request\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "candidate from real worker"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func unknownExternalRuntime(t *testing.T, fixture, endpoint, root, home, log string) *runtime.Session {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	p := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "unknown.http-documents", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"provider.ts"}, Env: map[string]string{"DOCUMENTS_ENDPOINT": endpoint}}}, Root: fixture, Official: true}
	plugins := []pluginpkg.Plugin{p}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{p.ID: true}, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: p.ID}}}}}}
	rt, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: home, WuuHome: filepath.Join(home, "wuu"), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	rt.WorkerClient = unknownExternalClient{logPath: log}
	return rt
}

// A real worker writes its draft in a child host, which then exits before its
// terminal observer. A new host recovers through the selected SDK process and
// separate HTTP documents authority; the model client is controlled locally.
func TestUnconfirmedTerminalRecoveryThroughExternalDocuments(t *testing.T) {
	fixture := os.Getenv("WUU_PROVIDER_AUTHORITY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/terminal-unknown-external-e2e.mjs")
	}
	if endpoint := os.Getenv("WUU_UNKNOWN_TERMINAL_CHILD_ENDPOINT"); endpoint != "" {
		root, home, log := os.Getenv("WUU_UNKNOWN_TERMINAL_ROOT"), os.Getenv("WUU_UNKNOWN_TERMINAL_HOME"), os.Getenv("WUU_UNKNOWN_TERMINAL_MODEL_LOG")
		rt := unknownExternalRuntime(t, fixture, endpoint, root, home, log)
		if _, err := rt.Sessions.CreateWithMetadata(unknownExternalRoot, root); err != nil {
			t.Fatal(err)
		}
		if err := rt.Sessions.UpsertParticipant(participant.Participant{ID: unknownExternalParticipant, Kind: participant.KindNamed, Name: "Unknown worker"}); err != nil {
			t.Fatal(err)
		}
		thread, err := rt.NewThreadRuntime(unknownExternalRoot)
		if err != nil {
			t.Fatal(err)
		}
		thread.AgentControl.SetWorkerTerminalTransitionHookForTest(func(id string) {
			digest := sha256.Sum256([]byte(id))
			path := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, unknownExternalRoot), "harness", "terminal-finalizations", hex.EncodeToString(digest[:])+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				os.Exit(31)
			}
			var record struct {
				Phase      string `json:"phase"`
				Generation string `json:"generation"`
				Status     string `json:"status"`
			}
			if json.Unmarshal(raw, &record) != nil || record.Phase != "draft" || record.Generation == "" || record.Status != "completed" {
				os.Exit(32)
			}
			if err := os.WriteFile(filepath.Join(home, "child-worker-id"), []byte(id), 0600); err != nil {
				os.Exit(33)
			}
			os.Exit(0)
		})
		_, err = thread.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "unknown_worker", ParticipantID: unknownExternalParticipant, Prompt: "finish"})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Second)
		t.Fatal("worker did not reach real draft barrier")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	workspace, home := t.TempDir(), t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "authority.json")
	logPath := filepath.Join(home, "model-calls")
	backend := exec.CommandContext(ctx, node, "backend.ts", stateFile)
	backend.Dir = fixture
	stdout, err := backend.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	backend.Stderr = &stderr
	if err := backend.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Process.Kill(); _ = backend.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("authority readiness: %v %s", scanner.Err(), stderr.String())
	}
	var ready struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil || ready.URL == "" {
		t.Fatalf("authority readiness: %s %v", scanner.Bytes(), err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, exe, "-test.run=^TestUnconfirmedTerminalRecoveryThroughExternalDocuments$", "-test.timeout=40s")
	child.Env = append(os.Environ(), "WUU_UNKNOWN_TERMINAL_CHILD_ENDPOINT="+ready.URL, "WUU_UNKNOWN_TERMINAL_ROOT="+workspace, "WUU_UNKNOWN_TERMINAL_HOME="+home, "WUU_UNKNOWN_TERMINAL_MODEL_LOG="+logPath)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("worker child: %v\n%s", err, out)
	}
	idBytes, err := os.ReadFile(filepath.Join(home, "child-worker-id"))
	if err != nil {
		t.Fatal(err)
	}
	workerID := string(idBytes)
	digest := sha256.Sum256([]byte(workerID))
	stateDir, err := statepath.WorkspaceDir(filepath.Join(home, "wuu"), workspace)
	if err != nil {
		t.Fatal(err)
	}
	artifact := statepath.SessionArtifactDir(stateDir, unknownExternalRoot)
	terminalPath := filepath.Join(artifact, "harness", "terminal-finalizations", hex.EncodeToString(digest[:])+".json")
	draft, err := os.ReadFile(terminalPath)
	if err != nil {
		t.Fatal(err)
	}
	var rawRecord map[string]any
	if err := json.Unmarshal(draft, &rawRecord); err != nil || rawRecord["phase"] != "draft" {
		t.Fatalf("child did not leave draft: %s, %v", draft, err)
	}
	rt := unknownExternalRuntime(t, fixture, ready.URL, workspace, home, logPath)
	defer rt.Cleanup()
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	th, err := srv.ensureThreadLoaded(binding, unknownExternalRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	msgs := waitForMethod(t, out, NotificationAgentMailbox)
	mailbox := remarshal[AgentMailboxNotification](t, notificationByMethod(t, msgs, NotificationAgentMailbox)["params"])
	if mailbox.Message.AgentID != workerID || mailbox.Message.Status != "interrupted" || mailbox.Message.Result != "candidate from real worker" || !strings.Contains(mailbox.Message.Error, "not confirmed") || !strings.Contains(mailbox.Message.Error, "tool effects may already have occurred") {
		t.Fatalf("mailbox=%+v", mailbox.Message)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(terminalPath); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal acknowledgement did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	entries, err := os.ReadDir(filepath.Join(artifact, "harness", "terminal-unknown"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("unknown evidence=%v %v", entries, err)
	}
	evidence, err := os.ReadFile(filepath.Join(artifact, "harness", "terminal-unknown", entries[0].Name()))
	if err != nil || !bytes.Equal(evidence, draft) {
		t.Fatalf("candidate evidence changed: %v", err)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil || string(calls) != "model request\n" {
		t.Fatalf("unexpected model calls after recovery: %q %v", calls, err)
	}
	runs, err := rt.Sessions.ListParticipantRuns(unknownExternalParticipant, 0)
	if err != nil || len(runs) != 1 || runs[0].Outcome != "interrupted" || !strings.Contains(runs[0].Summary, "not confirmed") {
		t.Fatalf("participant outcome=%+v, %v", runs, err)
	}
	tasks, err := harness.NewDocumentStore(binding.Documents, unknownExternalRoot, filepath.Join(artifact, "harness")).ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range tasks {
		if task.ID == workerID {
			found = true
			if task.Status != harness.TaskStatusInterrupted || !strings.Contains(task.Error, "not confirmed") {
				t.Fatalf("external task=%+v", task)
			}
		}
	}
	if !found {
		t.Fatalf("external task %s not found in %+v", workerID, tasks)
	}
	rawAuthority, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rawAuthority, []byte(unknownExternalRoot)) || !bytes.Contains(rawAuthority, []byte(workerID)) {
		t.Fatalf("external authority missing worker records")
	}
	t.Logf("real worker %s recovered through SDK and HTTP authority; no automatic second model call", workerID)
}
