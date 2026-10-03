package codemode

import (
	"encoding/json"
	"fmt"
	"sync"
)

const maxCallSummaryBytes = 8192

type callOutcome struct {
	id           int
	name, status string
}

// Keep only recent identities and aggregate outcomes, never tool arguments or results.
// Pending entries are also bounded by the service's concurrent-call limit.
type callHistory struct {
	mu                                    sync.Mutex
	recent                                []*callOutcome
	total, succeeded, failed, interrupted int
}

func (h *callHistory) begin(id int, name string) *callOutcome {
	h.mu.Lock()
	defer h.mu.Unlock()
	call := &callOutcome{id: id, name: name}
	h.total++
	if len(h.recent) == 16 {
		copy(h.recent, h.recent[1:])
		h.recent[len(h.recent)-1] = call
	} else {
		h.recent = append(h.recent, call)
	}
	return call
}

func (h *callHistory) finish(call *callOutcome, failed, interrupted bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case interrupted:
		call.status = "interrupted (effects unknown)"
		h.interrupted++
	case failed:
		call.status = "failed"
		h.failed++
	default:
		call.status = "succeeded"
		h.succeeded++
	}
}

// Called after all admitted Invoke calls return, including cancellation cleanup.
func (h *callHistory) summary() string {
	if h.total == 0 {
		return ""
	}
	header := fmt.Sprintf("Nested tool calls: %d succeeded, %d failed, %d interrupted.\n", h.succeeded, h.failed, h.interrupted)
	footer := "Tool effects are not rolled back; failed or interrupted calls may have partial effects. Check before retrying."
	lines, shown := "", 0
	for i := len(h.recent) - 1; i >= 0; i-- {
		call := h.recent[i]
		line := fmt.Sprintf("#%d %q: %s\n", call.id, call.name, call.status)
		encoded, _ := json.Marshal(header + line + lines + footer)
		// Reserve space for the omitted-call count before admitting another line.
		if len(encoded)+128 > maxCallSummaryBytes {
			break
		}
		lines = line + lines
		shown++
	}
	if shown < h.total {
		header += fmt.Sprintf("Showing last %d calls; %d earlier calls omitted.\n", shown, h.total-shown)
	}
	return header + lines + footer
}
