// Package sourceview lays out the configuration source map as text. The
// dashboard's config map and `kranz config sources` both render through it, so
// the two surfaces cannot drift apart in layout, only in colour: the dashboard
// passes its theme styles, the CLI passes none and gets plain text.
package sourceview

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
)

const (
	// serviceNameMaxWidth caps the name column of the service view, so one long
	// name does not push every file off to the right.
	serviceNameMaxWidth = 24
	// minWidth keeps a degenerate width from collapsing every column to zero.
	minWidth = 20
	// overrideHeading leads every override layer in the service view.
	overrideHeading = "  ↳ override"
	// detailIndent sets the rows under a file in from its name.
	detailIndent = "  "
	// itemMarker leads one entry of a list that did not fit on its label's
	// line, as in the service details.
	itemMarker = "  ↳ "
	// sourceMarker ends every branch, so the rails run down from markers
	// rather than from the first letter of a name.
	sourceMarker = "● "
)

// Styles colours the map. A nil field leaves that text plain.
type Styles struct {
	// Muted draws tree rails, summaries, and source kinds.
	Muted func(string) string
	// Strong draws file names and service names.
	Strong func(string) string
	// Label draws detail row labels.
	Label func(string) string
	// Accent draws override layers, the sources that change behaviour.
	Accent func(string) string
	// Warning draws the truncated marker.
	Warning func(string) string
	// Disabled draws the name of a disabled service.
	Disabled func(string) string
}

// Options configures one rendering.
type Options struct {
	// Width is the number of cells every line must fit in.
	Width  int
	Styles Styles
	// Disabled reports whether a service is disabled; nil means none are.
	Disabled func(service string) bool
}

type renderer struct {
	width  int
	styles Styles
}

// fieldGroup is a labelled list of overridden fields.
type fieldGroup struct {
	label  string
	fields []string
}

func newRenderer(options Options) renderer {
	return renderer{width: max(minWidth, options.Width), styles: options.Styles}
}

func paint(style func(string) string, text string) string {
	if style == nil || text == "" {
		return text
	}
	return style(text)
}

// BySource lists the resolved sources in deterministic order as the include tree. Every
// file is named by what it adds below its ancestors, so the tree reads without
// repeating their directories; `--output json` keeps the full paths. Merge
// order is the order of the rows, so no position number is drawn.
func BySource(sourceMap config.SourceMap, options Options) []string {
	r := newRenderer(options)
	sources := sourceMap.Sources
	tree := config.SourceTree(sources)
	parents := parentIDs(sources)
	names := shortNames(sources)
	nameByPath := make(map[string]string, len(sources))
	for _, source := range sources {
		nameByPath[source.DisplayPath] = names[source.ID]
	}
	shortName := func(displayPath string) string {
		if name, ok := nameByPath[displayPath]; ok {
			return name
		}
		return displayPath
	}

	overrides := 0
	for _, contribution := range sourceMap.Contributions {
		overrides += len(contribution.Overrides)
	}
	summary := strings.Join([]string{
		count(len(sources), "source"),
		count(len(sourceMap.ServiceOrder), "service"),
		count(overrides, "override"),
	}, " · ") + " · resolved order; field writes shown below"

	lines := r.intro(summary)
	for index, source := range sources {
		node := tree[source.ID]
		// A later root, such as a global --override layer applied after
		// composition, starts its own block instead of reading as the tail of
		// the tree above it.
		if index > 0 && node.Connector == "" {
			lines = append(lines, "")
		}
		stem := stemRails(node, parents[source.ID])
		lines = append(lines, r.sourceHeader(source, names[source.ID], node, stem)...)
		lines = append(lines, r.contribution(sourceMap.Contributions[source.ID], stem+detailIndent, shortName)...)
	}
	return r.fit(lines)
}

// ByService lists each effective service as one row, name and defining file,
// with the later override layers and their fields beneath it. Field paths
// drop the service name the row already carries.
func ByService(sourceMap config.SourceMap, options Options) []string {
	r := newRenderer(options)
	overridden := 0
	nameWidth := 0
	for _, name := range sourceMap.ServiceOrder {
		nameWidth = max(nameWidth, ansi.StringWidth(serviceHeading(name, options)))
		if len(sourceMap.Services[name].Overrides) > 0 {
			overridden++
		}
	}
	if overridden > 0 {
		// Short names must not push the override heading onto its own line.
		nameWidth = max(nameWidth, ansi.StringWidth(overrideHeading))
	}
	nameWidth = min(nameWidth, serviceNameMaxWidth, max(8, r.width/3))
	valueColumn := nameWidth + 3
	fieldIndent := strings.Repeat(" ", valueColumn+2)
	fullPath := func(displayPath string) string { return displayPath }

	summary := fmt.Sprintf("%s · %d overridden · defining file, then later overrides", count(len(sourceMap.ServiceOrder), "service"), overridden)
	lines := r.intro(summary)
	for _, name := range sourceMap.ServiceOrder {
		origin := sourceMap.Services[name]
		heading := paint(r.styles.Strong, name)
		if options.Disabled != nil && options.Disabled(name) {
			heading = paint(r.styles.Disabled, serviceHeading(name, options))
		}
		lines = append(lines, r.hanging(heading, origin.DefinedIn, valueColumn, nil)...)
		for _, group := range origin.Overrides {
			lines = append(lines, r.hanging(paint(r.styles.Muted, overrideHeading), group.Source, valueColumn, r.styles.Accent)...)
			for _, fields := range groupFields(group.Fields, fullPath, name+".") {
				lines = append(lines, r.list(fieldIndent, fields.label, fields.fields)...)
			}
		}
	}
	return r.fit(lines)
}

// serviceHeading is the unstyled heading of a service row.
func serviceHeading(name string, options Options) string {
	if options.Disabled != nil && options.Disabled(name) {
		return name + " (disabled)"
	}
	return name
}

// fit guarantees the width contract. At any realistic width the layout already
// fits; only a width narrower than the tree's own indentation needs cutting.
func (r renderer) fit(lines []string) []string {
	for index, line := range lines {
		if ansi.StringWidth(line) > r.width {
			lines[index] = ansi.Truncate(line, r.width-1, "") + "…"
		}
	}
	return lines
}

// intro is the view's summary followed by a blank separator line.
func (r renderer) intro(text string) []string {
	lines := make([]string, 0, 2)
	for _, line := range wrap(text, r.width) {
		lines = append(lines, paint(r.styles.Muted, line))
	}
	return append(lines, "")
}

// sourceHeader renders one source: branch, marker, name, and how the source
// joined the merge right after the name, so the eye does not cross the row to
// find it. A wrapped name or a tag pushed onto its own line keeps the stem, so
// a branch is never cut.
func (r renderer) sourceHeader(source config.ConfigSource, name string, node config.SourceTreeNode, stem string) []string {
	lead := node.Rails + node.Connector + sourceMarker
	leadWidth := ansi.StringWidth(lead)
	continuation := paint(r.styles.Muted, stem) + strings.Repeat(" ", max(0, leadWidth-ansi.StringWidth(stem)))

	nameLines := wrap(name, max(1, r.width-leadWidth))
	header := make([]string, 0, len(nameLines)+1)
	for index, part := range nameLines {
		prefix := continuation
		if index == 0 {
			prefix = paint(r.styles.Muted, lead)
		}
		header = append(header, prefix+paint(r.styles.Strong, part))
	}

	tags, tagsWidth := r.sourceTags(source)
	if tagsWidth == 0 {
		return header
	}
	last := len(header) - 1
	if ansi.StringWidth(header[last])+2+tagsWidth <= r.width {
		header[last] += "  " + tags
		return header
	}
	return append(header, continuation+tags)
}

// sourceTags marks how a source joined the merge, in parentheses like a
// disabled service's marker; square brackets would read as a key hint. An
// include says nothing the tree does not, so it goes unmarked, and an override
// layer is accented because it changes behaviour.
func (r renderer) sourceTags(source config.ConfigSource) (string, int) {
	styled := make([]string, 0, 2)
	plain := make([]string, 0, 2)
	if tag := kindTag(source.Kind); tag != "" {
		style := r.styles.Muted
		if source.Kind == config.SourceOverride {
			style = r.styles.Accent
		}
		styled, plain = append(styled, paint(style, tag)), append(plain, tag)
	}
	if source.Truncated {
		styled, plain = append(styled, paint(r.styles.Warning, "(truncated)")), append(plain, "(truncated)")
	}
	return strings.Join(styled, " "), ansi.StringWidth(strings.Join(plain, " "))
}

// kindTag names a source kind after the include key that pulled the file in,
// so a glob match and a discovery scan read as the `glob:` and `discover:`
// the reader wrote.
func kindTag(kind config.ConfigSourceKind) string {
	switch kind {
	case config.SourceNestedInclude:
		return ""
	case config.SourceGlob:
		return "(via glob)"
	case config.SourceDiscovery:
		return "(via discover)"
	default:
		return "(" + kind.Label() + ")"
	}
}

func (r renderer) contribution(contribution config.SourceContribution, rails string, name func(string) string) []string {
	lines := make([]string, 0, 1+len(contribution.Overrides))
	if len(contribution.Services) > 0 {
		lines = append(lines, r.list(rails, "services", contribution.Services)...)
	}
	for _, group := range groupFields(contribution.Overrides, name, "") {
		lines = append(lines, r.list(rails, group.label, group.fields)...)
	}
	if len(lines) == 0 {
		lines = append(lines, paint(r.styles.Muted, rails+"no services"))
	}
	return lines
}

// groupFields splits overridden fields by the file whose value each replaced,
// in first-seen order, so the file is named once in the label instead of
// beside every field: "overrides <file>" for those, "sets" for fields no
// earlier file had set. trim drops a prefix the row already carries. Fields
// are sorted within a group, so both views list them in the same order.
func groupFields(overrides []config.FieldOverride, name func(string) string, trim string) []fieldGroup {
	groups := make([]fieldGroup, 0, 2)
	index := make(map[string]int, 2)
	for _, override := range overrides {
		label := "sets"
		if override.ReplacedSource != "" {
			label = "overrides " + name(override.ReplacedSource)
		}
		position, ok := index[label]
		if !ok {
			position = len(groups)
			index[label] = position
			groups = append(groups, fieldGroup{label: label})
		}
		groups[position].fields = append(groups[position].fields, strings.TrimPrefix(override.Field, trim))
	}
	for _, group := range groups {
		sort.Strings(group.fields)
	}
	// "sets" goes last in both views, whatever order provenance arrived in.
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].label != "sets" && groups[j].label == "sets" })
	return groups
}

// list puts items after "label:" when they all fit on one line, otherwise the
// label alone with one item per line beneath it, as the service details do.
func (r renderer) list(rails, label string, items []string) []string {
	heading := paint(r.styles.Muted, rails) + paint(r.styles.Label, label+":")
	joined := strings.Join(items, ", ")
	if ansi.StringWidth(rails+label+": "+joined) <= r.width {
		return []string{heading + " " + joined}
	}
	lead := paint(r.styles.Muted, rails+itemMarker)
	indent := paint(r.styles.Muted, rails) + strings.Repeat(" ", ansi.StringWidth(itemMarker))
	column := ansi.StringWidth(rails + itemMarker)
	lines := []string{heading}
	for _, item := range items {
		lines = append(lines, r.value(lead, indent, column, item, nil)...)
	}
	return lines
}

// value wraps text into the column after lead. Continuation lines start with
// indent, which is as wide as lead and carries the same tree rails, so the
// value stays aligned and a branch is never cut by a wrapped line.
func (r renderer) value(lead, indent string, column int, text string, style func(string) string) []string {
	wrapped := wrap(text, max(1, r.width-column))
	lines := make([]string, 0, len(wrapped))
	for index, part := range wrapped {
		if index > 0 {
			lead = indent
		}
		lines = append(lines, lead+paint(style, part))
	}
	return lines
}

// hanging puts heading in the left column and wraps text in the column at
// valueColumn. A heading wider than the column gets a line of its own so the
// values of every row stay aligned.
func (r renderer) hanging(heading, text string, valueColumn int, style func(string) string) []string {
	headingWidth := ansi.StringWidth(heading)
	indent := strings.Repeat(" ", valueColumn)
	if headingWidth+1 > valueColumn {
		return append([]string{heading}, r.value(indent, indent, valueColumn, text, style)...)
	}
	return r.value(heading+strings.Repeat(" ", valueColumn-headingWidth), indent, valueColumn, text, style)
}

// parentIDs marks the sources at least one other source hangs off, using the
// same parent rule as config.SourceTree.
func parentIDs(sources []config.ConfigSource) map[string]bool {
	known := make(map[string]bool, len(sources))
	for _, source := range sources {
		known[source.ID] = true
	}
	parents := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.ParentID != "" && known[source.ParentID] {
			parents[source.ParentID] = true
		}
	}
	return parents
}

// stemRails is the tree prefix under a source, as wide as its header's lead:
// the rails its children inherit, plus a stem under its marker down to its
// first child when it has one.
func stemRails(node config.SourceTreeNode, hasChildren bool) string {
	rails := node.Rails
	if node.Connector != "" {
		if node.Last {
			rails += "  "
		} else {
			rails += "│ "
		}
	}
	if hasChildren {
		return rails + "│ "
	}
	return rails + "  "
}

// shortNames names every source by what it adds to the tree: the path below
// the directory of the nearest ancestor that contains it, without a kranz.yaml
// base name, which only repeats that the directory holds a configuration.
// Names that collide fall back to the full display path, so a name, and every
// "overrides" label that points at it, always means one file.
func shortNames(sources []config.ConfigSource) map[string]string {
	byID := make(map[string]config.ConfigSource, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	names := make(map[string]string, len(sources))
	uses := make(map[string]int, len(sources))
	for _, source := range sources {
		trimmed := ""
		parent, ok := byID[source.ParentID]
		// The step bound guards against a cyclic parent chain.
		for step := 0; ok && step < len(sources); step++ {
			if dir := sharedDir(source.DisplayPath, parent.DisplayPath); len(dir) > len(trimmed) {
				trimmed = dir
			}
			parent, ok = byID[parent.ParentID]
		}
		name := strings.TrimPrefix(source.DisplayPath, trimmed)
		if dir, base := path.Split(name); dir != "" && (base == "kranz.yaml" || base == "kranz.yml") {
			name = strings.TrimSuffix(dir, "/")
		}
		names[source.ID] = name
		uses[name]++
	}
	for _, source := range sources {
		if uses[names[source.ID]] > 1 {
			names[source.ID] = source.DisplayPath
		}
	}
	return names
}

// sharedDir is the directory prefix path shares with the file that included
// it, including the trailing slash; empty when there is none.
func sharedDir(path, parentPath string) string {
	slash := strings.LastIndexByte(parentPath, '/')
	if slash < 0 {
		return ""
	}
	dir := parentPath[:slash+1]
	if !strings.HasPrefix(path, dir) {
		return ""
	}
	return dir
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// wrap breaks text at word, slash, and comma boundaries, hard-wrapping only a
// segment that is wider than the whole line.
func wrap(text string, width int) []string {
	wordWrapped := strings.Split(ansi.Wordwrap(text, width, "/,"), "\n")
	lines := make([]string, 0, len(wordWrapped))
	for _, line := range wordWrapped {
		if ansi.StringWidth(line) <= width {
			lines = append(lines, line)
			continue
		}
		lines = append(lines, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	return lines
}
