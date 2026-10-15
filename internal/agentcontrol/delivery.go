package agentcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

const (
	agentResultConsumerAwaitAgents    = "await_agents"
	agentResultConsumerAutoCompletion = "auto_completion"
	agentResultConsumerNestedPending  = "nested_followup_pending"
	agentResultConsumerNestedFollowup = "nested_followup"
	agentResultConsumerSpawnAgent     = "spawn_agent"
)

type agentResultDelivery struct {
	ResultID    string
	AgentID     string
	ParentID    string
	ThreadID    string
	AgentPath   string
	Status      string
	CompletedAt time.Time
	CreatedAt   time.Time
	ConsumedBy  string
	ConsumedAt  time.Time
}

type PendingRootAgentCompletion struct {
	ResultID string
	Snapshot subagent.SubAgentSnapshot
}

func agentResultDeliveryID(snap subagent.SubAgentSnapshot) string {
	agentID := strings.TrimSpace(snap.ID)
	if agentID == "" || !isFinalSubAgentStatus(snap.Status) {
		return ""
	}
	completedAt := ""
	if !snap.CompletedAt.IsZero() {
		completedAt = snap.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	errText := ""
	if snap.Error != nil {
		errText = snap.Error.Error()
	}
	h := sha256.New()
	h.Write([]byte(agentID))
	h.Write([]byte{0})
	h.Write([]byte(string(snap.Status)))
	h.Write([]byte{0})
	h.Write([]byte(completedAt))
	h.Write([]byte{0})
	h.Write([]byte(snap.Result))
	h.Write([]byte{0})
	h.Write([]byte(errText))
	sum := h.Sum(nil)
	return "agent_result_" + hex.EncodeToString(sum[:12])
}

// AgentResultDeliveryID returns the stable delivery id for a terminal worker
// result. Non-terminal snapshots do not have a consumable delivery id.
func (c *AgentControl) AgentResultDeliveryID(snap subagent.SubAgentSnapshot) string {
	return agentResultDeliveryID(snap)
}

func (c *AgentControl) ensureAgentResultDelivery(snap subagent.SubAgentSnapshot) (agentResultDelivery, error) {
	resultID := agentResultDeliveryID(snap)
	if c == nil || resultID == "" {
		return agentResultDelivery{}, nil
	}
	now := time.Now().UTC()
	delivery := agentResultDelivery{
		ResultID:    resultID,
		AgentID:     strings.TrimSpace(snap.ID),
		ParentID:    strings.TrimSpace(snap.ParentID),
		ThreadID:    strings.TrimSpace(c.rootThreadID),
		AgentPath:   strings.TrimSpace(snap.AgentPath),
		Status:      strings.TrimSpace(string(snap.Status)),
		CompletedAt: snap.CompletedAt,
		CreatedAt:   now,
	}
	c.resultDeliveriesMu.Lock()
	defer c.resultDeliveriesMu.Unlock()
	if c.resultDeliveries == nil {
		c.resultDeliveries = make(map[string]agentResultDelivery)
	}
	if existing, ok := c.resultDeliveries[resultID]; ok {
		return existing, nil
	}
	if err := c.recordAgentResultDeliveryEvent(agentthread.Event{
		Type:        agentthread.EventResultReady,
		ThreadID:    delivery.ThreadID,
		AgentID:     delivery.AgentID,
		ParentID:    delivery.ParentID,
		ResultID:    delivery.ResultID,
		Path:        delivery.AgentPath,
		Status:      agentThreadStatusForResultDelivery(delivery.Status),
		CompletedAt: delivery.CompletedAt,
		CreatedAt:   now,
	}); err != nil {
		return agentResultDelivery{}, err
	}
	c.resultDeliveries[resultID] = delivery
	return delivery, nil
}

func agentThreadStatusForResultDelivery(status string) agentthread.Status {
	switch subagent.Status(strings.TrimSpace(status)) {
	case subagent.StatusCompleted:
		return agentthread.StatusCompleted
	case subagent.StatusFailed:
		return agentthread.StatusFailed
	case subagent.StatusCancelled:
		return agentthread.StatusCancelled
	default:
		return agentthread.Status(strings.TrimSpace(status))
	}
}

func (c *AgentControl) claimAgentResultDelivery(snap subagent.SubAgentSnapshot, consumer string) (string, bool, string, error) {
	delivery, err := c.ensureAgentResultDelivery(snap)
	if err != nil {
		return "", false, "", err
	}
	if delivery.ResultID == "" {
		return "", false, "", nil
	}
	claimed, consumedBy, err := c.ClaimAgentResultDeliveryID(delivery.ResultID, consumer)
	return delivery.ResultID, claimed, consumedBy, err
}

// ClaimAgentResultDeliveryID marks a terminal result as consumed by one model
// delivery path. It returns false when another path has already consumed it.
func (c *AgentControl) ClaimAgentResultDeliveryID(resultID, consumer string) (bool, string, error) {
	if c == nil {
		return false, "", nil
	}
	resultID = strings.TrimSpace(resultID)
	consumer = strings.TrimSpace(consumer)
	if resultID == "" {
		return false, "", nil
	}
	if consumer == "" {
		consumer = "unknown"
	}
	// Refresh locates the durable ready record and its metadata. The store then
	// performs the actual compare-and-append under its cross-process lock.
	if err := c.refreshAgentResultDeliveries(); err != nil {
		return false, "", err
	}
	now := time.Now().UTC()
	c.resultDeliveriesMu.Lock()
	delivery, ok := c.resultDeliveries[resultID]
	if !ok {
		c.resultDeliveriesMu.Unlock()
		return false, "", nil
	}
	if c.threadStore == nil || c.threadStore.Dir() == "" {
		if delivery.ConsumedBy != "" {
			consumedBy := delivery.ConsumedBy
			c.resultDeliveriesMu.Unlock()
			return false, consumedBy, nil
		}
		delivery.ConsumedBy = consumer
		delivery.ConsumedAt = now
		c.resultDeliveries[resultID] = delivery
		c.resultDeliveriesMu.Unlock()
		return true, "", nil
	}
	c.resultDeliveriesMu.Unlock()
	event := agentthread.Event{
		Type:        agentthread.EventResultClaim,
		ThreadID:    delivery.ThreadID,
		AgentID:     delivery.AgentID,
		ParentID:    delivery.ParentID,
		ResultID:    delivery.ResultID,
		Consumer:    consumer,
		Path:        delivery.AgentPath,
		Status:      agentThreadStatusForResultDelivery(delivery.Status),
		CompletedAt: delivery.CompletedAt,
		CreatedAt:   now,
	}
	claimed, consumedBy, err := c.threadStore.ClaimResultDelivery(event)
	if err != nil {
		return false, "", err
	}
	c.resultDeliveriesMu.Lock()
	delivery, ok = c.resultDeliveries[resultID]
	if ok {
		if claimed {
			delivery.ConsumedBy = consumer
			delivery.ConsumedAt = now
		} else if consumedBy != "" {
			delivery.ConsumedBy = consumedBy
		}
		c.resultDeliveries[resultID] = delivery
	}
	c.resultDeliveriesMu.Unlock()
	return claimed, consumedBy, nil
}

func (c *AgentControl) transitionAgentResultDeliveryConsumer(resultID, fromConsumer, consumer string) (bool, string, error) {
	if c == nil {
		return false, "", nil
	}
	resultID = strings.TrimSpace(resultID)
	fromConsumer = strings.TrimSpace(fromConsumer)
	consumer = strings.TrimSpace(consumer)
	if resultID == "" || fromConsumer == "" || consumer == "" {
		return false, "", nil
	}
	if err := c.refreshAgentResultDeliveries(); err != nil {
		return false, "", err
	}
	now := time.Now().UTC()
	c.resultDeliveriesMu.Lock()
	delivery, ok := c.resultDeliveries[resultID]
	if !ok || delivery.ConsumedBy != fromConsumer {
		consumedBy := delivery.ConsumedBy
		c.resultDeliveriesMu.Unlock()
		return false, consumedBy, nil
	}
	if c.threadStore == nil || c.threadStore.Dir() == "" {
		delivery.ConsumedBy = consumer
		delivery.ConsumedAt = now
		c.resultDeliveries[resultID] = delivery
		c.resultDeliveriesMu.Unlock()
		return true, consumer, nil
	}
	c.resultDeliveriesMu.Unlock()
	event := agentthread.Event{
		Type:        agentthread.EventResultClaim,
		ThreadID:    delivery.ThreadID,
		AgentID:     delivery.AgentID,
		ParentID:    delivery.ParentID,
		ResultID:    delivery.ResultID,
		Consumer:    consumer,
		Path:        delivery.AgentPath,
		Status:      agentThreadStatusForResultDelivery(delivery.Status),
		CompletedAt: delivery.CompletedAt,
		CreatedAt:   now,
	}
	transitioned, consumedBy, err := c.threadStore.TransitionResultDelivery(event, fromConsumer)
	if err != nil {
		return false, consumedBy, err
	}
	if transitioned || consumedBy != "" {
		c.resultDeliveriesMu.Lock()
		delivery, ok = c.resultDeliveries[resultID]
		if ok {
			delivery.ConsumedBy = consumedBy
			if transitioned {
				delivery.ConsumedBy = consumer
				delivery.ConsumedAt = now
			}
			c.resultDeliveries[resultID] = delivery
		}
		c.resultDeliveriesMu.Unlock()
	}
	return transitioned, consumedBy, nil
}

func (c *AgentControl) ReleaseAgentResultDeliveryClaim(resultID, consumer string) error {
	if c == nil {
		return nil
	}
	resultID = strings.TrimSpace(resultID)
	consumer = strings.TrimSpace(consumer)
	if resultID == "" || consumer == "" {
		return nil
	}
	if err := c.refreshAgentResultDeliveries(); err != nil {
		return err
	}
	now := time.Now().UTC()
	c.resultDeliveriesMu.Lock()
	delivery, ok := c.resultDeliveries[resultID]
	if !ok || delivery.ConsumedBy != consumer {
		c.resultDeliveriesMu.Unlock()
		return nil
	}
	if c.threadStore == nil || c.threadStore.Dir() == "" {
		delivery.ConsumedBy = ""
		delivery.ConsumedAt = time.Time{}
		c.resultDeliveries[resultID] = delivery
		c.resultDeliveriesMu.Unlock()
		return nil
	}
	c.resultDeliveriesMu.Unlock()
	event := agentthread.Event{
		Type:        agentthread.EventResultRelease,
		ThreadID:    delivery.ThreadID,
		AgentID:     delivery.AgentID,
		ParentID:    delivery.ParentID,
		ResultID:    delivery.ResultID,
		Consumer:    consumer,
		Path:        delivery.AgentPath,
		Status:      agentThreadStatusForResultDelivery(delivery.Status),
		CompletedAt: delivery.CompletedAt,
		CreatedAt:   now,
	}
	released, err := c.threadStore.ReleaseResultDelivery(event)
	if err != nil {
		return err
	}
	if released {
		c.resultDeliveriesMu.Lock()
		delivery, ok = c.resultDeliveries[resultID]
		if ok && delivery.ConsumedBy == consumer {
			delivery.ConsumedBy = ""
			delivery.ConsumedAt = time.Time{}
			c.resultDeliveries[resultID] = delivery
		}
		c.resultDeliveriesMu.Unlock()
	}
	return nil
}

func (c *AgentControl) recordAgentResultDeliveryEvent(event agentthread.Event) error {
	if c == nil || c.threadStore == nil {
		return nil
	}
	return c.threadStore.AppendEvent(event)
}

func (c *AgentControl) restoreAgentResultDeliveries() {
	_ = c.refreshAgentResultDeliveries()
}

func (c *AgentControl) refreshAgentResultDeliveries() error {
	if c == nil || c.threadStore == nil {
		return nil
	}
	c.resultDeliveriesMu.Lock()
	deliveries := make(map[string]agentResultDelivery, len(c.resultDeliveries))
	for resultID, delivery := range c.resultDeliveries {
		deliveries[resultID] = delivery
	}
	c.resultDeliveriesMu.Unlock()
	events, err := c.threadStore.ReadEvents()
	if err != nil {
		return err
	}
	for _, event := range events {
		resultID := strings.TrimSpace(event.ResultID)
		if resultID == "" {
			continue
		}
		switch event.Type {
		case agentthread.EventResultReady:
			delivery := deliveries[resultID]
			delivery.ResultID = resultID
			delivery.AgentID = strings.TrimSpace(event.AgentID)
			delivery.ParentID = strings.TrimSpace(event.ParentID)
			delivery.ThreadID = strings.TrimSpace(event.ThreadID)
			delivery.AgentPath = strings.TrimSpace(event.Path)
			delivery.Status = strings.TrimSpace(string(event.Status))
			delivery.CompletedAt = event.CompletedAt
			delivery.CreatedAt = event.CreatedAt
			deliveries[resultID] = delivery
		case agentthread.EventResultClaim:
			delivery := deliveries[resultID]
			delivery.ResultID = resultID
			delivery.AgentID = firstNonEmptyString(delivery.AgentID, strings.TrimSpace(event.AgentID))
			delivery.ParentID = firstNonEmptyString(delivery.ParentID, strings.TrimSpace(event.ParentID))
			delivery.ThreadID = firstNonEmptyString(delivery.ThreadID, strings.TrimSpace(event.ThreadID))
			delivery.AgentPath = firstNonEmptyString(delivery.AgentPath, strings.TrimSpace(event.Path))
			delivery.Status = firstNonEmptyString(delivery.Status, strings.TrimSpace(string(event.Status)))
			if delivery.CompletedAt.IsZero() {
				delivery.CompletedAt = event.CompletedAt
			}
			delivery.ConsumedBy = strings.TrimSpace(event.Consumer)
			delivery.ConsumedAt = event.CreatedAt
			deliveries[resultID] = delivery
		case agentthread.EventResultRelease:
			delivery := deliveries[resultID]
			if delivery.ConsumedBy == strings.TrimSpace(event.Consumer) {
				delivery.ConsumedBy = ""
				delivery.ConsumedAt = time.Time{}
				deliveries[resultID] = delivery
			}
		}
	}
	c.resultDeliveriesMu.Lock()
	c.resultDeliveries = deliveries
	c.resultDeliveriesMu.Unlock()
	return nil
}

// PendingRootAgentCompletions rebuilds the auto-completion queue from durable
// result-ready events. It refreshes the ledger on every call so a second
// app-server observes claims committed by the first one.
func (c *AgentControl) PendingRootAgentCompletions() ([]PendingRootAgentCompletion, error) {
	pending, err := c.pendingAgentCompletions()
	if err != nil {
		return nil, err
	}
	out := pending[:0]
	for _, completion := range pending {
		if c.isRootChildSnapshot(completion.Snapshot) {
			out = append(out, completion)
		}
	}
	return out, nil
}

func (c *AgentControl) pendingAgentCompletions() ([]PendingRootAgentCompletion, error) {
	if c == nil {
		return nil, nil
	}
	if err := c.refreshAgentResultDeliveries(); err != nil {
		return nil, fmt.Errorf("refresh result deliveries: %w", err)
	}
	c.resultDeliveriesMu.Lock()
	deliveries := make([]agentResultDelivery, 0, len(c.resultDeliveries))
	for _, delivery := range c.resultDeliveries {
		if delivery.ConsumedBy == "" || delivery.ConsumedBy == agentResultConsumerNestedPending {
			deliveries = append(deliveries, delivery)
		}
	}
	c.resultDeliveriesMu.Unlock()

	out := make([]PendingRootAgentCompletion, 0, len(deliveries))
	for _, delivery := range deliveries {
		var snap subagent.SubAgentSnapshot
		found := false
		if c.manager != nil {
			if sa := c.manager.Get(delivery.AgentID); sa != nil {
				snap = sa.Snapshot()
				found = true
			}
		}
		if !found && strings.TrimSpace(c.historyDir) != "" {
			run, err := subagent.LoadPersistedRun(filepath.Join(c.historyDir, delivery.AgentID+".json"))
			if err == nil {
				snap = snapshotFromPersistedRun(run)
				found = true
			}
		}
		if !found || !isFinalSubAgentStatus(snap.Status) || agentResultDeliveryID(snap) != delivery.ResultID {
			continue
		}
		out = append(out, PendingRootAgentCompletion{ResultID: delivery.ResultID, Snapshot: snap})
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].Snapshot.CompletedAt, out[j].Snapshot.CompletedAt
		if left.Equal(right) {
			return out[i].ResultID < out[j].ResultID
		}
		if left.IsZero() {
			return false
		}
		if right.IsZero() {
			return true
		}
		return left.Before(right)
	})
	return out, nil
}

// replayPendingNestedAgentCompletions resumes durable child-to-parent handoffs
// that were recorded after shutdown closed worker-turn admission. It never
// overrides shutdown: deliverNestedResultToParent rechecks the stopping gate
// before rehydrating or queueing the parent turn, and an unsuccessful replay
// leaves the result-ready ledger entry unclaimed for the next owner.
func (c *AgentControl) replayPendingNestedAgentCompletions() {
	pending, err := c.pendingAgentCompletions()
	if err != nil {
		providers.DebugLogf("agentcontrol: restore pending nested completions: %v", err)
		return
	}
	for _, completion := range pending {
		if c.isStopping() {
			return
		}
		if c.isRootChildSnapshot(completion.Snapshot) {
			continue
		}
		replayCtx, cancelReplay := context.WithTimeout(context.Background(), nestedResultDeliveryWait)
		delivered := c.deliverNestedResultToParent(replayCtx, completion.Snapshot)
		cancelReplay()
		if !delivered {
			providers.DebugLogf("agentcontrol: pending nested completion %s remains queued for parent %s", completion.Snapshot.ID, completion.Snapshot.ParentID)
		}
	}
}

func (c *AgentControl) parentPersistedRunContainsResult(parentID, resultID string) (bool, error) {
	if c == nil || strings.TrimSpace(c.historyDir) == "" {
		return false, nil
	}
	run, err := subagent.LoadPersistedRun(filepath.Join(c.historyDir, strings.TrimSpace(parentID)+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !isFinalSubAgentStatus(run.Status) {
		return false, nil
	}
	resultID = strings.TrimSpace(resultID)
	for _, message := range run.Messages {
		if strings.Contains(message.Content, resultID) {
			return true, nil
		}
	}
	return false, nil
}

// settleClosedParentResult requires the parent's transition lock and a closed
// edge. Keep an applied result acknowledged; otherwise release its reservation
// so stopping the parent preserves the result without waking it.
func (c *AgentControl) settleClosedParentResult(parentID, resultID string) (bool, error) {
	applied, err := c.parentPersistedRunContainsResult(parentID, resultID)
	if err != nil {
		return false, err
	}
	if applied {
		transitioned, consumer, err := c.transitionAgentResultDeliveryConsumer(resultID, agentResultConsumerNestedPending, agentResultConsumerNestedFollowup)
		return transitioned || consumer == agentResultConsumerNestedFollowup, err
	}
	return false, c.ReleaseAgentResultDeliveryClaim(resultID, agentResultConsumerNestedPending)
}

// commitDurablyAppliedNestedResults advances pending parent-inbox reservations
// only after the parent's terminal persisted history contains the stable result
// ID. A crash before that point leaves the pending claim replayable; a crash
// after persistence but before this transition is repaired by startup replay.
func (c *AgentControl) commitDurablyAppliedNestedResults(parentID string) error {
	if c == nil || strings.TrimSpace(parentID) == "" {
		return nil
	}
	if err := c.refreshAgentResultDeliveries(); err != nil {
		return err
	}
	c.resultDeliveriesMu.Lock()
	resultIDs := make([]string, 0)
	for resultID, delivery := range c.resultDeliveries {
		if delivery.ConsumedBy == agentResultConsumerNestedPending && strings.TrimSpace(delivery.ParentID) == strings.TrimSpace(parentID) {
			resultIDs = append(resultIDs, resultID)
		}
	}
	c.resultDeliveriesMu.Unlock()
	for _, resultID := range resultIDs {
		applied, err := c.parentPersistedRunContainsResult(parentID, resultID)
		if err != nil {
			return err
		}
		if !applied {
			continue
		}
		transitioned, consumedBy, err := c.transitionAgentResultDeliveryConsumer(resultID, agentResultConsumerNestedPending, agentResultConsumerNestedFollowup)
		if err != nil {
			return err
		}
		if !transitioned && consumedBy != agentResultConsumerNestedFollowup {
			return fmt.Errorf("nested result %s consumer = %q, want %q", resultID, consumedBy, agentResultConsumerNestedFollowup)
		}
	}
	return nil
}

func (c *AgentControl) AgentResultDeliveryConsumer(resultID string) (string, error) {
	if c == nil {
		return "", nil
	}
	if err := c.refreshAgentResultDeliveries(); err != nil {
		return "", err
	}
	c.resultDeliveriesMu.Lock()
	delivery, ok := c.resultDeliveries[strings.TrimSpace(resultID)]
	c.resultDeliveriesMu.Unlock()
	if !ok {
		return "", nil
	}
	return delivery.ConsumedBy, nil
}

// agentResultDeliveryConsumed reports whether a terminal result has already
// been handed to the model through one of the delivery paths.
func (c *AgentControl) agentResultDeliveryConsumed(resultID string) bool {
	if c == nil {
		return false
	}
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return false
	}
	c.resultDeliveriesMu.Lock()
	delivery, ok := c.resultDeliveries[resultID]
	c.resultDeliveriesMu.Unlock()
	return ok && delivery.ConsumedBy != ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
