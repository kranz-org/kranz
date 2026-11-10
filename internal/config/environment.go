package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// environmentSpec retains accepted inline values separately from live file
// inputs. Specs are immutable after loading and can be shared by config copies.
type environmentSpec struct {
	parent     *environmentSpec
	dotenv     []string
	skipDotenv bool
	files      []string
	values     map[string]string
}

func inlineEnvironmentDefaults(cfg *Config) map[string]string {
	if cfg.explicitEnv == nil && len(cfg.dotenvPaths) == 0 {
		return cloneStringMap(cfg.Defaults.Env)
	}
	return cloneStringMap(cfg.explicitEnv)
}

func (e *environmentSpec) resolve() (map[string]string, error) {
	if e == nil {
		return make(map[string]string), nil
	}
	values, err := e.parent.resolve()
	if err != nil {
		return nil, err
	}
	dotenv := e.dotenv
	if e.skipDotenv {
		dotenv = nil
	}
	for _, path := range dotenv {
		next, err := readDotEnv(path)
		if err != nil {
			return nil, err
		}
		values = mergeStringMap(values, dotenvWithoutHostOverrides(next))
	}
	for _, path := range e.files {
		next, err := readDotEnv(path)
		if err != nil {
			return nil, err
		}
		values = mergeStringMap(values, next)
	}
	return mergeStringMap(values, e.values), nil
}

// ResolveEnvironment reads external env files for this invocation. Inline
// configuration and interpolation remain bound to the accepted configuration.
func (a Action) ResolveEnvironment() (map[string]string, error) {
	if a.environment == nil {
		return cloneStringMap(a.Env), nil
	}
	values, err := a.environment.resolve()
	if err != nil {
		return nil, fmt.Errorf("read environment files: %w", err)
	}
	for key, value := range values {
		values[key] = os.ExpandEnv(value)
	}
	return mergeStringMap(values, a.invocationEnv), nil
}

// Preserve recipes across the YAML round trips used by overrides and cache
// copies. A patch changes only the corresponding leaf's accepted inline values
// and file references; it never turns inherited file values into inline values.
func restoreEnvironmentSpecs(result, base *Config, patch *yaml.Node) {
	result.dotenvPaths = append([]string(nil), base.dotenvPaths...)
	for name, svc := range result.Services {
		before, exists := base.Services[name]
		if !exists {
			continue
		}
		node := childNode(childNode(patch, "services"), name)
		svc.environment = patchedEnvironment(before.environment, node, svc.Dir, svc.EnvFiles)
		if svc.environment != nil && childNode(node, "is_dotenv_disabled") != nil && svc.environment.parent != nil {
			defaults := *svc.environment.parent
			defaults.skipDotenv = svc.DisableDotenv
			svc.environment = rebaseEnvironment(svc.environment, &defaults)
		}
		for role, action := range map[string]*Action{"start": svc.Lifecycle.Start, "stop": svc.Lifecycle.Stop, "logs": svc.Lifecycle.Logs} {
			var previous *Action
			switch role {
			case "start":
				previous = before.Lifecycle.Start
			case "stop":
				previous = before.Lifecycle.Stop
			case "logs":
				previous = before.Lifecycle.Logs
			}
			if action != nil && previous != nil {
				action.environment = patchedEnvironment(previous.environment, childNode(childNode(node, "lifecycle"), role), action.Dir, action.EnvFiles)
				action.environment = rebaseEnvironment(action.environment, svc.environment)
			}
		}
		for name, action := range svc.Actions {
			action.environment = patchedEnvironment(before.Actions[name].environment, childNode(childNode(node, "actions"), name), action.Dir, action.EnvFiles)
			action.environment = rebaseEnvironment(action.environment, svc.environment)
			svc.Actions[name] = action
		}
		result.Services[name] = svc
	}
	for name, group := range result.ActionGroups {
		before, exists := base.ActionGroups[name]
		if !exists {
			continue
		}
		node := childNode(childNode(patch, "action_groups"), name)
		group.environment = patchedEnvironment(before.environment, node, group.Dir, group.EnvFiles)
		for name, action := range group.Actions {
			action.environment = patchedEnvironment(before.Actions[name].environment, childNode(childNode(node, "actions"), name), action.Dir, action.EnvFiles)
			action.environment = rebaseEnvironment(action.environment, group.environment)
			group.Actions[name] = action
		}
		result.ActionGroups[name] = group
	}
}

func childNode(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	return mappingValue(node, key)
}

func rebaseEnvironment(spec, parent *environmentSpec) *environmentSpec {
	if spec == nil {
		return nil
	}
	copy := *spec
	copy.parent = parent
	return &copy
}

func patchedEnvironment(spec *environmentSpec, node *yaml.Node, dir string, files []string) *environmentSpec {
	if spec == nil || node == nil {
		return spec
	}
	copy := *spec
	if env := childNode(node, "env"); env != nil {
		copy.values = cloneStringMap(spec.values)
		if env.Tag == "!!null" || len(env.Content) == 0 {
			copy.values = nil
			copy.parent = nil
		} else {
			if copy.values == nil {
				copy.values = make(map[string]string)
			}
			for i := 0; i+1 < len(env.Content); i += 2 {
				key, value := env.Content[i].Value, env.Content[i+1]
				if value.Tag == "!!null" {
					delete(copy.values, key)
				} else {
					copy.values[key] = value.Value
				}
			}
		}
	}
	if childNode(node, "env_files") != nil {
		copy.files = resolvedEnvFiles(dir, files)
	}
	return &copy
}
