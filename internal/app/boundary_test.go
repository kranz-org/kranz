package app

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestUIDoesNotImportRuntimePackages proves the application/runtime boundary
// the refactor introduces: internal/ui's production code may only reach the
// runtime through internal/app, never by importing internal/service,
// internal/health, or internal/port directly. Test files are exempt — a test
// fake implementing an interface those packages declare does not violate the
// boundary a real delivery surface must respect.
func TestUIDoesNotImportRuntimePackages(t *testing.T) {
	forbidden := []string{
		"github.com/kranz-org/kranz/internal/service",
		"github.com/kranz-org/kranz/internal/health",
		"github.com/kranz-org/kranz/internal/port",
	}

	uiDir := filepath.Join("..", "ui")
	files, err := filepath.Glob(filepath.Join(uiDir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", uiDir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no files found in %s", uiDir)
	}

	fileSet := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			for _, forbiddenPath := range forbidden {
				if path == forbiddenPath {
					t.Errorf("%s imports %s directly; it must go through internal/app instead", file, path)
				}
			}
		}
	}
}
