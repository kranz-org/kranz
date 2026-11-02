package logcommand

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func completed(t *testing.T, command, input string) Snapshot {
	t.Helper()
	p := Start(Options{Command: command, Environment: os.Environ()})
	t.Cleanup(p.Cancel)
	if !p.Send(input, true) {
		t.Fatal("input rejected")
	}
	select {
	case <-p.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("processor did not complete")
	}
	return p.Snapshot()
}

func TestPipelineSupportsExpansionFilteringAndEOFBuffers(t *testing.T) {
	got := completed(t, `sort | awk '!/skip/ { print toupper($0); print "detail" }'`, "zebra\nskip\napple\n")
	if got.Err != nil || strings.Join(got.Lines, "|") != "APPLE|detail|ZEBRA|detail" || !got.Finished {
		t.Fatalf("unexpected pipeline output: %#v", got)
	}
}

func TestLiveInputIsIncrementalAndPreservesBlankLines(t *testing.T) {
	p := Start(Options{Command: `while IFS= read -r line; do printf '%s\n' "$line"; done`, Environment: os.Environ()})
	defer p.Cancel()
	for index, input := range []string{"first\n\n", "last\n"} {
		if !p.Send(input, index == 1) {
			t.Fatal("input rejected")
		}
		deadline := time.After(2 * time.Second)
		for len(p.Snapshot().Lines) < index+2 {
			select {
			case <-p.Changed:
			case <-deadline:
				t.Fatal("live output was buffered")
			}
		}
	}
	select {
	case <-p.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("EOF did not finish the command")
	}
	if got := p.Snapshot(); got.Err != nil || strings.Join(got.Lines, "|") != "first||last" {
		t.Fatalf("unexpected live output: %#v", got)
	}
}

func TestFailedCommandsKeepBoundedDiagnostics(t *testing.T) {
	got := completed(t, `printf 'partial\n'; awk 'BEGIN { for(i=0;i<20000;i++) printf "error" }' >&2; exit 7`, "")
	if got.Err == nil || !strings.Contains(got.Err.Error(), "exit status 7") || len(got.Err.Error()) > 8300 || len(got.Lines) != 1 {
		t.Fatal("failed command did not retain bounded diagnostics and output")
	}
}

func TestProcessorBoundsRetainedAndUnfinishedOutput(t *testing.T) {
	t.Run("retention", func(t *testing.T) {
		got := completed(t, `awk 'BEGIN { for(i=0;i<22000;i++) printf "%0300d\n", i }'`, "")
		if got.Err != nil || got.First == 0 || len(got.Lines) > MaxOutputLines {
			t.Fatal("output retention was not bounded")
		}
		bytes := 0
		for _, line := range got.Lines {
			bytes += len(line) + 1
		}
		if bytes > MaxOutputBytes {
			t.Fatal("output byte budget exceeded")
		}
	})
	t.Run("unfinished line", func(t *testing.T) {
		got := completed(t, `head -c 1100000 /dev/zero | tr '\000' x`, "")
		if got.Err == nil || !strings.Contains(got.Err.Error(), "1 MiB") {
			t.Fatal("unbounded partial line was accepted")
		}
	})
}

func TestCancelStopsThePipelineChildren(t *testing.T) {
	p := Start(Options{Command: `sleep 60 & child=$!; printf '%s\n' "$child"; wait`, Environment: os.Environ()})
	defer p.Cancel()
	deadline := time.After(2 * time.Second)
	for len(p.Snapshot().Lines) == 0 {
		select {
		case <-p.Changed:
		case <-deadline:
			t.Fatal("child did not start")
		}
	}
	pid, err := strconv.Atoi(p.Snapshot().Lines[0])
	if err != nil {
		t.Fatal("child did not report its process identity")
	}
	p.Cancel()
	select {
	case <-p.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline cancellation blocked")
	}
	// A killed child may briefly remain a zombie until its parent is reaped.
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pipeline child survived cancellation")
}
