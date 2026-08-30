package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript renders one shell's completion into a file the shell can read.
func writeScript(t *testing.T, shell, name string) string {
	t.Helper()
	script, err := Completion(DefaultTree(), shell)
	if err != nil {
		t.Fatalf("%s: %v", shell, err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func lookShell(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return path
}

// bashReply drives the generated function the way the shell does: the words
// typed so far, with the cursor on the last one, and whatever the function puts
// in COMPREPLY as the answer.
func bashReply(t *testing.T, bash, script string, words ...string) []string {
	t.Helper()
	const driver = `source "$1"
shift
COMP_WORDS=("$@")
COMP_CWORD=$(($# - 1))
_kranz
printf '%s\n' "${COMPREPLY[@]}"`
	arguments := append([]string{"-c", driver, "_", script}, words...)
	output, err := exec.Command(bash, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("bash refused the script: %v\n%s", err, output)
	}
	var reply []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			reply = append(reply, line)
		}
	}
	return reply
}

// The generated script is a program, and the only proof it works is a shell
// running it. These cases are the ones a user actually types: a flag prefix, a
// value that has a fixed set, and a subcommand whose flags differ from the
// group's.
func TestBashCompletionAnswersTheWordsAUserTypes(t *testing.T) {
	bash := lookShell(t, "bash")
	script := writeScript(t, "bash", "kranz.bash")

	for _, test := range []struct {
		name    string
		words   []string
		want    []string
		exclude []string
	}{
		{
			name:  "a flag prefix narrows to one flag",
			words: []string{"kranz", "logs", "--ta"},
			want:  []string{"--tail"},
		},
		{
			name:    "a subcommand replaces the group's flags",
			words:   []string{"kranz", "logs", "clear", "--"},
			want:    []string{"--with-actions", "--force"},
			exclude: []string{"--tail", "--follow"},
		},
		{
			name:  "a group offers the flags of the subcommand it runs bare",
			words: []string{"kranz", "logs", "--"},
			want:  []string{"--tail", "--follow", "--run"},
		},
		{
			name:  "a selector does not hide the flags that follow it",
			words: []string{"kranz", "logs", "api", "--r"},
			want:  []string{"--run", "--runs"},
		},
		{
			name:  "an option with a fixed set completes its value",
			words: []string{"kranz", "logs", "--source", ""},
			want:  []string{"stdout", "stderr", "kranz"},
		},
		{
			name:    "the first word completes commands, not flags",
			words:   []string{"kranz", "co"},
			want:    []string{"completion", "config"},
			exclude: []string{"--config"},
		},
		{
			name:  "a group completes its subcommands",
			words: []string{"kranz", "config", ""},
			want:  []string{"check", "explain", "show"},
		},
		{
			name:  "global flags are offered everywhere",
			words: []string{"kranz", "status", "--outp"},
			want:  []string{"--output"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reply := bashReply(t, bash, script, test.words...)
			for _, want := range test.want {
				if !contains(reply, want) {
					t.Errorf("`%s` offers %v, missing %q", strings.Join(test.words, " "), reply, want)
				}
			}
			for _, unwanted := range test.exclude {
				if contains(reply, unwanted) {
					t.Errorf("`%s` offers %q, which that command does not accept", strings.Join(test.words, " "), unwanted)
				}
			}
		})
	}
}

// macOS still ships bash 3.2, so the script has to work without the builtins
// added in bash 4. Sourcing under the system shell is what proves it.
func TestBashCompletionRunsOnTheShellMacOSShips(t *testing.T) {
	bash := lookShell(t, "bash")
	script := writeScript(t, "bash", "kranz.bash")
	if output, err := exec.Command(bash, "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("bash cannot parse the script: %v\n%s", err, output)
	}
	if strings.Contains(readFile(t, script), "mapfile") {
		t.Error("the script uses mapfile, which bash 3.2 does not have")
	}
}

func TestZshCompletionParses(t *testing.T) {
	zsh := lookShell(t, "zsh")
	script := writeScript(t, "zsh", "_kranz")
	if output, err := exec.Command(zsh, "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("zsh cannot parse the script: %v\n%s", err, output)
	}
}

// The zsh script is checked through stubs for the completion builtins it calls,
// which is what makes its branching observable outside an interactive shell.
func TestZshCompletionSelectsTheRightList(t *testing.T) {
	zsh := lookShell(t, "zsh")
	script := writeScript(t, "zsh", "_kranz")
	const stubs = `_describe() { local name=$2; print -r -- ${(P)name}; }
compadd() { local -a rest; rest=("${@:#--}"); print -r -- $rest; }
_files() { print -r -- "<files>"; }
words=("$@")
CURRENT=$#
source "$ZPROBE_SCRIPT"`
	probeScript := filepath.Join(t.TempDir(), "probe.zsh")
	if err := os.WriteFile(probeScript, []byte(stubs), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		words   []string
		want    []string
		exclude []string
	}{
		{words: []string{"kranz", "logs", "--"}, want: []string{"--tail:", "--follow:"}},
		{words: []string{"kranz", "logs", "clear", "--"}, want: []string{"--force:"}, exclude: []string{"--tail:"}},
		{words: []string{"kranz", "logs", "--source", ""}, want: []string{"stdout stderr kranz"}},
		{words: []string{"kranz", "-f", ""}, want: []string{"<files>"}},
		{words: []string{"kranz", ""}, want: []string{"logs:show and clear logs"}},
	} {
		t.Run(strings.Join(test.words, " "), func(t *testing.T) {
			command := exec.Command(zsh, append([]string{"-f", probeScript}, test.words...)...)
			command.Env = append(os.Environ(), "ZPROBE_SCRIPT="+script)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("zsh refused the script: %v\n%s", err, output)
			}
			for _, want := range test.want {
				if !strings.Contains(string(output), want) {
					t.Errorf("`%s` offers %q, missing %q", strings.Join(test.words, " "), output, want)
				}
			}
			for _, unwanted := range test.exclude {
				if strings.Contains(string(output), unwanted) {
					t.Errorf("`%s` offers %q, which that command does not accept", strings.Join(test.words, " "), unwanted)
				}
			}
		})
	}
}

func TestFishCompletionParses(t *testing.T) {
	fish := lookShell(t, "fish")
	script := writeScript(t, "fish", "kranz.fish")
	if output, err := exec.Command(fish, "--no-execute", script).CombinedOutput(); err != nil {
		t.Fatalf("fish cannot parse the script: %v\n%s", err, output)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
