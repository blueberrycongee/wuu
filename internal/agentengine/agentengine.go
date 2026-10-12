// Package agentengine defines the boundary between the Wuu App Server and an
// Agent Engine: a system that owns its own agent loop, model access,
// authentication, tools, native session state, and resume semantics.
//
// The built-in "wuu" engine is the native StreamRunner loop. Claude and Codex
// are integrated as separate engines behind the same Factory/Session seam;
// Wuu only unifies the upper product semantics (thread, turn, run, events,
// status, stop, resume, desktop presentation) and never reaches into an
// external engine's internals.
package agentengine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// EngineID identifies the agent engine a thread is bound to. A thread binds
// its engine at creation and never silently switches.
type EngineID string

const (
	// EngineWuu is the built-in engine: the native StreamRunner loop that
	// has always driven Wuu conversations. Threads created before engine
	// binding existed default to it.
	EngineWuu EngineID = "wuu"
)

// ErrUnknownEngine reports a thread bound to an engine this build cannot host.
var ErrUnknownEngine = errors.New("unknown agent engine")

// ErrNoActiveTurn reports an Interrupt call with no in-flight turn.
var ErrNoActiveTurn = errors.New("no active turn on this engine session")

// NormalizeEngineID treats an empty id as the built-in wuu engine, which is
// the legacy default for threads persisted before engine binding.
func NormalizeEngineID(id string) EngineID {
	if strings.TrimSpace(id) == "" {
		return EngineWuu
	}
	return EngineID(strings.TrimSpace(id))
}

// IsKnownEngine reports whether this build can host the engine.
func IsKnownEngine(id EngineID) bool {
	return id == EngineWuu
}

// KnownEngineIDs returns the engines this build can host, in stable order.
func KnownEngineIDs() []EngineID {
	return []EngineID{EngineWuu}
}

// CheckEngine returns an explicit error when the engine is not known to this
// build, so callers surface a clear message instead of silently running the
// native loop for a thread bound elsewhere.
func CheckEngine(id EngineID) error {
	if !IsKnownEngine(id) {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, id)
	}
	return nil
}

// Descriptor describes one engine for capability display and selection.
type Descriptor struct {
	ID           EngineID
	Version      string
	Capabilities []string
}

// OpenRequest carries what a Factory needs to start a new engine-bound thread.
// The fields are engine-agnostic; each engine interprets them in its own
// native terms (model, auth, session reference).
type OpenRequest struct {
	ThreadID  string
	SessionID string
	RootDir   string
}

// ResumeRequest is OpenRequest plus the persisted binding for resumption.
// The wuu engine resumes from Wuu's canonical history, so it carries no
// adapter state; external engines use their native session reference.
type ResumeRequest struct {
	OpenRequest
	AdapterVersion     string
	ProtocolVersion    string
	ExternalSessionRef string
}

// TurnInput is the normalized user input for one turn.
type TurnInput struct {
	// History is the Wuu canonical message history for the turn. The wuu
	// engine consumes it directly; external engines receive it as the
	// seed for their own native context.
	History []providers.ChatMessage
}

// EventSink receives engine events converted to Wuu's unified event shape.
// The wuu engine emits native StreamEvents; external engine adapters
// translate their native events into the same shape at this boundary.
type EventSink func(providers.StreamEvent)

// ApprovalKind identifies the external-engine action waiting for a user
// decision. The host owns presentation and timeout semantics; engines only
// translate between their native protocol and this stable request shape.
type ApprovalKind string

const (
	ApprovalCommandExecution ApprovalKind = "command_execution"
	ApprovalFileChange       ApprovalKind = "file_change"
	ApprovalPermissions      ApprovalKind = "permissions"
)

// ApprovalDecision is the host's answer to an external engine request.
type ApprovalDecision string

const (
	ApprovalAccept           ApprovalDecision = "accept"
	ApprovalAcceptForSession ApprovalDecision = "accept_for_session"
	ApprovalDecline          ApprovalDecision = "decline"
	ApprovalCancel           ApprovalDecision = "cancel"
)

// ApprovalRequest carries only user-facing, engine-neutral approval details.
// Native permission payloads remain opaque and are echoed back only when the
// user explicitly accepts them.
type ApprovalRequest struct {
	Kind        ApprovalKind
	EngineID    EngineID
	ThreadID    string
	TurnID      string
	ItemID      string
	Command     string
	CWD         string
	FilePath    string
	Reason      string
	Permissions any
}

// ApprovalHandler blocks until the host accepts, declines, cancels, expires,
// or the owning turn context ends. Missing handlers must fail closed.
type ApprovalHandler func(context.Context, ApprovalRequest) (ApprovalDecision, error)

// UserInputRequest carries an engine question without granting any permission.
// Blocking controls presentation; both modes return answers to the original call.
type UserInputRequest struct {
	EngineID  EngineID
	ThreadID  string
	TurnID    string
	ItemID    string
	Blocking  bool
	Questions []UserInputQuestion
}

type UserInputQuestion struct {
	ID          string
	Header      string
	Question    string
	AllowCustom bool
	Secret      bool
	Options     []UserInputOption
}

type UserInputOption struct {
	Label       string
	Description string
}

// UserInputHandler returns question-ID keyed answers. An empty map means no answer.
// The owning turn must cancel its context on completion, interruption or disconnect.
type UserInputHandler func(context.Context, UserInputRequest) (map[string][]string, error)

// TurnResult is the outcome of one engine turn.
type TurnResult struct {
	// Result is the native loop result of the built-in wuu engine. External
	// engines produce their own opaque results that adapters translate into
	// Wuu items and notifications.
	Result agent.LoopResult
}

// Session is one engine-bound conversation handle.
type Session interface {
	RunTurn(context.Context, TurnInput, EventSink) (TurnResult, error)
	Interrupt(context.Context, string) error
	Close(context.Context) error
}

// SteeringSession accepts human input in its currently running native turn.
// Implementations must bind the request to that turn and never start a new one.
// A successful result is the native turn ID that acknowledged the input.
// ErrSteerRejected proves the input was not accepted; other errors leave delivery
// uncertain and must never cause an automatic retry.
type SteeringSession interface {
	Steer(context.Context, providers.ChatMessage) (string, error)
}

var ErrSteerRejected = errors.New("native steering input was not accepted")
var ErrSteerUnsupported = errors.New("native engine does not support active-turn steering")

// Factory creates and resumes engine sessions.
type Factory interface {
	Descriptor(context.Context) (Descriptor, error)
	Open(context.Context, OpenRequest) (Session, error)
	Resume(context.Context, ResumeRequest) (Session, error)
}

// Registry maps engine ids to factories. The runtime registers the built-in
// wuu engine at startup; external engines register as they are added.
type Registry struct {
	mu        sync.RWMutex
	factories map[EngineID]Factory
}

// ThreadBinding carries the app-server-side state an engine needs to bind a
// session to an existing thread runtime (the live path with cached runtimes).
type ThreadBinding struct {
	ThreadID string
	RootDir  string
	Model    string
	Effort   string
	// Speed is empty to inherit, "standard" to disable acceleration, or "fast".
	Speed string
	// ModelOptions carries native option IDs and values; adapters preserve wire types.
	ModelOptions map[string]string
	// PermissionMode is the host's engine-neutral access selection. External
	// engines map it to their native sandbox or permission mode.
	PermissionMode string
	// Instructions are durable, host-selected instructions for this session.
	// Engines map them to their native instruction surface; they are not user
	// prompt text and must not be silently discarded.
	Instructions string
	// MCPServers are host-owned, thread-scoped tool servers attached only to
	// external engines. The built-in engine ignores them.
	MCPServers []MCPServer
	// ExternalRef is the engine's persisted native session reference (for
	// example a codex thread id); empty means the engine must create one.
	ExternalRef string
	// PersistRef stores a newly created native session reference back into
	// the thread's persisted binding.
	PersistRef func(ref string) error
	// PersistTurnRef records native admission before dispatch, then its turn ID
	// before consuming events. Pending stays true until a terminal outcome or
	// an explicit admission rejection is durably confirmed.
	PersistTurnRef func(turnID string, pending bool) error
	// RequestApproval lets an external engine reuse the host's interaction
	// broker and conversation card. Nil means interactive approval is not
	// available and the engine must decline.
	RequestApproval ApprovalHandler
	// RequestUserInput reuses the host question UI; nil reports unavailable.
	RequestUserInput UserInputHandler
}

// MCPServer is one host-provided Streamable HTTP MCP endpoint.
type MCPServer struct {
	Name string
	URL  string
}

// ThreadBoundFactory is an optional factory extension for engines that bind
// to an existing app-server thread runtime instead of being opened fresh.
// The built-in wuu engine is the prime example; external engines implement
// it to route turns for threads already bound to them.
type ThreadBoundFactory interface {
	SessionForThread(context.Context, ThreadBinding) (Session, error)
}

// FailedSession returns a session that fails every turn with err. It is used
// when an engine cannot produce a real session (missing binary, auth failure,
// protocol error) so the turn terminates with the specific error instead of a
// generic unavailable message.
func FailedSession(err error) Session {
	return &failedSession{err: err}
}

type failedSession struct {
	err error
}

func (f *failedSession) RunTurn(context.Context, TurnInput, EventSink) (TurnResult, error) {
	if f == nil || f.err == nil {
		return TurnResult{}, errors.New("engine session unavailable")
	}
	return TurnResult{}, f.err
}

func (f *failedSession) Interrupt(context.Context, string) error {
	return ErrNoActiveTurn
}

func (f *failedSession) Close(context.Context) error {
	return nil
}

// NewRegistry returns an empty engine registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[EngineID]Factory)}
}

// Register installs a factory under its own id. It replaces a previous
// factory with the same id.
func (r *Registry) Register(f Factory) error {
	if r == nil || f == nil {
		return errors.New("engine factory is required")
	}
	desc, err := f.Descriptor(context.Background())
	if err != nil {
		return fmt.Errorf("describe engine: %w", err)
	}
	id := EngineID(strings.TrimSpace(string(desc.ID)))
	if id == "" {
		return errors.New("engine descriptor id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[id] = f
	return nil
}

// Lookup returns the factory for an engine id, normalized for legacy threads.
func (r *Registry) Lookup(id EngineID) (Factory, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[NormalizeEngineID(string(id))]
	return f, ok
}

// Unregister removes a factory by id. Settings-driven enable/disable uses
// this to take an engine out of rotation; threads already bound to it keep
// their history readable but fail turns with an explicit error.
func (r *Registry) Unregister(id EngineID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.factories, NormalizeEngineID(string(id)))
}

// Descriptors lists all registered engines in stable order.
func (r *Registry) Descriptors() []Descriptor {
	if r == nil {
		return nil
	}
	// Factories may call back into the registry while describing themselves.
	// Snapshot their identities without holding the lock across that call.
	r.mu.RLock()
	factories := make(map[EngineID]Factory, len(r.factories))
	ids := make([]string, 0, len(r.factories))
	for id, factory := range r.factories {
		factories[id] = factory
		ids = append(ids, string(id))
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	out := make([]Descriptor, 0, len(ids))
	for _, id := range ids {
		if desc, err := factories[EngineID(id)].Descriptor(context.Background()); err == nil {
			out = append(out, desc)
		}
	}
	return out
}
