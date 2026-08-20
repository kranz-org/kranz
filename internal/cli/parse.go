package cli

import (
	"fmt"
	"path/filepath"
	"strings"
)

type OutputFormat string

const (
	OutputText OutputFormat = "text"
	OutputJSON OutputFormat = "json"
)

type GlobalOptions struct {
	ConfigPaths []string
	Directory   string
	Project     string
	Output      OutputFormat
}

type Invocation struct {
	Globals     GlobalOptions
	CommandPath []string
	Args        []string
	Help        bool
}

// RequestedOutput is a best-effort pre-scan used when parsing itself fails.
// It lets `--output json` keep stdout machine-readable even for usage errors.
func RequestedOutput(args []string) OutputFormat {
	for index, arg := range args {
		if arg == "--output" && index+1 < len(args) && args[index+1] == string(OutputJSON) {
			return OutputJSON
		}
		if arg == "--output=json" {
			return OutputJSON
		}
	}
	return OutputText
}

func (i Invocation) Command() string { return PathString(i.CommandPath) }

// Parse resolves global coordinates and the command path. Once a leaf command
// is known, remaining arguments belong to that command except recognized
// global flags, which are accepted consistently before or after subcommands.
func Parse(tree *Command, args []string) (Invocation, error) {
	invocation := Invocation{Globals: GlobalOptions{Directory: ".", Output: OutputText}}
	current := tree
	commandStarted := false

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if value, consumed, recognized, err := globalValue(args, index); recognized {
			if err != nil {
				return Invocation{}, err
			}
			index += consumed
			if err := applyGlobal(&invocation.Globals, arg, value); err != nil {
				return Invocation{}, err
			}
			continue
		}
		if arg == "--help" || arg == "-h" {
			invocation.Help = true
			continue
		}
		if arg == "--version" || arg == "-v" {
			if commandStarted || len(args) != 1 {
				return Invocation{}, usageError("invalid_arguments", "--version does not accept additional arguments")
			}
			invocation.CommandPath = []string{"version"}
			commandStarted = true
			current = tree.Child("version")
			continue
		}

		if !commandStarted {
			if strings.HasPrefix(arg, "-") {
				return Invocation{}, usageError("unknown_option", fmt.Sprintf("unknown option %s", arg))
			}
			child := current.Child(arg)
			if child == nil {
				return Invocation{}, unknownCommand(arg)
			}
			invocation.CommandPath = append(invocation.CommandPath, arg)
			current = child
			commandStarted = true
			continue
		}
		if len(current.Children) > 0 && len(invocation.Args) == 0 && !strings.HasPrefix(arg, "-") {
			if child := current.Child(arg); child != nil {
				invocation.CommandPath = append(invocation.CommandPath, arg)
				current = child
				continue
			}
			// The token is not a subcommand. A group with a default takes it as
			// that subcommand's argument, so `kranz port 8080` works; the
			// default then reports what is wrong with the token if anything is.
			fallback := current.Child(current.Default)
			if fallback == nil {
				return Invocation{}, usageError("unknown_subcommand", fmt.Sprintf("unknown command %q for %q", arg, invocation.Command()))
			}
			invocation.CommandPath = append(invocation.CommandPath, fallback.Name)
			current = fallback
			invocation.Args = append(invocation.Args, arg)
			continue
		}
		invocation.Args = append(invocation.Args, arg)
	}

	if len(invocation.CommandPath) == 1 && invocation.CommandPath[0] == "help" {
		invocation.Help = true
		invocation.CommandPath = append([]string(nil), invocation.Args...)
		invocation.Args = nil
		if _, err := tree.Resolve(invocation.CommandPath); err != nil {
			return Invocation{}, usageError("unknown_command", err.Error())
		}
	}
	if invocation.Help && len(invocation.CommandPath) == 0 {
		return invocation, nil
	}
	if len(current.Children) > 0 && commandStarted && !invocation.Help {
		child := current.Child(current.Default)
		if child == nil {
			return Invocation{}, usageError("missing_subcommand", fmt.Sprintf("command %q requires a subcommand", invocation.Command()))
		}
		invocation.CommandPath = append(invocation.CommandPath, child.Name)
		current = child
	}
	if invocation.Command() == "version" && len(invocation.Args) > 0 {
		return Invocation{}, usageError("invalid_arguments", "version does not accept additional arguments")
	}
	return invocation, nil
}

func globalValue(args []string, index int) (value string, consumed int, recognized bool, err error) {
	arg := args[index]
	for _, option := range []string{"-f", "--config", "-C", "--directory", "-p", "--project", "--output"} {
		if arg == option {
			if index+1 >= len(args) {
				return "", 0, true, usageError("missing_option_value", fmt.Sprintf("%s requires a value", arg))
			}
			return args[index+1], 1, true, nil
		}
		if strings.HasPrefix(arg, option+"=") {
			if option == "-f" || option == "-C" || option == "-p" {
				continue
			}
			value := strings.TrimPrefix(arg, option+"=")
			if value == "" {
				return "", 0, true, usageError("missing_option_value", fmt.Sprintf("%s requires a value", option))
			}
			return value, 0, true, nil
		}
	}
	return "", 0, false, nil
}

func applyGlobal(options *GlobalOptions, spelling, value string) error {
	switch {
	case spelling == "-f" || spelling == "--config" || strings.HasPrefix(spelling, "--config="):
		options.ConfigPaths = append(options.ConfigPaths, value)
	case spelling == "-C" || spelling == "--directory" || strings.HasPrefix(spelling, "--directory="):
		options.Directory = value
	case spelling == "-p" || spelling == "--project" || strings.HasPrefix(spelling, "--project="):
		options.Project = value
	case spelling == "--output" || strings.HasPrefix(spelling, "--output="):
		format := OutputFormat(value)
		if format != OutputText && format != OutputJSON {
			return usageError("invalid_output", fmt.Sprintf("invalid output format %q (expected text or json)", value))
		}
		options.Output = format
	}
	return nil
}

func unknownCommand(arg string) *Error {
	err := usageError("unknown_command", fmt.Sprintf("unknown command %q", arg))
	extension := strings.ToLower(filepath.Ext(arg))
	if extension == ".yaml" || extension == ".yml" || filepath.Base(arg) == "Procfile" || filepath.Base(arg) == "Procfile.dev" {
		err.Hint = fmt.Sprintf("Did you mean `kranz -f %s`?", arg)
	}
	return err
}
