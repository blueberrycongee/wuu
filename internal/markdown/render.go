package markdown

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
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
	aligns  []xast.Alignment
	numCols int
}

// collectTable walks a Table node and extracts cell content. Width
// allocation happens later in emitTable so it can take terminal width
// into account.
func (w *Writer) collectTable(table *xast.Table) tableData {
	td := tableData{
		aligns: table.Alignments,
	}

	for child := table.FirstChild(); child != nil; child = child.NextSibling() {
		var cells []string
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

	td.numCols = len(td.headers)
	for _, row := range td.rows {
		if len(row) > td.numCols {
			td.numCols = len(row)
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

const (
	// minColumnWidth is the smallest a column can be after allocation.
	minColumnWidth = 3
	// safetyMargin reserves a few columns to absorb terminal-resize
	// races and prevent flicker. Mirrors CC's SAFETY_MARGIN.
	tableSafetyMargin = 2
	// maxRowLines is the threshold above which we switch from
	// horizontal box-drawing to a vertical key:value layout.
	maxRowLines = 4
)

// emitTable renders the table to w.out, choosing between horizontal
// (box-drawing) and vertical (key:value) layouts based on whether the
// content fits the available width.
func (w *Writer) emitTable(td tableData) {
	w.startBlock()
	if td.numCols == 0 || (len(td.headers) == 0 && len(td.rows) == 0) {
		return
	}

	// 1. Compute min (longest single token) and ideal (full content)
	// widths per column. Min lets us know how narrow we can squeeze.
	minWidths := make([]int, td.numCols)
	idealWidths := make([]int, td.numCols)

	measure := func(cells []string) {
		for i, c := range cells {
			if i >= td.numCols {
				continue
			}
			id := lipgloss.Width(c)
			if id > idealWidths[i] {
				idealWidths[i] = id
			}
			mn := longestWordWidth(c)
			if mn > minWidths[i] {
				minWidths[i] = mn
			}
		}
	}
	measure(td.headers)
	for _, row := range td.rows {
		measure(row)
	}
	for i := range minWidths {
		if minWidths[i] < minColumnWidth {
			minWidths[i] = minColumnWidth
		}
		if idealWidths[i] < minWidths[i] {
			idealWidths[i] = minWidths[i]
		}
	}

	// 2. Compute available content width (terminal width minus borders).
	// Borders consume: leading "│" + per-col (" cell " + "│") = 1 + 3*N.
	borderOverhead := 1 + 3*td.numCols
	available := w.width - borderOverhead - tableSafetyMargin
	if available < td.numCols*minColumnWidth {
		available = td.numCols * minColumnWidth
	}

	// 3. Allocate column widths via the 3-tier strategy:
	//    a) Everything fits at ideal: use ideal widths.
	//    b) min fits but ideal doesn't: distribute extra proportionally.
	//    c) Even min doesn't fit: hard-wrap (scale all columns down).
	totalIdeal := sumInts(idealWidths)
	totalMin := sumInts(minWidths)

	var widths []int
	hardWrap := false
	switch {
	case totalIdeal <= available:
		widths = idealWidths
	case totalMin <= available:
		extra := available - totalMin
		widths = make([]int, td.numCols)
		copy(widths, minWidths)
		// Distribute the extra space in proportion to (ideal - min).
		overflows := make([]int, td.numCols)
		totalOverflow := 0
		for i := range widths {
			overflows[i] = idealWidths[i] - minWidths[i]
			totalOverflow += overflows[i]
		}
		if totalOverflow > 0 {
			given := 0
			for i := range widths {
				bonus := overflows[i] * extra / totalOverflow
				widths[i] += bonus
				given += bonus
			}
			// Distribute any remainder to the first columns.
			rem := extra - given
			for i := 0; i < td.numCols && rem > 0; i++ {
				if widths[i] < idealWidths[i] {
					widths[i]++
					rem--
				}
			}
		}
	default:
		hardWrap = true
		widths = make([]int, td.numCols)
		// Scale min widths down proportionally.
		for i := range widths {
			widths[i] = minWidths[i] * available / totalMin
			if widths[i] < minColumnWidth {
				widths[i] = minColumnWidth
			}
		}
	}

	// 4. Wrap each row's cells. If any row produces too many lines and
	// we have terminal width to spare, fall back to vertical layout.
	wrappedHeader := wrapCells(td.headers, widths, hardWrap)
	// Apply bold style to header cell content. This is done after
	// wrapping so each wrapped line gets its own bold envelope and
	// cell padding (added later in renderWrappedRow) stays unstyled.
	for i := range wrappedHeader {
		for j, line := range wrappedHeader[i] {
			if line == "" {
				continue
			}
			wrappedHeader[i][j] = w.styles.Strong.Render(line)
		}
	}
	wrappedRows := make([][][]string, 0, len(td.rows))
	maxLinesInAnyRow := 0
	for _, row := range td.rows {
		wr := wrapCells(row, widths, hardWrap)
		wrappedRows = append(wrappedRows, wr)
		for _, lines := range wr {
			if len(lines) > maxLinesInAnyRow {
				maxLinesInAnyRow = len(lines)
			}
		}
	}

	// Vertical fallback: if any row wraps to more than maxRowLines,
	// the table is too cramped for horizontal — switch to key:value.
	if maxLinesInAnyRow > maxRowLines {
		w.emitTableVertical(td)
		return
	}

	// 5. Render horizontal table.
	topBorder := w.tableBorder("┌", "─", "┬", "┐", widths)
	midBorder := w.tableBorder("├", "─", "┼", "┤", widths)
	botBorder := w.tableBorder("└", "─", "┴", "┘", widths)

	w.out.WriteString(topBorder)
	w.out.WriteString("\n")

	if len(td.headers) > 0 {
		w.out.WriteString(w.renderWrappedRow(wrappedHeader, widths, td.aligns, true))
		w.out.WriteString(midBorder)
		w.out.WriteString("\n")
	}

	for _, row := range wrappedRows {
		w.out.WriteString(w.renderWrappedRow(row, widths, td.aligns, false))
	}

	w.out.WriteString(botBorder)
	w.out.WriteString("\n")
	w.needsNewline = true
}

// emitTableVertical renders the table as a key:value list, used when
// the terminal is too narrow for a useful horizontal layout.
func (w *Writer) emitTableVertical(td tableData) {
	sep := strings.Repeat("─", min(w.width, 40))
	w.out.WriteString(sep)
	w.out.WriteString("\n")
	for _, row := range td.rows {
		for i, cell := range row {
			label := ""
			if i < len(td.headers) {
				label = td.headers[i]
			} else {
				label = fmt.Sprintf("col%d", i+1)
			}
			lines := wrapAnsi(cell, w.width-len(label)-2, false)
			if len(lines) == 0 {
				lines = []string{""}
			}
			w.out.WriteString(label)
			w.out.WriteString(": ")
			w.out.WriteString(lines[0])
			w.out.WriteString("\n")
			indent := strings.Repeat(" ", len(label)+2)
			for _, line := range lines[1:] {
				w.out.WriteString(indent)
				w.out.WriteString(line)
				w.out.WriteString("\n")
			}
		}
		w.out.WriteString(sep)
		w.out.WriteString("\n")
	}
	w.needsNewline = true
}

// wrapCells wraps each cell in cells to its allocated width and
// returns one []string per cell (one entry per wrapped line).
func wrapCells(cells []string, widths []int, hardWrap bool) [][]string {
	out := make([][]string, len(widths))
	for i := range widths {
		var content string
		if i < len(cells) {
			content = cells[i]
		}
		out[i] = wrapAnsi(content, widths[i], hardWrap)
		if len(out[i]) == 0 {
			out[i] = []string{""}
		}
	}
	return out
}

// renderWrappedRow renders a multi-line row using the wrapped cell
// content. Each output line covers all columns with proper padding
// and box-drawing borders.
func (w *Writer) renderWrappedRow(wrapped [][]string, widths []int, aligns []xast.Alignment, isHeader bool) string {
	maxLines := 0
	for _, lines := range wrapped {
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
	}
	if maxLines == 0 {
		maxLines = 1
	}

	var b strings.Builder
	for line := 0; line < maxLines; line++ {
		b.WriteString("│")
		for i, cw := range widths {
			var content string
			if line < len(wrapped[i]) {
				content = wrapped[i][line]
			}
			align := xast.AlignNone
			if isHeader {
				align = xast.AlignCenter
			} else if i < len(aligns) {
				align = aligns[i]
			}
			b.WriteString(" ")
			b.WriteString(padCell(content, cw, align))
			b.WriteString(" │")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (w *Writer) tableBorder(left, fill, mid, right string, widths []int) string {
	var b strings.Builder
	b.WriteString(left)
	for i, cw := range widths {
		b.WriteString(strings.Repeat(fill, cw+2))
		if i < len(widths)-1 {
			b.WriteString(mid)
		}
	}
	b.WriteString(right)
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

// wrapAnsi wraps a (possibly ANSI-styled) string to the given width
// using reflow's ANSI-aware wrappers, then post-processes the output
// so that ANSI escape sequences open before a wrap point are
// re-emitted at the start of the next line. This preserves styled
// spans (bold/italic/color) across wrapped lines.
//
// In soft mode (hard == false) breaks happen at word boundaries; in
// hard mode words longer than width are broken inside.
func wrapAnsi(text string, width int, hard bool) []string {
	if width <= 0 {
		return []string{text}
	}
	if text == "" {
		return []string{""}
	}
	if lipgloss.Width(text) <= width {
		return []string{text}
	}

	// First pass: soft word-wrap. wordwrap is ANSI-aware (doesn't
	// count escapes toward width) and never breaks inside words.
	soft := wordwrap.String(text, width)

	// Second pass: hard wrap any line that's still too wide (only
	// when the caller explicitly asked for hard wrapping).
	wrapped := soft
	if hard {
		wrapped = wrap.String(soft, width)
	}

	lines := splitNonEmpty(wrapped)
	return restoreAnsiAcrossLines(lines)
}

// splitNonEmpty splits on \n and drops trailing empty lines so an
// empty cell still produces []string{""} (one empty visible row).
func splitNonEmpty(s string) []string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// restoreAnsiAcrossLines walks a sequence of pre-wrapped lines and
// for each line: tracks the ANSI escape sequences active at its end,
// then prepends those sequences to the start of the next line so the
// styling continues. Each line that ends with active styles gets a
// reset (\x1b[0m) appended so cell padding (spaces) is unstyled.
//
// This is a simplified ANSI tracker: it recognizes SGR (Select Graphic
// Rendition) sequences of the form \x1b[...m and treats \x1b[0m as a
// full reset. It does not handle non-SGR escape sequences (cursor
// moves, OSC, etc.) — those aren't expected inside markdown cell
// content.
func restoreAnsiAcrossLines(lines []string) []string {
	if len(lines) <= 1 {
		return lines
	}
	out := make([]string, len(lines))
	var active []string // currently-open SGR codes from previous lines

	for i, line := range lines {
		prefix := strings.Join(active, "")
		// Scan this line to update the active set.
		newActive := append([]string(nil), active...)
		var b strings.Builder
		b.WriteString(prefix)
		j := 0
		for j < len(line) {
			if j+1 < len(line) && line[j] == 0x1b && line[j+1] == '[' {
				// SGR sequence: find the terminating 'm'.
				end := j + 2
				for end < len(line) && line[end] != 'm' {
					end++
				}
				if end < len(line) {
					seq := line[j : end+1]
					b.WriteString(seq)
					if seq == "\x1b[0m" || seq == "\x1b[m" {
						// Full reset.
						newActive = nil
					} else {
						newActive = append(newActive, seq)
					}
					j = end + 1
					continue
				}
			}
			b.WriteByte(line[j])
			j++
		}
		// If there are still active styles at end of line, append a
		// reset so cell padding doesn't inherit the style.
		if len(newActive) > 0 {
			b.WriteString("\x1b[0m")
		}
		out[i] = b.String()
		active = newActive
	}
	return out
}

// longestWordWidth returns the display width of the widest contiguous
// non-whitespace token in s. Used as the minimum column width when
// soft-wrapping.
func longestWordWidth(s string) int {
	max := 0
	cur := 0
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur > max {
				max = cur
			}
			cur = 0
			continue
		}
		cur += lipgloss.Width(string(r))
	}
	if cur > max {
		max = cur
	}
	return max
}

func sumInts(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
