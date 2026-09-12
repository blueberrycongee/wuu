package channels

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func flexibleTestSession(t *testing.T, service *Service, agentID, roomID, sessionRef string) *AgentClient {
	t.Helper()
	ctx := context.Background()
	client, err := service.BindAgent(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: sessionRef, RoomID: roomID, Title: "Independent investigation", Objective: "Find evidence for one hypothesis", Model: "frontier-model", Provider: "byok", Effort: "high", RuntimeVersion: "collaboration-v1"}); err != nil {
		t.Fatal(err)
	}
	session, err := service.BindAgentSession(ctx, agentID, sessionRef)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestFlexibleSessionDiscoveryDoesNotGrantInboxOrControlAccess(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner, peer, outsider := createTestAgent(t, service, "Owner"), createTestAgent(t, service, "Peer"), createTestAgent(t, service, "Outsider")
	room := createTestRoom(t, service, owner, peer)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parallel-investigation")
	peerClient, _ := service.BindAgent(ctx, peer.Agent.ID)
	outsiderClient, _ := service.BindAgent(ctx, outsider.Agent.ID)
	sessions, err := peerClient.ListCollaborationSessions(ctx, CollaborationSessionListParams{RoomID: room.ID})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("room discovery = %#v, %v", sessions, err)
	}
	got, err := peerClient.GetCollaborationSession(ctx, session.SessionRef())
	if err != nil || got.Title == "" || got.Objective == "" || got.Model != "frontier-model" || got.Effort != "high" || got.RuntimeVersion != "collaboration-v1" {
		t.Fatalf("peer metadata = %#v, %v", got, err)
	}
	if _, err := peerClient.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionCancelled}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("peer mutation error = %v", err)
	}
	if _, err := service.ReceiveCollaboration(ctx, peer.Agent.ID, peer.Token, session.SessionRef(), 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("peer inbox error = %v", err)
	}
	if _, err := outsiderClient.ListCollaborationSessions(ctx, CollaborationSessionListParams{RoomID: room.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("outsider list error = %v", err)
	}
	if _, err := outsiderClient.GetCollaborationSession(ctx, session.SessionRef()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("outsider get error = %v", err)
	}
	ownOnly, err := peerClient.ListCollaborationSessions(ctx, CollaborationSessionListParams{})
	if err != nil || len(ownOnly) != 0 {
		t.Fatalf("unscoped discovery = %#v, %v", ownOnly, err)
	}
}

func TestSessionDeliverySurvivesRestartUntilAcknowledged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	sessionA := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "session-a")
	sessionB := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "session-b")
	first, err := sessionA.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, TargetSessionRef: sessionB.SessionRef(), Body: "Check this counterexample", RequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := sessionB.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].ID != first.ID || !messages[0].ConsumedAt.IsZero() {
		t.Fatalf("receive = %#v, %v", messages, err)
	}
	if err := sessionA.AcknowledgeCollaboration(ctx, []string{first.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("sibling ack = %v", err)
	}
	dispatches, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil || len(dispatches) != 1 {
		t.Fatalf("pending before persisted input = %#v, %v", dispatches, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	sessionB, err = service.BindAgentSession(ctx, owner.Agent.ID, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := sessionB.ReceiveCollaboration(ctx, 10)
	if err != nil || len(replay) != 1 || replay[0].ID != first.ID {
		t.Fatalf("restart replay = %#v, %v", replay, err)
	}
	for range 2 {
		if err := sessionB.AcknowledgeCollaboration(ctx, []string{first.ID}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := sessionB.ReceiveCollaboration(ctx, 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("after ack = %#v, %v", after, err)
	}
}

func TestSessionRequestsAreScopedToSourceAndRepliesFindOriginalSession(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	sender, recipient := createTestAgent(t, service, "Sender"), createTestAgent(t, service, "Recipient")
	room := createTestRoom(t, service, sender, recipient)
	senderA := flexibleTestSession(t, service, sender.Agent.ID, room.ID, "sender-a")
	senderB := flexibleTestSession(t, service, sender.Agent.ID, room.ID, "sender-b")
	target := flexibleTestSession(t, service, recipient.Agent.ID, room.ID, "target")
	params := CollaborationSendParams{RoomID: room.ID, TargetSessionRef: target.SessionRef(), Body: "Find a counterexample", RequestID: "same-local-id"}
	original, err := senderA.SendCollaboration(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := senderA.SendCollaboration(ctx, params)
	if err != nil || retry.ID != original.ID {
		t.Fatalf("idempotent retry = %#v, %v", retry, err)
	}
	distinct, err := senderB.SendCollaboration(ctx, params)
	if err != nil || distinct.ID == original.ID {
		t.Fatalf("sibling request = %#v, %v", distinct, err)
	}
	changed := params
	changed.ArtifactRefs = []string{"different-evidence"}
	if _, err := senderA.SendCollaboration(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same request changed evidence = %v", err)
	}
	forged := params
	forged.FromSessionRef = senderB.SessionRef()
	if _, err := senderA.SendCollaboration(ctx, forged); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("forged source session = %v", err)
	}
	if _, err := target.ReceiveCollaboration(ctx, 10); err != nil {
		t.Fatal(err)
	}
	reply, err := target.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, ReplyTo: original.ID, Body: "The boundary case fails", RequestID: "reply-1"})
	if err != nil || reply.TargetSessionRef != senderA.SessionRef() || reply.ToAgentID != sender.Agent.ID {
		t.Fatalf("reply route = %#v, %v", reply, err)
	}
	checkA, err := senderA.ReceiveCollaboration(ctx, 10)
	if err != nil || len(checkA) != 1 || checkA[0].ID != reply.ID {
		t.Fatalf("sender A reply = %#v, %v", checkA, err)
	}
	checkB, err := senderB.ReceiveCollaboration(ctx, 10)
	if err != nil || len(checkB) != 0 {
		t.Fatalf("sender B stole reply = %#v, %v", checkB, err)
	}
}

func TestSessionInputRetainsHumanAndAgentOrigins(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
	child := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "child")
	humanParams := CollaborationSessionSendParams{SessionRef: child.SessionRef(), Body: "User requirement", RequestID: "input-1"}
	human, err := service.EnqueueSessionInput(ctx, humanParams)
	if err != nil || human.FromType != MemberHuman || human.FromID != "human-1" || human.FromSessionRef != "" {
		t.Fatalf("human envelope = %#v, %v", human, err)
	}
	again, err := service.EnqueueSessionInput(ctx, humanParams)
	if err != nil || again.ID != human.ID {
		t.Fatalf("human retry = %#v, %v", again, err)
	}
	agent, err := service.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: child.SessionRef(), Body: "Delegated task", ActorID: owner.Agent.ID, SourceSessionRef: parent.SessionRef(), RequestID: "input-1"})
	if err != nil || agent.FromType != MemberAgent || agent.FromID != owner.Agent.ID || agent.FromSessionRef != parent.SessionRef() {
		t.Fatalf("agent envelope = %#v, %v", agent, err)
	}
}

func TestSessionAdmissionUsesSlotsAcrossConcurrentSessions(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.agentRunLimit, service.roomRunLimit, service.globalRunLimit = 2, 3, 4
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	refs := []string{"a", "b", "c", "d", "e", "f"}
	for _, ref := range refs {
		flexibleTestSession(t, service, owner.Agent.ID, room.ID, ref)
	}
	var wg sync.WaitGroup
	outcomes := make(chan CollaborationSessionBinding, len(refs))
	failures := make(chan error, len(refs))
	for _, ref := range refs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			binding, err := service.AdmitCollaborationSession(ctx, ref)
			if err != nil {
				failures <- err
				return
			}
			outcomes <- binding
		}(ref)
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var active []CollaborationSessionBinding
	for binding := range outcomes {
		if binding.State == CollaborationSessionStarting {
			active = append(active, binding)
		} else if binding.State != CollaborationSessionQueued {
			t.Fatalf("state = %s", binding.State)
		}
	}
	if len(active) != 2 {
		t.Fatalf("admitted %d sessions, want 2", len(active))
	}
	queued, err := service.ListQueuedCollaborationSessions(ctx)
	if err != nil || len(queued) != 4 {
		t.Fatalf("queue = %#v, %v", queued, err)
	}
	client, _ := service.BindAgent(ctx, owner.Agent.ID)
	if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: active[0].SessionRef, State: CollaborationSessionCompleted}); err != nil {
		t.Fatal(err)
	}
	next, err := service.AdmitCollaborationSession(ctx, queued[0].SessionRef)
	if err != nil || next.State != CollaborationSessionStarting {
		t.Fatalf("drain = %#v, %v", next, err)
	}
	if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: next.SessionRef, State: CollaborationSessionCancelled}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitCollaborationSession(ctx, next.SessionRef); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled auto restart = %v", err)
	}
}

type recordingSessionController struct {
	service *Service
	creates []CollaborationSessionCreateParams
}

func (controller *recordingSessionController) CreateSession(ctx context.Context, params CollaborationSessionCreateParams) (CollaborationSessionBinding, error) {
	controller.creates = append(controller.creates, params)
	client, err := controller.service.BindAgent(ctx, params.NamedAgentID)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	return client.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: params.SessionRef, RoomID: params.RoomID, Title: params.Title, Objective: params.Objective, ParentSessionRef: params.ParentSessionRef})
}
func (controller *recordingSessionController) SendSession(ctx context.Context, params CollaborationSessionSendParams) (CollaborationSessionBinding, error) {
	return controller.service.LookupCollaborationSession(ctx, params.SessionRef)
}
func (controller *recordingSessionController) StopSession(ctx context.Context, params CollaborationSessionControlParams) (CollaborationSessionBinding, error) {
	return controller.service.LookupCollaborationSession(ctx, params.SessionRef)
}
func (controller *recordingSessionController) ResumeSession(ctx context.Context, params CollaborationSessionControlParams) (CollaborationSessionBinding, error) {
	return controller.service.LookupCollaborationSession(ctx, params.SessionRef)
}

func TestSessionControllerReservationsAndDelegationAuthorization(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner, peer, stranger := createTestAgent(t, service, "Owner"), createTestAgent(t, service, "Peer"), createTestAgent(t, service, "Stranger")
	room := createTestRoom(t, service, owner, peer, stranger)
	parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "creator-session")
	controller := &recordingSessionController{service: service}
	service.SetSessionController(controller)
	params := CollaborationSessionCreateParams{NamedAgentID: peer.Agent.ID, RoomID: room.ID, Objective: "Explore independently", RequestID: "spawn-1"}
	first, err := parent.CreateSession(ctx, params)
	if err != nil || first.PrincipalID != peer.Agent.ID || first.ParentSessionRef != parent.SessionRef() || first.WorkID != "" {
		t.Fatalf("delegated session = %#v, %v", first, err)
	}
	retry, err := parent.CreateSession(ctx, params)
	if err != nil || retry.SessionRef != first.SessionRef {
		t.Fatalf("create retry = %#v, %v", retry, err)
	}
	changed := params
	changed.Objective = "Different objective"
	if _, err := parent.CreateSession(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("request reuse = %v", err)
	}
	if len(controller.creates) != 2 || controller.creates[0].Token != "" {
		t.Fatalf("controller creates = %#v", controller.creates)
	}
	if _, err := parent.StopSession(ctx, CollaborationSessionControlParams{SessionRef: first.SessionRef}); err != nil {
		t.Fatalf("creator stop = %v", err)
	}
	strangerClient, _ := service.BindAgent(ctx, stranger.Agent.ID)
	if _, err := strangerClient.StopSession(ctx, CollaborationSessionControlParams{SessionRef: first.SessionRef}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("peer stop = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.SetSessionController(&recordingSessionController{service: service})
	parent, err = service.BindAgentSession(ctx, owner.Agent.ID, "creator-session")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := parent.CreateSession(ctx, params)
	if err != nil || recovered.SessionRef != first.SessionRef {
		t.Fatalf("restart reservation = %#v, %v", recovered, err)
	}
}

func TestSessionStateMigrationRetainsExistingBinding(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "legacy-session")
	var schema string
	if err := service.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'collaboration_session_bindings'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(schema, "collaboration_session_bindings", "legacy_binding", 1)
	legacy = strings.ReplaceAll(legacy, "'idle', 'queued', 'starting', 'running', 'waiting', 'interrupted', 'missing', 'completed', 'cancelled', 'failed'", "'idle', 'running', 'interrupted', 'missing'")
	for _, statement := range []string{legacy, `INSERT INTO legacy_binding SELECT * FROM collaboration_session_bindings`, `DROP TABLE collaboration_session_bindings`, `ALTER TABLE legacy_binding RENAME TO collaboration_session_bindings`} {
		if _, err := service.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, _ := service.BindAgent(ctx, owner.Agent.ID)
	before, err := client.GetCollaborationSession(ctx, session.SessionRef())
	if err != nil || before.Objective == "" || before.RoomID != room.ID || before.PrincipalID != owner.Agent.ID {
		t.Fatalf("migrated = %#v, %v", before, err)
	}
	after, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionFailed, FailureReason: "Provider rejected the request"})
	if err != nil || after.State != CollaborationSessionFailed || after.FailureReason == "" {
		t.Fatalf("new terminal state = %#v, %v", after, err)
	}
}

func TestIndependentSessionsDoNotClaimIdentityConversationMessages(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner, sender := createTestAgent(t, service, "Owner"), createTestAgent(t, service, "Sender")
	room := createTestRoom(t, service, owner, sender)
	conversation := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "conversation")
	independent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "independent")
	if _, err := independent.BindCollaborationSession(ctx, CollaborationSessionBindParams{SessionRef: independent.SessionRef(), RoomID: room.ID, Purpose: CollaborationSessionWork}); err != nil {
		t.Fatal(err)
	}
	senderClient, _ := service.BindAgent(ctx, sender.Agent.ID)
	message, err := senderClient.SendCollaboration(ctx, CollaborationSendParams{RoomID: room.ID, ToAgentID: owner.Agent.ID, Body: "A new question for the identity"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := independent.ReceiveCollaboration(ctx, 10)
	if err != nil || len(private) != 0 {
		t.Fatalf("independent session claimed identity input: %#v, %v", private, err)
	}
	legacy, err := independent.Check(ctx)
	if err != nil || len(legacy.Collaboration) != 0 {
		t.Fatalf("independent Check claimed identity input: %#v, %v", legacy, err)
	}
	incoming, err := conversation.ReceiveCollaboration(ctx, 10)
	if err != nil || len(incoming) != 1 || incoming[0].ID != message.ID {
		t.Fatalf("conversation inbox = %#v, %v", incoming, err)
	}
	peers, err := conversation.RoomPeers(ctx, room.ID)
	if err != nil || len(peers) != 2 {
		t.Fatalf("peers = %#v, %v", peers, err)
	}
	for _, peer := range peers {
		if peer.MemoryIndex != "" {
			t.Fatal("peer discovery included private memory")
		}
	}
}

func TestCompletedSessionAcceptsFollowupWhileCancelledSessionStaysStopped(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "completed-session")
	if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionCompleted}); err != nil {
		t.Fatal(err)
	}
	input, err := service.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: session.SessionRef(), Body: "Follow up on the result"})
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitCollaborationSession(ctx, session.SessionRef())
	if err != nil || admitted.State != CollaborationSessionStarting {
		t.Fatalf("completed followup admission = %#v, %v", admitted, err)
	}
	received, err := session.ReceiveCollaboration(ctx, 10)
	if err != nil || len(received) != 1 || received[0].ID != input.ID {
		t.Fatalf("followup = %#v, %v", received, err)
	}
	if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionCancelled}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: session.SessionRef(), Body: "This must require resume"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled delivery = %v", err)
	}
}

func TestIndependentSessionsAndWorkShareAdmissionCapacity(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.agentRunLimit, service.roomRunLimit, service.globalRunLimit = 1, 1, 1
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	independent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "independent")
	if _, err := service.AdmitCollaborationSession(ctx, independent.SessionRef()); err != nil {
		t.Fatal(err)
	}
	coordinator, err := service.BindRuntime(ctx, room.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: room.CreatedBy, Body: "Implement the change"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := coordinator.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Implementation", Body: "Implement the change", SourceMessageID: source.Message.ID, OwnerID: owner.Agent.ID, VerificationRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := coordinator.StartWorkRun(ctx, WorkRunStartParams{WorkID: task.ID, Kind: WorkRunProducer, NamedAgentID: owner.Agent.ID, Profile: "implementation", RequestID: "mixed-capacity"})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != WorkRunQueued {
		t.Fatalf("work bypassed independent session capacity: %+v", run)
	}
	if _, err := independent.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: independent.SessionRef(), State: CollaborationSessionCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := service.AdmitQueuedWorkRuns(ctx); err != nil {
		t.Fatal(err)
	}
	work, err := coordinator.GetWork(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if findRunState(work.Runs, run.ID) != WorkRunRunning {
		t.Fatalf("released session did not admit queued work: %+v", work.Runs)
	}
	another := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "another")
	admitted, err := service.AdmitCollaborationSession(ctx, another.SessionRef())
	if err != nil {
		t.Fatal(err)
	}
	if admitted.State != CollaborationSessionQueued {
		t.Fatalf("independent session bypassed active Work capacity: %+v", admitted)
	}
}
