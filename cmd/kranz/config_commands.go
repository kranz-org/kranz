package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
)

// config show and config explain answer two different questions about a
// layered configuration: what Kranz ended up with, and which file put it
// there. Both read the same layers the loader reads.

// secretKeyFragments identify environment variables whose value must never be
// printed. Redaction is by key rather than by value shape, because a password
// is not distinguishable from any other string once it is out of context.
var secretKeyFragments = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY", "PRIVATE_KEY", "ACCESS_KEY", "CREDENTIAL", "AUTH", "SESSION_KEY", "SIGNING", "DSN"}

func isSecretKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, fragment := range secretKeyFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}

func runConfigShow(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	provenance := false
	for _, arg := range args {
		if arg != "--provenance" {
			return &kranzcli.Error{Code: "invalid_arguments", Message: fmt.Sprintf("unknown config show option %q", arg), Hint: "The only option is --provenance.", ExitCode: kranzcli.ExitUsage}
		}
		provenance = true
	}
	cfg, paths, err := loadProject(options)
	if err != nil {
		return err
	}
	document, err := effectiveDocument(cfg)
	if err != nil {
		return err
	}
	if options.Output == kranzcli.OutputJSON {
		var plain any
		if err := document.Decode(&plain); err != nil {
			return err
		}
		return kranzcli.WriteJSON(stdout, plain)
	}
	if provenance {
		sources, err := fieldSources(paths)
		if err != nil {
			return err
		}
		annotateProvenance(document, nil, sources)
	}
	encoder := yaml.NewEncoder(stdout)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return err
	}
	return encoder.Close()
}

// effectiveDocument renders the merged configuration with secrets redacted and
// declaration order restored. Encoding the struct alone would sort the service
// and action maps alphabetically, which discards ordering the configuration
// deliberately expresses.
func effectiveDocument(cfg *config.Config) (*yaml.Node, error) {
	var document yaml.Node
	if err := document.Encode(cfg); err != nil {
		return nil, err
	}
	root := &document
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	orderMapping(mappingValue(root, "services"), cfg.ServiceOrder)
	for _, name := range cfg.ServiceNames() {
		service := mappingValue(mappingValue(root, "services"), name)
		orderMapping(mappingValue(service, "actions"), cfg.Services[name].ActionOrder)
	}
	orderMapping(mappingValue(root, "action_groups"), cfg.ActionGroupOrder)
	for _, name := range cfg.ActionGroupNames() {
		group := mappingValue(mappingValue(root, "action_groups"), name)
		orderMapping(mappingValue(group, "actions"), cfg.ActionGroups[name].ActionOrder)
	}
	redactEnvironment(root)
	return root, nil
}

// mappingValue returns the value node for key in a mapping, or nil.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

// orderMapping rewrites a mapping's key/value pairs into the declared order.
// Keys the order does not mention keep their relative position at the end,
// so an ordering that has fallen behind the configuration still renders.
func orderMapping(mapping *yaml.Node, order []string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode || len(order) == 0 {
		return
	}
	remaining := make(map[string]*yaml.Node, len(mapping.Content)/2)
	var keyNodes = make(map[string]*yaml.Node, len(mapping.Content)/2)
	var seen []string
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index].Value
		keyNodes[key] = mapping.Content[index]
		remaining[key] = mapping.Content[index+1]
		seen = append(seen, key)
	}
	ordered := make([]*yaml.Node, 0, len(mapping.Content))
	placed := make(map[string]bool, len(order))
	for _, key := range order {
		if value, ok := remaining[key]; ok {
			ordered = append(ordered, keyNodes[key], value)
			placed[key] = true
		}
	}
	for _, key := range seen {
		if !placed[key] {
			ordered = append(ordered, keyNodes[key], remaining[key])
		}
	}
	mapping.Content = ordered
}

// redactEnvironment replaces the value of every secret-looking environment
// variable anywhere in the document. It walks the whole tree because env
// mappings appear under defaults, services, action groups, and actions.
func redactEnvironment(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Value == "env" && value.Kind == yaml.MappingNode {
				for envIndex := 0; envIndex+1 < len(value.Content); envIndex += 2 {
					if isSecretKey(value.Content[envIndex].Value) {
						value.Content[envIndex+1].Value = "[redacted]"
						value.Content[envIndex+1].Tag = "!!str"
						value.Content[envIndex+1].Style = 0
					}
				}
				continue
			}
			redactEnvironment(value)
		}
		return
	}
	for _, child := range node.Content {
		redactEnvironment(child)
	}
}

// annotateProvenance writes the file that last set each leaf as a line comment,
// so `config show --provenance` reads as the effective file plus its sources.
func annotateProvenance(node *yaml.Node, path []string, sources map[string]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			childPath := append(append([]string(nil), path...), key.Value)
			if value.Kind == yaml.MappingNode || value.Kind == yaml.SequenceNode {
				annotateProvenance(value, childPath, sources)
				continue
			}
			if source, ok := sources[strings.Join(childPath, ".")]; ok {
				key.LineComment = "from " + source
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			annotateProvenance(child, path, sources)
		}
	}
}

// fieldSources maps a dotted field path to the last layer that set it. Layers
// are read raw, in merge order, because provenance is a question about the
// files rather than about the merged result.
func fieldSources(paths []string) (map[string]string, error) {
	sources := make(map[string]string)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			// A Procfile is not YAML. It defines whole services rather than
			// individual fields, so it is recorded at the service level by the
			// caller's fallback rather than parsed here.
			continue
		}
		root := &document
		if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
			root = root.Content[0]
		}
		recordSources(root, nil, filepath.Base(path), sources)
	}
	return sources, nil
}

func recordSources(node *yaml.Node, path []string, file string, sources map[string]string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		childPath := append(append([]string(nil), path...), key.Value)
		joined := strings.Join(childPath, ".")
		sources[joined] = file
		if value.Kind == yaml.MappingNode {
			recordSources(value, childPath, file, sources)
		}
	}
}

func runConfigExplain(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "config explain accepts at most one service", ExitCode: kranzcli.ExitUsage}
	}
	cfg, paths, err := loadProject(options)
	if err != nil {
		return err
	}
	sources, err := fieldSources(paths)
	if err != nil {
		return err
	}

	prefix := ""
	if len(args) == 1 {
		if _, ok := cfg.Services[args[0]]; !ok {
			return &kranzcli.Error{
				Code:     "service_not_found",
				Message:  fmt.Sprintf("service %q was not found", args[0]),
				Hint:     "Run `kranz list services` to see what this project defines.",
				ExitCode: kranzcli.ExitNotFound,
			}
		}
		prefix = "services." + args[0] + "."
	}

	type entry struct {
		Field  string `json:"field"`
		Source string `json:"source"`
	}
	entries := make([]entry, 0, len(sources))
	for field, source := range sources {
		if prefix != "" && !strings.HasPrefix(field, prefix) {
			continue
		}
		entries = append(entries, entry{field, source})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Field < entries[j].Field })

	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, entries)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(stdout, "No layered fields to explain.")
		return nil
	}
	// One layer means every field comes from the same file, which is worth
	// saying once rather than repeating on every row.
	if len(paths) == 1 {
		_, _ = fmt.Fprintf(stdout, "All fields come from %s.\n\n", filepath.Base(paths[0]))
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "FIELD\tSET BY")
	for _, item := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", item.Field, item.Source)
	}
	return w.Flush()
}
