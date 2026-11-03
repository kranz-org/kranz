package ui

import (
	"regexp"
	"sort"
	"strings"
)

var logSGR = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// Only graphic rendition survives a command. Cursor movement, OSC, clipboard
// sequences, alternate-screen commands and other terminal controls are removed.
func styledReadableLog(raw string) string {
	var out strings.Builder
	start := 0
	for _, span := range logSGR.FindAllStringIndex(raw, -1) {
		out.WriteString(readableLog(raw[start:span[0]]))
		out.WriteString(raw[span[0]:span[1]])
		start = span[1]
	}
	out.WriteString(readableLog(raw[start:]))
	return out.String()
}

type logStyleSpan struct {
	offset int
	style  string
}

func logStyleSpans(line string) (string, []logStyleSpan) {
	var text strings.Builder
	var spans []logStyleSpan
	start, active := 0, ""
	for _, span := range logSGR.FindAllStringIndex(line, -1) {
		text.WriteString(line[start:span[0]])
		sequence := line[span[0]:span[1]]
		parameters := sequence[2 : len(sequence)-1]
		reset := parameters == ""
		for _, code := range strings.Split(parameters, ";") {
			if code == "0" {
				reset = true
			}
		}
		if reset {
			active = ""
		}
		if parameters != "" && parameters != "0" {
			// Bound state even for a pathological sequence with no reset.
			if len(active)+len(sequence) > 4096 {
				active = ""
			}
			active += sequence
		}
		spans = append(spans, logStyleSpan{text.Len(), active})
		start = span[1]
	}
	text.WriteString(line[start:])
	return text.String(), spans
}

func styledVisualLogRow(row logVisualRow, spans []logStyleSpan) string {
	var out strings.Builder
	active := ""
	for _, cell := range row.cells {
		style := ""
		if cell.start >= 0 {
			index := sort.Search(len(spans), func(i int) bool { return spans[i].offset > cell.start }) - 1
			if index >= 0 {
				style = spans[index].style
			}
		}
		if style != active {
			if active != "" {
				out.WriteString("\x1b[0m")
			}
			out.WriteString(style)
			active = style
		}
		out.WriteString(cell.text)
	}
	if active != "" {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

func projectedStyledLogRows(line string, width int, wrap bool) []string {
	plain, spans := logStyleSpans(line)
	var result []string
	for _, row := range projectLogLine(plain, "", width, wrap) {
		result = append(result, styledVisualLogRow(row, spans))
	}
	return result
}

func hasLogStyle(line string) bool { return logSGR.MatchString(line) }
