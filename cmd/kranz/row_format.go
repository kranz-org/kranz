package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"text/template"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

// rowTemplate is the Docker-style formatter shared by commands whose natural
// output is one record per line. Its data is always an explicit map assembled
// by the command, so templates are insulated from internal Go type changes.
type rowTemplate struct {
	table    bool
	template *template.Template
}

func parseRowFormat(command string, output kranzcli.OutputFormat, args []string) (*rowTemplate, error) {
	format := ""
	formatSet := false
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--format":
			if formatSet {
				return nil, rowFormatUsageError(command, "--format may be specified only once")
			}
			if index+1 >= len(args) {
				return nil, rowFormatUsageError(command, "--format requires a template")
			}
			index++
			format = args[index]
			formatSet = true
		case strings.HasPrefix(args[index], "--format="):
			if formatSet {
				return nil, rowFormatUsageError(command, "--format may be specified only once")
			}
			format = strings.TrimPrefix(args[index], "--format=")
			formatSet = true
			if format == "" {
				return nil, rowFormatUsageError(command, "--format requires a template")
			}
		default:
			return nil, rowFormatUsageError(command, fmt.Sprintf("unknown %s argument %q", command, args[index]))
		}
	}
	if !formatSet {
		return nil, nil
	}
	if output == kranzcli.OutputJSON {
		return nil, rowFormatUsageError(command, "--format cannot be combined with --output=json")
	}

	formatter := &rowTemplate{}
	if strings.HasPrefix(format, "table ") {
		formatter.table = true
		format = strings.TrimPrefix(format, "table ")
	}
	if format == "" {
		return nil, rowFormatUsageError(command, "--format requires a template after table")
	}
	// Docker accepts visible escape spellings in a quoted shell argument.
	format = strings.NewReplacer(`\t`, "\t", `\n`, "\n").Replace(format)
	parsed, err := template.New(command).Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"split": strings.Split,
		"join":  strings.Join,
	}).Parse(format)
	if err != nil {
		return nil, rowFormatUsageError(command, "invalid format template: "+err.Error())
	}
	formatter.template = parsed
	return formatter, nil
}

func rowFormatUsageError(command, message string) error {
	return &kranzcli.Error{
		Code:     "invalid_format",
		Message:  message,
		Hint:     fmt.Sprintf("Use `kranz %s --format '{{.Field}}'` or prefix the template with `table `.", command),
		ExitCode: kranzcli.ExitUsage,
	}
}

func (formatter *rowTemplate) write(output io.Writer, headers map[string]any, rows []map[string]any) error {
	if formatter == nil {
		return nil
	}
	// Execute once against the header map even without `table`: this catches an
	// unknown field consistently when the command happens to return zero rows.
	if _, err := formatter.execute(headers); err != nil {
		return rowFormatUsageError(formatter.template.Name(), "invalid format template: "+err.Error())
	}
	w := output
	var aligned *tabwriter.Writer
	if formatter.table {
		aligned = tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
		w = aligned
		if err := formatter.writeRow(w, headers); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := formatter.writeRow(w, row); err != nil {
			return err
		}
	}
	if aligned != nil {
		return aligned.Flush()
	}
	return nil
}

func (formatter *rowTemplate) writeRow(output io.Writer, row map[string]any) error {
	rendered, err := formatter.execute(row)
	if err != nil {
		return rowFormatUsageError(formatter.template.Name(), "invalid format template: "+err.Error())
	}
	if _, err := io.WriteString(output, rendered); err != nil {
		return err
	}
	if !strings.HasSuffix(rendered, "\n") {
		_, err = io.WriteString(output, "\n")
	}
	return err
}

func (formatter *rowTemplate) execute(row map[string]any) (string, error) {
	var output bytes.Buffer
	err := formatter.template.Execute(&output, row)
	return output.String(), err
}
