package agent

import (
	"sort"
	"strings"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const requestContextUpdateMarker = "[CONTEXT_UPDATE]"

type ContextSegmentLifecycle = loopdriver.ContextSegmentLifecycle
type ContextSegmentPlacement = loopdriver.ContextSegmentPlacement
type ContextSegmentCachePolicy = loopdriver.ContextSegmentCachePolicy
type ContextSegment = loopdriver.ContextSegment

const (
	ContextSegmentRequestOnly  = loopdriver.ContextSegmentRequestOnly
	ContextSegmentAfterHistory = loopdriver.ContextSegmentAfterHistory
	ContextSegmentVolatile     = loopdriver.ContextSegmentVolatile
)

// RequestOnlyContextMessages wraps messages as volatile request-only context.
func RequestOnlyContextMessages(messages []providers.ChatMessage) []ContextSegment {
	if len(messages) == 0 {
		return nil
	}
	return []ContextSegment{RequestOnlyContextSegment(messages)}
}

// RequestOnlyContextSegment builds a request-only segment from ChatMessages
// while marking them hidden from user-facing transcript surfaces.
func RequestOnlyContextSegment(messages []providers.ChatMessage) ContextSegment {
	return ContextSegment{
		Messages:    normalizeRequestOnlyMessages(messages),
		Lifecycle:   ContextSegmentRequestOnly,
		Placement:   ContextSegmentAfterHistory,
		CachePolicy: ContextSegmentVolatile,
		Durable:     false,
		VisibleInUI: false,
	}
}

// RequestOnlyContextBlocks wraps typed runtime context blocks as volatile
// request-only context. The segment keeps the typed blocks as the source of
// truth; ChatMessages are only the provider request projection.
func RequestOnlyContextBlocks(blocks []wuucontext.Block) []ContextSegment {
	if len(blocks) == 0 {
		return nil
	}
	return []ContextSegment{RequestOnlyContextBlockSegment(blocks)}
}

// RequestOnlyContextBlockSegment builds a request-only segment from typed
// context blocks.
func RequestOnlyContextBlockSegment(blocks []wuucontext.Block) ContextSegment {
	copied := append([]wuucontext.Block(nil), blocks...)
	return ContextSegment{
		Messages:    requestOnlyMessagesFromBlocks(copied),
		Blocks:      copied,
		Lifecycle:   ContextSegmentRequestOnly,
		Placement:   ContextSegmentAfterHistory,
		CachePolicy: ContextSegmentVolatile,
		Durable:     false,
		VisibleInUI: false,
	}
}

// RequestAssembly is the provider request view produced from durable live
// history plus request-only context segments.
type RequestAssembly struct {
	Messages            []providers.ChatMessage
	RequestOnlyMessages []providers.ChatMessage
	Segments            []ContextSegment
}

func assembleModelRequest(history []providers.ChatMessage, segments []ContextSegment) RequestAssembly {
	total := len(history)
	for _, segment := range segments {
		if !isRequestOnlyAfterHistorySegment(segment) {
			continue
		}
		total += len(segment.Messages)
	}
	messages := make([]providers.ChatMessage, 0, total)
	messages = append(messages, history...)
	requestOnly := make([]providers.ChatMessage, 0, total-len(history))
	usedSegments := make([]ContextSegment, 0, len(segments))
	for _, segment := range segments {
		if !isRequestOnlyAfterHistorySegment(segment) {
			continue
		}
		normalized := normalizeRequestOnlyMessages(segment.Messages)
		if len(normalized) == 0 {
			continue
		}
		segment.Messages = normalized
		messages = append(messages, normalized...)
		requestOnly = append(requestOnly, normalized...)
		usedSegments = append(usedSegments, segment)
	}
	return RequestAssembly{
		Messages:            messages,
		RequestOnlyMessages: requestOnly,
		Segments:            usedSegments,
	}
}

func requestContextSegments(provider func() []ContextSegment) []ContextSegment {
	if provider == nil {
		return nil
	}
	return provider()
}

func normalizeRequestOnlyMessages(messages []providers.ChatMessage) []providers.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]providers.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.Role) == "" {
			msg.Role = "user"
		}
		msg.Hidden = true
		out = append(out, msg)
	}
	return out
}

// requestOnlyBlockProjection pairs a typed context block with its provider
// request message so callers can filter blocks and messages in lockstep.
type requestOnlyBlockProjection struct {
	block   wuucontext.Block
	message providers.ChatMessage
}

func projectRequestOnlyBlocks(blocks []wuucontext.Block) []requestOnlyBlockProjection {
	if len(blocks) == 0 {
		return nil
	}
	counts := make(map[string]int, len(blocks))
	projections := make([]requestOnlyBlockProjection, 0, len(blocks))
	for _, block := range blocks {
		rendered := compileRequestOnlyBlocks([]wuucontext.Block{block})
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		name := wuucontext.SystemReminderBlockMessageName(block, 0)
		ordinal := counts[name]
		counts[name] = ordinal + 1
		if ordinal > 0 {
			name = wuucontext.SystemReminderBlockMessageName(block, ordinal)
		}
		projections = append(projections, requestOnlyBlockProjection{
			block: block,
			message: providers.ChatMessage{
				Role:    "user",
				Name:    name,
				Content: activeRequestContextContent(name, rendered),
				Hidden:  true,
			},
		})
	}
	return projections
}

func compileRequestOnlyBlocks(blocks []wuucontext.Block) string {
	if wuucontext.DynamicContextProjectionEnabled() {
		return wuucontext.CompileRequestBlocks(blocks)
	}
	return wuucontext.CompileBlocks(blocks)
}

func activeRequestContextContent(name, rendered string) string {
	rule := "Only the latest context update with this key applies; it replaces earlier active or inactive updates."
	if wuucontext.DynamicContextProjectionEnabled() {
		rule = "Latest update for this key wins."
	}
	return "<system-reminder>\n" + requestContextUpdateMarker +
		"\nkey: " + name +
		"\nstatus: active" +
		"\nrule: " + rule + "\n\n" +
		rendered + "\n</system-reminder>"
}

func inactiveRequestContextMessage(name string) providers.ChatMessage {
	rule := "This context is no longer active. Ignore earlier active updates with this key."
	if wuucontext.DynamicContextProjectionEnabled() {
		rule = "Inactive; ignore earlier active updates for this key."
	}
	return providers.ChatMessage{
		Role:   "user",
		Name:   name,
		Hidden: true,
		Content: "<system-reminder>\n" + requestContextUpdateMarker +
			"\nkey: " + name +
			"\nstatus: inactive" +
			"\nrule: " + rule + "\n" +
			"</system-reminder>",
	}
}

func isSupersedableRequestContextMessage(msg providers.ChatMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(msg.Name), "wuu_ctx_")
}

func isInactiveRequestContextMessage(msg providers.ChatMessage) bool {
	return isSupersedableRequestContextMessage(msg) &&
		strings.Contains(msg.Content, requestContextUpdateMarker+"\nkey: ") &&
		strings.Contains(msg.Content, "\nstatus: inactive\n")
}

func requestOnlyMessagesFromBlocks(blocks []wuucontext.Block) []providers.ChatMessage {
	projections := projectRequestOnlyBlocks(blocks)
	if len(projections) == 0 {
		return nil
	}
	messages := make([]providers.ChatMessage, 0, len(projections))
	for _, projection := range projections {
		messages = append(messages, projection.message)
	}
	return messages
}

// requestContextMessageKey identifies a request-only context message for
// emit-on-change gating. Named messages (system-reminder block projections)
// key by name so a changed snapshot supersedes its prior copy; unnamed
// messages key by content so identical injections collapse to one copy.
func requestContextMessageKey(msg providers.ChatMessage) string {
	if name := strings.TrimSpace(msg.Name); name != "" {
		return "name:" + name
	}
	return "content:" + msg.Content
}

// filterUnsentRequestContext drops request-only context messages whose exact
// content the provider transcript already retains. Changed blocks pass
// through: the new snapshot appends after history while the prior copy stays
// inside the stable prefix, keeping requests append-only.
func filterUnsentRequestContext(segments []ContextSegment, sent map[string]string) []ContextSegment {
	if len(segments) == 0 {
		return nil
	}
	out := make([]ContextSegment, 0, len(segments))
	for _, segment := range segments {
		if len(segment.Blocks) > 0 {
			projections := projectRequestOnlyBlocks(segment.Blocks)
			blocks := make([]wuucontext.Block, 0, len(projections))
			messages := make([]providers.ChatMessage, 0, len(projections))
			for _, projection := range projections {
				if sent[requestContextMessageKey(projection.message)] == projection.message.Content {
					continue
				}
				blocks = append(blocks, projection.block)
				messages = append(messages, projection.message)
			}
			if len(messages) == 0 {
				continue
			}
			segment.Blocks = blocks
			segment.Messages = messages
			out = append(out, segment)
			continue
		}
		messages := make([]providers.ChatMessage, 0, len(segment.Messages))
		for _, msg := range normalizeRequestOnlyMessages(segment.Messages) {
			if sent[requestContextMessageKey(msg)] == msg.Content {
				continue
			}
			messages = append(messages, msg)
		}
		if len(messages) == 0 {
			continue
		}
		segment.Messages = messages
		out = append(out, segment)
	}
	return out
}

// requestContextContentByKey projects context segments to the request-only
// messages they would emit and returns a key→content map. Used to reconcile a
// previous run's retained context against the current run's fresh context.
func requestContextContentByKey(segments []ContextSegment) map[string]string {
	out := make(map[string]string)
	for _, segment := range segments {
		if !isRequestOnlyAfterHistorySegment(segment) {
			continue
		}
		var messages []providers.ChatMessage
		if len(segment.Blocks) > 0 {
			for _, projection := range projectRequestOnlyBlocks(segment.Blocks) {
				messages = append(messages, projection.message)
			}
		} else {
			messages = normalizeRequestOnlyMessages(segment.Messages)
		}
		for _, msg := range messages {
			out[requestContextMessageKey(msg)] = msg.Content
		}
	}
	return out
}

// reconciledRetainedContext preserves typed runtime-context update streams and
// re-affirms generic request-only messages byte-identically. Typed updates can
// remain in the prefix because later active/inactive messages explicitly
// supersede their earlier values.
func reconciledRetainedContext(retained []RetainedContextMessage, currentSegments []ContextSegment) []RetainedContextMessage {
	if len(retained) == 0 {
		return nil
	}
	current := requestContextContentByKey(currentSegments)
	out := make([]RetainedContextMessage, 0, len(retained))
	for _, entry := range retained {
		// Derived ledgers removed from the default projection must not ride
		// the retained stream across turns: drop stale copies instead of
		// re-splicing (and later tombstoning) them on every request.
		if !wuucontext.DerivedContextLedgersEnabled() && wuucontext.IsDerivedLedgerBlockName(entry.Message.Name) {
			continue
		}
		// Typed runtime blocks form an ordered update stream. Keeping every
		// prior update preserves the previous request byte-for-byte; the latest
		// active/inactive update determines current semantics. Generic request
		// messages have no such contract and are retained only when re-affirmed
		// byte-identically.
		if isSupersedableRequestContextMessage(entry.Message) {
			out = append(out, entry)
			continue
		}
		if content, ok := current[requestContextMessageKey(entry.Message)]; ok && content == entry.Message.Content {
			out = append(out, entry)
		}
	}
	return out
}

// inactiveRequestContextMessages emits deterministic tombstones for typed
// context blocks that were active in the retained transcript but are absent
// from the current snapshot. This lets the request remain append-only without
// leaving a removed policy or stale runtime fact active.
func inactiveRequestContextMessages(currentSegments []ContextSegment, sent map[string]string) []providers.ChatMessage {
	if len(sent) == 0 {
		return nil
	}
	current := requestContextContentByKey(currentSegments)
	keys := make([]string, 0)
	for key, content := range sent {
		if !strings.HasPrefix(key, "name:wuu_ctx_") {
			continue
		}
		if _, ok := current[key]; ok {
			continue
		}
		name := strings.TrimPrefix(key, "name:")
		if isInactiveRequestContextMessage(providers.ChatMessage{Name: name, Content: content}) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]providers.ChatMessage, 0, len(keys))
	for _, key := range keys {
		out = append(out, inactiveRequestContextMessage(strings.TrimPrefix(key, "name:")))
	}
	return out
}

// recordSentRequestContext marks msgs as retained in the provider transcript.
func recordSentRequestContext(sent map[string]string, msgs []providers.ChatMessage) map[string]string {
	if len(msgs) == 0 {
		return sent
	}
	if sent == nil {
		sent = make(map[string]string, len(msgs))
	}
	for _, msg := range msgs {
		sent[requestContextMessageKey(msg)] = msg.Content
	}
	return sent
}

func isRequestOnlyAfterHistorySegment(segment ContextSegment) bool {
	return segment.Lifecycle == ContextSegmentRequestOnly &&
		segment.Placement == ContextSegmentAfterHistory &&
		segment.CachePolicy == ContextSegmentVolatile &&
		!segment.Durable &&
		!segment.VisibleInUI
}
