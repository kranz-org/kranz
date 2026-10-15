package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Offsets are UTF-8 boundaries in readable source, never screen coordinates.
// Metadata cells use -1; tabs retain one source span across their screen cells.
type logCell struct {
	text              string
	start, end, width int
}
type logVisualRow struct {
	cells      []logCell
	start, end int
}

func readableLog(raw string) string {
	raw = ansi.Strip(raw)
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.TrimSuffix(raw, "\n")
	var out strings.Builder
	line := []string{}
	cursor := 0
	flush := func() { out.WriteString(strings.Join(line, "")); line = nil; cursor = 0 }
	g := uniseg.NewGraphemes(raw)
	for g.Next() {
		s := g.Str()
		switch s {
		case "\n":
			flush()
			out.WriteByte('\n')
		case "\r":
			cursor = 0
		case "\b":
			cursor = max(0, cursor-1)
		default:
			if s != "\t" && strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) }) {
				continue
			}
			if cursor < len(line) {
				line[cursor] = s
			} else {
				line = append(line, s)
			}
			cursor++
		}
	}
	flush()
	return out.String()
}

func projectLogLine(text, prefix string, width int, wrap bool) []logVisualRow {
	width = max(1, width)
	rows := []logVisualRow{{start: -1, end: -1}}
	column := 0
	add := func(s string, start, end, w int) {
		if wrap && column > 0 && column+w > width {
			rows = append(rows, logVisualRow{start: start, end: start})
			column = 0
		}
		if column+w <= width {
			row := &rows[len(rows)-1]
			row.cells = append(row.cells, logCell{s, start, end, w})
			if start >= 0 {
				if row.start < 0 || len(row.cells) == 1 {
					row.start = start
				}
				row.end = end
			}
		}
		column += w
	}
	g := uniseg.NewGraphemes(prefix)
	for g.Next() {
		add(g.Str(), -1, -1, g.Width())
	}
	g = uniseg.NewGraphemes(text)
	for g.Next() {
		a, b := g.Positions()
		s := g.Str()
		w := g.Width()
		if s == "\t" {
			w = 8 - column%8
			for range w {
				add(" ", a, b, 1)
			}
			continue
		}
		add(s, a, b, w)
	}
	if !wrap && column > width {
		row := &rows[0]
		used := 0
		for _, cell := range row.cells {
			used += cell.width
		}
		for len(row.cells) > 0 && used+1 > width {
			used -= row.cells[len(row.cells)-1].width
			row.cells = row.cells[:len(row.cells)-1]
		}
		row.end = 0
		for _, cell := range row.cells {
			if cell.end >= 0 {
				row.end = cell.end
			}
		}
		row.cells = append(row.cells, logCell{"…", -1, -1, 1})
	}
	return rows
}

func visualRowText(row logVisualRow) string {
	var b strings.Builder
	for _, c := range row.cells {
		b.WriteString(c.text)
	}
	return b.String()
}

func projectedTextRows(text string, width int, wrap bool) []string {
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		for _, row := range projectLogLine(line, "", width, wrap) {
			rows = append(rows, styleLogLine(visualRowText(row)))
		}
	}
	return rows
}

func projectedRowCount(text string, width int, wrap bool) int {
	if !wrap {
		return 1 + strings.Count(text, "\n")
	}
	count := 0
	for _, line := range strings.Split(text, "\n") {
		count += len(projectLogLine(line, "", width, true))
	}
	return count
}
