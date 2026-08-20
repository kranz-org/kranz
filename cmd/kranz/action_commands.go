package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
	"github.com/kranz-org/kranz/internal/service"
)

// Actions are configured, so listing and describing them needs only the
// project. Running one is a runtime operation: the supervisor owns the
// execution slot, and running it anywhere else would let two callers run the
// same action at once.

func runActionList(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "action list accepts at most one owner", ExitCode: kranzcli.ExitUsage}
	}
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	owner := ""
	if len(args) == 1 {
		owner = args[0]
	}
	type entry struct {
		ID          string `json:"id"`
		Owner       string `json:"owner"`
		OwnerKind   string `json:"owner_kind"`
		Description string `json:"description"`
		Interactive bool   `json:"interactive"`
		Confirm     bool   `json:"confirm"`
	}
	entries := make([]entry, 0)
	for _, id := range cfg.ActionIDs() {
		if owner != "" && id.Owner != owner {
			continue
		}
		action, ok := cfg.ResolveAction(id)
		if !ok {
			continue
		}
		entries = append(entries, entry{
			ID:          actionIDString(id),
			Owner:       id.Owner,
			OwnerKind:   string(id.OwnerKind),
			Description: action.Description,
			Interactive: action.Interactive != nil && *action.Interactive,
			Confirm:     action.Confirm != nil && *action.Confirm,
		})
	}
	if owner != "" && len(entries) == 0 {
		return &kranzcli.Error{
			Code:     "owner_not_found",
			Message:  fmt.Sprintf("no service or action group named %q defines actions", owner),
			Hint:     "Run `kranz action list` to see every action this project defines.",
			ExitCode: kranzcli.ExitNotFound,
		}
	}
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, entries)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(stdout, "This project defines no actions.")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ACTION\tOWNER\tKIND\tINTERACTIVE\tDESCRIPTION")
	for _, item := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", item.ID, item.Owner, item.OwnerKind, item.Interactive, orDash(item.Description))
	}
	return w.Flush()
}

// resolveActionID matches OWNER/ACTION against the configured actions rather
// than splitting the string and guessing an owner kind, so a service action and
// an action-group action of the same name stay distinguishable.
func resolveActionID(cfg *config.Config, reference string) (config.ActionID, config.Action, error) {
	var matches []config.ActionID
	for _, id := range cfg.ActionIDs() {
		if actionIDString(id) == reference {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 1:
		action, _ := cfg.ResolveAction(matches[0])
		return matches[0], action, nil
	case 0:
		hint := "Run `kranz action list` to see every action this project defines."
		if !strings.Contains(reference, "/") {
			hint = fmt.Sprintf("Actions are named OWNER/ACTION, for example `api/%s`.", reference)
		}
		return config.ActionID{}, config.Action{}, &kranzcli.Error{
			Code:     "action_not_found",
			Message:  fmt.Sprintf("action %q was not found", reference),
			Hint:     hint,
			ExitCode: kranzcli.ExitNotFound,
		}
	default:
		return config.ActionID{}, config.Action{}, &kranzcli.Error{
			Code:     "ambiguous_action",
			Message:  fmt.Sprintf("action %q is defined by more than one owner", reference),
			ExitCode: kranzcli.ExitConflict,
		}
	}
}

func runActionInfo(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "action info takes exactly one OWNER/ACTION", ExitCode: kranzcli.ExitUsage}
	}
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	id, action, err := resolveActionID(cfg, args[0])
	if err != nil {
		return err
	}
	interactive := action.Interactive != nil && *action.Interactive
	confirm := action.Confirm != nil && *action.Confirm
	if options.Output == kranzcli.OutputJSON {
		return kranzcli.WriteJSON(stdout, struct {
			ID          string `json:"id"`
			Owner       string `json:"owner"`
			OwnerKind   string `json:"owner_kind"`
			Description string `json:"description"`
			Command     string `json:"command"`
			Dir         string `json:"dir"`
			Timeout     string `json:"timeout"`
			Interactive bool   `json:"interactive"`
			Confirm     bool   `json:"confirm"`
		}{actionIDString(id), id.Owner, string(id.OwnerKind), action.Description, action.Command, action.Dir, action.Timeout.String(), interactive, confirm})
	}
	_, _ = fmt.Fprintf(stdout, "Action:      %s\n", actionIDString(id))
	_, _ = fmt.Fprintf(stdout, "Owner:       %s (%s)\n", id.Owner, id.OwnerKind)
	if action.Description != "" {
		_, _ = fmt.Fprintf(stdout, "Description: %s\n", action.Description)
	}
	_, _ = fmt.Fprintf(stdout, "Command:     %s\n", orDash(action.Command))
	_, _ = fmt.Fprintf(stdout, "Directory:   %s\n", orDash(action.Dir))
	if action.Timeout > 0 {
		_, _ = fmt.Fprintf(stdout, "Timeout:     %s\n", action.Timeout)
	}
	_, _ = fmt.Fprintf(stdout, "Interactive: %t\n", interactive)
	_, _ = fmt.Fprintf(stdout, "Confirm:     %t\n", confirm)
	return nil
}

func runActionRun(options kranzcli.GlobalOptions, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return &kranzcli.Error{Code: "invalid_arguments", Message: "action run takes exactly one OWNER/ACTION", ExitCode: kranzcli.ExitUsage}
	}
	cfg, _, err := loadProject(options)
	if err != nil {
		return err
	}
	id, action, err := resolveActionID(cfg, args[0])
	if err != nil {
		return err
	}
	// An interactive action needs the real terminal handed to it under a
	// supervisor lease. Refusing it plainly is better than running it with no
	// terminal, where it would block forever on a prompt nobody can answer.
	if action.Interactive != nil && *action.Interactive {
		return &kranzcli.Error{
			Code:     "interactive_action",
			Message:  fmt.Sprintf("action %q is interactive and cannot be run by this command yet", actionIDString(id)),
			Hint:     "Run it from the TUI with `kranz attach`.",
			ExitCode: kranzcli.ExitUsage,
		}
	}

	record, err := resolveSession(options)
	if err != nil {
		return err
	}
	client, err := kranzruntime.DialContext(context.Background(), record.Socket, version)
	if err != nil {
		return classifyRuntimeError(err)
	}
	defer func() { _ = client.Close() }()

	result, err := client.RunAction(context.Background(), id)
	if err != nil {
		return classifyRuntimeError(err)
	}

	if options.Output == kranzcli.OutputJSON {
		if err := kranzcli.WriteJSON(stdout, struct {
			ID       string   `json:"id"`
			Status   string   `json:"status"`
			ExitCode int      `json:"exit_code"`
			Duration string   `json:"duration"`
			Stdout   []string `json:"stdout"`
			Stderr   []string `json:"stderr"`
			Error    string   `json:"error"`
		}{actionIDString(id), result.Status.String(), result.ExitCode, result.Duration.String(), result.Stdout, result.Stderr, result.Error}); err != nil {
			return err
		}
	} else {
		for _, line := range result.Stdout {
			_, _ = fmt.Fprintln(stdout, line)
		}
		for _, line := range result.Stderr {
			_, _ = fmt.Fprintln(stdout, line)
		}
		_, _ = fmt.Fprintf(stdout, "%s %s in %s (exit %d)\n", actionIDString(id), result.Status, result.Duration.Round(1e6), result.ExitCode)
	}

	// A failed action has to fail the command, or a script that runs a
	// migration through Kranz cannot tell that the migration did not apply.
	// The outcome is already reported above, so JSON output carries the exit
	// code alone rather than a second envelope contradicting the first.
	if result.Status != service.ActionSucceeded {
		if options.Output == kranzcli.OutputJSON {
			return requestedExitError{code: kranzcli.ExitInternal}
		}
		return &kranzcli.Error{
			Code:     "action_failed",
			Message:  fmt.Sprintf("action %q %s", actionIDString(id), result.Status),
			ExitCode: kranzcli.ExitInternal,
		}
	}
	return nil
}
