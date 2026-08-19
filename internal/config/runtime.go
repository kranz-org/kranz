package config

import (
	"fmt"
	"regexp"
	"strings"
)

var runtimeSlugSeparators = regexp.MustCompile(`[^a-z0-9_.-]+`)
var runtimeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

func ValidateRuntimeName(name string) error {
	if !runtimeNamePattern.MatchString(name) {
		return fmt.Errorf("runtime name must match [a-z0-9][a-z0-9_.-]{0,62}, got %q", name)
	}
	return nil
}

// RuntimeName returns the explicit runtime name or a stable slug derived from
// the project title. Validate guarantees explicit names already use the
// public 63-character alphabet.
func (c *Config) RuntimeName() string {
	if c.Runtime.Name != "" {
		return c.Runtime.Name
	}
	name := strings.ToLower(strings.TrimSpace(c.Project))
	name = runtimeSlugSeparators.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-._")
	if name == "" {
		name = "kranz"
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-._")
	}
	return name
}
