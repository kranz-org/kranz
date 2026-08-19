package config

import (
	"regexp"
	"strings"
)

var runtimeSlugSeparators = regexp.MustCompile(`[^a-z0-9_.-]+`)

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
