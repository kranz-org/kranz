package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const SchemaVersion = 1

const (
	ExitInternal    = 1
	ExitUsage       = 2
	ExitConfig      = 3
	ExitNotFound    = 4
	ExitConflict    = 5
	ExitUnavailable = 6
)

// Error is a stable, causal command failure suitable for text or JSON output.
type Error struct {
	Code     string
	Message  string
	Hint     string
	ExitCode int
	Cause    error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func usageError(code, message string) *Error {
	return &Error{Code: code, Message: message, ExitCode: ExitUsage}
}

// AsError preserves classified failures and wraps unexpected ones.
func AsError(err error) *Error {
	var commandError *Error
	if errors.As(err, &commandError) {
		return commandError
	}
	return &Error{Code: "internal", Message: "internal error", ExitCode: ExitInternal, Cause: err}
}

type errorEnvelope struct {
	SchemaVersion int          `json:"schema_version"`
	Error         errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// WriteError renders a failure without mixing diagnostics into JSON stdout.
// JSON is written to stdout as the command result; text diagnostics use stderr.
func WriteError(stdout, stderr io.Writer, format OutputFormat, err error) int {
	commandError := AsError(err)
	if format == OutputJSON {
		if encodeErr := json.NewEncoder(stdout).Encode(errorEnvelope{SchemaVersion: SchemaVersion, Error: errorPayload{
			Code: commandError.Code, Message: commandError.Error(), Hint: commandError.Hint,
		}}); encodeErr != nil {
			return ExitInternal
		}
		return commandError.ExitCode
	}
	if _, writeErr := fmt.Fprintf(stderr, "Kranz: %s.\n", commandError.Error()); writeErr != nil {
		return ExitInternal
	}
	if commandError.Hint != "" {
		if _, writeErr := fmt.Fprintln(stderr, commandError.Hint); writeErr != nil {
			return ExitInternal
		}
	}
	return commandError.ExitCode
}

type resultEnvelope struct {
	SchemaVersion int `json:"schema_version"`
	Data          any `json:"data"`
}

// WriteJSON writes a versioned success envelope.
func WriteJSON(w io.Writer, data any) error {
	return json.NewEncoder(w).Encode(resultEnvelope{SchemaVersion: SchemaVersion, Data: data})
}
