package markdown

import (
	"strings"
)

// StreamCollector accumulates streaming markdown deltas and returns
// the full rendered output on each commit. The caller should replace
// (not append) its cached render on each call, so that block-level
// structures like tables render correctly as they stream in.
type StreamCollector struct {
	buffer strings.Builder
	width  int
	styles Styles
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
}

// CommitCompleteLines renders everything up to the last newline and
// returns the full rendered output. Returns "" if there is nothing
// to render yet (no newline received).
func (c *StreamCollector) CommitCompleteLines() string {
	src := c.buffer.String()
	lastNL := strings.LastIndexByte(src, '\n')
	if lastNL < 0 {
		return ""
	}

	rendered := Render(src[:lastNL+1], c.width, c.styles)
	return strings.TrimRight(rendered, "\n")
}

// Finalize renders any remaining buffer content and resets state.
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
	return strings.TrimRight(rendered, "\n")
}
