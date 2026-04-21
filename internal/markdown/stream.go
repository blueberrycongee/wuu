package markdown

import (
	"strings"
)

// StreamCollector accumulates streaming text deltas and renders
// markdown incrementally on each Commit tick (100ms). This eliminates
// the visible jump between raw text (during streaming) and rendered
// markdown (at stream end) that occurred with the old raw-then-render
// approach.
//
// Aligned with Codex CLI's approach: markdown is rendered on newline
// boundaries during streaming, so the visual output is stable
// throughout the response — no sudden reformatting at EventDone.
type StreamCollector struct {
	buffer strings.Builder
	width  int
	styles Styles
	// dirty tracks whether new content was pushed since last Commit.
	dirty bool
}

// NewStreamCollector creates a new collector for streaming markdown.
func NewStreamCollector(width int, styles Styles) *StreamCollector {
	return &StreamCollector{
		width:  width,
		styles: styles,
	}
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

// Commit renders the accumulated buffer through the markdown pipeline
// and clears the dirty flag. Called on each 100ms tick during
// streaming. Rendering on every tick (not every token) keeps the cost
// manageable while producing stable, visually consistent output that
// won't jump when the stream ends.
func (c *StreamCollector) Commit() string {
	c.dirty = false
	src := c.buffer.String()
	if src == "" {
		return ""
	}
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	rendered := Render(src, c.width, c.styles)
	if rendered == "" {
		return src // fallback to raw if render produces nothing
	}
	return strings.TrimRight(rendered, "\n")
}

// Finalize renders the complete buffer through the markdown pipeline
// and resets state. Called once when the stream ends (EventDone).
// Since Commit already renders markdown on each tick, this is mostly
// a final pass to ensure the last partial line is included.
func (c *StreamCollector) Finalize() string {
	src := c.buffer.String()
	if src == "" {
		c.buffer.Reset()
		return ""
	}
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
	}

	rendered := Render(src, c.width, c.styles)
	c.buffer.Reset()
	c.dirty = false
	return strings.TrimRight(rendered, "\n")
}
