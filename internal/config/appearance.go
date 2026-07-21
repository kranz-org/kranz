package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SaveUIAppearance atomically updates the ui mapping in a native Kranz file.
// Working with YAML nodes preserves unrelated keys, ordering, and comments.
func SaveUIAppearance(path string, appearance UIConfig) error {
	if path == "" {
		return errors.New("project configuration path is empty")
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve project configuration %s: %w", path, err)
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Errorf("read project configuration %s: %w", resolvedPath, err)
	}
	format, err := detectFormat(data)
	if err != nil {
		return fmt.Errorf("inspect project configuration %s: %w", resolvedPath, err)
	}
	if format != SourceKranz {
		return errors.New("project appearance can only be saved to a native Kranz configuration")
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse project configuration %s: %w", resolvedPath, err)
	}
	root, err := yamlMappingRoot(&document)
	if err != nil {
		return fmt.Errorf("update project configuration %s: %w", resolvedPath, err)
	}
	ui := mappingValue(root, "ui")
	if ui == nil {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "ui"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"},
		)
		ui = root.Content[len(root.Content)-1]
	}
	if ui.Kind != yaml.MappingNode {
		return fmt.Errorf("field 'ui' must be a mapping")
	}
	setMappingString(ui, "theme", appearance.Theme)
	setMappingString(ui, "accent", appearance.Accent)
	setMappingString(ui, "background", appearance.Background)
	setMappingString(ui, "color_mode", appearance.ColorMode)

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode project configuration %s: %w", resolvedPath, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish project configuration %s: %w", resolvedPath, err)
	}
	return replaceFileAtomically(resolvedPath, output.Bytes())
}

func yamlMappingRoot(document *yaml.Node) (*yaml.Node, error) {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, errors.New("configuration must contain one YAML document")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("configuration root must be a mapping")
	}
	return root, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setMappingString(mapping *yaml.Node, key, value string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != key {
			continue
		}
		if value == "" {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return
		}
		mapping.Content[index+1].Kind = yaml.ScalarNode
		mapping.Content[index+1].Tag = "!!str"
		mapping.Content[index+1].Value = value
		return
	}
	if value == "" {
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func replaceFileAtomically(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat project configuration %s: %w", path, err)
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".kranz-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary project configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("preserve project configuration permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary project configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary project configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary project configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace project configuration %s: %w", path, err)
	}
	// Sync the containing directory so the rename itself survives a sudden
	// power loss, not just the contents of the temporary file.
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open project configuration directory: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("sync project configuration directory: %w", err)
	}
	return nil
}
