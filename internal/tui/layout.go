package tui

// layoutRect describes a rectangular region of the terminal.
type layoutRect struct {
	X, Y, Width, Height int
}

// layoutZone identifies a semantic region of the fullscreen TUI.
// Aligned with Claude Code's FullscreenLayout topology:
//   - scrollable: main transcript content (scrolls with history)
//   - bottom: fixed bottom pane (input, image bar, panels)
//   - overlay: rendered on top of scrollable but still in scroll context
//   - modal: floating pane above everything (resume picker, ask_user)
//   - bottomFloat: corner elements that float over scroll area
type layoutZone int

const (
	zoneScrollable layoutZone = iota
	zoneBottom
	zoneOverlay
	zoneModal
	zoneBottomFloat
)

// layout holds computed rectangles for all UI regions.
type layout struct {
	Terminal layoutRect
	Header   layoutRect
	Chat     layoutRect // scrollable content area
	Input    layoutRect
	Compact  bool // true when terminal width < 80

	// Bottom pane aggregate height (input + separators + panels + image bar).
	// Used to compute the scrollable region independently.
	BottomPaneHeight int
}

// computeLayout calculates all layout rectangles from terminal dimensions.
// inputLines is the current number of lines in the input area (clamped 3-15).
// workerPanelLines is the height of the optional worker activity panel
// (0 when no workers are active).
// imageBarLines is 1 when images are attached, 0 otherwise.
func computeLayout(termWidth, termHeight, inputLines, workerPanelLines, imageBarLines int) layout {
	compact := termWidth < 80

	headerH := 1
	inputOuterH := inputLines
	sepH := 1 // chat ↔ input separator
	if workerPanelLines > 0 {
		sepH++ // extra separator above worker panel
	}
	// Reserve exactly one line below the viewport for the inline status
	// indicator (Generating/Running <tool>/Thinking). Keeping it outside
	// the viewport is what prevents the spinner animation from forcing a
	// full viewport rebuild on every frame — see renderInlineStatus usage
	// in Model.View and the inlineSpinMsg handler.
	inlineStatusH := 1

	// ── FullscreenLayout-style bottom pane height ──
	// Everything that stays fixed at the bottom: inline status, separators,
	// worker/process panels, image bar, input.
	bottomH := inlineStatusH + workerPanelLines + imageBarLines + inputOuterH
	if workerPanelLines > 0 {
		bottomH += 1 // sep above worker panel
	}
	bottomH += 1 // main chat↔bottom separator

	chatH := termHeight - headerH - bottomH
	if chatH < 4 {
		chatH = 4
	}

	innerW := termWidth
	if innerW < 16 {
		innerW = 16
	}

	y := 0
	header := layoutRect{X: 0, Y: y, Width: termWidth, Height: headerH}
	y += headerH

	chat := layoutRect{X: 0, Y: y, Width: termWidth, Height: chatH}
	y += chatH + workerPanelLines

	input := layoutRect{X: 0, Y: y, Width: innerW, Height: inputLines}

	return layout{
		Terminal:         layoutRect{X: 0, Y: 0, Width: termWidth, Height: termHeight},
		Header:           header,
		Chat:             chat,
		Input:            input,
		Compact:          compact,
		BottomPaneHeight: bottomH,
	}
}

// clampInputLines clamps the input line count to [3, maxLines].
func clampInputLines(lines, maxLines int) int {
	if maxLines <= 0 {
		maxLines = 15
	}
	if lines < 3 {
		return 3
	}
	if lines > maxLines {
		return maxLines
	}
	return lines
}
