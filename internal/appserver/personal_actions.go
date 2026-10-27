package appserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// This adapter covers only the native local file operations below. It neither
// grants account access nor changes Coding policy, MCP, shell or plugin tools.
type personalActions struct {
	server         *Server
	state          *runtime.State
	store          *personalreview.Store
	engine         *personalreview.Engine
	mu             sync.Mutex
	kitsMu         sync.Mutex
	kits           map[string]*tools.Toolkit
	ctx            context.Context
	cancel         context.CancelFunc
	wake           chan struct{}
	done           chan struct{}
	drainMu        sync.Mutex
	delivered      map[string]bool
	waitMu         sync.Mutex
	waiters        map[string]chan struct{}
	admissionsMu   sync.Mutex
	admissions     map[personalreview.Key]personalActionAdmission
	admissionEpoch uint64
	effectGate     chan struct{}
}

// Admission provenance is live host state, never a serialized owner claim or a
// persisted click proof. Recovered work needs a new authenticated admission.
type personalActionAdmission struct {
	epoch         uint64
	principal     principal.Verified
	delegation    *access.Delegation
	authorization *runtime.ExecutionAuthorizationBinding
	sessionID     string
	state         *runtime.State
}

func newPersonalActions(s *Server) (*personalActions, error) {
	state, err := s.rt.RetainState()
	if err != nil {
		return nil, err
	}
	if state.Host == nil {
		_ = state.Close()
		return nil, errors.New("selected Personal review provider is unavailable")
	}
	documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := state.Host.CallService(ctx, personalActionsConsumerID, persistence.ServiceName, method, raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	stateRoot := s.rt.ProductDataRoot()
	if stateRoot == "" {
		stateRoot = filepath.Dir(s.rt.SessionDir)
	}
	if err := personalreview.CheckLegacyMigration(context.Background(), filepath.Join(stateRoot, "personal-review.sqlite"), documents); err != nil {
		_ = state.Close()
		if errors.Is(err, personalreview.ErrLegacyMigrationRequired) {
			return nil, fmt.Errorf("%w; stop all hosts, then inspect with `wuu state migrate-personal-review --user-space %q --composition-profile <same-profile> [--config <same-config>]`; apply only the inspected source digest with --apply --expect-source-digest", err, s.rt.UserBinding().UserSpaceID)
		}
		return nil, err
	}
	store, err := personalreview.NewDocumentStore(documents)
	if err != nil {
		_ = state.Close()
		return nil, err
	}
	p := &personalActions{server: s, state: state, store: store, kits: map[string]*tools.Toolkit{}, wake: make(chan struct{}, 1), done: make(chan struct{}), delivered: map[string]bool{}, waiters: map[string]chan struct{}{}, admissions: map[personalreview.Key]personalActionAdmission{}, effectGate: make(chan struct{}, 1)}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	operations := map[string]personalreview.Operation{}
	for _, name := range []string{"write_file", "edit_file"} {
		operations[name] = personalreview.Operation{Version: "personal-local-file.v1", Effect: personalreview.DataWrite, Describe: p.describe, Execute: p.execute}
	}
	p.engine, err = personalreview.New(personalreview.Options{Store: store, Operations: operations, Authenticate: p.authenticate, WithAuthority: p.withAuthority, WithExecutionAuthority: p.withExecutionAuthority, Review: p.review})
	if err == nil {
		err = p.engine.Recover(context.Background())
	}
	if err != nil {
		p.cancel()
		store.Close()
		_ = state.Close()
		return nil, err
	}
	return p, nil
}
func (p *personalActions) start() {
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-p.wake:
				_ = p.drain(p.ctx)
			case <-ticker.C:
				_ = p.drain(p.ctx)
			}
		}
	}()
	p.signal()
}
func (p *personalActions) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *personalActions) close() error {
	p.cancel()
	<-p.done
	p.admissionsMu.Lock()
	admissions := p.admissions
	p.admissions = nil
	p.admissionsMu.Unlock()
	var closeErr error
	for _, admission := range admissions {
		closeErr = errors.Join(closeErr, admission.state.Close())
	}
	return errors.Join(closeErr, p.store.Close(), p.state.Close())
}
func (p *personalActions) ownerID() string {
	return "personal-owner:" + p.server.rt.UserBinding().UserSpaceID
}
func (p *personalActions) authenticate(ctx context.Context) (personalreview.Identity, error) {
	b := p.server.rt.UserBinding()
	if err := clientauthority.RequireOwner(ctx, b); err != nil {
		return personalreview.Identity{}, err
	}
	return personalreview.Identity{UserID: p.ownerID(), SpaceID: b.UserSpaceID, Channel: personalreview.OwnerUI}, nil
}

var errPersonalTaskInactive = errors.New("selected Personal task provider is inactive")

func (p *personalActions) selectedTaskProvider(state *runtime.State) (pluginpkg.Plugin, *runtime.StateServices, error) {
	services, err := state.Services()
	if err != nil {
		return pluginpkg.Plugin{}, nil, err
	}
	host := state.Host
	if host == nil || host.ServiceRegistry() == nil || p.server.rt.SafeMode {
		return pluginpkg.Plugin{}, nil, errPersonalTaskInactive
	}
	id, found := host.ServiceRegistry().ProviderPluginIDFor(personalActionsConsumerID, "personal.agents", 1)
	if !found {
		return pluginpkg.Plugin{}, nil, errPersonalTaskInactive
	}
	active := false
	for _, status := range host.Statuses() {
		if status.ID == id && status.State == pluginhost.StateActive {
			active = true
			break
		}
	}
	if active {
		for _, item := range services.Snapshot.ActivePlugins {
			if item.ID == id && item.SubjectID != "" {
				return item, services, nil
			}
		}
	}
	return pluginpkg.Plugin{}, nil, errPersonalTaskInactive
}
func (p *personalActions) stateRoot() string {
	root := p.server.rt.ProductDataRoot()
	if root == "" {
		root = filepath.Dir(p.server.rt.SessionDir)
	}
	return root
}
func (p *personalActions) retainState(ctx context.Context) (*runtime.State, error) {
	if runtime.HasInvocationState(ctx) {
		return runtime.RetainProductInvocationState(ctx)
	}
	return p.server.rt.RetainState()
}
func (p *personalActions) withAuthority(ctx context.Context, scope personalreview.Scope, use func(personalreview.Authority) error) error {
	if err := p.server.authorizePersonalOperation(ctx, "review.execution.status", scope.SessionID); err != nil {
		return err
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return err
	}
	defer state.Close()
	selected, services, err := p.selectedTaskProvider(state)
	if errors.Is(err, errPersonalTaskInactive) {
		// Definitive lifecycle revocation invalidates unclaimed approvals. It
		// supplies no task data or positive authority and cannot dispatch effects.
		return use(personalreview.Authority{Domain: p.server.rt.UserBinding().Domain(), Scope: scope, BoundaryReason: err.Error()})
	}
	if err != nil {
		return err
	}
	// This is a selected-provider snapshot, not an execution fence. Dispatch
	// requires the distinct WithExecutionAuthority boundary in the review engine.
	doc, err := services.Settings.ReadState(ctx, selected.SubjectID, pluginsettings.ScopeUser)
	if err != nil {
		return err
	}
	authority, err := p.authority(ctx, state, selected, scope.SessionID, scope.TaskID, doc)
	if err != nil {
		return err
	}
	if _, _, err := p.selectedTaskProvider(state); err != nil {
		return err
	}
	if err := p.server.authorizePersonalOperation(ctx, "review.execution.status", scope.SessionID); err != nil {
		return err
	}
	return use(authority)
}

func personalHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func (p *personalActions) authority(ctx context.Context, state *runtime.State, selected pluginpkg.Plugin, sessionID, taskID string, doc pluginsettings.StateDocument) (personalreview.Authority, error) {
	owner := "plugin:" + selected.ID
	b := p.server.rt.UserBinding()
	a := personalreview.Authority{Domain: b.Domain(), OwnerID: p.ownerID(), Scope: personalreview.Scope{SpaceID: b.UserSpaceID, SessionID: sessionID}, Active: true}
	settings, err := p.store.GetSettings(ctx, b.UserSpaceID)
	if err != nil {
		return a, err
	}
	a.PolicyRevision = settings.Revision
	metadata, found, err := state.Sessions.Find(sessionID)
	if err != nil {
		return a, err
	}
	if !found || metadata.UserSpaceID != b.UserSpaceID || metadata.ProductDomain != "personal" || metadata.Owner != owner || metadata.Visibility != "plugin" || metadata.ArchivedAt != nil {
		a.Active = false
		a.BoundaryReason = "execution is not an active Personal session"
		return a, nil
	}
	sourceSession := sessionID
	sourceSeq := 0
	contextParts := []string{metadata.ID, metadata.CWD, metadata.EngineID, metadata.PermissionMode, selected.ID, selected.SubjectID, selected.Fingerprint}
	taskFound := false
	prefix := "personal.v1." + personalHash([]byte(b.UserSpaceID))[:28] + "."
	owningAgent := metadata.AgentID
	var routineID string
	var routineRevision int64
	for key, raw := range doc.Values {
		if !strings.HasPrefix(key, prefix+"task.") && !strings.HasPrefix(key, prefix+"agent.") {
			continue
		}
		var item struct {
			UserSpaceID          string `json:"user_space_id"`
			TaskID               string `json:"task_id"`
			AgentID              string `json:"agent_id"`
			MainSessionID        string `json:"main_session_id"`
			State                string `json:"state"`
			Status               string `json:"status"`
			Goal                 string `json:"goal"`
			RoutineID            string `json:"routine_id"`
			RoutineRevision      int64  `json:"routine_revision"`
			RequirementsRevision int64  `json:"requirements_revision"`
			Cancel               bool   `json:"cancel_requested"`
			Source               struct {
				SessionID  string `json:"session_id"`
				MessageSeq int    `json:"message_seq"`
				Binding    string `json:"message_binding"`
			} `json:"source"`
			Execution struct {
				SessionID            string `json:"session_id"`
				RequestID            string `json:"request_id"`
				RequirementsRevision int64  `json:"requirements_revision"`
			} `json:"execution"`
		}
		if json.Unmarshal([]byte(raw), &item) != nil || item.UserSpaceID != b.UserSpaceID {
			continue
		}
		if item.MainSessionID == sessionID && item.Status != "active" {
			a.Active = false
		}
		if item.TaskID == "" || item.Execution.SessionID != sessionID {
			continue
		}
		if taskFound {
			return a, errors.New("ambiguous Personal execution ownership")
		}
		taskFound = true
		a.Scope.TaskID = item.TaskID
		a.Scope.RequirementsRevision = item.RequirementsRevision
		a.Active = !item.Cancel && item.RequirementsRevision > 0 && item.Execution.RequirementsRevision == item.RequirementsRevision && item.State != "stopped" && item.State != "completed" && item.State != "failed"
		sourceSession = item.Source.SessionID
		owningAgent = item.AgentID
		routineID = item.RoutineID
		routineRevision = item.RoutineRevision
		// Historical task versions did not retain each changed human source. Those
		// remain manually reviewable, never eligible for inferred automatic consent.
		if item.Source.Binding == "host_turn" && item.RequirementsRevision == 1 {
			sourceSeq = item.Source.MessageSeq
		}
		contextParts = append(contextParts, item.TaskID, item.Goal, strconv.FormatInt(item.RequirementsRevision, 10), item.Execution.RequestID)
		if granted, delegated := runtime.ExecutionAuthorizationScope(ctx); delegated && (granted.TaskID != item.TaskID || granted.RunID != item.Execution.RequestID || granted.Revision != item.RequirementsRevision) {
			a.Active = false
			a.BoundaryReason = "task execution no longer matches admitted run"
		}
	}
	if !taskFound {
		if metadata.ParentID != "" {
			a.Active = false
			a.BoundaryReason = "background task authority is unavailable"
			return a, nil
		}
		prefix := "conversation:" + sessionID + ":"
		if strings.HasPrefix(taskID, prefix) {
			sourceSeq, _ = strconv.Atoi(strings.TrimPrefix(taskID, prefix))
		}
		a.Scope.TaskID = prefix + strconv.Itoa(sourceSeq)
		a.Scope.RequirementsRevision = 1
	}

	var agentState struct {
		AgentID     string `json:"agent_id"`
		UserSpaceID string `json:"user_space_id"`
		Status      string `json:"status"`
	}
	if raw, ok := doc.Values[prefix+"agent."+owningAgent]; ok {
		if json.Unmarshal([]byte(raw), &agentState) != nil || agentState.AgentID != owningAgent || agentState.UserSpaceID != b.UserSpaceID || agentState.Status != "active" {
			a.Active = false
		}
	} else if taskFound {
		a.Active = false
	}
	if routineID != "" {
		var routine struct {
			RoutineID           string `json:"routine_id"`
			AgentID             string `json:"agent_id"`
			ConfigRevision      int64  `json:"config_revision"`
			Status              string `json:"status"`
			Cron                string `json:"cron"`
			CancellationPending bool   `json:"cancellation_pending"`
			SourceBlocked       bool   `json:"source_blocked"`
			Source              *struct {
				TimeMax string `json:"time_max"`
			} `json:"source"`
		}
		raw, ok := doc.Values["personal.routines.v1."+personalHash([]byte(b.UserSpaceID))[:28]+".routine."+routineID]
		if !ok || json.Unmarshal([]byte(raw), &routine) != nil || routine.RoutineID != routineID || routine.AgentID != owningAgent || routine.ConfigRevision != routineRevision || routine.CancellationPending || routine.SourceBlocked || (routine.Status != "active" && !(routine.Status == "completed" && routine.Cron == "" && routine.Source == nil)) {
			a.Active = false
		}
		if routine.Source != nil {
			until, err := time.Parse(time.RFC3339, routine.Source.TimeMax)
			if err != nil || !time.Now().Before(until) {
				a.Active = false
			}
		}
		contextParts = append(contextParts, routineID, strconv.FormatInt(routineRevision, 10))
	}
	if sourceSeq > 0 {
		sourceMeta, ok, err := state.Sessions.Find(sourceSession)
		if err != nil {
			return a, err
		}
		if !ok || sourceMeta.UserSpaceID != b.UserSpaceID || sourceMeta.ProductDomain != "personal" || sourceMeta.Owner != owner || sourceMeta.ParentID != "" {
			a.BoundaryReason = "source is not the owning Personal conversation"
		} else {
			page, err := state.Sessions.WithContext(ctx).ReadHistoryPage(sourceSession, sourceSeq, 1)
			if err != nil {
				return a, err
			}
			if len(page.Records) == 1 {
				r := page.Records[0]
				if r.Seq == sourceSeq && r.Role == "user" && r.Origin == "user" && !r.Hidden && !r.ReadOnly && strings.TrimSpace(r.Content) != "" {
					a.UserMessages = []personalreview.HumanMessage{{ID: fmt.Sprintf("%s:%d", sourceSession, sourceSeq), Text: r.Content}}
				}
			}
		}
	}
	raw, _ := json.Marshal(struct {
		Parts  []string
		Source []personalreview.HumanMessage
	}{contextParts, a.UserMessages})
	a.Scope.ContextDigest = personalHash(raw)
	a.AllowIntentReview = settings.AutoReviewEnabled && len(a.UserMessages) > 0
	return a, nil
}
func (p *personalActions) scope(ctx context.Context, id string) (personalreview.Scope, error) {
	source := loopdriver.ExecutionContextFromContext(ctx).SourceMessage
	taskID := "conversation:" + id + ":0"
	if source != nil && source.SessionID == id {
		taskID = "conversation:" + id + ":" + strconv.Itoa(source.Seq)
	}
	var scope personalreview.Scope
	err := p.withAuthority(ctx, personalreview.Scope{SessionID: id, TaskID: taskID}, func(a personalreview.Authority) error {
		if !a.Active || a.BoundaryReason != "" {
			return errors.New("Personal action execution scope is unavailable")
		}
		scope = a.Scope
		return nil
	})
	return scope, err
}
func decodePersonalAction(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected action payload suffix")
	}
	return nil
}
func (p *personalActions) describe(raw json.RawMessage) (personalreview.Summary, error) {
	var a tools.FileAction
	if err := decodePersonalAction(raw, &a); err != nil {
		return personalreview.Summary{}, err
	}
	if a.Operation != "write_file" && a.Operation != "edit_file" || a.Target == "" || !filepath.IsAbs(a.Target) || len(a.Arguments) > 32*1024 {
		return personalreview.Summary{}, errors.New("unsupported Personal file action")
	}
	title := "Save Personal file"
	effect := "Creates the displayed file with the exact displayed content. No message is sent."
	if a.Exists {
		effect = "Changes this file only if its existing content still matches the displayed SHA-256. No message is sent."
	}
	if a.Operation == "edit_file" {
		title = "Edit Personal file"
	}
	return personalreview.Summary{Title: title, Account: "Personal local files", Target: a.Target, Consequence: effect}, nil
}
func (p *personalActions) safeFile(a tools.FileAction) error {
	root := p.server.rt.RootDir
	rel, err := filepath.Rel(root, a.Target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("Personal file action must stay inside its own files workspace")
	}
	// Product state and shared Coding settings/credentials are never actionable,
	// even in an unconfined session or with a misplaced user workspace.
	protectedRoots := []string{p.stateRoot(), p.server.rt.SessionDir}
	// The normal files root is deliberately nested under WuuHome. Only this
	// exact product-owned subtree is exempt; an arbitrary root under shared
	// application state must not become an approval escape hatch.
	if p.server.rt.WuuHome != "" && filepath.Clean(root) != filepath.Clean(p.server.rt.UserBinding().Root(p.server.rt.WuuHome)) {
		protectedRoots = append(protectedRoots, p.server.rt.WuuHome)
	}
	for _, protected := range protectedRoots {
		if protected == "" {
			continue
		}
		relative, err := filepath.Rel(protected, a.Target)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("Personal file actions cannot change application state or Coding settings")
		}
	}
	if reason := approvefor.HardDenyReason(approvefor.Request{PermissionMode: "standard", Tool: approvefor.Tool{Name: a.Operation}, Arguments: string(a.Arguments), CWD: a.CWD, ReferencedPaths: []string{a.Target}}); reason != "" {
		return errors.New(reason)
	}
	return nil
}
func (p *personalActions) gate(ctx context.Context, kit *tools.Toolkit, call providers.ToolCall) (toolresult.Result, error) {
	state, err := p.retainState(ctx)
	if err != nil {
		return toolresult.Result{}, err
	}
	defer state.Close()
	ctx = runtime.WithInvocationState(ctx, state)
	scope, err := p.scope(ctx, kit.SessionID())
	if err != nil {
		return toolresult.Result{}, err
	}
	identity, _ := json.Marshal(struct {
		Scope     personalreview.Scope
		Operation string
		Arguments string
	}{scope, call.Name, personalreview.ArgumentsDigest(json.RawMessage(call.Arguments))})
	key := personalreview.Key{SpaceID: scope.SpaceID, ID: "file-" + personalHash(identity)}
	var wake chan struct{}
	if !strings.HasPrefix(scope.TaskID, "conversation:") {
		p.waitMu.Lock()
		wake = p.waiters[key.ID]
		if wake == nil {
			wake = make(chan struct{})
			p.waiters[key.ID] = wake
		}
		p.waitMu.Unlock()
		defer func() {
			p.waitMu.Lock()
			if p.waiters[key.ID] == wake {
				delete(p.waiters, key.ID)
			}
			p.waitMu.Unlock()
		}()
	}
	r, err := p.engine.GetExecution(ctx, key)
	if errors.Is(err, personalreview.ErrNotFound) {
		a, err := kit.PrepareFileAction(ctx, call)
		if err != nil {
			return toolresult.Result{}, err
		}
		if err = p.safeFile(a); err != nil {
			return toolresult.Result{}, err
		}
		raw, _ := json.Marshal(a)
		r, err = p.engine.Propose(ctx, personalreview.Proposal{ID: key.ID, Scope: scope, Operation: call.Name, Arguments: raw})
		if err != nil {
			return toolresult.Result{}, err
		}
	} else if err != nil {
		return toolresult.Result{}, err
	}
	p.kitsMu.Lock()
	dispatchKit, cloneErr := kit.CloneForRoot(kit.RootDir())
	if cloneErr != nil {
		p.kitsMu.Unlock()
		return toolresult.Result{}, cloneErr
	}
	if p.kits[key.ID] == nil {
		p.kits[key.ID] = dispatchKit
	}
	p.kitsMu.Unlock()

	if err := p.rememberAdmission(ctx, key, scope.SessionID); err != nil {
		return toolresult.Result{}, err
	}
	if r.Status == personalreview.Ready || r.Status == personalreview.Approved {
		r, err = p.engine.Execute(ctx, key)
		if err != nil {
			return toolresult.Result{}, err
		}
	}
	if wake != nil && (r.Status == personalreview.AwaitingConfirmation || r.Status == personalreview.Blocked || r.Status == personalreview.Proposed) {
		_ = p.productAction(ctx, "actions.sync", r)
		timer := time.NewTimer(time.Until(r.Action.ExpiresAt))
		defer timer.Stop()
		select {
		case <-wake:
		case <-ctx.Done():
			if p.ctx.Err() == nil {
				_ = p.engine.CancelExecution(context.WithoutCancel(ctx), scope.SpaceID, scope.SessionID, scope)
			}
			return toolresult.Result{}, ctx.Err()
		case <-p.ctx.Done():
			return toolresult.Result{}, p.ctx.Err()
		case <-timer.C:
			_, _ = p.engine.Assess(ctx, key)
		}
		r, err = p.engine.GetExecution(ctx, key)
		if err != nil {
			return toolresult.Result{}, err
		}
		_ = p.productAction(ctx, "actions.sync", r)
	}
	if r.Status == personalreview.Succeeded {
		var result struct {
			ToolResult toolresult.Result `json:"tool_result"`
		}
		if err = json.Unmarshal(r.Result, &result); err != nil {
			return toolresult.Result{}, err
		}
		return result.ToolResult, nil
	}
	if r.Status == personalreview.Executing || r.Status == personalreview.Uncertain {
		return toolresult.Result{}, fmt.Errorf("personal_action_outcome_unknown: %s (%s). It may already have executed; inspect its receipt and reconcile without retry", r.Action.ID, r.Status)
	}
	return toolresult.Result{}, fmt.Errorf("personal_action_pending: %s (%s). This exact action has not executed. The owner action card is in the main conversation; do not retry or create another approval. Other work may continue", r.Action.ID, r.Status)
}
func (p *personalActions) toolkit(ctx context.Context, action personalreview.Action) (*tools.Toolkit, error) {
	id := action.ID
	p.kitsMu.Lock()
	kit := p.kits[id]
	p.kitsMu.Unlock()
	if kit != nil {
		return kit, nil
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	th, err := p.server.ensureThreadLoaded(state, action.Scope.SessionID)
	if err != nil {
		return nil, err
	}
	rt, err := p.server.ensureThreadRuntime(th)
	if err != nil {
		return nil, err
	}
	if rt.Toolkit == nil {
		return nil, errors.New("Personal native file tools unavailable")
	}
	rt.Toolkit.SetFileActionGate(p.gate)
	p.kitsMu.Lock()
	p.kits[id] = rt.Toolkit
	p.kitsMu.Unlock()
	return rt.Toolkit, nil
}
func (p *personalActions) execute(ctx context.Context, action personalreview.Action) (json.RawMessage, error) {
	var a tools.FileAction
	if err := decodePersonalAction(action.Arguments, &a); err != nil {
		return nil, err
	}
	if a.Operation != action.Operation {
		return nil, errors.New("file operation mismatch")
	}
	if err := p.safeFile(a); err != nil {
		return nil, err
	}
	// Review prepares the exact native runtime before entering the authority
	// critical section. Never create or refresh a runtime while catalog and
	// product-state locks cover the effect.
	p.kitsMu.Lock()
	kit := p.kits[action.ID]
	p.kitsMu.Unlock()
	if kit == nil {
		return nil, errors.New("reviewed native runtime is unavailable")
	}
	result, err := kit.ExecuteFileAction(ctx, a)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New("native file operation returned an error result")
	}
	return json.Marshal(map[string]any{"receipt": map[string]any{"operation": a.Operation, "target": a.Target, "status": "succeeded", "action_id": action.ID}, "tool_result": result})
}
func (p *personalActions) review(ctx context.Context, input personalreview.ReviewInput) (approvefor.Decision, error) {
	var a tools.FileAction
	if err := decodePersonalAction(input.Action.Arguments, &a); err != nil {
		return approvefor.Decision{Outcome: "deny", Reason: "Invalid frozen file action"}, nil
	}
	if err := p.safeFile(a); err != nil {
		return approvefor.Decision{Outcome: "deny", Reason: err.Error()}, nil
	}
	kit, err := p.toolkit(ctx, input.Action)
	if err != nil {
		return approvefor.Decision{Outcome: "unsure", Reason: "Native file runtime unavailable; action not executed"}, nil
	}
	if err = kit.ValidateFileAction(ctx, a); err != nil {
		return approvefor.Decision{Outcome: "deny", Reason: "Frozen file action cannot be validated; no effect was dispatched: " + err.Error()}, nil
	}
	if input.Confirmation != nil && input.Confirmation.Choice == personalreview.ApproveOnce && input.Confirmation.Digest == input.Action.Digest && input.Confirmation.OwnerID == input.Action.OwnerID {
		return approvefor.Decision{Outcome: "allow", Reason: "Authenticated owner confirmed this exact bounded file action"}, nil
	}
	settings, err := p.store.GetSettings(ctx, input.Action.Scope.SpaceID)
	if err != nil {
		return approvefor.Decision{}, err
	}
	if !settings.AutoReviewEnabled {
		return approvefor.Decision{Outcome: "unsure", Reason: "Review the file target and exact changes before approving once"}, nil
	}
	if len(input.UserMessages) == 0 {
		return approvefor.Decision{Outcome: "unsure", Reason: "Original authenticated user request is unavailable; confirm this exact action manually"}, nil
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return approvefor.Decision{Outcome: "unsure", Reason: "Personal state unavailable; manual review required"}, nil
	}
	defer state.Close()
	th, err := p.server.ensureThreadLoaded(state, input.Action.Scope.SessionID)
	if err != nil {
		return approvefor.Decision{Outcome: "unsure", Reason: "Personal model unavailable; manual review required"}, nil
	}
	rt, err := p.server.ensureThreadRuntime(th)
	if err != nil || rt.StreamRunner == nil {
		return approvefor.Decision{Outcome: "unsure", Reason: "Personal model unavailable; manual review required"}, nil
	}
	decision, err := (personalActionReviewer{runner: rt.StreamRunner}).Review(ctx, input)
	if err != nil || decision.Outcome == approvefor.OutcomeFailed || decision.Outcome == approvefor.OutcomeCancelled {
		return approvefor.Decision{Outcome: "unsure", Reason: "AutoReview unavailable; nothing executed. Review this exact action manually"}, nil
	}
	return decision, nil
}
func (p *personalActions) Call(ctx context.Context, operation string, raw json.RawMessage, verifiedSession string) (json.RawMessage, error) {
	if err := p.server.authorizePersonalOperation(ctx, operation, verifiedSession); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if operation == "review.execution.status" {
		if verifiedSession == "" {
			return nil, errors.New("host-resolved execution required")
		}
		records, err := p.engine.Records(ctx, p.server.rt.UserBinding().UserSpaceID)
		if err != nil {
			return nil, err
		}
		selected := []personalreview.Record{}
		for _, r := range records {
			if r.Action.Scope.SessionID == verifiedSession {
				selected = append(selected, r)
			}
		}
		return json.Marshal(map[string]any{"requests": selected})
	}
	if _, err := p.authenticate(ctx); err != nil {
		return nil, err
	}
	switch operation {
	case "review.list":
		if err := decodePersonalAction(raw, &struct{}{}); err != nil {
			return nil, err
		}
	case "review.settings":
		var input struct {
			Enabled          bool  `json:"enabled"`
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if err := decodePersonalAction(raw, &input); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if _, err := p.authenticate(ctx); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		_, err := p.store.SetSettings(ctx, p.server.rt.UserBinding().UserSpaceID, input.Enabled, input.ExpectedRevision, p.ownerID())
		p.mu.Unlock()
		if err != nil {
			return nil, err
		}
		p.signal()
	case "review.resolve", "review.revoke":
		var input struct {
			SpaceID          string                `json:"space_id"`
			ID               string                `json:"id"`
			Digest           string                `json:"digest"`
			ExpectedRevision int64                 `json:"expected_revision"`
			Choice           personalreview.Choice `json:"choice,omitempty"`
		}
		if err := decodePersonalAction(raw, &input); err != nil {
			return nil, err
		}
		if input.SpaceID != p.server.rt.UserBinding().UserSpaceID {
			return nil, personalreview.ErrUnauthorized
		}
		key := personalreview.Key{SpaceID: input.SpaceID, ID: input.ID}
		p.admissionsMu.Lock()
		admitted, live := p.admissions[key]
		p.admissionsMu.Unlock()
		if live {
			ctx = runtime.WithInvocationState(ctx, admitted.state)
		}
		var err error
		if operation == "review.resolve" {
			_, err = p.engine.Resolve(ctx, key, personalreview.Resolution{Digest: input.Digest, ExpectedRevision: input.ExpectedRevision, Choice: input.Choice})
		} else {
			if input.Choice != "" {
				return nil, errors.New("revoke does not accept a choice")
			}
			_, err = p.engine.Revoke(ctx, key, personalreview.ActionBinding{Digest: input.Digest, ExpectedRevision: input.ExpectedRevision})
		}
		if err != nil {
			return nil, err
		}

		record, err := p.engine.GetExecution(ctx, key)
		if err != nil {
			return nil, err
		}
		if err := p.rememberAdmission(ctx, key, record.Action.Scope.SessionID); err != nil {
			return nil, err
		}
		p.signal()
	default:
		return nil, errors.New("Personal review operation unavailable")
	}
	view, err := p.engine.OwnerSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := p.store.GetSettings(ctx, p.server.rt.UserBinding().UserSpaceID)
	if err != nil {
		return nil, err
	}
	view.Settings = &settings
	return json.Marshal(view)
}
func (p *personalActions) rememberAdmission(ctx context.Context, key personalreview.Key, sessionID string) error {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.Valid() {
		return errors.New("authenticated Personal principal is required")
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return err
	}
	p.admissionsMu.Lock()
	if p.admissions == nil {
		p.admissionsMu.Unlock()
		_ = state.Close()
		return errors.New("Personal action host is closed")
	}
	p.admissionEpoch++
	admission, exists := p.admissions[key]
	if exists {
		// Reconfirmation can renew caller authority, never rebind the pending
		// operation's implementation to a newly selected generation.
		admission.epoch = p.admissionEpoch
		admission.principal = caller
		admission.delegation = access.DelegationFromContext(ctx)
		admission.authorization = runtime.CaptureExecutionAuthorization(ctx)
		p.admissions[key] = admission
	} else {
		p.admissions[key] = personalActionAdmission{epoch: p.admissionEpoch, principal: caller, delegation: access.DelegationFromContext(ctx), authorization: runtime.CaptureExecutionAuthorization(ctx), sessionID: sessionID, state: state}
	}
	p.admissionsMu.Unlock()
	if exists {
		_ = state.Close()
	}
	return nil
}

// Releasing the execution admission does not erase its durable receipt. A later
// reader cannot revive the old scope; a new operation needs a fresh admission.
func (p *personalActions) releaseAdmission(key personalreview.Key, epoch uint64) error {
	p.admissionsMu.Lock()
	admission, found := p.admissions[key]
	if !found || admission.epoch != epoch {
		p.admissionsMu.Unlock()
		return nil
	}
	delete(p.admissions, key)
	p.admissionsMu.Unlock()
	p.kitsMu.Lock()
	delete(p.kits, key.ID)
	p.kitsMu.Unlock()
	return admission.state.Close()
}

func (p *personalActions) drain(ctx context.Context) error {
	p.drainMu.Lock()
	defer p.drainMu.Unlock()
	p.admissionsMu.Lock()
	admissions := make(map[personalreview.Key]personalActionAdmission, len(p.admissions))
	for key, admitted := range p.admissions {
		admissions[key] = admitted
	}
	p.admissionsMu.Unlock()
	for key, admitted := range admissions {
		actionCtx := runtime.WithInvocationState(principal.WithContext(ctx, admitted.principal), admitted.state)
		if admitted.delegation != nil {
			actionCtx = access.WithDelegation(actionCtx, *admitted.delegation)
		}
		actionCtx = runtime.WithExecutionAuthorization(actionCtx, admitted.authorization)
		if err := p.server.authorizePersonalOperation(actionCtx, "review.execution.status", admitted.sessionID); err != nil {
			if closeErr := p.releaseAdmission(key, admitted.epoch); closeErr != nil {
				return closeErr
			}
			continue
		}
		r, err := p.engine.GetExecution(actionCtx, key)
		if err != nil {
			return err
		}
		if r.Action.Scope.SessionID != admitted.sessionID {
			continue
		}

		if r.Status == personalreview.Approved || r.Status == personalreview.Ready {
			r, err = p.engine.Execute(actionCtx, r.Action.Key())
			if err != nil {
				return err
			}
		}
		switch r.Status {
		case personalreview.Succeeded, personalreview.Denied, personalreview.Revoked, personalreview.Stale, personalreview.Expired, personalreview.Uncertain:
		default:
			continue
		}
		p.waitMu.Lock()
		wake := p.waiters[r.Action.ID]
		if wake != nil {
			delete(p.waiters, r.Action.ID)
			close(wake)
		}
		p.waitMu.Unlock()
		if wake != nil {
			p.delivered[r.Action.ID] = true
		}
		if p.delivered[r.Action.ID] {
			if err := p.releaseAdmission(key, admitted.epoch); err != nil {
				return err
			}
			continue
		}
		// The product owns task admission and idempotent continuation. Its
		// adapter rereads this host receipt and rechecks task/routine/agent state;
		// stale or stopped workers must never be resumed by a generic send.
		err = p.productAction(actionCtx, "actions.resume", r)
		if err == nil {
			p.delivered[r.Action.ID] = true
			if err := p.releaseAdmission(key, admitted.epoch); err != nil {
				return err
			}
		}

	}
	return nil
}

func (p *personalActions) productAction(ctx context.Context, method string, r personalreview.Record) error {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.Valid() {
		return errors.New("authenticated Personal principal is required")
	}
	if err := p.server.AuthorizeResource(ctx, "personal/"+method,
		access.Resource{Kind: "user-space", ID: r.Action.Scope.SpaceID}, access.Resource{Kind: "session", ID: r.Action.Scope.SessionID}); err != nil {
		return err
	}
	state, err := p.retainState(ctx)
	if err != nil {
		return err
	}
	defer state.Close()
	host := state.Host
	if host == nil || host.ServiceRegistry() == nil {
		return errors.New("Personal task service is unavailable")
	}
	registry := host.ServiceRegistry()
	provider, ok := registry.ProviderPluginIDFor(personalActionsConsumerID, "personal.agents", 1)
	if !ok {
		return errPersonalTaskInactive
	}
	metadata, found, err := state.Sessions.Find(r.Action.Scope.SessionID)
	if err != nil {
		return err
	}
	if !found || metadata.Owner != "plugin:"+provider || metadata.UserSpaceID != r.Action.Scope.SpaceID {
		return errors.New("Personal action receipt is not owned by the selected task provider")
	}
	params, _ := json.Marshal(map[string]string{"session_id": r.Action.Scope.SessionID, "action_id": r.Action.ID})
	// Recovery never retargets a receipt to an unrelated replacement provider.
	// The selected provider rereads the bound receipt before any continuation.
	_, callErr := host.CallService(ctx, personalActionsConsumerID, "personal.agents", method, params)
	if callErr != nil {
		return callErr
	}
	return nil
}

const personalActionsConsumerID = "wuu.personal-actions-consumer"

type personalActionsConsumer struct{ active atomic.Bool }

func NewPersonalActionsConsumer() pluginhost.Client { return &personalActionsConsumer{} }
func (*personalActionsConsumer) ID() string         { return personalActionsConsumerID }
func (c *personalActionsConsumer) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if c.active.Load() {
		state = pluginhost.StateActive
	}
	return pluginhost.Status{ID: c.ID(), State: state}
}
func (*personalActionsConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*personalActionsConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: "personal.agents", MajorVersion: 1}, {Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (c *personalActionsConsumer) Activate(context.Context) error { c.active.Store(true); return nil }
func (c *personalActionsConsumer) Close(context.Context) error    { c.active.Store(false); return nil }

func PersonalActionsConsumerFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: personalActionsConsumerID, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return NewPersonalActionsConsumer(), nil
	}}
}
