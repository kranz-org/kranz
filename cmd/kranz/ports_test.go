package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kranzcli "github.com/kranz-org/kranz/internal/cli"
)

const portsProject = `project: Ports
services:
  api:
    command: sleep 60
    ports: [65401]
  web:
    command: sleep 60
  quiet:
    command: sleep 60
`

func portsDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte(portsProject), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func withDetectedPorts(t *testing.T, ports map[string][]int) {
	t.Helper()
	previous := detectedPorts
	detectedPorts = func(kranzcli.GlobalOptions) map[string][]int { return ports }
	t.Cleanup(func() { detectedPorts = previous })
}

// The port a service picked at runtime is the one the user needs, and it exists
// only in the running runtime. Reading the configuration alone answered what
// the file says rather than what is listening.
func TestPortsReportsDeclaredAndDetectedPorts(t *testing.T) {
	withDetectedPorts(t, map[string][]int{
		"api": {65401, 65402},
		"web": {65403},
	})

	output := runInspection(t, portsDirectory(t), "ports")

	for _, want := range []string{"65401", "65402", "65403"} {
		if !strings.Contains(output, want) {
			t.Errorf("ports omits %s:\n%s", want, output)
		}
	}
	// A port that is both declared and detected is one port, reported once as
	// declared rather than listed twice.
	if strings.Count(output, "65401") != 1 {
		t.Errorf("declared and detected port reported twice:\n%s", output)
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[1] {
		case "65401":
			if fields[2] != "declared" {
				t.Errorf("65401 origin = %s, want declared", fields[2])
			}
		case "65402", "65403":
			if fields[2] != "detected" {
				t.Errorf("%s origin = %s, want detected", fields[1], fields[2])
			}
		}
	}
	if strings.Contains(output, "quiet") {
		t.Errorf("a service with no ports appeared:\n%s", output)
	}
}

// A project that is not running is the ordinary case, not a failure: ports must
// still answer from the configuration alone.
func TestPortsWorksWithoutARuntime(t *testing.T) {
	withDetectedPorts(t, nil)

	output := runInspection(t, portsDirectory(t), "ports")
	if !strings.Contains(output, "65401") || !strings.Contains(output, "declared") {
		t.Errorf("ports without a runtime = %q", output)
	}
}

// Reporting nothing has to say why, and what would make there be something.
func TestPortsWithNothingToReportSaysWhatWouldHelp(t *testing.T) {
	withDetectedPorts(t, nil)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "kranz.yaml"), []byte("project: Quiet\nservices:\n  api:\n    command: sleep 60\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output := runInspection(t, directory, "ports")
	if !strings.Contains(output, "kranz up -d") {
		t.Errorf("empty ports output does not say how to get detected ports:\n%s", output)
	}
}

func TestPortsSelectorNarrowsTheReport(t *testing.T) {
	withDetectedPorts(t, map[string][]int{"web": {65403}})

	output := runInspection(t, portsDirectory(t), "ports", "web")
	if !strings.Contains(output, "65403") {
		t.Errorf("selected service is missing:\n%s", output)
	}
	if strings.Contains(output, "65401") {
		t.Errorf("selector did not narrow the report:\n%s", output)
	}
}

func TestPortsJSONCarriesOrigin(t *testing.T) {
	withDetectedPorts(t, map[string][]int{"web": {65403}})

	var envelope struct {
		Data []struct {
			Service string `json:"service"`
			Port    int    `json:"port"`
			Origin  string `json:"origin"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(runInspection(t, portsDirectory(t), "ports", "--output", "json")), &envelope); err != nil {
		t.Fatal(err)
	}
	origins := make(map[int]string, len(envelope.Data))
	for _, item := range envelope.Data {
		origins[item.Port] = item.Origin
	}
	if origins[65401] != "declared" || origins[65403] != "detected" {
		t.Errorf("origins = %v", origins)
	}
}
