// Package settings persists user-level Kranz preferences.
package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const settingsFileName = "settings.yaml"

// Settings contains optional user overrides. Empty values defer to the project.
type Settings struct {
	Theme      string `yaml:"theme,omitempty"`
	Accent     string `yaml:"accent,omitempty"`
	Background string `yaml:"background,omitempty"`
	ColorMode  string `yaml:"color_mode,omitempty"`
}

// DefaultPath returns the platform-native per-user settings path.
func DefaultPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "kranz", settingsFileName), nil
}

// Load reads settings from path. A missing file is equivalent to empty settings.
func Load(path string) (Settings, error) {
	if path == "" {
		return Settings{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	var value Settings
	if err := yaml.Unmarshal(data, &value); err != nil {
		return Settings{}, fmt.Errorf("parse settings: %w", err)
	}
	return value, nil
}

// Save writes settings atomically with user-only permissions.
func Save(path string, value Settings) error {
	if path == "" {
		return errors.New("settings path is empty")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".settings-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary settings: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary settings: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary settings: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}
