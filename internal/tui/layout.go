package tui

// layoutRect describes a rectangular region of the terminal.
type layoutRect struct {
	X, Y, Width, Height int
}

// layout holds computed rectangles for all UI regions.
type layout struct {
	Terminal layoutRect
	Header   layoutRect
	Chat     layoutRect
	Input    layoutRect
	Footer   layoutRect
	Compact  bool // true when terminal width < 80
}

// computeLayout calculates all layout rectangles from terminal dimensions.
// inputLines is the current number of lines in the input area (clamped 3-15).
func computeLayout(termWidth, termHeight, inputLines int) layout {
	compact := termWidth < 80

	borderH := 0
	borderW := 0
	if !compact {
		borderH = 2 // top + bottom border
		borderW = 2 // left + right border
	}

	headerH := 1
	footerH := 1
	inputOuterH := inputLines + borderH
	// Chat area has no border, only input does.
	chatH := termHeight - headerH - footerH - inputOuterH
	if chatH < 4 {
		chatH = 4
	}

	innerW := termWidth - borderW
	if innerW < 16 {
		innerW = 16
	}

	y := 0
	header := layoutRect{X: 0, Y: y, Width: termWidth, Height: headerH}
	y += headerH

	chat := layoutRect{X: 0, Y: y, Width: termWidth, Height: chatH}
	y += chatH

	input := layoutRect{X: 0, Y: y, Width: innerW, Height: inputLines}
	y += inputLines + borderH

	footer := layoutRect{X: 0, Y: y, Width: termWidth, Height: footerH}

	return layout{
		Terminal: layoutRect{X: 0, Y: 0, Width: termWidth, Height: termHeight},
		Header:   header,
		Chat:     chat,
		Input:    input,
		Footer:   footer,
		Compact:  compact,
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
