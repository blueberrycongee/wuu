package appserver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestCollaborationAdmissionDoesNotDependOnPluginGeneration(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	srv := &Server{rt: rt, refreshExtensionsForTest: func(config.Config) error {
		return errors.New("ordinary plugin environment is unavailable")
	}}
	mutation, acquired, err := session.TryAcquirePluginGenerationMutationLease(rt.WuuHome)
	if err != nil || !acquired {
		t.Fatalf("hold plugin mutation: %v, %v", acquired, err)
	}
	defer mutation.Release()
	srv.pluginGenerationMutation.Store(true)
	thread := &threadState{ID: "collaboration-during-plugin-change", NamedAgentID: "shared-identity", PersistHistory: true}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	acquired, err = srv.tryAcquireThreadExecutionLeaseLocked(thread)
	if err != nil || !acquired {
		t.Fatalf("independent collaboration admission blocked: %v, %v", acquired, err)
	}
	defer thread.releaseThreadExecutionLeaseLocked()
	if thread.executionLease == nil || thread.pluginExecutionLease != nil {
		t.Fatal("collaboration must own its session lease without owning a plugin generation")
	}
	contender, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		_ = contender.Release()
		t.Fatal("removing plugin coupling also removed durable session exclusivity")
	}
}

func TestCollaborationRepeatedWakeKeepsIsolatedPromptAfterHistoryRefresh(t *testing.T) {
	requests := make(chan providers.ChatRequest, 2)
	client := &fakeClient{response: providersResponse("completed"), onChat: func(_ int, request providers.ChatRequest) {
		requests <- request
	}}
	rt := newTestRuntime(t, client)
	rt.WuuHome = filepath.Join(t.TempDir(), "state")
	rt.StreamRunner.SystemPrompt = "ordinary plugin prompt before collaboration"
	attachNamedAgentTestToolkit(t, rt)
	srv := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(srv.Close)
	srv.channelService.SetWakeSink(nil)
	identity, err := srv.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Isolated researcher"})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := srv.ensureNamedAgentThreadLocked(identity.Agent)
	if err != nil {
		t.Fatal(err)
	}
	original := thread.execRuntime
	for turn := 0; turn < 2; turn++ {
		if turn == 1 {
			rt.StreamRunner.SystemPrompt = "ordinary plugin prompt after extension replacement"
			srv.pluginGenerationEpoch.Add(1)
		}
		if err := srv.startNamedAgentWakeLocked(identity.Agent, thread); err != nil {
			t.Fatal(err)
		}
		select {
		case request := <-requests:
			foundIdentity := false
			for _, message := range request.Messages {
				if strings.Contains(message.Content, "ordinary plugin prompt") {
					t.Fatalf("wake %d inherited ordinary plugin prompt: %s", turn+1, message.Content)
				}
				if message.Role == "system" && strings.Contains(message.Content, identity.Agent.MemoryDir) {
					foundIdentity = true
				}
			}
			if !foundIdentity {
				t.Fatalf("wake %d lost its collaboration identity prompt", turn+1)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("wake %d did not reach the provider", turn+1)
		}
		waitForThreadLeaseRelease(t, rt.SessionDir, thread.ID)
	}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	if thread.execRuntime != original || thread.execRuntime.ExecutionProfile != runtime.CollaborationRuntimeVersion {
		t.Fatal("ordinary plugin replacement rebuilt the collaboration runtime")
	}
}

func TestCollaborationGenericRestorePreservesIdentityAndSessionAddress(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), "state")
	attachNamedAgentTestToolkit(t, rt)
	srv := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(srv.Close)
	srv.channelService.SetWakeSink(nil)
	ctx := context.Background()
	identity, err := srv.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Persistent researcher"})
	if err != nil {
		t.Fatal(err)
	}
	room := createAppserverTestRoom(t, srv.channelService, identity.Agent)
	client, err := srv.channelService.BindAgent(ctx, identity.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	const sessionRef = "independent-research-session"
	if _, err := client.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{SessionRef: sessionRef, RoomID: room.ID, Purpose: channels.CollaborationSessionWork, State: channels.CollaborationSessionIdle}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ensureAgentRuntimeSessionThreadLocked(agentRuntimeFromNamed(identity.Agent), sessionRef); err != nil {
		t.Fatal(err)
	}
	loaded, err := srv.loadPersistedThreadState(sessionRef, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NamedAgentID != identity.Agent.ID || loaded.Source != namedAgentSessionSource+identity.Agent.ID {
		t.Fatalf("generic restore lost identity ownership: %+v", loaded)
	}
	if loaded.CollaborationSessionRef != sessionRef {
		t.Fatalf("generic restore session address = %q, want %q", loaded.CollaborationSessionRef, sessionRef)
	}
}

func TestCollaborationGenericRestoreCannotSelectExternalEngine(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), "state")
	attachNamedAgentTestToolkit(t, rt)
	srv := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(srv.Close)
	srv.channelService.SetWakeSink(nil)
	identity, err := srv.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Pinned foundation"})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := srv.ensureNamedAgentThreadLocked(identity.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.SetEngine(rt.SessionDir, thread.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	loaded, err := srv.loadPersistedThreadState(thread.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := srv.ensureThreadRuntime(loaded)
	if created != nil {
		t.Cleanup(func() { releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: created}) })
	}
	if err == nil && created.EngineID != agentengine.EngineWuu {
		t.Fatalf("restored collaboration escaped its fixed foundation through engine %q", created.EngineID)
	}
}

func TestPluginMutationDoesNotInterruptCollaborationSessions(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	thread := &threadState{ID: "active-collaboration", NamedAgentID: "shared-identity", PersistHistory: true}
	executing := &Server{rt: rt, threads: map[string]*threadState{thread.ID: thread}}
	thread.mu.Lock()
	acquired, err := executing.tryAcquireThreadExecutionLeaseLocked(thread)
	if acquired {
		thread.running = true
	}
	thread.mu.Unlock()
	if err != nil || !acquired {
		t.Fatalf("collaboration admission: %v, %v", acquired, err)
	}
	defer func() {
		thread.mu.Lock()
		thread.running = false
		thread.releaseThreadExecutionLeaseLocked()
		thread.mu.Unlock()
	}()
	for _, mutating := range []*Server{executing, {rt: rt, threads: map[string]*threadState{}}} {
		release, err := mutating.beginPluginGenerationMutation("change", pluginGenerationMutationActivation)
		if err != nil {
			t.Fatalf("ordinary plugin change was blocked by collaboration: %v", err)
		}
		release()
	}
	if !thread.running || thread.executionLease == nil {
		t.Fatal("ordinary plugin change interrupted collaboration execution")
	}
}
