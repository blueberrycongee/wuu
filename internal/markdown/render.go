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

const thematicBreakMaxWidth = 20

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
		goldmark.WithExtensions(extension.Table),
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
		case *ast.TextBlock:
			if entering {
				w.startBlock()
			} else {
				w.flushPendingLine()
			}
		case *ast.Heading:
			if entering {
				w.startBlock()
				w.pushInline(w.headingStyle(node.Level))
			} else {
				w.flushPendingLine()
				w.popInline()
				w.needsNewline = true
			}
		case *ast.ThematicBreak:
			if entering {
				w.startBlock()
				w.openLine()
				w.lineBuf.WriteString(strings.Repeat("─", min(thematicBreakMaxWidth, w.width)))
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
		case *xast.Table:
			if entering {
				td := w.collectTable(node)
				w.emitTable(td)
				return ast.WalkSkipChildren, nil
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

// tableData holds extracted table content for rendering.
type tableData struct {
	headers []string
	rows    [][]string
	widths  []int
	aligns  []xast.Alignment
}

// collectTable walks a Table node and extracts cell content and column widths.
func (w *Writer) collectTable(table *xast.Table) tableData {
	td := tableData{
		aligns: table.Alignments,
	}

	for child := table.FirstChild(); child != nil; child = child.NextSibling() {
		var cells []string
		// Both TableHeader and TableRow contain TableCell children.
		for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
			content := w.renderInlineChildren(cell)
			cells = append(cells, content)
		}
		switch child.(type) {
		case *xast.TableHeader:
			td.headers = cells
		case *xast.TableRow:
			td.rows = append(td.rows, cells)
		}
	}

	// Compute column widths.
	numCols := len(td.headers)
	if numCols == 0 && len(td.rows) > 0 {
		numCols = len(td.rows[0])
	}
	td.widths = make([]int, numCols)
	for i, h := range td.headers {
		if w := lipgloss.Width(h); w > td.widths[i] {
			td.widths[i] = w
		}
	}
	for _, row := range td.rows {
		for i, cell := range row {
			if i < numCols {
				if w := lipgloss.Width(cell); w > td.widths[i] {
					td.widths[i] = w
				}
			}
		}
	}
	for i := range td.widths {
		if td.widths[i] < 3 {
			td.widths[i] = 3
		}
	}
	return td
}

// renderInlineChildren renders a node's inline children to a styled string.
func (w *Writer) renderInlineChildren(n ast.Node) string {
	var buf strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		w.renderInlineNode(c, &buf)
	}
	return strings.TrimSpace(buf.String())
}

func (w *Writer) renderInlineNode(n ast.Node, buf *strings.Builder) {
	switch node := n.(type) {
	case *ast.Text:
		txt := string(node.Segment.Value(w.source))
		buf.WriteString(w.applyInlineStyle(txt))
	case *ast.String:
		buf.WriteString(w.applyInlineStyle(string(node.Value)))
	case *ast.CodeSpan:
		var content strings.Builder
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				content.Write(t.Segment.Value(w.source))
			}
		}
		buf.WriteString(w.styles.CodeSpan.Render(content.String()))
	case *ast.Emphasis:
		if node.Level == 2 {
			w.pushInline(w.styles.Strong)
		} else {
			w.pushInline(w.styles.Emphasis)
		}
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			w.renderInlineNode(c, buf)
		}
		w.popInline()
	case *ast.Link:
		w.pushInline(w.styles.Link)
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			w.renderInlineNode(c, buf)
		}
		w.popInline()
		if dest := string(node.Destination); dest != "" {
			buf.WriteString(" ")
			buf.WriteString(lipgloss.NewStyle().Faint(true).Render("(" + dest + ")"))
		}
	default:
		// Recurse for unknown inline nodes.
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			w.renderInlineNode(c, buf)
		}
	}
}

// emitTable renders the table with box-drawing borders to w.out.
func (w *Writer) emitTable(td tableData) {
	w.startBlock()

	numCols := len(td.widths)
	if numCols == 0 {
		return
	}

	// Build border lines.
	topBorder := w.tableBorder("┌", "─", "┬", "┐", td.widths)
	midBorder := w.tableBorder("├", "─", "┼", "┤", td.widths)
	botBorder := w.tableBorder("└", "─", "┴", "┘", td.widths)

	w.out.WriteString(topBorder)
	w.out.WriteString("\n")

	// Header row.
	if len(td.headers) > 0 {
		w.out.WriteString(w.tableRow(td.headers, td.widths, td.aligns))
		w.out.WriteString("\n")
		w.out.WriteString(midBorder)
		w.out.WriteString("\n")
	}

	// Data rows.
	for _, row := range td.rows {
		w.out.WriteString(w.tableRow(row, td.widths, td.aligns))
		w.out.WriteString("\n")
	}

	w.out.WriteString(botBorder)
	w.out.WriteString("\n")
	w.needsNewline = true
}

func (w *Writer) tableBorder(left, fill, mid, right string, widths []int) string {
	var b strings.Builder
	b.WriteString(left)
	for i, cw := range widths {
		// +2 for 1-space padding on each side.
		b.WriteString(strings.Repeat(fill, cw+2))
		if i < len(widths)-1 {
			b.WriteString(mid)
		}
	}
	b.WriteString(right)
	return b.String()
}

func (w *Writer) tableRow(cells []string, widths []int, aligns []xast.Alignment) string {
	var b strings.Builder
	b.WriteString("│")
	for i, cw := range widths {
		var cell string
		if i < len(cells) {
			cell = cells[i]
		}
		var align xast.Alignment
		if i < len(aligns) {
			align = aligns[i]
		}
		b.WriteString(" ")
		b.WriteString(padCell(cell, cw, align))
		b.WriteString(" │")
	}
	return b.String()
}

func padCell(cell string, width int, align xast.Alignment) string {
	visWidth := lipgloss.Width(cell)
	if visWidth >= width {
		return cell
	}
	gap := width - visWidth
	switch align {
	case xast.AlignCenter:
		left := gap / 2
		right := gap - left
		return strings.Repeat(" ", left) + cell + strings.Repeat(" ", right)
	case xast.AlignRight:
		return strings.Repeat(" ", gap) + cell
	default: // AlignLeft, AlignNone
		return cell + strings.Repeat(" ", gap)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
