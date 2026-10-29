package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalmemory"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const personalMemoryPrefix = "personal.records.v1."

var personalMemoryID = regexp.MustCompile(`^mem_[a-f0-9]{64}$`)
var personalMemorySourceRef = regexp.MustCompile(`^personal-message:([^:]+):([1-9][0-9]*)$`)

type memorySourceEvidence struct {
	Ref        string `json:"ref"`
	OccurredAt string `json:"occurred_at"`
	Content    string `json:"content,omitempty"`
}
type memoryActor struct {
	AgentID, MainSessionID string
	Main                   bool
	Authority              personalreview.Authority
}
type memoryWrite struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Expected *string `json:"expected"`
	Value    string  `json:"value"`
}
type memoryRecordSource struct {
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	OccurredAt string `json:"occurred_at"`
}
type memoryRecord struct {
	ID            string               `json:"id"`
	Revision      int64                `json:"revision"`
	Domain        string               `json:"product_domain"`
	Space         string               `json:"user_space_id"`
	Status        string               `json:"status"`
	Tier          string               `json:"tier"`
	Content       string               `json:"content"`
	Contributor   string               `json:"contributor_agent_id"`
	UpdatedBy     string               `json:"updated_by_agent_id"`
	Sources       []memoryRecordSource `json:"sources"`
	Blocked       []string             `json:"blocked_sources"`
	Superseded    []string             `json:"superseded_sources"`
	Contributions []struct {
		AgentID   string               `json:"agent_id"`
		SessionID string               `json:"executor_session_id"`
		Sources   []memoryRecordSource `json:"sources"`
	} `json:"contributions"`
	Conflict *struct {
		Sources []memoryRecordSource `json:"sources"`
	} `json:"conflict"`
}

// This route is session-scoped resource authority, not permission for a worker
// to read the source conversation. The host selects that exact persisted source.
func (p *personalActions) callMemory(ctx context.Context, operation string, raw json.RawMessage, sessionID string) (json.RawMessage, error) {
	if operation == "memory.operations" || operation == "memory.reconcile" {
		return p.recoverMemory(ctx, operation, raw)
	}
	if err := p.server.authorizePersonalOperation(ctx, operation, sessionID); err != nil {
		return nil, err
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	ctx = runtime.WithInvocationState(ctx, state)
	selected, services, err := p.selectedTaskProvider(state)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "memory.read":
		if err = decodePersonalAction(raw, &struct{}{}); err != nil {
			return nil, err
		}
		docs := p.memoryDocuments(state)
		records, err := personalmemory.NewJournal(docs, p.server.rt.UserBinding().UserSpaceID, selected.SubjectID).Records(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"records": records})
	case "memory.operation":
		var input struct {
			ID string `json:"id"`
		}
		if err = decodePersonalAction(raw, &input); err != nil {
			return nil, err
		}
		op, err := personalmemory.NewJournal(p.memoryDocuments(state), p.server.rt.UserBinding().UserSpaceID, selected.SubjectID).Operation(ctx, input.ID)
		if errors.Is(err, personalmemory.ErrNotFound) {
			return json.RawMessage("null"), nil
		}
		if err != nil {
			return nil, err
		}
		if op.Redacted {
			return nil, errors.New("forgotten_record: prior publication content was removed")
		}
		if op.SessionID != sessionID {
			return nil, errors.New("memory intent belongs to another session")
		}
		if op.RunID != "" {
			scope, ok := runtime.ExecutionAuthorizationScope(ctx)
			if !ok || scope.RunID != op.RunID || scope.TaskID != op.TaskID || scope.Revision != op.Revision {
				return nil, errors.New("memory intent belongs to another execution")
			}
		}
		if op.State == "committed" {
			records, err := personalmemory.NewJournal(p.memoryDocuments(state), p.server.rt.UserBinding().UserSpaceID, selected.SubjectID).Records(ctx)
			if err != nil {
				return nil, err
			}
			if records[op.Key] != op.Result {
				return nil, personalmemory.ErrRevisionConflict
			}
		}
		return json.Marshal(map[string]any{"write": memoryWrite{ID: op.ID, Key: op.Key, Expected: op.Expected, Value: op.Value}})
	case "memory.source":
		var input struct {
			Ref string `json:"ref"`
		}
		if err = decodePersonalAction(raw, &input); err != nil {
			return nil, err
		}
		doc, err := services.Settings.ReadState(ctx, selected.SubjectID, pluginsettings.ScopeUser)
		if err != nil {
			return nil, err
		}
		actor, err := p.memoryActor(ctx, state, selected, doc, sessionID)
		if err != nil {
			return nil, err
		}
		source, err := p.memorySource(ctx, state, selected, actor, input.Ref)
		if err != nil {
			return nil, err
		}
		return json.Marshal(source)
	case "memory.commit":
		var input memoryWrite
		if err = decodePersonalAction(raw, &input); err != nil {
			return nil, err
		}
		return p.commitMemory(ctx, state, selected, input, sessionID)
	default:
		return nil, errors.New("Personal memory operation unavailable")
	}
}
func (p *personalActions) memoryDocuments(state *runtime.State) *persistence.Client {
	return persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, callErr := state.Host.CallService(ctx, personalActionsConsumerID, persistence.ServiceName, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	})
}
func (p *personalActions) memoryActor(ctx context.Context, state *runtime.State, selected pluginpkg.Plugin, doc pluginsettings.StateDocument, sessionID string) (memoryActor, error) {
	var actor memoryActor
	metadata, found, err := state.Sessions.WithContext(ctx).Find(sessionID)
	if err != nil {
		return actor, err
	}
	if !found || metadata.Owner != "plugin:"+selected.ID || metadata.Visibility != "plugin" || metadata.ProductDomain != "personal" || metadata.UserSpaceID != p.server.rt.UserBinding().UserSpaceID || metadata.ArchivedAt != nil {
		return actor, errors.New("memory session is outside selected Personal identity")
	}
	prefix := "personal.v1." + personalHash([]byte(metadata.UserSpaceID))[:28] + "."
	var agent struct {
		AgentID string `json:"agent_id"`
		Space   string `json:"user_space_id"`
		Main    string `json:"main_session_id"`
		Status  string `json:"status"`
	}
	if json.Unmarshal([]byte(doc.Values[prefix+"agent."+metadata.AgentID]), &agent) != nil || agent.AgentID != metadata.AgentID || agent.Space != metadata.UserSpaceID || agent.Status != "active" || agent.Main == "" {
		return actor, errors.New("memory requires the active bound Personal agent")
	}
	actor.AgentID, actor.MainSessionID, actor.Main = agent.AgentID, agent.Main, agent.Main == sessionID
	authority, err := p.authority(ctx, state, selected, sessionID, "", doc)
	if err != nil {
		return actor, err
	}
	if !authority.Active || authority.BoundaryReason != "" {
		return actor, errors.New("memory task authority is no longer active")
	}
	if !actor.Main {
		scope, ok := runtime.ExecutionAuthorizationScope(ctx)
		if !ok || scope.Kind != "exact-run" || scope.SessionID != sessionID || scope.TaskID != authority.Scope.TaskID || scope.Revision != authority.Scope.RequirementsRevision {
			return actor, errors.New("memory publication requires the exact admitted worker run")
		}
	}
	actor.Authority = authority
	return actor, nil
}
func memoryCanonicalRef(id string, seq int) string {
	encoded := strings.ReplaceAll(url.QueryEscape(id), "+", "%20")
	for from, to := range map[string]string{"%21": "!", "%27": "'", "%28": "(", "%29": ")", "%2A": "*"} {
		encoded = strings.ReplaceAll(encoded, from, to)
	}
	return "personal-message:" + encoded + ":" + strconv.Itoa(seq)
}
func (p *personalActions) memorySource(ctx context.Context, state *runtime.State, selected pluginpkg.Plugin, actor memoryActor, ref string) (memorySourceEvidence, error) {
	result := memorySourceEvidence{Ref: ref}
	match := personalMemorySourceRef.FindStringSubmatch(ref)
	if match == nil {
		return result, errors.New("unsupported canonical memory source")
	}
	sourceID, err := url.QueryUnescape(match[1])
	if err != nil {
		return result, err
	}
	seq, err := strconv.Atoi(match[2])
	if err != nil || seq < 1 || memoryCanonicalRef(sourceID, seq) != ref {
		return result, errors.New("invalid canonical memory source")
	}
	if !actor.Main {
		wanted := fmt.Sprintf("%s:%d", sourceID, seq)
		if len(actor.Authority.UserMessages) != 1 || actor.Authority.UserMessages[0].ID != wanted {
			return result, errors.New("memory source is not the exact host-bound task source")
		}
	} else if sourceID != actor.MainSessionID {
		return result, errors.New("main memory source must be its bound conversation")
	}
	metadata, found, err := state.Sessions.WithContext(ctx).Find(sourceID)
	if err != nil {
		return result, err
	}
	if !found || metadata.Owner != "plugin:"+selected.ID || metadata.AgentID != actor.AgentID || metadata.ID != actor.MainSessionID || metadata.ProductDomain != "personal" || metadata.UserSpaceID != p.server.rt.UserBinding().UserSpaceID {
		return result, errors.New("memory source is outside its bound Personal conversation")
	}
	page, err := state.Sessions.WithContext(ctx).ReadActiveHistoryQuery(session.HistoryReadQuery{SessionID: sourceID, StartSeq: seq, EndSeq: seq, Limit: 1})
	if err != nil {
		return result, err
	}
	for _, record := range page.Records {
		if record.Seq != seq {
			continue
		}
		at := record.At
		if record.Role != "user" || record.Origin != "user" || record.Hidden || record.ReadOnly || record.Truncated || strings.TrimSpace(record.Content) == "" || len(record.Content) > 64000 || at.IsZero() || at.After(time.Now()) {
			return result, errors.New("memory source must be an exact active original user message with persisted timestamp")
		}
		result.OccurredAt = at.UTC().Format("2006-01-02T15:04:05.000Z")
		if actor.Main {
			result.Content = record.Content
		}
		return result, nil
	}
	return result, errors.New("memory source is absent or retracted")
}

// Each lease keeps the exact graph and selected providers alive until terminal
// publication AND durable guard cleanup. Lost transport never unlocks a writer.
type personalMemoryLease struct {
	state     *runtime.State
	ctx       context.Context
	scopes    []*pluginhost.ServiceScope
	settings  *pluginsettings.Client
	sessions  *session.Store
	grants    *executionauth.Client
	journal   *personalmemory.Journal
	selected  pluginpkg.Plugin
	operation personalmemory.Operation
}

func (l *personalMemoryLease) close() error {
	var err error
	for i := len(l.scopes) - 1; i >= 0; i-- {
		err = errors.Join(err, l.scopes[i].Close())
	}
	return errors.Join(err, l.state.Close())
}
func (p *personalActions) openMemoryLease(ctx context.Context, state *runtime.State, selected pluginpkg.Plugin) (*personalMemoryLease, error) {
	held := state.Retain()
	if held == nil {
		return nil, errors.New("memory provider generation closed")
	}
	// All asynchronous contexts borrow the lease-owned handle, never the
	// outer invocation's handle which closes as soon as the request returns.
	state = held
	l := &personalMemoryLease{state: held, selected: selected}
	fail := func(err error) (*personalMemoryLease, error) { return nil, errors.Join(err, l.close()) }
	open := func(consumer, service string) (*pluginhost.ServiceScope, error) {
		scope, err := state.Host.OpenServiceScope(ctx, consumer, service)
		if err == nil {
			l.scopes = append(l.scopes, scope)
			ctx = runtime.WithInvocationState(scope.Context, state)
		}
		return scope, err
	}
	if _, err := open(personalActionsConsumerID, "personal.agents"); err != nil {
		return fail(err)
	}
	settingsScope, err := open(runtime.StateServicesConsumer, pluginsettings.ServiceName)
	if err != nil {
		return fail(err)
	}
	l.settings = pluginsettings.NewClient(inspectionServiceCall(state.Host, settingsScope, runtime.StateServicesConsumer, pluginsettings.ServiceName))
	sessionsScope, err := open(runtime.SessionStateConsumer, session.ServiceName)
	if err != nil {
		return fail(err)
	}
	l.sessions = session.NewClient(ctx, inspectionServiceCall(state.Host, sessionsScope, runtime.SessionStateConsumer, session.ServiceName))
	_, err = open(personalActionsConsumerID, persistence.ServiceName)
	if err != nil {
		return fail(err)
	}
	l.journal = personalmemory.NewJournal(p.memoryDocuments(state), p.server.rt.UserBinding().UserSpaceID, selected.SubjectID)
	if _, available := state.Host.ServiceRegistry().ProviderPluginIDFor(personalActionsConsumerID, executionauth.ServiceName, 1); !available {
		l.ctx = ctx
		return l, nil
	}
	grantScope, err := open(personalActionsConsumerID, executionauth.ServiceName)
	if err != nil {
		return fail(err)
	}
	l.grants = executionauth.NewClient(func(callCtx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method == "release-commit" {
			return grantScope.CallCleanup(callCtx, executionauth.ServiceName, method, raw)
		}
		if method == "acquire-commit" {
			caller, ok := principal.FromContext(callCtx)
			if !ok {
				return nil, errors.New("memory commit principal missing")
			}
			if err := caller.Check(callCtx); err != nil {
				return nil, err
			}
			callCtx = pluginhost.WithServiceAdmission(callCtx, personalActionsConsumerID, executionauth.ServiceName, method, pluginhost.ServiceAdmission{Version: 1, Principal: caller.Identity(), Authority: "execution-grant", Action: "execution/commit/acquire", InputJSON: string(raw), InputDigest: executionauth.RawRequestDigest(raw)})
		}
		result, callErr := state.Host.CallService(callCtx, personalActionsConsumerID, executionauth.ServiceName, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	})
	l.ctx = ctx
	return l, nil
}
func (p *personalActions) memoryBindings(state *runtime.State) (map[string]string, error) {
	result := map[string]string{}
	for _, binding := range []struct{ consumer, service string }{{personalActionsConsumerID, "personal.agents"}, {personalActionsConsumerID, persistence.ServiceName}, {personalActionsConsumerID, executionauth.ServiceName}, {runtime.StateServicesConsumer, pluginsettings.ServiceName}, {runtime.SessionStateConsumer, session.ServiceName}} {
		provider, ok := state.Host.ServiceRegistry().ProviderPluginIDFor(binding.consumer, binding.service, 1)
		if !ok && binding.service == executionauth.ServiceName {
			continue
		}
		if !ok {
			return nil, errors.New("selected memory dependency is unavailable")
		}
		result[binding.service] = provider
	}
	return result, nil
}
func (p *personalActions) commitMemory(ctx context.Context, state *runtime.State, selected pluginpkg.Plugin, input memoryWrite, sessionID string) (json.RawMessage, error) {
	ctx, cancelWork := context.WithCancel(ctx)
	stopClosing := context.AfterFunc(p.ctx, cancelWork)
	defer func() { stopClosing(); cancelWork() }()

	if strings.TrimSpace(input.ID) == "" || len(input.ID) > 200 || !personalMemoryID.MatchString(input.Key) || len(input.Value) > pluginsettings.MaxStateValueBytes || input.Expected != nil && len(*input.Expected) > pluginsettings.MaxStateValueBytes {
		return nil, errors.New("invalid memory publication intent")
	}
	// The gate is only a live-host drain barrier. Durable selected-provider guards
	// below are the authorization boundary, including across processes and restart.
	select {
	case p.effectGate <- struct{}{}:
		defer func() { <-p.effectGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	if held, ok := p.memoryLeases.Load(input.ID); ok {
		previous := held.(*personalMemoryLease)
		if previous.operation.SessionID != sessionID || previous.operation.Key != input.Key || previous.operation.Value != input.Value || !reflect.DeepEqual(previous.operation.Expected, input.Expected) {
			return nil, personalmemory.ErrIntentConflict
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		resolved, err := previous.journal.Abort(cleanup, previous.operation.ID, previous.operation.Digest)
		if err != nil {
			return nil, err
		}
		previous.operation = resolved
		if err = previous.release(cleanup); err != nil {
			return nil, err
		}
		p.memoryLeases.Delete(input.ID)
		defer previous.close()
		if resolved.State == "committed" {
			return previous.committedResult(cleanup)
		}
		return nil, personalmemory.ErrAborted
	}
	l, err := p.openMemoryLease(ctx, state, selected)
	if err != nil {
		return nil, err
	}
	ctx = l.ctx
	retained := false
	defer func() {
		if !retained {
			_ = l.close()
		}
	}()
	bindings, err := p.memoryBindings(state)
	if err != nil {
		return nil, err
	}
	doc, err := l.settings.ReadState(ctx, selected.SubjectID, pluginsettings.ScopeUser)
	if err != nil {
		return nil, err
	}
	actor, err := p.memoryActor(ctx, state, selected, doc, sessionID)
	if err != nil {
		return nil, err
	}
	op := personalmemory.Operation{ID: input.ID, Key: input.Key, Expected: input.Expected, Value: input.Value, SessionID: sessionID, AgentID: actor.AgentID, TaskID: actor.Authority.Scope.TaskID, Revision: actor.Authority.Scope.RequirementsRevision, Bindings: bindings, TaskGuardID: "memory-task-" + personalHash([]byte(selected.SubjectID+"\x00"+input.ID)), SessionGuardID: "memory-session-" + personalHash([]byte(selected.SubjectID+"\x00"+input.ID))}
	if scope, delegated := runtime.ExecutionAuthorizationScope(ctx); delegated {
		if l.grants == nil {
			return nil, errors.New("selected execution commit authority unavailable")
		}
		proof := access.DelegationFromContext(ctx)
		if proof == nil || scope.SessionID != sessionID {
			return nil, errors.New("memory execution proof mismatch")
		}
		op.RunID = scope.RunID
		op.Authorization = &executionauth.CommitRequest{ID: "memory-" + personalHash([]byte(selected.SubjectID+"\x00"+input.ID)), Authorization: executionauth.ValidateRequest{Reference: executionauth.Reference{Provider: proof.Provider, ID: proof.Grant}, Scope: scope, Action: "personal/memory.commit", Resources: []access.Resource{{Kind: "user-space", ID: scope.UserSpaceID}, {Kind: "session", ID: sessionID}}}}
	}
	if len(actor.Authority.UserMessages) == 1 {
		op.SourceRef = actor.Authority.UserMessages[0].ID
	}
	op.Digest = executionauth.RequestDigest(op)
	if op.Authorization != nil {
		op.Authorization.OperationDigest = op.Digest
	}
	op, err = l.journal.Prepare(ctx, op)
	if err != nil {
		return nil, err
	}
	l.operation = op
	if op.State != "pending" {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err = l.release(cleanup); err != nil {
			retained = true
			p.retainMemoryLease(l)
			return nil, err
		}
		if op.Redacted {
			return nil, errors.New("forgotten_record: prior publication content was removed")
		}
		if op.State == "committed" {
			return l.committedResult(cleanup)
		}
		return nil, personalmemory.ErrAborted
	}
	taskRequest := pluginsettings.InspectionRequest{ReadRequest: pluginsettings.ReadRequest{Subject: selected.SubjectID, Scope: pluginsettings.ScopeUser}, ID: op.TaskGuardID}
	var outcome personalmemory.Operation
	runErr := func() error {
		guard, err := l.settings.AcquireInspection(ctx, taskRequest)
		if err != nil {
			return err
		}
		doc = guard.Document
		if _, err = l.sessions.AcquireInspection(ctx, op.SessionGuardID); err != nil {
			return err
		}
		current, err := p.memoryActor(ctx, state, selected, doc, sessionID)
		if err != nil {
			return err
		}
		if current.AgentID != op.AgentID || current.Authority.Scope.TaskID != op.TaskID || current.Authority.Scope.RequirementsRevision != op.Revision {
			return errors.New("memory task changed before publication")
		}
		if err = p.server.authorizePersonalOperation(ctx, "memory.commit", sessionID); err != nil {
			return err
		}
		var next memoryRecord
		if json.Unmarshal([]byte(input.Value), &next) != nil {
			return errors.New("invalid memory record")
		}
		evidenceSources := append([]memoryRecordSource(nil), next.Sources...)
		if len(next.Contributions) > 0 {
			evidenceSources = append(evidenceSources, next.Contributions[len(next.Contributions)-1].Sources...)
		}
		if next.Conflict != nil {
			evidenceSources = append(evidenceSources, next.Conflict.Sources...)
		}
		for _, source := range evidenceSources {
			if !current.Main && !strings.HasPrefix(source.Ref, "personal-message:") {
				return errors.New("worker memory requires the exact host-bound canonical user source")
			}
			if strings.HasPrefix(source.Ref, "personal-message:") {
				evidence, err := p.memorySource(ctx, state, selected, current, source.Ref)
				if err != nil {
					return err
				}
				if source.Kind != "message" || source.OccurredAt != evidence.OccurredAt {
					return errors.New("memory source timestamp or kind changed")
				}
			}
		}
		if op.Authorization != nil {
			if l.grants == nil {
				return errors.New("selected execution commit authority unavailable")
			}
			if _, err = l.grants.AcquireCommit(ctx, *op.Authorization); err != nil {
				return err
			}
		}
		legacy := map[string]string{}
		for key, value := range doc.Values {
			if strings.HasPrefix(key, personalMemoryPrefix) {
				legacy[strings.TrimPrefix(key, personalMemoryPrefix)] = value
			}
		}
		outcome, err = l.journal.Publish(ctx, op.ID, op.Digest, legacy, func(operation personalmemory.Operation, records map[string]string) error {
			return validateMemoryRecord(operation, records, current.Main, p.server.rt.UserBinding().UserSpaceID)
		})
		return err
	}()
	// A canceled/unknown write is resolved by a terminal CAS, not by treating its
	// missing response as failure. If storage is unavailable, keep all guards.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if runErr != nil || outcome.State != "committed" {
		outcome, err = l.journal.Abort(cleanup, op.ID, op.Digest)
		if err != nil {
			retained = true
			p.retainMemoryLease(l)
			return nil, errors.Join(runErr, err)
		}
	}
	l.operation = outcome
	if err = l.release(cleanup); err != nil {
		retained = true
		p.retainMemoryLease(l)
		return nil, errors.Join(runErr, err)
	}
	if outcome.State == "committed" {
		return json.Marshal(pluginhost.StorageCompareExchangeResult{Swapped: true, Value: &outcome.Result})
	}
	return nil, runErr
}
func (l *personalMemoryLease) committedResult(ctx context.Context) (json.RawMessage, error) {
	op := l.operation
	if op.Redacted {
		return nil, errors.New("forgotten_record: prior publication content was removed")
	}
	records, err := l.journal.Records(ctx)
	if err != nil {
		return nil, err
	}
	if records[op.Key] != op.Result {
		return nil, personalmemory.ErrRevisionConflict
	}
	return json.Marshal(pluginhost.StorageCompareExchangeResult{Swapped: true, Value: &op.Result})
}

func (l *personalMemoryLease) release(ctx context.Context) error {
	op := l.operation
	var err error
	if op.State != "committed" && op.State != "not-committed" {
		return errors.New("memory publication outcome is unresolved")
	}
	if op.Authorization != nil {
		if l.grants == nil {
			return errors.New("original execution commit authority unavailable")
		}
		request := executionauth.ReleaseCommitRequest{CommitRequest: *op.Authorization, Outcome: op.State}
		if op.State == "committed" {
			request.ReceiptDigest = op.ReceiptDigest
		}
		if _, releaseErr := l.grants.ReleaseCommit(ctx, request); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}
	// Release-before-acquire tombstones prevent delayed acquisitions after a
	// rejected or lost request. None of these cleanup calls admits another write.
	err = errors.Join(err, l.sessions.ReleaseInspection(ctx, op.SessionGuardID), l.settings.ReleaseInspection(ctx, pluginsettings.InspectionRequest{ReadRequest: pluginsettings.ReadRequest{Subject: l.selected.SubjectID, Scope: pluginsettings.ScopeUser}, ID: op.TaskGuardID}))
	return err
}

// A returned-but-uncertain publication is protected by durable provider guards,
// not by retaining a canceled process generation forever. Handoff waits for the
// physical host call stack to drain; no guard is released or outcome guessed.
func (p *personalActions) retainMemoryLease(l *personalMemoryLease) {
	p.memoryLeases.Store(l.operation.ID, l)
	context.AfterFunc(l.ctx, func() { p.handoffMemoryLease(l) })
}
func (p *personalActions) handoffMemoryLease(l *personalMemoryLease) {
	p.effectGate <- struct{}{}
	defer func() { <-p.effectGate }()
	if current, ok := p.memoryLeases.Load(l.operation.ID); ok && current == l {
		p.memoryLeases.Delete(l.operation.ID)
		_ = l.close()
	}
}

func (p *personalActions) closeMemoryLeases() error {
	// close cancels admission first. Drain active publication/reconciliation
	// before handing unresolved durable guards to restart recovery.
	p.effectGate <- struct{}{}
	defer func() { <-p.effectGate }()
	var result error
	p.memoryLeases.Range(func(key, value any) bool {
		l := value.(*personalMemoryLease)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		op, err := l.journal.Abort(ctx, l.operation.ID, l.operation.Digest)
		if err == nil {
			l.operation = op
			err = l.release(ctx)
		}
		// A provider outage must not retain a dead process generation forever.
		// No writer remains in this host; durable guards are NOT released on
		// uncertainty and the persisted intent remains discoverable on restart.
		p.memoryLeases.Delete(key)
		result = errors.Join(result, err, l.close())
		return true
	})
	return result
}
func memorySourceToken(source memoryRecordSource) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode([]string{source.Kind, source.Ref})
	return personalHash(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}
func validateMemoryRecord(op personalmemory.Operation, records map[string]string, main bool, space string) error {
	var next, previous memoryRecord
	if json.Unmarshal([]byte(op.Value), &next) != nil || next.ID != op.Key || next.Domain != "personal" || next.Space != space || next.Revision < 1 {
		return errors.New("memory record scope mismatch")
	}
	if op.Expected != nil {
		if json.Unmarshal([]byte(*op.Expected), &previous) != nil {
			return errors.New("invalid prior memory record")
		}
		if previous.Status == "forgotten" {
			return errors.New("forgotten_record")
		}
	}
	if next.Revision != previous.Revision+1 {
		return personalmemory.ErrRevisionConflict
	}
	if !main && (next.Status != "candidate" || next.Tier == "core" || previous.Revision > 0 && previous.Status != "candidate" || previous.Contributor != "" && previous.Contributor != op.AgentID) {
		return errors.New("worker may publish only its own non-core candidate")
	}
	if next.Status == "forgotten" {
		if !main {
			return errors.New("only main may forget memory")
		}
		return nil
	}
	if next.Status != "candidate" && next.Status != "active" && next.Status != "conflict" || len(next.Content) > 16384 || len(next.Sources) == 0 || len(next.Sources) > 16 || next.UpdatedBy != op.AgentID || len(next.Contributions) == 0 {
		return errors.New("invalid memory publication")
	}
	last := next.Contributions[len(next.Contributions)-1]
	if last.AgentID != op.AgentID || last.SessionID != op.SessionID {
		return errors.New("memory contribution actor mismatch")
	}
	sources := map[string]bool{}
	for _, source := range next.Sources {
		sources[memorySourceToken(source)] = true
	}
	for key, raw := range records {
		var record memoryRecord
		if json.Unmarshal([]byte(raw), &record) != nil {
			return errors.New("invalid stored memory record")
		}
		blocked := record.Superseded
		if record.Status == "forgotten" {
			blocked = record.Blocked
		}
		for _, token := range blocked {
			if sources[token] {
				return errors.New("forgotten_source or superseded_source")
			}
		}
		if key == op.Key || record.Status == "forgotten" {
			continue
		}
		used := append([]memoryRecordSource(nil), record.Sources...)
		for _, contribution := range record.Contributions {
			used = append(used, contribution.Sources...)
		}
		if record.Conflict != nil {
			used = append(used, record.Conflict.Sources...)
		}
		for _, source := range used {
			if sources[memorySourceToken(source)] {
				return errors.New("source_already_recorded")
			}
		}
	}
	return nil
}

// Recovery is an authenticated owner maintenance operation, never a model tool.
// Single-host provider contracts plus the host's effect drain serialize it with
// all writers. Shared providers need their own exclusive-writer recovery proof.
func (p *personalActions) recoverMemory(ctx context.Context, operation string, raw json.RawMessage) (json.RawMessage, error) {
	ctx, cancelWork := context.WithCancel(ctx)
	stopClosing := context.AfterFunc(p.ctx, cancelWork)
	defer func() { stopClosing(); cancelWork() }()

	ownerCtx := ctx
	if err := clientauthority.RequireOwner(ctx, p.server.rt.UserBinding()); err != nil {
		return nil, err
	}
	var input struct {
		ID             string `json:"id,omitempty"`
		WritersStopped bool   `json:"writers_stopped,omitempty"`
		Reason         string `json:"reason,omitempty"`
	}
	if err := decodePersonalAction(raw, &input); err != nil {
		return nil, err
	}
	if operation == "memory.reconcile" && (!input.WritersStopped || strings.TrimSpace(input.Reason) == "" || input.ID == "") {
		return nil, errors.New("memory reconciliation requires stopped writers and recorded disposition evidence")
	}
	select {
	case p.effectGate <- struct{}{}:
		defer func() { <-p.effectGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	ctx = runtime.WithInvocationState(ctx, state)
	selected, _, err := p.selectedTaskProvider(state)
	if err != nil {
		return nil, err
	}
	var l *personalMemoryLease
	retained := false
	if operation == "memory.reconcile" {
		if held, ok := p.memoryLeases.Load(input.ID); ok {
			l = held.(*personalMemoryLease)
			retained = true
		}
	}
	if l == nil {
		l, err = p.openMemoryLease(ctx, state, selected)
		if err != nil {
			return nil, err
		}
		defer l.close()
	} else {
		// Resolve the original captured graph, never a new same-named provider.
		state = l.state
		ctx = context.WithoutCancel(l.ctx)
	}
	if operation == "memory.operations" {
		ops, err := l.journal.Operations(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"operations": ops})
	}
	op, err := l.journal.Operation(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	bindings, err := p.memoryBindings(state)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(bindings, op.Bindings) {
		return nil, errors.New("memory recovery requires the original selected provider bindings")
	}
	topology, err := l.settings.InspectionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	if err = requireSingleHostInspection(topology); err != nil {
		return nil, err
	}
	topology, err = l.sessions.InspectionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	if err = requireSingleHostInspection(topology); err != nil {
		return nil, err
	}
	authority, err := p.memoryDocuments(state).Authority(ctx)
	if err != nil {
		return nil, err
	}
	if authority.StorageTopology != persistence.SingleHost || authority.ExecutionTopology != persistence.SingleHost {
		return nil, errors.New("memory recovery requires exclusive single-host document authority")
	}
	if err = clientauthority.RequireOwner(ownerCtx, p.server.rt.UserBinding()); err != nil {
		return nil, err
	}
	op, err = l.journal.Reconcile(ctx, op.ID, op.Digest, input.Reason)
	if err != nil {
		return nil, err
	}
	l.operation = op
	if err = l.release(ctx); err != nil {
		return nil, err
	}
	if retained {
		p.memoryLeases.Delete(op.ID)
		if err = l.close(); err != nil {
			return nil, err
		}
	}
	return json.Marshal(op)
}
