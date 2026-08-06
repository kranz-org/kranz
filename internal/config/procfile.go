package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var procfileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const (
	procfileShutdownSignal  = 15
	procfileShutdownTimeout = 30 * time.Second
)

// parseProcfile converts the supported Procfile subset into Kranz's config
// model. It deliberately performs no file I/O or command rewriting.
func parseProcfile(path string, data []byte) (*Config, error) {
	directory := filepath.Clean(filepath.Dir(path))
	if absolute, err := filepath.Abs(directory); err == nil {
		directory = absolute
	}
	cfg := &Config{
		Project:  filepath.Base(directory),
		Services: make(map[string]Service),
		Source:   SourceProcfile,
	}

	for index, rawLine := range strings.Split(string(data), "\n") {
		lineNumber := index + 1
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		namePart, commandPart, found := strings.Cut(rawLine, ":")
		if !found {
			return nil, procfileLineError(path, lineNumber, "expected <name>: <command>")
		}
		name := strings.TrimSpace(namePart)
		command := strings.TrimSpace(commandPart)
		if name == "" {
			return nil, procfileLineError(path, lineNumber, "empty name")
		}
		if !procfileNamePattern.MatchString(name) {
			return nil, procfileLineError(path, lineNumber, fmt.Sprintf("invalid name %q", name))
		}
		if command == "" {
			return nil, procfileLineError(path, lineNumber, "empty command")
		}
		if _, exists := cfg.Services[name]; exists {
			return nil, procfileLineError(path, lineNumber, fmt.Sprintf("duplicate name %q", name))
		}

		cfg.Services[name] = Service{
			Command: command,
			Dir:     directory,
			Shutdown: ShutdownConfig{
				Signal:  procfileShutdownSignal,
				Timeout: procfileShutdownTimeout,
			},
		}
		cfg.ServiceOrder = append(cfg.ServiceOrder, name)
	}

	return cfg, nil
}

func procfileLineError(path string, line int, detail string) error {
	return fmt.Errorf("parse Procfile %s line %d: %s", path, line, detail)
}
