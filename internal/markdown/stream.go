package markdown

import (
	"strings"
)

// StreamCollector accumulates streaming markdown deltas and returns
// rendered lines incrementally, committing only on line boundaries.
type StreamCollector struct {
	buffer         strings.Builder
	committedLines int
	width          int
	styles         Styles
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

// CommitCompleteLines renders up to the last newline and returns
// only the newly rendered lines since the last commit.
func (c *StreamCollector) CommitCompleteLines() []string {
	src := c.buffer.String()
	lastNL := strings.LastIndexByte(src, '\n')
	if lastNL < 0 {
		return nil
	}

	// Render everything up to and including the last newline.
	rendered := Render(src[:lastNL+1], c.width, c.styles)
	if rendered == "" {
		return nil
	}
	lines := strings.Split(rendered, "\n")

	// Strip trailing blank lines (goldmark may add them after paragraphs).
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if c.committedLines >= len(lines) {
		return nil
	}
	out := make([]string, len(lines)-c.committedLines)
	copy(out, lines[c.committedLines:])
	c.committedLines = len(lines)
	return out
}

// Finalize renders any remaining buffer content and resets state.
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
	lines := strings.Split(rendered, "\n")

	var out []string
	if c.committedLines < len(lines) {
		out = make([]string, len(lines)-c.committedLines)
		copy(out, lines[c.committedLines:])
	}
	c.reset()
	return out
}

func (c *StreamCollector) reset() {
	c.buffer.Reset()
	c.committedLines = 0
}
