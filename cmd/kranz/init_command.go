package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
)

// stdin is a variable so tests can drive the wizard without a terminal.
var stdin io.Reader = os.Stdin

// isTerminal reports whether the wizard may prompt. It is a variable for the
// same reason: a test needs to exercise both the interactive and the
// non-interactive path deterministically.
var isTerminal = func() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// importableSources are the configuration formats init can convert, in the
// order it offers them.
var importableSources = []string{"kranz.yaml", "kranz.yml", "process-compose.yaml", "process-compose.yml", "Procfile.dev", "Procfile"}

type initOptions struct {
	from       string
	fromSet    bool
	project    string
	service    string
	command    string
	outputPath string
	assumeYes  bool
}

type initResult struct {
	Path         string   `json:"path"`
	Written      bool     `json:"written"`
	Project      string   `json:"project"`
	Services     []string `json:"services"`
	Actions      int      `json:"actions"`
	NextCommands []string `json:"next_commands"`
}

func parseInitOptions(args []string) (initOptions, error) {
	options := initOptions{outputPath: "kranz.yaml"}
	value := func(index *int, name string) (string, error) {
		if *index+1 >= len(args) {
			return "", &kranzcli.Error{Code: "missing_option_value", Message: name + " requires a value", ExitCode: kranzcli.ExitUsage}
		}
		*index++
		return args[*index], nil
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--yes" || arg == "-y":
			options.assumeYes = true
		case arg == "--from":
			source, err := value(&index, "--from")
			if err != nil {
				return initOptions{}, err
			}
			options.from, options.fromSet = source, true
		case strings.HasPrefix(arg, "--from="):
			options.from, options.fromSet = strings.TrimPrefix(arg, "--from="), true
		case arg == "--project":
			project, err := value(&index, "--project")
			if err != nil {
				return initOptions{}, err
			}
			options.project = project
		case strings.HasPrefix(arg, "--project="):
			options.project = strings.TrimPrefix(arg, "--project=")
		case arg == "--service":
			service, err := value(&index, "--service")
			if err != nil {
				return initOptions{}, err
			}
			options.service = service
		case strings.HasPrefix(arg, "--service="):
			options.service = strings.TrimPrefix(arg, "--service=")
		case arg == "--command":
			command, err := value(&index, "--command")
			if err != nil {
				return initOptions{}, err
			}
			options.command = command
		case strings.HasPrefix(arg, "--command="):
			options.command = strings.TrimPrefix(arg, "--command=")
		case arg == "--output-file" || arg == "-o":
			path, err := value(&index, arg)
			if err != nil {
				return initOptions{}, err
			}
			options.outputPath = path
		case strings.HasPrefix(arg, "--output-file="):
			options.outputPath = strings.TrimPrefix(arg, "--output-file=")
		default:
			return initOptions{}, &kranzcli.Error{
				Code:     "unknown_option",
				Message:  fmt.Sprintf("unknown init argument %q", arg),
				Hint:     "Run `kranz init --help` to see what init accepts.",
				ExitCode: kranzcli.ExitUsage,
			}
		}
	}
	if options.fromSet && options.from == "" {
		return initOptions{}, &kranzcli.Error{Code: "missing_option_value", Message: "--from requires a path", ExitCode: kranzcli.ExitUsage}
	}
	return options, nil
}

func runInit(globals kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	options, err := parseInitOptions(args)
	if err != nil {
		return err
	}
	// `--project NAME` is parsed as the global runtime selector before init
	// ever sees it. init addresses no runtime, so the global value is the
	// project name here, which is also the spelling the CLI reference documents.
	if options.project == "" {
		options.project = globals.Project
	}
	directory := globals.Directory
	target := options.outputPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(directory, target)
	}

	prompt := bufio.NewReader(stdin)
	// JSON is a machine contract: prompts and the human-readable preview would
	// corrupt the single envelope. With JSON selected, init follows the same
	// deterministic non-interactive path as a pipe and reports any missing
	// inputs as a structured error.
	interactive := globals.Output == kranzcli.OutputText && isTerminal() && !options.assumeYes

	document, err := buildInitDocument(directory, options, interactive, prompt, stdout)
	if err != nil {
		return err
	}
	rendered, err := renderDocument(document)
	if err != nil {
		return err
	}

	if globals.Output == kranzcli.OutputText {
		// The preview is the point of the wizard: the user approves a file they
		// have actually read, not a description of one.
		_, _ = fmt.Fprintf(stdout, "\n%s\n%s\n", relativeTo(directory, target), strings.Repeat("-", len(relativeTo(directory, target))))
		_, _ = fmt.Fprint(stdout, rendered)
	}

	if _, err := os.Stat(target); err == nil {
		if !options.assumeYes {
			if !interactive {
				return &kranzcli.Error{
					Code:     "file_exists",
					Message:  fmt.Sprintf("%s already exists", relativeTo(directory, target)),
					Hint:     "Pass --yes to overwrite it, or -o PATH to write somewhere else.",
					ExitCode: kranzcli.ExitConflict,
				}
			}
			confirmed, err := confirm(prompt, stdout, fmt.Sprintf("\n%s already exists. Overwrite it?", relativeTo(directory, target)))
			if err != nil {
				return err
			}
			if !confirmed {
				_, _ = fmt.Fprintln(stdout, "Nothing was written.")
				return nil
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if interactive {
		confirmed, err := confirm(prompt, stdout, fmt.Sprintf("\nWrite %s?", relativeTo(directory, target)))
		if err != nil {
			return err
		}
		if !confirmed {
			_, _ = fmt.Fprintln(stdout, "Nothing was written.")
			return nil
		}
	}

	if err := config.WriteFileAtomically(target, []byte(rendered)); err != nil {
		return err
	}
	// A written file that does not load is worse than no file, so success is
	// only reported after the loader has accepted what was just written.
	cfg, err := config.LoadFiles([]string{target})
	if err != nil {
		return &kranzcli.Error{
			Code:     "invalid_config",
			Message:  fmt.Sprintf("%s was written but does not load", relativeTo(directory, target)),
			ExitCode: kranzcli.ExitConfig,
			Cause:    err,
		}
	}
	if globals.Output == kranzcli.OutputJSON {
		reportedPath := target
		if absolute, absoluteErr := filepath.Abs(target); absoluteErr == nil {
			reportedPath = absolute
		}
		return kranzcli.WriteJSON(stdout, initResult{
			Path:         reportedPath,
			Written:      true,
			Project:      cfg.Project,
			Services:     cfg.ServiceNames(),
			Actions:      len(cfg.ActionIDs()),
			NextCommands: []string{"kranz config check", "kranz up"},
		})
	}
	_, _ = fmt.Fprintf(stdout, "\nWrote %s.\nNext: `kranz config check`, then `kranz up`.\n", relativeTo(directory, target))
	return nil
}

func relativeTo(directory, path string) string {
	if relative, err := filepath.Rel(directory, path); err == nil && !strings.HasPrefix(relative, "..") {
		return relative
	}
	return path
}

// buildInitDocument produces the configuration to write, either by converting
// an existing source or by asking for the minimum a valid project needs.
func buildInitDocument(directory string, options initOptions, interactive bool, prompt *bufio.Reader, stdout io.Writer) (*yaml.Node, error) {
	source := options.from
	if !options.fromSet && options.project == "" && options.service == "" && options.command == "" {
		source = detectImportableSource(directory, options.outputPath)
		if source != "" && interactive {
			confirmed, err := confirm(prompt, stdout, fmt.Sprintf("Found %s. Import it?", source))
			if err != nil {
				return nil, err
			}
			if !confirmed {
				source = ""
			}
		}
	}
	if source != "" {
		return importDocument(directory, source)
	}
	return wizardDocument(directory, options, interactive, prompt, stdout)
}

// detectImportableSource finds a configuration worth converting, skipping the
// file init is about to write so init never offers to import its own output.
func detectImportableSource(directory, outputPath string) string {
	for _, candidate := range importableSources {
		if candidate == outputPath {
			continue
		}
		if info, err := os.Stat(filepath.Join(directory, candidate)); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// importDocument converts a supported source by loading it through the same
// loader the runtime uses and re-rendering the result. Translating the formats
// by hand would let the imported file and the running project disagree.
func importDocument(directory, source string) (*yaml.Node, error) {
	path := source
	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, &kranzcli.Error{
			Code:     "source_not_found",
			Message:  fmt.Sprintf("cannot import %s", source),
			Hint:     "Pass --from PATH with a Kranz, Process Compose, or Procfile source.",
			ExitCode: kranzcli.ExitNotFound,
			Cause:    err,
		}
	}
	cfg, err := config.LoadFiles([]string{path})
	if err != nil {
		return nil, &kranzcli.Error{
			Code:     "invalid_source",
			Message:  fmt.Sprintf("%s could not be imported", source),
			ExitCode: kranzcli.ExitConfig,
			Cause:    err,
		}
	}
	return effectiveDocument(authoringConfig(cfg, directory))
}

// authoringConfig undoes the normalization the loader applies, so an import
// produces a file a person could have written rather than a dump of the
// runtime's internal view. Two things make the effective configuration unfit to
// write back: `command` is expanded into `lifecycle.start`, and re-emitting
// both is rejected as a conflict; and relative paths are resolved against the
// project, baking one machine's absolute directories into a portable file.
func authoringConfig(cfg *config.Config, directory string) *config.Config {
	projectDir := mustAbs(directory)
	authored := *cfg
	authored.Services = make(map[string]config.Service, len(cfg.Services))

	if authored.Defaults.Dir == projectDir || authored.Defaults.Dir == "." {
		authored.Defaults.Dir = ""
	}
	for name, svc := range cfg.Services {
		if svc.Lifecycle.Start != nil && svc.Lifecycle.Start.Command == svc.Command {
			// Keep whichever spelling carries the most information: the
			// shorthand when lifecycle.start says nothing else, the explicit
			// block when it does.
			if lifecycleStartAddsNothing(*svc.Lifecycle.Start, svc, projectDir) {
				svc.Lifecycle.Start = nil
			} else {
				svc.Command = ""
			}
		}
		if svc.Dir == projectDir || svc.Dir == authored.Defaults.Dir {
			svc.Dir = ""
		}
		if svc.Shell == cfg.Defaults.Shell {
			svc.Shell = ""
		}
		if svc.Supervision == config.SupervisionProcess {
			svc.Supervision = ""
		}
		if svc.Lifecycle.Start != nil {
			start := *svc.Lifecycle.Start
			if start.Dir == projectDir || start.Dir == authored.Defaults.Dir {
				start.Dir = ""
			}
			if start.Shell == cfg.Defaults.Shell {
				start.Shell = ""
			}
			svc.Lifecycle.Start = &start
		}
		authored.Services[name] = svc
	}
	return &authored
}

// lifecycleStartAddsNothing reports whether a lifecycle.start block says
// anything the service-level shorthand does not already say.
func lifecycleStartAddsNothing(start config.Action, svc config.Service, projectDir string) bool {
	if start.Description != "" || start.Timeout != 0 || len(start.Env) > 0 || len(start.EnvFiles) > 0 {
		return false
	}
	if start.Dir != "" && start.Dir != svc.Dir && start.Dir != projectDir {
		return false
	}
	if start.Shell != "" && start.Shell != svc.Shell {
		return false
	}
	return true
}

func wizardDocument(directory string, options initOptions, interactive bool, prompt *bufio.Reader, stdout io.Writer) (*yaml.Node, error) {
	project := options.project
	serviceName := options.service
	command := options.command

	if interactive {
		var err error
		if project == "" {
			project, err = ask(prompt, stdout, "Project name", filepath.Base(mustAbs(directory)))
			if err != nil {
				return nil, err
			}
		}
		if serviceName == "" {
			serviceName, err = ask(prompt, stdout, "First service name", "app")
			if err != nil {
				return nil, err
			}
		}
		if command == "" {
			command, err = ask(prompt, stdout, "Command to run it", "")
			if err != nil {
				return nil, err
			}
		}
	}
	if project == "" {
		project = filepath.Base(mustAbs(directory))
	}
	if serviceName == "" {
		serviceName = "app"
	}
	if command == "" {
		// A service without a command cannot start, and a configuration that
		// cannot start is not a useful thing to have written.
		return nil, &kranzcli.Error{
			Code:     "missing_command",
			Message:  "a service needs a command",
			Hint:     "Run `kranz init --project NAME --service NAME --command COMMAND`, or run init in a terminal to be asked.",
			ExitCode: kranzcli.ExitUsage,
		}
	}

	cfg := &config.Config{
		Project:      project,
		Services:     map[string]config.Service{serviceName: {Command: command}},
		ServiceOrder: []string{serviceName},
	}

	// package.json scripts become actions rather than services: they are things
	// the user runs on demand, and nothing here executes them to find out.
	if scripts := packageScripts(directory); len(scripts) > 0 {
		accepted := scripts
		if interactive {
			confirmed, err := confirm(prompt, stdout, fmt.Sprintf("Found %d package.json script(s). Add them as actions?", len(scripts)))
			if err != nil {
				return nil, err
			}
			if !confirmed {
				accepted = nil
			}
		}
		if len(accepted) > 0 {
			service := cfg.Services[serviceName]
			service.Actions = make(map[string]config.Action, len(accepted))
			names := make([]string, 0, len(accepted))
			for name := range accepted {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				service.Actions[name] = config.Action{Command: "npm run " + name}
			}
			service.ActionOrder = names
			cfg.Services[serviceName] = service
		}
	}
	return effectiveDocument(cfg)
}

func mustAbs(directory string) string {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return directory
	}
	return absolute
}

// packageScripts reads the scripts a package.json declares. It parses the file
// and never runs anything: discovering what a project can do must not have the
// side effects of doing it.
func packageScripts(directory string) map[string]string {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	return manifest.Scripts
}

func renderDocument(document *yaml.Node) (string, error) {
	var buffer strings.Builder
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func ask(prompt *bufio.Reader, stdout io.Writer, question, fallback string) (string, error) {
	if fallback != "" {
		_, _ = fmt.Fprintf(stdout, "%s [%s]: ", question, fallback)
	} else {
		_, _ = fmt.Fprintf(stdout, "%s: ", question)
	}
	line, err := prompt.ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return fallback, nil
		}
		return "", err
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

func confirm(prompt *bufio.Reader, stdout io.Writer, question string) (bool, error) {
	_, _ = fmt.Fprintf(stdout, "%s [y/N]: ", question)
	line, err := prompt.ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
