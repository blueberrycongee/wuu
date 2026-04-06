package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

// markdownRenderer provides cached Glamour rendering with width tracking.
type markdownRenderer struct {
	mu       sync.Mutex
	renderer *glamour.TermRenderer
	width    int
}

// newMarkdownRenderer creates a renderer for the given width.
func newMarkdownRenderer(width int) (*markdownRenderer, error) {
	if width < 40 {
		width = 40
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	return &markdownRenderer{renderer: r, width: width}, nil
}

// Render renders markdown content. Returns the rendered string.
func (mr *markdownRenderer) Render(content string) (string, error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if strings.TrimSpace(content) == "" {
		return "(empty)", nil
	}

	rendered, err := mr.renderer.Render(content)
	if err != nil {
		return content, err // fallback to raw content
	}
	return strings.TrimSpace(rendered), nil
}

// RenderStreaming renders partial markdown during streaming.
// Handles incomplete markdown gracefully (unclosed code blocks, etc).
func (mr *markdownRenderer) RenderStreaming(content string) string {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if strings.TrimSpace(content) == "" {
		return ""
	}

	// For very short content, just return raw (avoids Glamour overhead)
	if len(content) < 20 {
		return content
	}

	rendered, err := mr.renderer.Render(content)
	if err != nil {
		return content // fallback to raw
	}
	return strings.TrimSpace(rendered)
}

// UpdateWidth recreates the renderer if width changed.
// Returns true if the renderer was recreated.
func (mr *markdownRenderer) UpdateWidth(width int) (bool, error) {
	if width < 40 {
		width = 40
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if mr.width == width {
		return false, nil
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return false, err
	}
	mr.renderer = r
	mr.width = width
	return true, nil
}

// Width returns the current rendering width.
func (mr *markdownRenderer) Width() int {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	return mr.width
}
