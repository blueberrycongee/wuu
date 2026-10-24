package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

// Cover populated legacy domain rows, including opaque payloads and references,
// rather than accepting a migration that only preserves empty schema tables.
func TestLegacyDomainMigrationParity(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			now := time.Now().UTC().Truncate(time.Microsecond)
			record := HistoryRecord{Role: "assistant", Content: "full history", DisplayContent: "display", Origin: "origin", OriginID: "origin-id", Cause: "cause", PresentationKind: "panel", RelatedSessionID: "related", ReadOnly: true, Phase: "final", ProviderItemID: "item", EngineTurnID: "native-turn", EngineTurnPending: true, ProviderItemModel: "native-model", ClientID: "client", Hidden: true, Steered: true, ReasoningContent: "reasoning", ReasoningBlocks: json.RawMessage(`["reason"]`), ProviderItems: json.RawMessage(`[{"opaque":"native"}]`), ContentParts: json.RawMessage(`[{"type":"text","text":"part"}]`), Images: json.RawMessage(`[{"url":"attachment://image"}]`), Files: json.RawMessage(`[{"path":"attachment://file","name":"a.pdf"}]`), ToolCalls: json.RawMessage(`[{"id":"call"}]`), DiscoveredTools: json.RawMessage(`["tool"]`), ToolCallID: "call", ToolResultKind: "structured", ToolResult: json.RawMessage(`{"opaque":"result"}`), FinishReason: "stop", StopReason: "complete", Truncated: true, Name: "name", At: now, InputTokens: 11, OutputTokens: 12, ContextTokens: 13, CacheCreationTokens: 14, CacheReadTokens: 15, RetryCount: 2, MaxRetries: 3, Provider: "provider", Model: "model"}
			metadata := Session{ID: "all-fields", CreatedAt: now, UpdatedAt: now, Title: "title", TitleRevision: 7, Summary: "summary", Entries: 2, CWD: "/workspace", Source: "project-session", Owner: "owner", Visibility: "visible", ParentID: "parent", ContextSource: "seed", CreationRequestID: "creation", Provider: "provider", Model: "model", Variant: "variant", Effort: "high", Speed: "fast", ModelOptions: map[string]string{"key": "value"}, PermissionMode: "safe", ApproveForMe: true, ProjectRole: "worker", Instructions: "instructions", ToolPolicyJSON: `{"allow":["tool"]}`, EngineID: "wuu", EngineRef: "native-ref", WorkspaceID: "workspace", ForkedFromID: "source", ForkedFromTurnID: "turn", ForkedFromItemID: "item", PinnedAt: &now, WorktreePath: "/worktree", WorktreeBaseHEAD: "head", WorktreeBaseRepo: "/base"}
			if _, err := CreateInitialized(dir, metadata, []HistoryRecord{record}); err != nil {
				t.Fatal(err)
			}
			folder, err := CreateFolder(dir, "folder")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := UpdateOrganization(dir, metadata.ID, &folder.ID); err != nil {
				t.Fatal(err)
			}
			control, err := ChangeControl(dir, metadata.ID, "manager", ControlActive, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := EnqueueInbox(dir, InboxMessage{ClientID: "queued", SessionID: metadata.ID, RelatedSessionID: "child", Cause: "result", Content: "input", Wake: true, CreatedAt: now, Controls: []Control{control}}); err != nil {
				t.Fatal(err)
			}
			seed, err := PutContextSeed(dir, ContextSeed{ID: "seed", Version: 1, Body: "summary", Source: HistorySnapshot{SessionID: metadata.ID, ThroughSeq: 1}, References: []ContextSeedReference{{ID: "ref", Label: "history", History: HistoryRef{Snapshot: HistorySnapshot{SessionID: metadata.ID, ThroughSeq: 1}, StartSeq: 1, EndSeq: 1}}}, Artifacts: []ContextSeedArtifact{{ID: "artifact", Label: "output"}}, Provenance: ContextSeedProvenance{Producer: "producer", SourceModel: "model", CreatedAt: now.Format(time.RFC3339Nano)}})
			if err != nil {
				t.Fatal(err)
			}
			launch, err := PutSessionLaunch(dir, SessionLaunchRecord{RequestID: "launch", Revision: 2, Kind: SessionLaunchKindHandoff, Status: SessionLaunchCommitted, SourceSession: metadata.ID, SourceCutoff: 1, SeedID: seed.ID, TargetSession: metadata.ID, InitialTurnID: "initial", Owner: "owner", Producer: "producer", Runtime: SessionRuntimeSelection{Provider: "provider", Model: "model", ModelOptions: map[string]string{"option": "yes"}}, Input: SessionLaunchInput{Prompt: "prompt", Intent: "handoff", ContextJSON: `{"context"
"bytes"
"os":true}`}, CreatedAt: now, UpdatedAt: now, CommittedAt: &now})
			if err != nil {
				t.Fatal(err)
			}
			binding := pluginprotocol.DriverBinding{Profile: "profile", ProviderID: "provider", ContractVersion: 1, DriverID: "driver", DriverVersion: "1"}
			if err := BindDriver(ctx, dir, metadata.ID, binding); err != nil {
				t.Fatal(err)
			}
			checkpoint := DriverCheckpointRecord{ContractVersion: 1, DriverID: "driver", DriverVersion: "1", State: json.RawMessage(`{"state":"retained"}`), UpdatedAt: now}
			if err := SaveDriverCheckpoint(dir, metadata.ID, checkpoint); err != nil {
				t.Fatal(err)
			}
			note := CompactionNote{SessionID: metadata.ID, ProviderKey: "provider", Markdown: "notes", CoveredMessages: 1, CoveredHash: "hash", UpdatedAt: now}
			if err := StoreCompactionNote(dir, note); err != nil {
				t.Fatal(err)
			}
			request := json.RawMessage(`{"request":"checkpoint"}`)
			if err := SaveRequestContextCheckpoint(dir, metadata.ID, request); err != nil {
				t.Fatal(err)
			}
			plugins := PluginGenerationSnapshot{Plugins: []PluginGenerationBinding{{ID: "plugin", Fingerprint: "fingerprint"}}}
			if err := WritePluginGenerationSnapshot(dir, metadata.ID, plugins); err != nil {
				t.Fatal(err)
			}
			if err := PutPluginTurnLifecycleOutbox(dir, "plugin", "event", json.RawMessage(`{"thread_id":"all-fields","turn_id":"turn","queue_id":"queue","status":"finished"}`)); err != nil {
				t.Fatal(err)
			}
			person := participant.Participant{ID: "person", Kind: participant.KindNamed, Name: "name", Role: "worker", Avatar: "legacy", Tagline: "tagline", Workspace: "/workspace", Model: "model", CreatedAt: now, UpdatedAt: now}
			if err := UpsertParticipant(dir, person); err != nil {
				t.Fatal(err)
			}
			run := ParticipantRun{ID: "run", ParticipantID: person.ID, AgentID: "agent", TaskID: "task", SessionID: metadata.ID, Summary: "result", Outcome: "done", CreatedAt: now, UpdatedAt: now}
			if err := UpsertParticipantRun(dir, run); err != nil {
				t.Fatal(err)
			}
			work, err := SaveProjectWork(dir, ProjectWork{ID: "work", ProjectID: metadata.ID, Revision: 3, Title: "work", Brief: "brief", Acceptance: "acceptance", Authority: "authority", Phase: "done", LeadID: "lead", ExecutorID: "executor", Workspace: "/workspace", Evidence: "evidence"}, 0, "work-operation")
			if err != nil {
				t.Fatal(err)
			}
			if err := ReconcileProjectWork(dir, work.ID, work.Version); err != nil {
				t.Fatal(err)
			}
			receipt := ModelInputReceiptRecord{OperationID: "large", ContractVersion: 1, DriverID: "driver", DriverVersion: "1", Payload: json.RawMessage("{\n \"large\": \"" + strings.Repeat("raw", 5000) + "\"\n}"), CreatedAt: now}
			if err := SaveModelInputReceipt(dir, metadata.ID, receipt); err != nil {
				t.Fatal(err)
			}
			// Compare the legacy reader's observable records, after its normal writes.
			expectedSession, _, err := Find(dir, metadata.ID)
			if err != nil {
				t.Fatal(err)
			}
			expectedHistory, err := LoadHistoryRecords(dir, metadata.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			expectedInbox, err := PendingInbox(dir, metadata.ID)
			if err != nil {
				t.Fatal(err)
			}
			expectedOutbox, err := ListPluginTurnLifecycleOutbox(dir)
			if err != nil {
				t.Fatal(err)
			}
			originalBytes, err := os.ReadFile(DBPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			documents, err := persistence.Open(ctx, t.TempDir(), backend)
			if err != nil {
				t.Fatal(err)
			}
			defer documents.Close()
			if _, err := MigrateLegacySQLite(ctx, dir, documents); err != nil {
				t.Fatal(err)
			}
			afterBytes, err := os.ReadFile(DBPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(originalBytes, afterBytes) {
				t.Fatal("migration changed legacy source bytes")
			}
			store := NewStore(ctx, documents)
			equal := func(label string, got, want any, err error) {
				t.Helper()
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s changed during migration (error %v)", label, err)
				}
			}
			sess, _, err := store.Find(metadata.ID)
			equal("session", sess, expectedSession, err)
			history, err := store.LoadHistoryRecords(metadata.ID, true)
			equal("history", history, expectedHistory, err)
			inbox, err := store.PendingInbox(metadata.ID)
			equal("inbox", inbox, expectedInbox, err)
			outbox, err := store.ListPluginTurnLifecycleOutbox()
			equal("outbox", outbox, expectedOutbox, err)
			gotSeed, _, err := store.LoadContextSeed(seed.ID)
			equal("seed", gotSeed, seed, err)
			gotLaunch, _, err := store.LoadSessionLaunch(launch.RequestID)
			equal("launch", gotLaunch, launch, err)
			gotBinding, _, err := store.LoadDriverBinding(metadata.ID)
			equal("binding", gotBinding, binding, err)
			gotCheckpoint, _, err := store.LoadDriverCheckpoint(metadata.ID)
			equal("checkpoint", gotCheckpoint, checkpoint, err)
			gotNote, _, err := store.LoadCompactionNote(metadata.ID, note.ProviderKey)
			equal("note", gotNote, note, err)
			gotRequest, err := store.LoadRequestContextCheckpoint(metadata.ID)
			equal("request", gotRequest, request, err)
			gotPlugins, _, err := store.ReadPluginGenerationSnapshot(metadata.ID)
			equal("plugins", gotPlugins, plugins, err)
			gotPerson, err := store.GetParticipant(person.ID)
			equal("participant", gotPerson, person, err)
			gotRuns, err := store.ListParticipantRuns(person.ID, 10)
			equal("participant runs", gotRuns, []ParticipantRun{run}, err)
			gotWork, err := store.ReadProjectWork(work.ID)
			equal("work", gotWork, work, err)
			gotReceipt, _, err := store.ProjectWorkOperation("work-operation")
			equal("work operation", gotReceipt, work, err)
			pending, err := store.PendingProjectWork(metadata.ID)
			if err != nil || len(pending) != 0 {
				t.Fatalf("reconciliation lost: %v", err)
			}
			receipts, err := store.LoadModelInputReceipts(metadata.ID)
			equal("compressed receipt", receipts, []ModelInputReceiptRecord{receipt}, err)
		})
	}
}
