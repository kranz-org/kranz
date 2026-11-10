package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kranz-org/kranz/internal/actionparams"
	"gopkg.in/yaml.v3"
)

// ArgvList is a command written either as one scalar or as a sequence of
// elements. Both spellings describe the same ordered argument vector.
type ArgvList []string

// UnmarshalYAML accepts a scalar or a sequence.
func (a *ArgvList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*a = ArgvList{node.Value}
		return nil
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("argv elements must be strings")
			}
			values = append(values, item.Value)
		}
		*a = values
		return nil
	default:
		return fmt.Errorf("expected a string or a list of strings")
	}
}

// ActionParam declares one control of a parameterized action.
type ActionParam struct {
	Type       string             `yaml:"type"`
	Options    ActionParamOptions `yaml:"options,omitempty"`
	Default    any                `yaml:"default,omitempty"`
	Required   bool               `yaml:"required,omitempty"`
	Optional   bool               `yaml:"optional,omitempty"`
	Flag       string             `yaml:"flag,omitempty"`
	Arg        string             `yaml:"arg,omitempty"`
	Positional *bool              `yaml:"positional,omitempty"`
	Env        string             `yaml:"env,omitempty"`
	Confirm    string             `yaml:"confirm,omitempty"`
	Prompt     string             `yaml:"prompt,omitempty"`
	Min        *int64             `yaml:"min,omitempty"`
	Max        *int64             `yaml:"max,omitempty"`
	MinLength  *int               `yaml:"min_length,omitempty"`
	MaxLength  *int               `yaml:"max_length,omitempty"`
	Pattern    string             `yaml:"pattern,omitempty"`
}

// ActionOption is one selectable value of a list control.
type ActionOption struct {
	Value   string
	Label   string
	Confirm string
	Flag    string
	Args    []string
	Run     ArgvList
}

// ActionParamOptions is written as a sequence of values or as a mapping of
// value to its option node.
type ActionParamOptions []ActionOption

// UnmarshalYAML accepts either the list or the mapping spelling.
func (o *ActionParamOptions) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		options := make([]ActionOption, 0, len(node.Content))
		for _, item := range node.Content {
			switch item.Kind {
			case yaml.ScalarNode:
				options = append(options, ActionOption{Value: item.Value})
			case yaml.MappingNode:
				var option ActionOption
				if err := decodeOptionNode(item, &option); err != nil {
					return err
				}
				options = append(options, option)
			default:
				return fmt.Errorf("option values must be scalars or option nodes")
			}
		}
		*o = options
		return nil
	case yaml.MappingNode:
		options := make([]ActionOption, 0, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			value := node.Content[index].Value
			option := ActionOption{Value: value}
			child := node.Content[index+1]
			switch child.Kind {
			case yaml.ScalarNode:
				option.Label = child.Value
			case yaml.MappingNode:
				if err := decodeOptionNode(child, &option); err != nil {
					return fmt.Errorf("option %q: %w", value, err)
				}
			default:
				return fmt.Errorf("option %q must map to a value or a node", value)
			}
			options = append(options, option)
		}
		*o = options
		return nil
	default:
		return fmt.Errorf("options must be a list or a mapping")
	}
}

// MarshalYAML writes the canonical sequence form so a cached configuration
// round-trips through the same decoder.
func (o ActionParamOptions) MarshalYAML() (any, error) {
	return []ActionOption(o), nil
}

func decodeOptionNode(node *yaml.Node, option *ActionOption) error {
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index].Value
		child := node.Content[index+1]
		switch key {
		case "value":
			option.Value = child.Value
		case "flag":
			option.Flag = child.Value
		case "label":
			option.Label = child.Value
		case "confirm":
			option.Confirm = child.Value
		case "args":
			if child.Kind == yaml.ScalarNode {
				option.Args = []string{child.Value}
				continue
			}
			if child.Kind != yaml.SequenceNode {
				return fmt.Errorf("field 'args' must be a list of strings")
			}
			for _, item := range child.Content {
				option.Args = append(option.Args, item.Value)
			}
		case "run":
			var run ArgvList
			if err := child.Decode(&run); err != nil {
				return err
			}
			option.Run = run
		default:
			return fmt.Errorf("unknown option field %q", key)
		}
	}
	return nil
}

// CompileAction builds the pure parameter schema for an action. It returns nil
// for an action without parameters; such an action keeps its legacy or argv
// execution path unchanged.
func CompileAction(id string, action Action) (*actionparams.Compiled, error) {
	if len(action.Params) == 0 {
		return nil, nil
	}
	if action.Command != "" {
		return nil, fmt.Errorf("params cannot be combined with the legacy 'command' field")
	}
	if len(action.Run) > 0 && len(action.Argv) > 0 {
		return nil, fmt.Errorf("params require either 'run' or 'argv', not both")
	}
	if len(action.Run) == 0 && len(action.Argv) == 0 {
		hasOptionRun := false
		for _, param := range action.Params {
			for _, option := range param.Options {
				if len(option.Run) > 0 {
					hasOptionRun = true
				}
			}
		}
		if !hasOptionRun {
			return nil, fmt.Errorf("params require 'run' or 'argv'")
		}
	}
	order := action.ParamOrder
	if len(order) == 0 {
		order = make([]string, 0, len(action.Params))
		for name := range action.Params {
			order = append(order, name)
		}
		sort.Strings(order)
	}
	params := make(map[string]actionparams.Param, len(action.Params))
	for name, declared := range action.Params {
		converted, err := convertActionParam(name, declared)
		if err != nil {
			return nil, err
		}
		params[name] = converted
	}
	source := actionparams.Source{
		ID:          id,
		Order:       order,
		Params:      params,
		Run:         append([]string(nil), action.Run...),
		Argv:        append([]string(nil), action.Argv...),
		Env:         action.Env,
		Confirm:     action.ConfirmationRequired(),
		Interactive: action.InteractiveEnabled(),
	}
	return actionparams.Compile(source)
}

// RawValuesFromAny converts a decoded static value map into surface-neutral raw
// values. It is used for prerequisite parameters.
func RawValuesFromAny(values map[string]any) (actionparams.RawValues, error) {
	raw := make(actionparams.RawValues, len(values))
	for name, input := range values {
		value, ok := actionparams.ValueFromAny(input)
		if !ok {
			return nil, fmt.Errorf("parameter %q has an unsupported value", name)
		}
		switch value.Kind {
		case actionparams.KindBool:
			flag := value.Bool
			raw[name] = actionparams.Raw{Present: true, Bool: &flag}
		case actionparams.KindInt:
			number := value.Int
			raw[name] = actionparams.Raw{Present: true, Int: &number}
		case actionparams.KindString:
			raw[name] = actionparams.Raw{Present: true, Text: value.Text}
		case actionparams.KindStrings:
			raw[name] = actionparams.Raw{Present: true, Strings: value.Strings}
		}
	}
	return raw, nil
}

// RenderedAction returns the immutable execution definition of one rendered
// invocation: argv without a shell plus the resolved environment.
func (a Action) RenderedAction(rendered actionparams.Rendered, params map[string]any) Action {
	executable := a
	executable.Command = ""
	executable.Shell = ""
	executable.Run = nil
	executable.Argv = append(ArgvList(nil), rendered.Argv...)
	executable.Env = rendered.Env
	if a.environment != nil {
		projected := make(map[string]string)
		for key, value := range rendered.Env {
			if previous, exists := a.Env[key]; !exists || previous != value {
				projected[key] = value
			}
		}
		for _, param := range a.Params {
			if param.Env != "" {
				if value, ok := rendered.Env[param.Env]; ok {
					projected[param.Env] = value
				}
			}
		}
		executable.invocationEnv = projected
	}
	executable.ParamValues = params
	executable.CommandPreview = rendered.Preview
	return executable
}

func convertActionParam(name string, declared ActionParam) (actionparams.Param, error) {
	control, ok := actionparams.ParseControl(declared.Type)
	if !ok {
		return actionparams.Param{}, fmt.Errorf("parameter %q: unknown type %q", name, declared.Type)
	}
	param := actionparams.Param{
		Control:   control,
		Required:  declared.Required,
		Optional:  declared.Optional,
		Confirm:   declared.Confirm,
		Prompt:    declared.Prompt,
		Min:       declared.Min,
		Max:       declared.Max,
		MinLength: declared.MinLength,
		MaxLength: declared.MaxLength,
		Pattern:   declared.Pattern,
	}
	for _, option := range declared.Options {
		param.Options = append(param.Options, actionparams.Option{
			Value:   option.Value,
			Label:   option.Label,
			Confirm: option.Confirm,
			Flag:    option.Flag,
			Args:    append([]string(nil), option.Args...),
			Run:     append([]string(nil), option.Run...),
		})
	}
	if declared.Default != nil {
		value, ok := actionparams.ValueFromAny(declared.Default)
		if !ok {
			return actionparams.Param{}, fmt.Errorf("parameter %q: unsupported default value", name)
		}
		param.Default = &value
	}
	projections := 0
	if declared.Positional != nil && *declared.Positional {
		param.Projection = actionparams.Projection{Kind: actionparams.ProjectionPositional}
		projections++
	}
	if declared.Flag != "" {
		param.Projection = actionparams.Projection{Kind: actionparams.ProjectionFlag, Name: declared.Flag}
		projections++
	}
	if declared.Arg != "" {
		param.Projection = actionparams.Projection{Kind: actionparams.ProjectionArg, Name: declared.Arg, Joined: strings.HasSuffix(declared.Arg, "=")}
		projections++
	}
	if declared.Env != "" {
		param.Projection = actionparams.Projection{Kind: actionparams.ProjectionEnv, Name: declared.Env}
		projections++
	}
	if projections > 1 {
		return actionparams.Param{}, fmt.Errorf("parameter %q: declare at most one of flag, arg, positional, or env", name)
	}
	return param, nil
}
