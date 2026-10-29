package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
)

type Engine struct {
	store                  *Store
	operations             map[string]Operation
	clock                  func() time.Time
	withAuthority          func(context.Context, Scope, func(Authority) error) error
	withExecutionAuthority func(context.Context, Scope, func(context.Context, Authority) error) error
	authenticate           func(context.Context) (Identity, error)
	review                 func(context.Context, ReviewInput) (approvefor.Decision, error)
}

func New(options Options) (*Engine, error) {
	if options.Store == nil || options.WithAuthority == nil || options.Authenticate == nil || options.Review == nil {
		return nil, errors.New("Personal review requires store, host authority, identity and reviewer")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	operations := make(map[string]Operation, len(options.Operations))
	for name, operation := range options.Operations {
		if strings.TrimSpace(name) == "" || operation.Version == "" || operation.Describe == nil || operation.Execute == nil {
			return nil, fmt.Errorf("incomplete Personal operation %q", name)
		}
		operations[name] = operation
	}
	return &Engine{store: options.Store, operations: operations, clock: options.Clock, withAuthority: options.WithAuthority, withExecutionAuthority: options.WithExecutionAuthority, authenticate: options.Authenticate, review: options.Review}, nil
}

func (e *Engine) Propose(ctx context.Context, proposal Proposal) (Record, error) {
	if proposal.ID == "" || len(proposal.ID) > 128 || proposal.Operation == "" || proposal.Scope.SpaceID == "" || proposal.Scope.SessionID == "" || proposal.Scope.TaskID == "" || proposal.Scope.RequirementsRevision < 1 {
		return Record{}, errors.New("incomplete Personal action scope")
	}
	arguments, err := canonicalArguments(proposal.Arguments)
	if err != nil {
		return Record{}, err
	}
	proposal.Arguments = arguments
	key := Key{SpaceID: proposal.Scope.SpaceID, ID: proposal.ID}
	if old, err := e.store.get(ctx, key); err == nil {
		if digest(old.Action.Proposal) != digest(proposal) {
			return Record{}, ErrConflict
		}
		return old, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Record{}, err
	}
	var created Record
	err = e.withAuthority(ctx, proposal.Scope, func(authority Authority) error {
		action := Action{Proposal: proposal, OwnerID: authority.OwnerID, PolicyRevision: authority.PolicyRevision, CreatedAt: e.clock().UTC(), Effect: Unknown}
		action.ExpiresAt = action.CreatedAt.Add(10 * time.Minute)
		operation, known := e.operations[proposal.Operation]
		if known {
			action.HandlerVersion = operation.Version
			action.Effect = operation.Effect
			var err error
			action.Summary, err = operation.Describe(append(json.RawMessage(nil), arguments...))
			if err != nil {
				return err
			}
			if action.Summary.Title == "" || action.Summary.Target == "" || action.Summary.Consequence == "" {
				return errors.New("operation omitted its reviewable summary")
			}
		}
		action.Digest = digest(action)
		created = Record{Action: action, Status: Proposed, Outcome: "ask", Reason: "Awaiting action assessment"}
		if status, reason := e.check(created, authority); status != "" {
			created.Status = status
			created.Outcome = "deny"
			created.Reason = reason
		}
		var err error
		created, err = e.store.save(ctx, created, 0, "host", e.clock())
		return err
	})
	if errors.Is(err, ErrConflict) {
		old, getErr := e.store.get(ctx, key)
		if getErr == nil && digest(old.Action.Proposal) == digest(proposal) {
			return old, nil
		}
	}
	if err != nil {
		return Record{}, err
	}
	if created.Status != Proposed {
		return created, nil
	}
	return e.Assess(ctx, key)
}

// Assess is an explicit retry after review infrastructure failure or missing
// context. Denied, expired, revoked, stale and uncertain actions are terminal.
func (e *Engine) Assess(ctx context.Context, key Key) (Record, error) {
	r, err := e.store.get(ctx, key)
	if err != nil {
		return Record{}, err
	}
	if r.Status != Proposed && r.Status != Blocked && r.Status != AwaitingConfirmation {
		return r, nil
	}
	authority, stopped, err := e.inspect(ctx, r)
	if err != nil {
		return Record{}, err
	}
	if stopped.Status != "" {
		return stopped, nil
	}
	status, outcome, reason := Ready, "allow", "Registered read or internal bookkeeping within the current host boundary"
	consent := e.matchConsent(r.Action, authority, r.ConsentID)
	if needsConfirmation(r.Action.Effect) {
		decision, reviewErr := e.review(ctx, reviewInput(r, authority, consent))
		status, outcome, reason = reviewStatus(decision, reviewErr)
		if status == Ready && consent == nil && !validApproval(r) && !canReviewIntent(authority) {
			status = AwaitingConfirmation
			outcome = "ask"
			reason = "Owner confirmation required for this exact action"
		}
		if status == Ready && consent != nil {
			r.ConsentID = consent.ID
		}
	}
	var saved Record
	err = e.withAuthority(ctx, r.Action.Scope, func(current Authority) error {
		if s, why := e.check(r, current); s != "" {
			status, outcome, reason = s, "deny", why
		} else if status == Ready && needsConfirmation(r.Action.Effect) && consent == nil && !validApproval(r) && !canReviewIntent(current) {
			status, outcome, reason = AwaitingConfirmation, "ask", "Exact owner confirmation is required; automatic intent review is unavailable"
		}
		r.Status, r.Outcome, r.Reason = status, outcome, reason
		if status != Ready && status != Blocked {
			r.Approval = nil
		}
		var err error
		saved, err = e.store.save(ctx, r, r.Revision, "reviewer", e.clock())
		return err
	})
	if errors.Is(err, ErrConflict) {
		return e.store.get(ctx, key)
	}
	return saved, err
}

func (e *Engine) Resolve(ctx context.Context, key Key, resolution Resolution) (Record, error) {
	if resolution.Choice != ApproveOnce && resolution.Choice != Reject {
		return Record{}, errors.New("unsupported confirmation choice")
	}
	for range 8 {
		r, err := e.store.get(ctx, key)
		if err != nil {
			return Record{}, err
		}
		identity, err := e.owner(ctx, r)
		if err != nil {
			return Record{}, err
		}
		if resolution.Digest != r.Action.Digest {
			return Record{}, ErrConflict
		}
		// Exact repeats only return the current state. They never dispatch or
		// resurrect an action that was revoked, expired or already attempted.
		if r.Approval != nil && r.Approval.Resolution == resolution && r.Approval.OwnerID == identity.UserID {
			return r, nil
		}
		if r.Status != AwaitingConfirmation || r.Revision != resolution.ExpectedRevision {
			return Record{}, ErrConflict
		}
		var saved Record
		err = e.withAuthority(ctx, r.Action.Scope, func(current Authority) error {
			if _, err := e.owner(ctx, r); err != nil {
				return err
			}
			if status, reason := e.check(r, current); status != "" {
				r.Status, r.Outcome, r.Reason = status, "deny", reason
				if _, err := e.store.save(ctx, r, r.Revision, "host", e.clock()); err != nil {
					return err
				}
				return ErrConflict
			}
			r.Approval = &Approval{Resolution: resolution, OwnerID: identity.UserID, At: e.clock().UTC()}
			if resolution.Choice == Reject {
				r.Status, r.Outcome, r.Reason = Denied, "deny", "Owner rejected this action"
			} else {
				r.Status, r.Outcome, r.Reason = Approved, "ask", "Confirmed once; fresh assessment required before execution"
			}
			var err error
			saved, err = e.store.save(ctx, r, r.Revision, identity.UserID, e.clock())
			return err
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return saved, err
	}
	return Record{}, ErrConflict
}

// Execute owns dispatch. It never returns an allow token for a caller to replay
// with different arguments. The registered handler receives the frozen action.
func (e *Engine) Execute(ctx context.Context, key Key) (Record, error) {
	r, err := e.store.get(ctx, key)
	if err != nil {
		return Record{}, err
	}
	if r.Status != Ready && r.Status != Approved {
		return r, nil
	}
	authority, stopped, err := e.inspect(ctx, r)
	if err != nil {
		return Record{}, err
	}
	if stopped.Status != "" {
		return stopped, nil
	}
	if needsConfirmation(r.Action.Effect) {
		if !validApproval(r) && r.ConsentID == "" && !canReviewIntent(authority) {
			return Record{}, ErrUnauthorized
		}
		decision, reviewErr := e.review(ctx, reviewInput(r, authority, e.matchConsent(r.Action, authority, r.ConsentID)))
		status, outcome, reason := reviewStatus(decision, reviewErr)
		if status != Ready {
			r.Status, r.Outcome, r.Reason = status, outcome, reason
			// An unavailable reviewer blocks dispatch, not the owner's still
			// valid confirmation. Assess and Execute both review again on retry.
			if status != Blocked {
				r.Approval = nil
			}
			return e.store.save(ctx, r, r.Revision, "reviewer", e.clock())
		}
	}
	if e.withExecutionAuthority == nil {
		return Record{}, ErrExecutionAuthorityUnavailable
	}
	var claimed Record
	var result json.RawMessage
	var executionErr error
	entered := false
	err = e.withExecutionAuthority(ctx, r.Action.Scope, func(ctx context.Context, current Authority) error {
		if entered {
			return ErrExecutionAuthorityUnavailable
		}
		entered = true
		if err := ctx.Err(); err != nil {
			return err
		}
		if status, reason := e.check(r, current); status != "" {
			r.Status, r.Outcome, r.Reason = status, "deny", reason
			var err error
			claimed, err = e.store.save(ctx, r, r.Revision, "host", e.clock())
			return err
		}
		// Opt-in and complete original source evidence must still exist at the
		// dispatch boundary, even if a host forgot to advance its policy revision.
		if needsConfirmation(r.Action.Effect) && !validApproval(r) && r.ConsentID == "" && !canReviewIntent(current) {
			r.Status, r.Outcome, r.Reason = AwaitingConfirmation, "ask", "Exact owner confirmation is required; automatic intent review is unavailable"
			var err error
			claimed, err = e.store.save(ctx, r, r.Revision, "host", e.clock())
			return err
		}
		r.Status, r.Outcome, r.Reason = Executing, "allow", "Durable dispatch claim; result not yet known"
		var err error
		claimed, err = e.store.save(ctx, r, r.Revision, "host", e.clock())
		if err != nil {
			return err
		}
		// The claim is committed before any effect. Errors and cancellation after
		// this point are uncertain, since the provider may already have acted.
		result, executionErr = e.operations[r.Action.Operation].Execute(ctx, cloneAction(r.Action))
		return nil
	})
	if err == nil && !entered {
		return Record{}, ErrExecutionAuthorityUnavailable
	}

	// A boundary cleanup failure after the durable claim is not a safe retry.
	// Finalize an uncertain receipt now rather than leaving an executing record
	// indefinitely until another host restart happens to recover it.
	if err != nil && claimed.Status == Executing {
		executionErr = errors.Join(executionErr, err)
		err = nil
	}
	if errors.Is(err, ErrConflict) {
		return e.store.get(ctx, key)
	}
	if err != nil {
		return Record{}, err
	}
	if claimed.Status != Executing {
		return claimed, nil
	}
	claimed.Status, claimed.Outcome, claimed.Reason = Succeeded, "allow", "Execution returned a receipt"
	claimed.Result = result
	if executionErr != nil || !json.Valid(result) {
		claimed.Status, claimed.Outcome, claimed.Reason = Uncertain, "deny", "Execution result is unknown; reconcile without replay"
		claimed.Result = nil
	}
	// Persist even if the caller disconnected after the effect. Failure leaves
	// an executing claim, which recovery converts to uncertain, never ready.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return e.store.save(persistCtx, claimed, claimed.Revision, "executor", e.clock())
}

func (e *Engine) Revoke(ctx context.Context, key Key, binding ActionBinding) (Record, error) {
	for range 8 {
		r, err := e.store.get(ctx, key)
		if err != nil {
			return Record{}, err
		}
		identity, err := e.owner(ctx, r)
		if err != nil {
			return Record{}, err
		}
		if binding.Digest != r.Action.Digest {
			return Record{}, ErrConflict
		}
		if r.Status == Revoked {
			return r, nil
		}
		if binding.ExpectedRevision != r.Revision {
			return Record{}, ErrConflict
		}
		switch r.Status {
		case Proposed, Ready, AwaitingConfirmation, Approved, Blocked:
			var saved Record
			err = e.withAuthority(ctx, r.Action.Scope, func(Authority) error {
				// Ledger CAS decides revocation versus dispatch. An owner
				// transport that expired while inspecting cannot change state.
				if _, err := e.owner(ctx, r); err != nil {
					return err
				}
				r.Status, r.Outcome, r.Reason = Revoked, "deny", "Owner revoked the pending action"
				var err error
				saved, err = e.store.save(ctx, r, r.Revision, identity.UserID, e.clock())
				return err
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			return saved, err
		case Revoked:
			return r, nil
		default:
			return r, errors.New("action no longer revocable; a started effect cannot be undone")
		}
	}
	return Record{}, ErrConflict
}

// CancelExecution invalidates unclaimed actions of one already verified owning
// execution. It is an internal host lifecycle operation, not a client endpoint
// or owner confirmation. The caller must verify cancellation ownership first.
// An optional exact scope restricts cancellation to one admitted execution.
// Started and uncertain effects are retained for reconciliation, never revoked.
func (e *Engine) CancelExecution(ctx context.Context, spaceID, sessionID string, exact ...Scope) error {
	if len(exact) > 1 || len(exact) == 1 && (exact[0].SpaceID != spaceID || exact[0].SessionID != sessionID || exact[0].TaskID == "" || exact[0].RequirementsRevision < 1 || exact[0].ContextDigest == "") {
		return errors.New("exact cancellation requires one complete matching action scope")
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("Personal action cancellation requires an execution session")
	}
	records, err := e.Records(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Action.Scope.SessionID != sessionID || len(exact) == 1 && record.Action.Scope != exact[0] {
			continue
		}
		switch record.Status {
		case Proposed, Ready, Approved, AwaitingConfirmation, Blocked:
		default:
			continue
		}
		for attempt := 0; attempt < 8; attempt++ {
			err = e.withAuthority(ctx, record.Action.Scope, func(Authority) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				// Dispatch may have claimed this action while cancellation was
				// waiting for the authority boundary. Read the durable state again.
				current, err := e.store.get(ctx, record.Action.Key())
				if err != nil {
					return err
				}
				switch current.Status {
				case Proposed, Ready, Approved, AwaitingConfirmation, Blocked:
					current.Status, current.Outcome, current.Reason = Revoked, "deny", "Owning execution was cancelled; pending action was not executed"
					_, err = e.store.save(ctx, current, current.Revision, "host", e.clock())
					return err
				default:
					return nil
				}
			})
			if !errors.Is(err, ErrConflict) {
				break
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// GetExecution reads a durable action for the trusted host coordinator. It is
// not an owner endpoint: exposing it requires a separately verified session or
// owner scope. A record is evidence, never a dispatch authorization token.
func (e *Engine) GetExecution(ctx context.Context, key Key) (Record, error) {
	return e.store.get(ctx, key)
}

// Records lists one space for host lifecycle recovery, without authenticating
// a client. Hosts must not expose this method as a broad plugin/client route.
func (e *Engine) Records(ctx context.Context, spaceID string) ([]Record, error) {
	if strings.TrimSpace(spaceID) == "" {
		return nil, errors.New("Personal execution listing requires a space")
	}
	keys, err := e.store.keys(ctx)
	if err != nil {
		return nil, err
	}
	records := []Record{}
	for _, key := range keys {
		if key.SpaceID != spaceID {
			continue
		}
		record, err := e.store.get(ctx, key)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (e *Engine) Get(ctx context.Context, key Key) (Record, error) {
	r, err := e.store.get(ctx, key)
	if err != nil {
		return Record{}, err
	}
	if _, err = e.owner(ctx, r); err != nil {
		return Record{}, err
	}
	return r, nil
}

func (e *Engine) List(ctx context.Context) ([]Record, error) {
	identity, err := e.authenticate(ctx)
	if err != nil || identity.Channel != OwnerUI || identity.UserID == "" || identity.SpaceID == "" {
		return nil, ErrUnauthorized
	}
	keys, err := e.store.keys(ctx)
	if err != nil {
		return nil, err
	}
	records := []Record{}
	for _, key := range keys {
		if key.SpaceID != identity.SpaceID {
			continue
		}
		record, err := e.store.get(ctx, key)
		if err != nil {
			return nil, err
		}
		if record.Action.OwnerID == identity.UserID {
			records = append(records, record)
		}
	}
	return records, nil
}

func (e *Engine) Audit(ctx context.Context, key Key) ([]Event, error) {
	if _, err := e.Get(ctx, key); err != nil {
		return nil, err
	}
	return e.store.Events(ctx, key)
}

// Recover never schedules work. Invoke only during host lifecycle recovery.
func (e *Engine) Recover(ctx context.Context) error {
	keys, err := e.store.keys(ctx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		r, err := e.store.get(ctx, key)
		if err != nil {
			return err
		}
		if r.Status == Executing {
			r.Status, r.Outcome, r.Reason = Uncertain, "deny", "Host interrupted after dispatch; reconcile without replay"
		} else if (r.Status == Proposed || r.Status == Ready || r.Status == Approved || r.Status == AwaitingConfirmation || r.Status == Blocked) && !e.clock().Before(r.Action.ExpiresAt) {
			r.Status, r.Outcome, r.Reason = Expired, "deny", "Action confirmation expired"
		} else {
			continue
		}
		if _, err = e.store.save(ctx, r, r.Revision, "recovery", e.clock()); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}
func (e *Engine) owner(ctx context.Context, r Record) (Identity, error) {
	identity, err := e.authenticate(ctx)
	if err != nil || identity.Channel != OwnerUI || identity.UserID == "" || identity.UserID != r.Action.OwnerID || identity.SpaceID != r.Action.Scope.SpaceID {
		return Identity{}, ErrUnauthorized
	}
	return identity, nil
}
func (e *Engine) inspect(ctx context.Context, r Record) (Authority, Record, error) {
	var authority Authority
	var stopped Record
	err := e.withAuthority(ctx, r.Action.Scope, func(current Authority) error {
		authority = current
		authority.UserMessages = append([]HumanMessage(nil), current.UserMessages...)
		authority.Consents = append([]Consent(nil), current.Consents...)
		if status, reason := e.check(r, current); status != "" {
			r.Status, r.Outcome, r.Reason = status, "deny", reason
			var err error
			stopped, err = e.store.save(ctx, r, r.Revision, "host", e.clock())
			return err
		}
		return nil
	})
	return authority, stopped, err
}
func (e *Engine) check(r Record, a Authority) (Status, string) {
	frozen := r.Action
	frozen.Digest = ""
	if r.Action.Digest != digest(frozen) {
		return Denied, "Action digest is invalid"
	}
	if a.Domain != "personal" {
		return Denied, "Personal actions cannot inherit Coding authority"
	}
	if a.BoundaryReason != "" {
		return Denied, "Host boundary denied this action: " + a.BoundaryReason
	}
	if a.OwnerID == "" || a.PolicyRevision < 1 || !a.Active || a.Scope != r.Action.Scope || a.OwnerID != r.Action.OwnerID || a.PolicyRevision != r.Action.PolicyRevision {
		return Stale, "Task, owner, execution or policy changed"
	}
	if !e.clock().Before(r.Action.ExpiresAt) {
		return Expired, "Action confirmation expired"
	}
	if r.Approval != nil && !validApproval(r) {
		return Denied, "Owner confirmation does not match the frozen action"
	}
	if r.ConsentID != "" && r.Approval == nil {
		consent := e.matchConsent(r.Action, a, r.ConsentID)
		if consent == nil || consent.ID != r.ConsentID {
			return Denied, "Bound user authorization was withdrawn, expired or changed"
		}
	}
	operation, ok := e.operations[r.Action.Operation]
	if !ok {
		return Denied, "Operation has no trusted effect contract"
	}
	if operation.Version != r.Action.HandlerVersion || operation.Effect != r.Action.Effect {
		return Stale, "Operation contract changed"
	}
	switch operation.Effect {
	case Bookkeeping, ReadOnly, DataWrite, ExternalWrite:
	case SensitiveAuthority:
		return Denied, "Credentials, OAuth and lasting authority require their dedicated owner flow"
	default:
		return Denied, "Unknown effect cannot execute"
	}
	return "", ""
}
func canReviewIntent(authority Authority) bool {
	if !authority.AllowIntentReview || strings.TrimSpace(authority.Scope.ContextDigest) == "" || len(authority.UserMessages) == 0 {
		return false
	}
	for _, message := range authority.UserMessages {
		if strings.TrimSpace(message.ID) == "" || strings.TrimSpace(message.Text) == "" {
			return false
		}
	}
	return true
}

func validApproval(r Record) bool {
	return r.Approval != nil && r.Approval.Choice == ApproveOnce && r.Approval.OwnerID == r.Action.OwnerID && r.Approval.Digest == r.Action.Digest
}
func needsConfirmation(effect Effect) bool { return effect == DataWrite || effect == ExternalWrite }
func reviewStatus(decision approvefor.Decision, err error) (Status, string, string) {
	if err != nil {
		return Blocked, "ask", "Reviewer unavailable; action not executed"
	}
	if strings.TrimSpace(decision.Reason) == "" {
		return Blocked, "ask", "Reviewer omitted its rationale; action not executed"
	}
	switch decision.Outcome {
	case approvefor.OutcomeAllow:
		return Ready, "allow", decision.Reason
	case approvefor.OutcomeUnsure:
		return AwaitingConfirmation, "ask", decision.Reason
	case approvefor.OutcomeDeny:
		return Denied, "deny", decision.Reason
	default:
		return Blocked, "ask", "No valid safety verdict; action not executed"
	}
}
func cloneAction(action Action) Action {
	action.Arguments = append(json.RawMessage(nil), action.Arguments...)
	return action
}
func reviewInput(r Record, a Authority, consent *Consent) ReviewInput {
	input := ReviewInput{Action: cloneAction(r.Action), UserMessages: append([]HumanMessage(nil), a.UserMessages...)}
	if consent != nil {
		copy := *consent
		input.Consent = &copy
	}
	if r.Approval != nil {
		approval := *r.Approval
		input.Confirmation = &approval
	}
	return input
}

func (e *Engine) matchConsent(action Action, authority Authority, selectedID string) *Consent {
	for _, consent := range authority.Consents {
		if selectedID != "" && consent.ID != selectedID {
			continue
		}
		if consent.ID == "" || consent.ActionID != action.ID || consent.OwnerID != action.OwnerID || consent.Scope != action.Scope || consent.PolicyRevision != action.PolicyRevision || consent.Operation != action.Operation || consent.HandlerVersion != action.HandlerVersion || consent.ArgumentsDigest != ArgumentsDigest(action.Arguments) || !e.clock().Before(consent.ExpiresAt) {
			continue
		}
		for _, message := range authority.UserMessages {
			if message.ID != "" && message.ID == consent.MessageID && strings.TrimSpace(message.Text) != "" {
				return &consent
			}
		}
	}
	return nil
}
