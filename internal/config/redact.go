package config

import (
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

var secretKeyFragments = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY", "PRIVATE_KEY", "ACCESS_KEY", "CREDENTIAL", "AUTH", "SESSION_KEY", "SIGNING", "DSN"}

// IsSecretKey is the shared config-show/MCP environment-key redaction rule.
func IsSecretKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, fragment := range secretKeyFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}

func isCredentialURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User == nil {
		return false
	}
	_, hasPassword := parsed.User.Password()
	return hasPassword
}

func redactEnvironmentValue(name, value string) (string, bool) {
	if IsSecretKey(name) || isCredentialURL(value) {
		return "[redacted]", true
	}
	return value, false
}

// RedactYAMLNode replaces secret-looking environment values anywhere in an
// effective configuration document.
func RedactYAMLNode(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Value == "env" && value.Kind == yaml.MappingNode {
				for envIndex := 0; envIndex+1 < len(value.Content); envIndex += 2 {
					envValue := value.Content[envIndex+1]
					if redacted, changed := redactEnvironmentValue(value.Content[envIndex].Value, envValue.Value); changed {
						envValue.Value = redacted
						envValue.Tag = "!!str"
						envValue.Style = 0
					}
				}
				continue
			}
			RedactYAMLNode(value)
		}
		return
	}
	for _, child := range node.Content {
		RedactYAMLNode(child)
	}
}

// RedactedCopy returns a deep-copy effective configuration with every env map
// filtered by the same key rule as config show.
func RedactedCopy(cfg *Config) (*Config, error) {
	payload, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	RedactYAMLNode(&document)
	redacted, err := yaml.Marshal(&document)
	if err != nil {
		return nil, err
	}
	var result Config
	if err := yaml.Unmarshal(redacted, &result); err != nil {
		return nil, err
	}
	result.ServiceOrder = append([]string(nil), cfg.ServiceOrder...)
	result.ActionGroupOrder = append([]string(nil), cfg.ActionGroupOrder...)
	result.Paths = append([]string(nil), cfg.Paths...)
	result.WatchPaths = append([]string(nil), cfg.WatchPaths...)
	for name, service := range result.Services {
		service.ActionOrder = append([]string(nil), cfg.Services[name].ActionOrder...)
		result.Services[name] = service
	}
	for name, group := range result.ActionGroups {
		group.ActionOrder = append([]string(nil), cfg.ActionGroups[name].ActionOrder...)
		result.ActionGroups[name] = group
	}
	return &result, nil
}
