package markdown

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	xast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Render parses markdown input and returns a styled terminal string.
func Render(input string, width int, styles Styles) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}

	defer func() {
		// Never crash the caller — markdown rendering is best-effort.
		_ = recover()
	}()

	md := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough),
	)
	src := []byte(input)
	doc := md.Parser().Parse(text.NewReader(src))

	w := &Writer{
		source: src,
		out:    &strings.Builder{},
		styles: styles,
		width:  width,
	}
	w.walk(doc)
	w.flushPendingLine()
	return strings.TrimRight(w.out.String(), "\n")
}

// Writer holds the rendering state while walking the AST.
type Writer struct {
	source       []byte
	out          *strings.Builder
	styles       Styles
	inlineStyles []lipgloss.Style // nested emphasis/strong/strike
	indentStack  []indentContext  // nested list/blockquote
	listIndices  []int            // 0 = unordered, n>0 = next ordered counter
	width        int
	needsNewline bool

	// Code block buffering.
	inCodeBlock   bool
	codeBlockBuf  strings.Builder
	codeBlockLang string

	// Current line being built (for inline content).
	lineBuf       strings.Builder
	pendingMarker string // list/quote marker for the next written line
}

type indentContext struct {
	prefix string // continuation indent (no marker)
	marker string // first-line marker for the next list item
	isList bool
}

// walk traverses the AST and dispatches handlers.
func (w *Writer) walk(n ast.Node) {
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := node.(type) {
		case *ast.Document:
			// no-op
		case *ast.Paragraph:
			if entering {
				w.startBlock()
			} else {
				w.flushPendingLine()
				w.needsNewline = true
			}
		case *ast.Heading:
			if entering {
				w.startBlock()
				w.openLine()
				prefix := strings.Repeat("#", node.Level) + " "
				w.lineBuf.WriteString(w.headingStyle(node.Level).Render(prefix))
			} else {
				// Apply heading style to whole accumulated line content.
				w.flushPendingLine()
				w.needsNewline = true
			}
		case *ast.ThematicBreak:
			if entering {
				w.startBlock()
				w.openLine()
				w.lineBuf.WriteString(strings.Repeat("─", min(40, w.width)))
				w.flushPendingLine()
				w.needsNewline = true
			}
		case *ast.FencedCodeBlock:
			if entering {
				w.startBlock()
				w.inCodeBlock = true
				w.codeBlockBuf.Reset()
				if node.Info != nil {
					info := string(node.Info.Segment.Value(w.source))
					w.codeBlockLang = strings.Fields(info)[0]
				} else {
					w.codeBlockLang = ""
				}
				// Collect all code lines from the node.
				lines := node.Lines()
				for i := 0; i < lines.Len(); i++ {
					seg := lines.At(i)
					w.codeBlockBuf.Write(seg.Value(w.source))
				}
				w.emitCodeBlock()
				w.inCodeBlock = false
				w.needsNewline = true
				return ast.WalkSkipChildren, nil
			}
		case *ast.CodeBlock:
			if entering {
				w.startBlock()
				lines := node.Lines()
				var buf strings.Builder
				for i := 0; i < lines.Len(); i++ {
					seg := lines.At(i)
					buf.Write(seg.Value(w.source))
				}
				w.codeBlockLang = ""
				w.codeBlockBuf.Reset()
				w.codeBlockBuf.WriteString(buf.String())
				w.emitCodeBlock()
				w.needsNewline = true
				return ast.WalkSkipChildren, nil
			}
		case *ast.Blockquote:
			if entering {
				w.startBlock()
				w.indentStack = append(w.indentStack, indentContext{
					prefix: w.styles.Blockquote.Render("│ "),
					isList: false,
				})
			} else {
				w.indentStack = w.indentStack[:len(w.indentStack)-1]
				w.needsNewline = true
			}
		case *ast.List:
			if entering {
				w.startBlock()
				if node.IsOrdered() {
					w.listIndices = append(w.listIndices, int(node.Start))
				} else {
					w.listIndices = append(w.listIndices, 0)
				}
			} else {
				w.listIndices = w.listIndices[:len(w.listIndices)-1]
				w.needsNewline = true
			}
		case *ast.ListItem:
			if entering {
				idx := len(w.listIndices) - 1
				var marker string
				if w.listIndices[idx] > 0 {
					marker = w.styles.OrderedListMarker.Render(fmt.Sprintf("%d. ", w.listIndices[idx]))
					w.listIndices[idx]++
				} else {
					marker = w.styles.UnorderedListMarker.Render("• ")
				}
				w.indentStack = append(w.indentStack, indentContext{
					prefix: "  ",
					marker: marker,
					isList: true,
				})
			} else {
				w.indentStack = w.indentStack[:len(w.indentStack)-1]
			}

		// --- Inline ---
		case *ast.Text:
			if entering {
				w.openLine()
				txt := string(node.Segment.Value(w.source))
				w.lineBuf.WriteString(w.applyInlineStyle(txt))
				if node.SoftLineBreak() {
					w.flushPendingLine()
				}
				if node.HardLineBreak() {
					w.flushPendingLine()
				}
			}
		case *ast.String:
			if entering {
				w.openLine()
				w.lineBuf.WriteString(w.applyInlineStyle(string(node.Value)))
			}
		case *ast.Emphasis:
			if entering {
				if node.Level == 2 {
					w.pushInline(w.styles.Strong)
				} else {
					w.pushInline(w.styles.Emphasis)
				}
			} else {
				w.popInline()
			}
		case *xast.Strikethrough:
			if entering {
				w.pushInline(w.styles.Strikethrough)
			} else {
				w.popInline()
			}
		case *ast.CodeSpan:
			if entering {
				var content strings.Builder
				for c := node.FirstChild(); c != nil; c = c.NextSibling() {
					if t, ok := c.(*ast.Text); ok {
						content.Write(t.Segment.Value(w.source))
					}
				}
				w.openLine()
				w.lineBuf.WriteString(w.styles.CodeSpan.Render(content.String()))
				return ast.WalkSkipChildren, nil
			}
		case *ast.Link:
			if entering {
				w.pushInline(w.styles.Link)
			} else {
				w.popInline()
				dest := string(node.Destination)
				if dest != "" {
					w.openLine()
					w.lineBuf.WriteString(" ")
					w.lineBuf.WriteString(lipgloss.NewStyle().Faint(true).Render("(" + dest + ")"))
				}
			}
		case *ast.AutoLink:
			if entering {
				url := string(node.URL(w.source))
				w.openLine()
				w.lineBuf.WriteString(w.styles.Link.Render(url))
			}
		}
		return ast.WalkContinue, nil
	})
}

func (w *Writer) startBlock() {
	if w.needsNewline && w.out.Len() > 0 {
		w.out.WriteString("\n")
		w.needsNewline = false
	}
}

// openLine ensures lineBuf is initialized for the current line.
// (no-op placeholder for clarity)
func (w *Writer) openLine() {}

func (w *Writer) flushPendingLine() {
	if w.lineBuf.Len() == 0 && w.pendingMarker == "" {
		return
	}

	// Build prefix from indent stack.
	var prefix strings.Builder
	for i, ctx := range w.indentStack {
		if i == len(w.indentStack)-1 && ctx.marker != "" {
			prefix.WriteString(ctx.marker)
			// After consuming the marker, future lines in the same item use spaces.
			w.indentStack[i].marker = ""
		} else {
			prefix.WriteString(ctx.prefix)
		}
	}

	line := prefix.String() + w.lineBuf.String()
	w.out.WriteString(line)
	w.out.WriteString("\n")
	w.lineBuf.Reset()
}

func (w *Writer) applyInlineStyle(text string) string {
	if len(w.inlineStyles) == 0 {
		return text
	}
	// Compose styles by applying them outermost-first.
	result := text
	for i := len(w.inlineStyles) - 1; i >= 0; i-- {
		result = w.inlineStyles[i].Render(result)
	}
	return result
}

func (w *Writer) pushInline(s lipgloss.Style) {
	w.inlineStyles = append(w.inlineStyles, s)
}

func (w *Writer) popInline() {
	if len(w.inlineStyles) > 0 {
		w.inlineStyles = w.inlineStyles[:len(w.inlineStyles)-1]
	}
}

func (w *Writer) headingStyle(level int) lipgloss.Style {
	switch level {
	case 1:
		return w.styles.H1
	case 2:
		return w.styles.H2
	case 3:
		return w.styles.H3
	case 4:
		return w.styles.H4
	case 5:
		return w.styles.H5
	default:
		return w.styles.H6
	}
}

func (w *Writer) emitCodeBlock() {
	code := strings.TrimRight(w.codeBlockBuf.String(), "\n")
	if code == "" {
		return
	}
	highlighted := HighlightCode(code, w.codeBlockLang)
	for _, line := range strings.Split(highlighted, "\n") {
		w.out.WriteString("    ")
		w.out.WriteString(line)
		w.out.WriteString("\n")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
