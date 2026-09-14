package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// applyConfigPatch applies one typed YAML merge patch. Null deletes a value,
// sequences replace sequences, an empty mapping clears a mapping, and a
// non-empty mapping merges recursively. Changing a value's structural type is
// rejected instead of depending on decoder coercion.
func applyConfigPatch(base *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	expanded, err := expandPatchEnvironment(data, filepath.Dir(path))
	if err != nil {
		return err
	}
	var patch yaml.Node
	if err := yaml.Unmarshal(expanded, &patch); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	patchRoot, err := yamlMappingRoot(&patch)
	if err != nil {
		return err
	}
	for _, forbidden := range []string{"defaults", "include", "overrides", "protected"} {
		if mappingValue(patchRoot, forbidden) != nil {
			return fmt.Errorf("override files cannot declare %s", forbidden)
		}
	}
	resolvePatchPaths(patchRoot, filepath.Dir(path))
	return applyConfigPatchNode(base, patchRoot)
}

// expandPatchEnvironment expands ${VAR} the same way the single-file loader
// does: process environment first, then the .env file beside the patch. Using
// os.ExpandEnv here would silently empty a variable that only the adjacent
// dotenv defines, so a base file and its override could disagree about ${VAR}.
// The dotenv read failure names the file so a malformed .env can be found.
func expandPatchEnvironment(data []byte, directory string) ([]byte, error) {
	dotenvPath := filepath.Join(directory, ".env")
	dotenv, err := readDotEnv(dotenvPath)
	if err != nil {
		return nil, fmt.Errorf("read .env for %s: %w", dotenvPath, err)
	}
	return []byte(os.Expand(string(data), envExpander(dotenv))), nil
}

func applyConfigPatchNode(base *Config, patchRoot *yaml.Node) error {
	baseData, err := yaml.Marshal(base)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(baseData, &document); err != nil {
		return err
	}
	root, err := yamlMappingRoot(&document)
	if err != nil {
		return err
	}
	if err := mergePatchMapping(root, patchRoot, nil); err != nil {
		return err
	}

	var rendered bytes.Buffer
	encoder := yaml.NewEncoder(&rendered)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	_ = encoder.Close()
	var result Config
	decoder := yaml.NewDecoder(bytes.NewReader(rendered.Bytes()))
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil {
		return err
	}
	result.Source = base.Source
	result.Paths = append([]string(nil), base.Paths...)
	result.WatchPaths = append([]string(nil), base.WatchPaths...)
	result.Sources = append([]ConfigSource(nil), base.Sources...)
	result.ServiceMetadata = make(map[string]EffectiveService, len(result.Services))
	for name := range result.Services {
		if metadata, exists := base.ServiceMetadata[name]; exists {
			result.ServiceMetadata[name] = metadata
		}
	}
	result.Provenance = append([]FieldProvenance(nil), base.Provenance...)
	result.CompositionDiagnostics = append([]CompositionDiagnostic(nil), base.CompositionDiagnostics...)
	result.DiscoveryScopes = append([]DiscoveryScope(nil), base.DiscoveryScopes...)
	result.dotenvEnv, result.explicitEnv = base.dotenvEnv, base.explicitEnv
	result.ServiceOrder = append([]string(nil), base.ServiceOrder...)
	for _, name := range mappingNodeKeyOrder(patchRoot, "services") {
		result.ServiceOrder = appendUniqueString(result.ServiceOrder, name)
	}
	result.ActionGroupOrder = append([]string(nil), base.ActionGroupOrder...)
	for _, name := range mappingNodeKeyOrder(patchRoot, "action_groups") {
		result.ActionGroupOrder = appendUniqueString(result.ActionGroupOrder, name)
	}
	synchronizePatchedStartSyntax(&result, patchRoot)
	*base = result
	return nil
}

func mappingNodeKeyOrder(root *yaml.Node, key string) []string {
	mapping := mappingValue(root, key)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	result := make([]string, 0, len(mapping.Content)/2)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		result = append(result, mapping.Content[index].Value)
	}
	return result
}

func deleteMappingValue(mapping *yaml.Node, key string) {
	if index := mappingIndex(mapping, key); index >= 0 {
		mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
	}
}

func retainMappingKeys(mapping *yaml.Node, allowed map[string]bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	content := make([]*yaml.Node, 0, len(mapping.Content))
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if allowed[mapping.Content[index].Value] {
			content = append(content, mapping.Content[index], mapping.Content[index+1])
		}
	}
	mapping.Content = content
}

func patchServiceNodes(path string) (map[string]*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded, err := expandPatchEnvironment(data, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(expanded, &document); err != nil {
		return nil, err
	}
	root, err := yamlMappingRoot(&document)
	if err != nil {
		return nil, err
	}
	resolvePatchPaths(root, filepath.Dir(path))
	services := mappingValue(root, "services")
	result := make(map[string]*yaml.Node)
	if services == nil || services.Kind != yaml.MappingNode {
		return result, nil
	}
	for index := 0; index+1 < len(services.Content); index += 2 {
		result[services.Content[index].Value] = cloneYAMLNode(services.Content[index+1])
	}
	return result, nil
}

func synchronizePatchedStartSyntax(result *Config, patchRoot *yaml.Node) {
	services := mappingValue(patchRoot, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index+1 < len(services.Content); index += 2 {
		name, patchService := services.Content[index].Value, services.Content[index+1]
		service, exists := result.Services[name]
		if !exists || patchService.Kind != yaml.MappingNode {
			continue
		}
		if command := mappingValue(patchService, "command"); command != nil {
			if command.Tag == "!!null" || command.Value == "" {
				service.Command = ""
				service.Lifecycle.Start = nil
			} else if service.Lifecycle.Start == nil {
				service.Lifecycle.Start = &Action{Command: service.Command}
			} else {
				start := *service.Lifecycle.Start
				start.Command = service.Command
				service.Lifecycle.Start = &start
			}
		}
		if lifecycle := mappingValue(patchService, "lifecycle"); lifecycle != nil && lifecycle.Kind == yaml.MappingNode {
			if start := mappingValue(lifecycle, "start"); start != nil {
				if start.Tag == "!!null" {
					service.Lifecycle.Start = nil
				} else if start.Kind == yaml.MappingNode && mappingValue(start, "command") != nil && service.Lifecycle.Start != nil {
					service.Command = service.Lifecycle.Start.Command
				}
			}
		}
		if service.Lifecycle.Start == nil && service.Command != "" {
			service.Lifecycle.Start = &Action{Command: service.Command}
		}
		if service.Lifecycle.Start != nil {
			start := *service.Lifecycle.Start
			var patchedStart *yaml.Node
			if lifecycle := mappingValue(patchService, "lifecycle"); lifecycle != nil {
				patchedStart = mappingValue(lifecycle, "start")
			}
			startHas := func(field string) bool {
				return patchedStart != nil && patchedStart.Kind == yaml.MappingNode && mappingValue(patchedStart, field) != nil
			}
			if mappingValue(patchService, "dir") != nil && !startHas("dir") {
				start.Dir = service.Dir
			}
			if mappingValue(patchService, "shell") != nil && !startHas("shell") {
				start.Shell = service.Shell
			}
			if mappingValue(patchService, "env") != nil && !startHas("env") {
				start.Env = service.Env
			}
			if mappingValue(patchService, "env_files") != nil && !startHas("env_files") {
				start.EnvFiles = service.EnvFiles
			}
			service.Lifecycle.Start = &start
		}
		result.Services[name] = service
	}
}

func mergePatchMapping(base, patch *yaml.Node, path []string) error {
	if base.Kind != yaml.MappingNode || patch.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected mapping", strings.Join(path, "."))
	}
	for index := 0; index+1 < len(patch.Content); index += 2 {
		key, incoming := patch.Content[index], patch.Content[index+1]
		fieldPath := append(append([]string(nil), path...), key.Value)
		baseIndex := mappingIndex(base, key.Value)
		if incoming.Tag == "!!null" {
			if baseIndex >= 0 {
				base.Content = append(base.Content[:baseIndex], base.Content[baseIndex+2:]...)
			}
			continue
		}
		if baseIndex < 0 {
			base.Content = append(base.Content, cloneYAMLNode(key), cloneYAMLNode(incoming))
			continue
		}
		current := base.Content[baseIndex+1]
		if current.Kind != incoming.Kind {
			return fmt.Errorf("%s: override type %s is incompatible with %s", strings.Join(fieldPath, "."), yamlKind(incoming.Kind), yamlKind(current.Kind))
		}
		if incoming.Kind == yaml.MappingNode && len(incoming.Content) > 0 {
			if err := mergePatchMapping(current, incoming, fieldPath); err != nil {
				return err
			}
			continue
		}
		base.Content[baseIndex+1] = cloneYAMLNode(incoming)
	}
	return nil
}

func mappingIndex(mapping *yaml.Node, key string) int {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return index
		}
	}
	return -1
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		clone.Content[index] = cloneYAMLNode(child)
	}
	return &clone
}

func yamlKind(kind yaml.Kind) string {
	switch kind {
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.ScalarNode:
		return "scalar"
	default:
		return "value"
	}
}

func resolvePatchPaths(root *yaml.Node, directory string) {
	resolveDirField := func(mapping *yaml.Node) {
		if mapping == nil || mapping.Kind != yaml.MappingNode {
			return
		}
		value := mappingValue(mapping, "dir")
		if value != nil && value.Kind == yaml.ScalarNode && value.Tag != "!!null" && value.Value != "" && !filepath.IsAbs(value.Value) {
			value.Value = filepath.Clean(filepath.Join(directory, value.Value))
		}
	}
	services := mappingValue(root, "services")
	if services != nil && services.Kind == yaml.MappingNode {
		for index := 1; index < len(services.Content); index += 2 {
			service := services.Content[index]
			resolveDirField(service)
			lifecycle := mappingValue(service, "lifecycle")
			if lifecycle != nil && lifecycle.Kind == yaml.MappingNode {
				for action := 1; action < len(lifecycle.Content); action += 2 {
					resolveDirField(lifecycle.Content[action])
				}
			}
			actions := mappingValue(service, "actions")
			if actions != nil && actions.Kind == yaml.MappingNode {
				for action := 1; action < len(actions.Content); action += 2 {
					resolveDirField(actions.Content[action])
				}
			}
		}
	}
	groups := mappingValue(root, "action_groups")
	if groups != nil && groups.Kind == yaml.MappingNode {
		for index := 1; index < len(groups.Content); index += 2 {
			resolveDirField(groups.Content[index])
		}
	}
}
