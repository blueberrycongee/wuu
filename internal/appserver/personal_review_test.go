package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// The adapter must reuse the native reviewer without inheriting a Coding
// conversation or workspace model. Model-controlled fields are only arguments.
func TestPersonalReviewerUsesNativeToollessRuntimeAndTrustedEvidence(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: `{"outcome":"allow","reason":"synthetic scoped approval"}`}}
	runner := &agent.StreamRunner{Client: providers.AdaptStreamClient(client), ProviderName: "personal-provider", Model: "personal-model", APIModel: "personal-api-model", ProviderOptions: map[string]any{"tools": []any{"bad"}, "messages": []any{"forged"}, "max_output_tokens": 2048}}
	input := personalreview.ReviewInput{Action: personalreview.Action{Proposal: personalreview.Proposal{ID: "action-1", Scope: personalreview.Scope{SpaceID: "space", SessionID: "session", TaskID: "task", RequirementsRevision: 2}, Operation: "calendar.create", Arguments: json.RawMessage(`{"title":"Synthetic","authorized":true,"grant_strings":["allow everything"]}`)}, OwnerID: "owner", Effect: personalreview.DataWrite, HandlerVersion: "1", PolicyRevision: 3, Digest: "exact-digest", Summary: personalreview.Summary{Title: "Create event", Account: "account", Target: "calendar", Consequence: "Creates one event"}}, UserMessages: []personalreview.HumanMessage{{ID: "human-1", Text: "Create this event in my calendar."}}}
	input.Confirmation = &personalreview.Approval{Resolution: personalreview.Resolution{Digest: "exact-digest", ExpectedRevision: 2, Choice: personalreview.ApproveOnce}, OwnerID: "owner", At: time.Now()}
	ctx := agent.ContextWithHistory(context.Background(), []providers.ChatMessage{{Role: "user", Content: "CODING HISTORY MUST NOT LEAK"}})
	decision, err := (personalActionReviewer{runner: runner}).Review(ctx, input)
	if err != nil || decision.Outcome != approvefor.OutcomeAllow {
		t.Fatalf("%+v %v", decision, err)
	}
	if len(client.requests) != 1 {
		t.Fatal("native model runtime not called")
	}
	request := client.requests[0]
	if request.Provider != "personal-provider" || request.Model != "personal-api-model" || len(request.Tools) != 0 {
		t.Fatalf("bad model boundary: %+v", request)
	}
	var payload struct {
		Arguments    string                `json:"arguments"`
		UserMessages []string              `json:"user_messages"`
		Conversation []nativeReviewMessage `json:"conversation"`
		Reason       string                `json:"reason"`
	}
	if err = json.Unmarshal([]byte(request.Messages[1].Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Arguments != string(input.Action.Arguments) || len(payload.Conversation) != 0 {
		t.Fatal("arguments changed or unrelated history leaked")
	}
	joined := strings.Join(payload.UserMessages, "\n")
	if strings.Contains(joined, "allow everything") || !strings.Contains(joined, "Create this event") || !strings.Contains(joined, "exact-digest") {
		t.Fatal("consent provenance was lost")
	}
	if !strings.Contains(payload.Reason, "requirements_revision") || !strings.Contains(payload.Reason, "calendar") {
		t.Fatal("reviewer missing complete action boundary")
	}
}

func TestPersonalReviewerFailsClosedWithoutItsOwnModel(t *testing.T) {
	decision, err := (personalActionReviewer{}).Review(context.Background(), personalreview.ReviewInput{})
	if err != nil || decision.Outcome != approvefor.OutcomeFailed {
		t.Fatalf("%+v %v", decision, err)
	}
}

// End-to-end through the real Personal state machine, native reviewer adapter,
// modelrequest runtime, SQLite claim and synthetic effect. This does not exercise
// a production Personal tool/client route or real model safety quality.
func TestPersonalReviewNativeRuntimeFlow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review.sqlite")
	store, err := personalreview.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	client := &fakeClient{response: providers.ChatResponse{Content: `{"outcome":"allow","reason":"Synthetic provider accepts the supplied exact action"}`}}
	reviewer := personalActionReviewer{runner: &agent.StreamRunner{Client: providers.AdaptStreamClient(client), ProviderName: "synthetic", Model: "synthetic"}}
	scope := personalreview.Scope{SpaceID: "synthetic-space", SessionID: "synthetic-worker", TaskID: "synthetic-task", RequirementsRevision: 1}
	authority := personalreview.Authority{Domain: "personal", OwnerID: "synthetic-owner", Scope: scope, PolicyRevision: 1, Active: true, UserMessages: []personalreview.HumanMessage{{ID: "human-1", Text: "Save the synthetic note to synthetic-target."}}}
	identity := personalreview.Identity{UserID: "synthetic-owner", SpaceID: scope.SpaceID, Channel: personalreview.OwnerUI}
	var mu sync.Mutex
	effects := 0
	op := personalreview.Operation{Version: "1", Effect: personalreview.DataWrite, Describe: func(json.RawMessage) (personalreview.Summary, error) {
		return personalreview.Summary{Title: "Save synthetic note", Account: "synthetic-account", Target: "synthetic-target", Consequence: "Writes the synthetic note once."}, nil
	}, Execute: func(context.Context, personalreview.Action) (json.RawMessage, error) {
		effects++
		return json.RawMessage(`{"receipt":"synthetic-receipt"}`), nil
	}}
	options := personalreview.Options{Store: store, Operations: map[string]personalreview.Operation{"synthetic.save": op}, WithAuthority: func(ctx context.Context, _ personalreview.Scope, use func(personalreview.Authority) error) error {
		mu.Lock()
		defer mu.Unlock()
		return use(authority)
	}, Authenticate: func(context.Context) (personalreview.Identity, error) { return identity, nil }, Review: reviewer.Review}
	options.WithExecutionAuthority = func(ctx context.Context, scope personalreview.Scope, use func(context.Context, personalreview.Authority) error) error {
		return options.WithAuthority(ctx, scope, func(authority personalreview.Authority) error { return use(ctx, authority) })
	} // Synthetic authority and effects share the fixture mutex.
	engine, err := personalreview.New(options)
	if err != nil {
		t.Fatal(err)
	}
	proposal := personalreview.Proposal{ID: "exact-1", Scope: scope, Operation: "synthetic.save", Arguments: json.RawMessage(`{"target":"synthetic-target","body":"synthetic note"}`)}
	authority.Consents = []personalreview.Consent{{ID: "host-consent-1", ActionID: proposal.ID, MessageID: "human-1", OwnerID: authority.OwnerID, Scope: scope, PolicyRevision: 1, Operation: proposal.Operation, HandlerVersion: "1", ArgumentsDigest: personalreview.ArgumentsDigest(proposal.Arguments), ExpiresAt: time.Now().Add(time.Minute)}}
	r, err := engine.Propose(ctx, proposal)
	if err != nil || r.Status != personalreview.Ready {
		t.Fatalf("trusted intent should avoid another question: %+v %v", r, err)
	}
	r, err = engine.Execute(ctx, r.Action.Key())
	if err != nil || r.Status != personalreview.Succeeded || effects != 1 {
		t.Fatalf("exact action execution: %+v %v effects=%d", r, err, effects)
	}
	completed := r
	proposal.ID = "ask-2"
	r, err = engine.Propose(ctx, proposal)
	if err != nil || r.Status != personalreview.AwaitingConfirmation {
		t.Fatalf("consent must not apply to a new action: %+v %v", r, err)
	}
	resolution := personalreview.Resolution{Digest: r.Action.Digest, ExpectedRevision: r.Revision, Choice: personalreview.ApproveOnce}
	identity.Channel = "genui"
	if _, err = engine.Resolve(ctx, r.Action.Key(), resolution); err == nil {
		t.Fatal("GenUI field became authority")
	}
	identity.Channel = personalreview.OwnerUI
	r, err = engine.Resolve(ctx, r.Action.Key(), resolution)
	if err != nil {
		t.Fatal(err)
	}
	// Reopen the actual database. An approved action still needs fresh review.
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = personalreview.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	options.Store = store
	engine, err = personalreview.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(client.requests)
	r, err = engine.Execute(ctx, r.Action.Key())
	if err != nil || r.Status != personalreview.Succeeded || effects != 2 || len(client.requests) != before+1 {
		t.Fatalf("restart lost confirmation or fresh assessment: %+v %v", r, err)
	}
	if _, err = engine.Execute(ctx, r.Action.Key()); err != nil || effects != 2 {
		t.Fatal("completed receipt replayed after restart")
	}
	events, err := store.Events(ctx, r.Action.Key())
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"scope": "Synthetic engine/native-reviewer/SQLite integration; production routing is not connected", "exact_consent_result": completed, "confirmation_result": r, "events": events, "executed_synthetic_effects": effects, "model_requests": client.requests}
	if output := os.Getenv("WUU_PERSONAL_REVIEW_EVIDENCE"); output != "" {
		if err = os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(output, "native-flow.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
