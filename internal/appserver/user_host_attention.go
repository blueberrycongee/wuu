package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const maxPendingAttentionAgents = 128
const maxAttentionBatchWindow = 15 * time.Minute

type userAttentionJob struct {
	owner      string
	input      pluginhost.SessionAttentionInput
	location   UserLocation
	host       *pluginhost.Host
	generation *runtime.PluginGeneration
}

// The buffered locator keeps revocable identity, not a live generation lease.
// A subscriber must reacquire the same current generation and recheck policy.
type userAttentionEvent struct {
	notification UserAttentionNotification
	owner        string
	input        pluginhost.SessionAttentionInput
	generation   *runtime.PluginGeneration
}

type userAttentionAgent struct {
	owner string
	agent string
}

type userAttentionBatch struct {
	event    userAttentionEvent
	deadline time.Time
}

// enqueueAttention is non-blocking because terminal publication holds the
// server output lock. Policy callbacks must never prevent another client from
// chatting, nor acquire a replacement owner's generation after revocation.
func (h *UserHost) enqueueAttention(job userAttentionJob) {
	job.host, job.generation = h.runtime.RetainPluginHost(pluginhost.CapabilitySessionAttention)
	if job.host == nil {
		return
	}
	h.mu.Lock()
	if h.closed || h.attentionLastTurn[job.input.SessionID] == job.input.TurnID {
		h.mu.Unlock()
		h.runtime.ReleasePluginGeneration(job.generation)
		return
	}
	// One terminal watermark per main session is sufficient: the single host
	// execution authority publishes each session's terminal transitions in order.
	// Bound distinct sessions; saturation suppresses attention, never chat.
	if _, exists := h.attentionLastTurn[job.input.SessionID]; !exists && len(h.attentionLastTurn) >= 4096 {
		h.mu.Unlock()
		h.runtime.ReleasePluginGeneration(job.generation)
		return
	}
	h.attentionLastTurn[job.input.SessionID] = job.input.TurnID
	select {
	case h.attentionJobs <- job:
		h.mu.Unlock()
	default:
		h.mu.Unlock()
		h.runtime.ReleasePluginGeneration(job.generation)
	}
}

func (h *UserHost) runAttentionPolicy() {
	defer close(h.attentionDone)
	defer func() {
		for {
			select {
			case job := <-h.attentionJobs:
				h.runtime.ReleasePluginGeneration(job.generation)
			default:
				return
			}
		}
	}()
	// One worker owns the bounded pending map and its timer. Pending batches
	// contain only revocable locators, never leases or result content, so reload
	// and shutdown need not wait for a batch window or per-agent goroutines.
	pending := make(map[userAttentionAgent]userAttentionBatch)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		if h.attentionCtx.Err() != nil {
			return
		}
		now := time.Now()
		var next time.Time
		for key, batch := range pending {
			if !now.Before(batch.deadline) {
				// Revocation of the latest locator suppresses the whole coalesced
				// notification. Earlier individual results remain in history.
				delete(pending, key)
				if h.revalidateAttention(h.attentionCtx, batch.event) {
					h.publishAttention(batch.event)
				}
			} else if next.IsZero() || batch.deadline.Before(next) {
				next = batch.deadline
			}
		}
		timer.Stop()
		var deadline <-chan time.Time
		if !next.IsZero() {
			timer.Reset(time.Until(next))
			deadline = timer.C
		}
		select {
		case <-h.attentionCtx.Done():
			return
		case <-deadline:
		case job := <-h.attentionJobs:
			key := userAttentionAgent{owner: job.owner, agent: job.input.AgentID}
			event, window, allowed := h.admitAttention(job)
			if !allowed {
				// A denied later turn must not erase another admitted result.
				// Its pending batch still rechecks policy at its own deadline.
				continue
			}
			batch, exists := pending[key]
			// A replacement main session or generation must never inherit the
			// previous owner's count or old policy admission.
			if exists && (batch.event.generation != event.generation || batch.event.input.SessionID != event.input.SessionID) {
				delete(pending, key)
				exists = false
			}
			if exists {
				event.notification.ItemCount += batch.event.notification.ItemCount
			}
			if window == 0 {
				delete(pending, key)
				h.publishAttention(event)
				continue
			}
			if !exists {
				// Saturation suppresses only best-effort attention, never a
				// conversation result or another agent's admitted batch.
				if len(pending) >= maxPendingAttentionAgents {
					continue
				}
				batch.deadline = time.Now().Add(window)
			}
			batch.event = event
			pending[key] = batch
		}
	}
}

func (h *UserHost) admitAttention(job userAttentionJob) (userAttentionEvent, time.Duration, bool) {
	defer h.runtime.ReleasePluginGeneration(job.generation)
	event := userAttentionEvent{
		notification: UserAttentionNotification{UserSpaceID: job.input.UserSpaceID, ProductDomain: job.input.ProductDomain, UserLocation: job.location, TurnID: job.input.TurnID, Outcome: job.input.Outcome, ItemCount: 1},
		owner:        job.owner, input: job.input, generation: job.generation,
	}
	if !h.runtime.IsCurrentPluginGeneration(job.generation) || !h.attentionOwnerCurrent(event) {
		return userAttentionEvent{}, 0, false
	}
	capability, ok := job.host.Capability(job.owner, pluginhost.CapabilitySessionAttention)
	if !ok {
		return userAttentionEvent{}, 0, false
	}
	ctx, cancel := context.WithTimeout(h.attentionCtx, 5*time.Second)
	defer cancel()
	var output pluginhost.SessionAttentionOutput
	if err := job.host.InvokeCapability(ctx, capability, job.input, &output); err != nil || !output.Allowed || ctx.Err() != nil {
		return userAttentionEvent{}, 0, false
	}
	if !h.attentionOwnerCurrent(event) || !h.runtime.IsCurrentPluginGeneration(job.generation) || ctx.Err() != nil {
		return userAttentionEvent{}, 0, false
	}
	if _, ok := job.host.Capability(job.owner, pluginhost.CapabilitySessionAttention); !ok {
		return userAttentionEvent{}, 0, false
	}
	// Clamp before multiplication so an untrusted extension value cannot
	// overflow time.Duration and accidentally turn batching into instant push.
	seconds := min(max(output.BatchWindowSeconds, 0), int(maxAttentionBatchWindow/time.Second))
	return event, time.Duration(seconds) * time.Second, true
}

func (h *UserHost) publishAttention(event userAttentionEvent) {
	// Attention is best effort. A bounded content-free queue covers the interval
	// between host startup and authenticated relay subscription; it is not an
	// external provider delivery receipt or a durable push outbox.
	h.mu.Lock()
	if h.closed || h.attentionCtx.Err() != nil {
		h.mu.Unlock()
		return
	}
	select {
	case h.attentionEvents <- event:
	default:
		// Prefer the latest navigable result when an offline transport falls behind.
		select {
		case <-h.attentionEvents:
		default:
		}
		select {
		case h.attentionEvents <- event:
		default:
		}
	}
	h.mu.Unlock()
	data, err := json.Marshal(struct {
		JSONRPC string                    `json:"jsonrpc"`
		Method  string                    `json:"method"`
		Params  UserAttentionNotification `json:"params"`
	}{"2.0", NotificationUserAttention, event.notification})
	if err == nil {
		_, _ = h.Write(append(data, '\n'))
	}
}

// Delayed delivery may outlive the persisted session or its main ownership.
// Re-read this authority both before and after calling the plugin's policy.
func (h *UserHost) attentionOwnerCurrent(event userAttentionEvent) bool {
	binding := h.runtime.UserBinding()
	input := event.input
	if input.UserSpaceID != binding.UserSpaceID || input.ProductDomain != binding.ProductDomain {
		return false
	}
	metadata, found, err := session.Find(h.runtime.SessionDir, input.SessionID)
	return err == nil && found && metadata.UserSpaceID == input.UserSpaceID && metadata.ProductDomain == input.ProductDomain &&
		metadata.Owner == "plugin:"+event.owner && metadata.ParentID == "" && metadata.Visibility == "plugin" &&
		metadata.AgentID != "" && metadata.AgentID == input.AgentID
}

// SubscribeUserAttention observes admitted attention independently of client
// connections. One host-owned subscriber is allowed at a time. At most 128
// pending locators are replayed; overflow drops the oldest. receive must return
// promptly and honor ctx in any external work. Cancellation leaves unconsumed
// events available to the next subscriber. Close terminates the subscription.
func (h *UserHost) SubscribeUserAttention(ctx context.Context, receive func(UserAttentionNotification)) error {
	if h == nil || ctx == nil || receive == nil {
		return errors.New("attention subscription requires host, context and receiver")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("user host is closed")
	}
	if h.attentionSubscribed {
		h.mu.Unlock()
		return errors.New("user host attention already subscribed")
	}
	h.attentionSubscribed = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.attentionSubscribed = false; h.mu.Unlock() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-h.attentionCtx.Done():
			return nil
		case event := <-h.attentionEvents:
			if ctx.Err() != nil || h.attentionCtx.Err() != nil {
				return ctx.Err()
			}
			if h.revalidateAttention(ctx, event) {
				receive(event.notification)
			}
		}
	}
}

func (h *UserHost) revalidateAttention(parent context.Context, event userAttentionEvent) bool {
	if parent.Err() != nil || h.attentionCtx.Err() != nil {
		return false
	}
	host, generation := h.runtime.RetainPluginHost(pluginhost.CapabilitySessionAttention)
	if host == nil {
		return false
	}
	defer h.runtime.ReleasePluginGeneration(generation)
	if generation != event.generation || !h.runtime.IsCurrentPluginGeneration(generation) || !h.attentionOwnerCurrent(event) {
		return false
	}
	capability, ok := host.Capability(event.owner, pluginhost.CapabilitySessionAttention)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(h.attentionCtx, cancel)
	defer stop()
	var output pluginhost.SessionAttentionOutput
	if err := host.InvokeCapability(ctx, capability, event.input, &output); err != nil || !output.Allowed || ctx.Err() != nil {
		return false
	}
	if !h.attentionOwnerCurrent(event) || !h.runtime.IsCurrentPluginGeneration(generation) || ctx.Err() != nil {
		return false
	}
	_, ok = host.Capability(event.owner, pluginhost.CapabilitySessionAttention)
	return ok
}
