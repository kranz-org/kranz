package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Frame assembly. Lipgloss joins blocks by measuring the display width of every
// line it is handed, which means a dashboard gets measured once to sit its two
// columns side by side, again to stack the header and footer onto them, and a
// third time to fit the result to the terminal. A frameBlock carries the width
// it already knows, so the frame is measured once on the way in and never
// again.

// frameBlock is a rectangle of rendered lines whose display width is known.
type frameBlock struct {
	lines []string
	width int
}

// measureBlock splits rendered output into lines and pads them to a common
// width, which is the one place the block pays for measurement.
func measureBlock(content string) frameBlock {
	lines := strings.Split(content, "\n")
	widths := make([]int, len(lines))
	width := 0
	for index, line := range lines {
		widths[index] = ansi.StringWidth(line)
		width = max(width, widths[index])
	}
	for index, line := range lines {
		if widths[index] < width {
			lines[index] = line + strings.Repeat(" ", width-widths[index])
		}
	}
	return frameBlock{lines: lines, width: width}
}

// joinBlocksHorizontal places blocks side by side, top aligned, the way
// lipgloss.JoinHorizontal(lipgloss.Top, …) does.
func joinBlocksHorizontal(blocks ...frameBlock) frameBlock {
	height, width := 0, 0
	for _, block := range blocks {
		height = max(height, len(block.lines))
		width += block.width
	}
	lines := make([]string, height)
	var row strings.Builder
	for index := range height {
		row.Reset()
		for _, block := range blocks {
			if index < len(block.lines) {
				row.WriteString(block.lines[index])
				continue
			}
			row.WriteString(strings.Repeat(" ", block.width))
		}
		lines[index] = row.String()
	}
	return frameBlock{lines: lines, width: width}
}

// joinBlocksVertical stacks blocks, left aligned, the way
// lipgloss.JoinVertical(lipgloss.Left, …) does.
func joinBlocksVertical(blocks ...frameBlock) frameBlock {
	width, height := 0, 0
	for _, block := range blocks {
		width = max(width, block.width)
		height += len(block.lines)
	}
	lines := make([]string, 0, height)
	for _, block := range blocks {
		for _, line := range block.lines {
			if block.width < width {
				line += strings.Repeat(" ", width-block.width)
			}
			lines = append(lines, line)
		}
	}
	return frameBlock{lines: lines, width: width}
}

func (b frameBlock) String() string { return strings.Join(b.lines, "\n") }
