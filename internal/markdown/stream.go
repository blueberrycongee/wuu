package markdown

import (
	"strings"
)

// StreamCollector accumulates streaming text deltas and renders
// markdown incrementally on newline boundaries. A line is only
// "committed" (returned) once it ends with \n, which avoids the
// visual instability of re-rendering partial lines on every tick.
//
// Aligned with Codex CLI's approach: markdown is rendered on newline
// boundaries during streaming, so the visual output is stable
// throughout the response — no sudden reformatting at EventDone.
type StreamCollector struct {
	buffer             strings.Builder
	width              int
	styles             Styles
	dirty              bool
	committedLineCount int
}

// NewStreamCollector creates a new collector for streaming markdown.
func NewStreamCollector(width int, styles Styles) *StreamCollector {
	return &StreamCollector{width: width, styles: styles}
}

// Push appends a delta to the buffer.
func (c *StreamCollector) Push(delta string) {
	c.buffer.WriteString(delta)
	c.dirty = true
}

// Dirty reports whether new content was pushed since the last Commit.
func (c *StreamCollector) Dirty() bool {
	return c.dirty
}

// Commit renders the accumulated buffer and returns ONLY the newly completed
// lines since the last commit. A line is "complete" only if it ends with \n.
// If the buffer has no \n, returns nil (nothing to commit yet).
// NO raw fallback — if Render returns "", return nil.
func (c *StreamCollector) Commit() []string {
	c.dirty = false
	src := c.buffer.String()
	lastNL := strings.LastIndex(src, "\n")
	if lastNL < 0 {
		return nil
	}
	source := src[:lastNL+1]
	rendered := Render(source, c.width, c.styles)
	if rendered == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) <= c.committedLineCount {
		return nil
	}
	out := make([]string, len(lines)-c.committedLineCount)
	copy(out, lines[c.committedLineCount:])
	c.committedLineCount = len(lines)
	return out
}

// Finalize renders the complete buffer (appending a temporary \n if missing)
// and returns any remaining lines beyond the last commit. Then resets state.
// NO raw fallback.
func (c *StreamCollector) Finalize() []string {
	src := c.buffer.String()
	if src == "" {
		c.reset()
		return nil
	}
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	rendered := Render(src, c.width, c.styles)
	if rendered == "" {
		c.reset()
		return nil
	}
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	prevCommitted := c.committedLineCount
	c.reset()
	if len(lines) <= prevCommitted {
		return nil
	}
	out := make([]string, len(lines)-prevCommitted)
	copy(out, lines[prevCommitted:])
	return out
}

func (c *StreamCollector) reset() {
	c.buffer.Reset()
	c.dirty = false
	c.committedLineCount = 0
}
