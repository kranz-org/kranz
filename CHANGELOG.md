# Changelog

All notable changes to Kranz are documented here. The project follows [Semantic Versioning](https://semver.org/), and release notes are generated from conventional commit subjects.

## [Unreleased]

### Added

- Stable absolute `SERVICE#N` and `OWNER/ACTION#N` identities across the TUI,
  CLI, runtime API, and MCP. Each real start has provenance, start reason,
  timing, live/final status, PID, exit code, and cause in a bounded per-target
  run catalog independent from log output and the transition journal.
- A run-aware TUI log viewer with Combined and Single run modes, previous/next
  and previous-failed navigation, a status-filterable keyboard/mouse run list,
  independent scroll/follow/search state, immutable historical pin/split
  snapshots, newer-run indication, and a quick return to latest.
- Explicit selected-run export to the terminal clipboard or a user-chosen file,
  including identity, provenance, canonical capture metadata, and exact
  truncation information. Run history remains scoped to the live runtime and
  is never persisted in the background.
- `kranz runs` and MCP run retention metadata expose the oldest retained run,
  independent run/entry/byte budgets, evicted summaries, and complete,
  partial, or unavailable output state.
- MCP runtime discovery through `kranz://runtimes` and the read-only `runtimes`
  tool. Results flag the connection's fixed binding; cross-runtime selector
  misses name matching sessions instead of implying global unavailability.
- `kranz mcp --attach-only` for agent registrations that must fail rather than
  silently create a missing runtime. Deliberate owner fallback now reports
  `owner_reason: created_missing_runtime` and hints about other live sessions.

### Changed

- Stdout, stderr, and lifecycle markers now share one capture-ordered sequence
  with timestamp and source. Config reloads appear inside the continuing run
  with their new generation instead of looking like a process restart.
- Relative log selectors such as `--run -1` resolve against the run catalog,
  while output always prints the absolute `#N` identity. A retained run whose
  prefix was evicted reports exact missing lines and bytes before its tail.

### Fixed

- Run-view titles now distinguish auto-updating combined output, the latest
  isolated run, and historical runs without conflating "live" process state
  with history position. Pinned panels show the `Shift+3` unpin action, which
  now removes the existing pin from any focused panel.
- Config and MCP service views now redact credential-bearing URLs even when
  their environment variable name is not itself secret-looking.
- Replacing an in-process runtime can no longer capture the outgoing owner's
  project directory as its restore point, which could leave an embedded client
  or test process in a deleted temporary directory after shutdown.

## [0.9.0] - 2026-08-25

### Added

- A foreground stdio MCP adapter in the existing `kranz` binary. Coding agents
  attach to the same registry/IPC runtime as the TUI and CLI, with owner
  fallback only when no runtime exists.
- Versioned MCP resources and an explicit safe tool allow-list for services,
  plans, ports, bounded normalized logs, waits, actions, and lifecycle changes.
- Shared application contracts for selectors, plans, confirmation tokens,
  action-run result history, config redaction, primary service actions, and log
  queries used by both CLI and MCP.
- A runtime journal, and the `changes` tool that reads it. The difference
  between two `status` results is not what happened: a service that crashed and
  was restarted looks identical to one that never moved. `changes` returns
  service transitions, detected-port changes, action runs, and configuration
  reloads after a cursor, and reports `truncated` when its bounded history has
  already dropped part of the answer. `wait` hands back the same cursor, so
  "what happened while I waited" is one follow-up call.
- Structured causes on service state. A service that stayed stopped because a
  prerequisite failed now says so, naming the action and the run of it that
  failed, and the same applies to a port conflict, a failed dependency, a start
  that could not exec, and an unsuccessful exit. Reading the causal chain out of
  log text was the part an agent could get wrong.
- Probe detail on health: the target each readiness and liveness check
  contacted and the error it last returned, through the new `health` tool with
  its recorded history.
- Run numbers for services, not only actions. Every start opens a numbered run
  and the lines it produces carry it, so `kranz logs api --run -1` reads the
  newest start alone instead of a time range guessed around a restart. A service
  line is labelled with its run only when the window spans more than one start.
- `kranz://graph` and the `graph` tool: nodes for services, action groups, and
  actions, with `dependency`, `prerequisite`, and `owns` edges and live service
  state folded in.
- MCP reaches the rest of the service, action, and group surface it was missing:
  `reload` after an agent edits a configuration, `doctor` for the same preflight
  checks `kranz doctor` runs, `port_inspect` for who holds any local port, and
  loader diagnostics in `kranz://config`.
- Every MCP tool declares an `outputSchema`, and `resources/templates/list`
  answers with an empty list rather than a method-not-found error.

### Fixed

- `kranz down` now completes an MCP-owned runtime instead of only stopping its
  managed application state. It removes the old registry session before
  disconnecting attached MCP bridges, closes every supervisor client without a
  shutdown deadlock, and waits for disappearance before printing `Stopped`, so
  an MCP client's immediate restart can initialize against a fresh owner.
- An MCP tool that panics now fails that one call. The MCP process may also be
  the supervisor, where an unrecovered panic took every managed service down
  with it; a service the runtime declines to answer for is reported as
  `service_unavailable` instead of dereferencing nil.
- A resolved plan with no targets no longer reports one empty wave, which a
  reader counting waves took for work to do.
- A request cancellation whose id is spelled differently than the request spelled
  it (`1` against `1.0`) now cancels that request.
- `kranz doctor` and the MCP preflight run one implementation of the checks
  rather than two that could drift apart.
- A `wait` that times out says so. The runtime now owns the wait deadline; the
  delivery adapter's own request deadline expired first and reported a timeout
  as a cancellation, which named neither what it was waiting for nor why.
- A prerequisite failure keeps its identity across the runtime socket. An
  attached client received "operation_failed" and a sentence to parse, while
  the owner process saw the structured failure; both now get
  `prerequisite_failed` with the service, the gating action, and its run.

## [0.8.2] - 2026-08-24

### Added

- `--help` documents the options each command parses, not just their spellings
  in the usage line. A flag whose value carries meaning now says what it means:
  `--run N` explains that a negative N counts back from the newest buffered run,
  so `--run -2` is the run before the latest.
- The CLI reference documents `--run`, `--runs`, `--source`, `--with-actions`,
  the display flags, and `kranz logs clear`, which it had omitted.
- Shell completion covers each command's own options, not just the command
  names: `kranz logs --<TAB>` offers the log flags and `kranz logs clear --<TAB>`
  offers only the two that command takes. An option with a fixed set completes
  its values, and one that takes a path completes filenames.

### Fixed

- The generated bash completion no longer uses `mapfile`, which the bash 3.2
  that macOS ships does not have; sourcing it there answered every keystroke
  with "command not found".
- `kranz logs -f` no longer looks like a `--follow` shorthand. The global parser
  claims `-f` as `--config` wherever it appears, so the alias could never reach
  the logs command; `--follow` is the spelling.

## [0.8.1] - 2026-08-23

### Added

- `kranz logs OWNER/ACTION` reads an action's output after it has finished, so a
  report or a migration can be read again without running it a second time.
  Actions are addressed by the same name `kranz action run` uses.
- Run addressing for action logs. Every execution is numbered and framed in the
  buffer; `--run N` selects one, `--run -1` the latest, and `--runs N` the last
  N. `--since` still narrows by time, and the three compose with `--tail`.
- `--with-actions` folds an owner's actions into one timeline with the owner's
  own output, labelled per line. An action group has no output of its own, so
  its bare name reports what to name instead.
- `kranz logs clear [SELECTOR ...]` discards buffered history for what it names,
  or for the whole project with `--force`.
- Display flags for reading one stream back: `--plain` prints the output as the
  command printed it, `--no-timestamps` and `--no-labels` drop one column each,
  and `--source stdout|stderr|kranz` narrows by origin before the tail applies.
- `kranz status` accepts tags, which the other selector commands already did.

### Changed

- A bare action selector now shows its whole latest run. An action produces a
  finite report, and capping it at the last lines cut off the part explaining
  what the run did. Services keep the recent-lines default; `--tail` and `--all`
  override both.
- A selector has one meaning across the CLI: a name a service answers to means
  that service, and a name no service answers to is tried as a tag. `plan` and
  `ports` previously unioned the two, so they could cover different services
  than `start` and `stop` did for the same word.
- Log lines are labelled `[stream source]` rather than `[service/source]`,
  because the stream address itself now contains a slash for an action.
- JSON log events carry `stream`, `kind`, `owner`, `action`, and `run` in place
  of `service`.
- A project may no longer define an action group and a service with the same
  name. Every action under such a group was ambiguous and unreachable, while the
  configuration still validated cleanly; `kranz config check` now says so.

### Fixed

- `--tail N` returns N lines. A pipe hands Kranz whatever chunk it read, and a
  chunk could hold many lines while counting as one, so the flag returned an
  unpredictable number of lines and could cut in the middle of one. Captured
  output is now stored one line per entry, which also corrects the log search
  hit count in the interface, the `N lines omitted` cap on a failed lifecycle
  hook, and the service prefix printed by a foreground `kranz up`, all of which
  counted chunks.
- `kranz action run --output json` returns one array element per output line.

## [0.8.0] - 2026-08-21

### Added

- A complete command-line workflow over one project runtime. `kranz up -d`
  leaves a project running in the background; `kranz ps`, `status`, `logs`,
  `start`, `stop`, `restart`, `reload`, `attach`, and `down` drive it from any
  terminal, and `down --force` recovers a session that has stopped answering.
- `kranz init` creates a configuration, as a wizard when there is a terminal
  and from flags when there is not. It converts an existing Kranz, Process
  Compose, or Procfile source, offers `package.json` scripts as actions without
  running them, previews the file before writing it, never replaces an existing
  file without consent, and reloads what it wrote before reporting success.
- Project inspection that needs no running runtime: `config check`,
  `config show` with secrets redacted, `config explain` for per-field
  provenance, `doctor`, `list`, `info`, `plan`, `graph`, `ports`, and
  `port inspect`. `plan` prints the same dependency waves the supervisor gates
  readiness on.
- `kranz logs` with selectors, `--tail`, `--since`, and `--follow`. Log entries
  carry a source and a monotonic sequence, both preserved across a hot reload,
  so following resumes from a cursor instead of reprinting. A stopped service
  keeps its buffer.
- `kranz action list`, `action info`, and `action run`. An action is identified
  by owner and name together, and a failed action fails the command.
- `kranz completion` for bash, zsh, and fish, generated from the command tree so
  a shell cannot offer a command the binary does not have.
- `--output json` on non-interactive result commands, wrapping results and
  failures in one versioned envelope, with exit codes that distinguish usage,
  configuration, missing, conflicting, and unavailable. Mutation results name
  the runtime and services they changed; `init` and `up -d` report the
  resources they created, and failed diagnostics remain one valid result
  envelope with a non-zero exit code.
- Linux `.deb` and `.rpm` packages for amd64 and arm64, installing the binary,
  the shell completions, and the documentation. Package verification pins the
  container platform to the package architecture, so it also works from an
  Apple Silicon release machine; set `PACKAGE_ARCH=arm64` to test arm64.

### Changed

- A command group runs its obvious subcommand when invoked bare: `kranz config`
  shows the configuration, `kranz action` lists actions, and `kranz port 8080`
  inspects that port.
- `kranz ports` reports the ports a running runtime saw its services open, not
  only the ports the configuration declares.
- `kranz status` reports health, uptime, and detected ports, and shows `-`
  rather than `0` for a service that has no process of its own. `kranz ps`
  reports running and total services instead of a bare total.
- A service with no configured readiness or liveness probe now shows `-`
  instead of the misleading `ready`; JSON uses `null` for unconfigured
  probes.
- `kranz info SERVICE` adds what the service is doing right now when a runtime
  is up.
- `start`, `stop`, `restart`, `reload`, and `down` say what they changed. A stop
  that expands to dependents names all of them.
- A bare `kranz logs` returns the last fifty lines instead of every buffered
  line; `--all` returns everything.
- `kranz graph` draws a dependency tree instead of listing each service with its
  dependencies indented beneath it.
- `kranz config explain` on a single-layer project says every field comes from
  that layer instead of repeating the filename on every row, and lists only
  leaf fields rather than every mapping on the way down.
- A command that needs a runtime the project has not started now says how to
  start one instead of reporting that a runtime the user never named is missing.
- The positional configuration form is removed. `kranz prod.yaml` becomes
  `kranz -f prod.yaml`, and the old shape is recognised and answered with that
  correction. Bare `kranz` still opens the TUI and every existing configuration
  file loads unchanged.
- `-p` is optional for every command. Without it, the target runtime is the one
  the working directory's configuration names; with it, the explicit value
  always wins, including from a directory that has a project of its own.
  Previously `status` resolved the runtime from the directory while `start`,
  `stop`, `restart`, `reload`, and `down` required `-p`, so a service the tool
  had just listed could not be acted on without naming the project again.

## [0.7.2] - 2026-08-18

### Fixed

- Services, action groups, and actions are now listed in the order the
  configuration declares them instead of alphabetically. Layered configurations
  keep the order of the base file and append only the entries an override
  introduces, in the order the override declares them.

### Security

- The documentation site now builds against a patched Vite, resolving three
  Vite development server advisories and one esbuild development server
  advisory reported against the previous transitive dependency.

## [0.7.1] - 2026-08-17

### Fixed

- Double-clicking a service or action group now opens it consistently with the
  keyboard interaction.
- Mouse clicks now make the focused service or action the footer command target
  instead of operating on the service selected during startup. Explicit
  checkbox multi-selection is preserved, and service-only controls are hidden
  and blocked while an action or action group is focused.

## [0.7.0] - 2026-08-13

### Added

- Detached service supervision with optional lifecycle start, stop, status,
  and log commands, observe-only resources, external-state reconciliation, and
  `stop_on_exit` ownership.
- Configurable lifecycle start confirmation and a runnable detached lifecycle
  playground covering actions, health, dependencies, and status transitions.
- A VitePress documentation site configured for GitHub Pages at `/kranz/`,
  including guides, reference pages, safe runnable examples, and link checks.
- Beginner-oriented documentation, individual walkthroughs for every runnable
  example, theme-aware brand artwork, and responsive SVG lifecycle diagrams.
- `before_start` prerequisites: a service can require named actions to succeed
  before it starts, referencing its own, another service's, or an action
  group's action, running them once per session or before every start, and
  sharing one run between services that require the same prerequisite.
- A runnable prerequisites example, an annotated reference configuration that
  is loaded and validated by a test, a complete field-by-field configuration
  reference, and new CLI, appearance, troubleshooting, and Process Compose
  compatibility pages.
- Interactive actions: `interactive: true` hands the real terminal to a command
  that has to be answered, such as a migration that confirms before it writes,
  and records its exit code and duration when it finishes. Running one always
  asks first, warning that Kranz is about to leave the screen, so the interface
  never disappears unannounced. Lifecycle commands
  and prerequisites cannot be interactive, because both run unattended.
- A MoonFlight showcase example: shared detached infrastructure, a migration
  other services wait to finish, two APIs behind an edge gateway, a front end
  on a runtime-discovered port with a prerequisite, two workers, and a project
  action group. It is the project shown in the documentation recordings.
- Reproducible terminal recordings generated from tapes in
  `docs/assets/tapes/`, one per feature: actions, interactive handoff,
  dependency gates, log search, appearance, prerequisites, detached lifecycle,
  runtime ports, and the Procfile quickstart. The site hero and the quickstart
  were previously captured by hand and could not be reproduced.

### Changed

- `command` is now shorthand for `lifecycle.start` and is normalized before
  layered configuration merging.
- A lifecycle status probe now follows the ordinary shell convention by
  default: exit `0` means running and any other exit code means stopped.
  Declaring `stopped_exit_codes` opts into the three-way contract in which an
  unlisted code is unclassified and becomes `unknown`. A probe that produced no
  exit code at all is never reported as stopped.
- `lifecycle.status.stopped_interval` defaults to a flat `30s` instead of a
  value derived from `interval`.
- TUI service stops always require confirmation, including `s`, `Shift+S`,
  restart, and all-service variants.
- The README is now a concise project overview and quickstart, with detailed
  usage moved into the documentation site.
- Detached services with a status probe show a neutral checking state before
  the first observation and can attach to an already-running external resource
  without invoking the start command again.
- Successful lifecycle commands keep noisy tool progress out of service logs;
  failed lifecycle commands retain bounded diagnostic output.
- Quit confirmation now presents the actual exit plan, separating managed
  processes, detached stop commands, and detached resources that will remain
  running, with retained resources visually emphasized.
- The primary list panel is labeled `SERVICES`, `ACTIONS`, or
  `SERVICES/ACTIONS` according to whether it contains services, top-level
  action groups, or both.
- Lifecycle start confirmation highlights each affected service and the exact
  command awaiting approval, including confirmed dependency starts.
- Confirmed actions use the same visual hierarchy for their owner,
  description, and command so consequential operations stand out before run.

### Fixed

- Quitting Kranz while an action holds the terminal no longer blocks shutdown
  forever waiting for a command only the user can finish.
- Documentation recordings sit in an evenly padded frame instead of gaining
  vertical-only spacing from the surrounding paragraph.
- `disabled` services are now actually excluded from select-all and start-all
  batch operations instead of only displaying a badge that claimed they were.
- An out-of-range service status is no longer rendered as the deliberate
  `unknown` state.

## [0.6.1] - 2026-08-10

### Changed

- Modal dialogs now use borderless elevated surfaces with more deliberate spacing and an adaptive color-preserving scrim for both light and dark themes.

### Fixed

- Mouse tracking is periodically re-enabled and restored on focus, so clicks recover when an integrated terminal silently drops mouse reporting.
- Returning focus to the terminal no longer starts a background-color probe that could briefly flash or repaint the TUI.
- Global and project appearance saves now require explicit confirmation, show the exact destination, and reuse the theme picker's styled appearance summary.

## [0.6.0] - 2026-08-06

### Added

- Live six-digit hex editors for custom accent and canvas colors in the theme picker, including keyboard editing, paste, mouse focus, immediate swatches, and preview updates.
- Custom `#RRGGBB` canvas values in `ui.background`; Kranz derives the remaining palette and readable text set from the selected canvas.
- A theme-picker action to reload the saved project and personal appearance without restarting Kranz.

### Changed

- Accent and canvas controls now cycle among the sources that actually exist, preserving custom colors after another source is previewed.
- Theme previews now resolve the complete candidate appearance, including project, theme, and custom color sources, and modal styling is consistent across confirmations and editors.
- Service Details are organized more clearly, project-local working directories are shown relative to the directory Kranz runs in, and stopped services with a dynamic health target show `[PORT]` instead of suggesting that listener detection is still running.

### Fixed

- Theme-picker state and layout remain stable while sources are edited, applied, saved, or reloaded.
- Modal borders are painted on the modal surface instead of inheriting the canvas behind them.
- User-entered accent colors are rendered verbatim rather than being silently shifted to meet an automatic contrast floor.

## [0.5.0] - 2026-08-04

### Added

- Native zero-configuration loading for `Procfile` and `Procfile.dev`, including strict parsing, adjacent `.env` loading, configuration watching, and commands that run from the Procfile directory.
- Runtime discovery of TCP listeners opened by a service or its child processes on macOS and Linux, with distinct declared and detected port roles in Details.
- Dynamic TCP and HTTP health targets that can follow a single detected listener or select one from a sorted multi-port service.
- Runnable Procfile, native Kranz YAML, Process Compose, and runtime-port examples in the repository.

### Changed

- Configuration auto-discovery now falls back to `Procfile.dev` and then `Procfile` after native Kranz and Process Compose files.
- Dashboard panel titles now sit within their borders, preserving more room for service details and logs while making focus clearer.
- The theme picker now separates temporary apply, global save, and project save actions, with clearer shortcuts and layout.

### Fixed

- Linux listener smoke coverage now verifies that its real test socket is released after inspection.
- Release automation now publishes the matching version section from this changelog as the GitHub Release body.

## [0.4.0] - 2026-08-01

### Added

- Full line editing in the regex log search, with caret movement, `Home`/`End`, `Ctrl+W` word deletion, `Ctrl+U` erase to the caret, and `Ctrl+V` paste.
- Horizontal scrolling in the search editor so a pattern wider than the bar stays visible under the caret.
- `Esc` in the dashboard to clear an active log filter, separating leaving the search from resetting the filter.
- A blink on the filtered log panel when a click lands outside the open search editor.
- A pinned golangci-lint configuration and a CI lint job.

### Changed

- `Enter` in the regex log search now applies the query without closing the editor, so a pattern can be refined in place.
- `Esc` in the regex log search now closes the editor and keeps the applied filter, discarding edits made since the last `Enter`.
- `Tab` now jumps to the first match when switching to highlight mode over an applied pattern.
- Opening the search now focuses the log panel being filtered, from both the keyboard and the footer control.
- `make lint` now runs a pinned golangci-lint through `go run` and no longer requires a local install.
- Race-detector tests now run on macOS as well as Linux in CI.

### Removed

- An unreachable start-all code path in the UI layer that was never bound to a key.

### Fixed

- Follow-up messages from the search editor are now forwarded to it, so clipboard paste and the caret blink work.
- A `//nolint` directive in the terminal background probe that used an unsupported separator and therefore suppressed nothing.
- Deprecated `lipgloss.Style.Copy` calls.
- A configuration test that wrote into the tracked `testdata` directory and left process environment changes behind.
- Log searcher tests that ignored the error from setting their pattern, so a broken pattern would have made them assert nothing.

## [0.3.0] - 2026-07-27

### Added

- `Left` and `Right` navigation to cycle the focused Services/Tags panel.
- Color-coded service state in focused and pinned log titles.
- Distinct lifecycle log boundaries for starts, stops, exits, and recovery attempts.
- Last-start, uptime, last-exit, and clearer restart-limit information in Details.
- Dependency-aware shutdown that stops transitive dependents before their dependencies.
- `Shift+S` forced shutdown for stopping only the selected targets.

### Changed

- Log clearing now asks for confirmation and targets the focused log panel, including pinned logs.
- Ordinary service output now uses neutral theme text while source prefixes are muted and Kranz lifecycle messages use a dedicated system color.
- Panel titles now separate their dynamic metadata consistently with a vertical divider.

### Fixed

- Mouse hover and wheel events now focus and scroll the panel beneath the pointer.
- The Forest theme now uses neutral primary text instead of a green-tinted near-white.

## [0.2.0] - 2026-07-26

### Added

- Expandable tag groups with aggregate details and inline service navigation.
- Tag selection that automatically selects every matching service.
- `Tab` and `Shift+Tab` navigation across dashboard panels, including pinned logs.

### Fixed

- Disabled ambiguous log pinning while a tag group row is focused.

## [0.1.1] - 2026-07-25

### Added

- Compact dashboard panels that collapse inactive sections in short terminals.
- Width-aware Details rendering for ports, ownership, directories, descriptions, tags, dependencies, checks, lifecycle settings, environment files, and commands.

### Fixed

- Mouse-wheel navigation in the Services and Tags panel.
- Homebrew formula generation to install the published release binaries correctly.

## [0.1.0] - 2026-07-22

### Added

- Keyboard-first service orchestration with dependency-aware and forced startup.
- Readiness and liveness checks, port ownership inspection, and safe external-port release.
- Searchable, wrappable, timestamped, and pinnable service logs.
- Contrast-oriented themes with independent project accent and terminal/theme background sources.
- Light and dark variants for every theme, with persisted Auto/Dark/Light selection.
- Explicit global-user and project-config save destinations in the live theme picker.
- Native compatibility for common Process Compose configurations.

[Unreleased]: https://github.com/kranz-org/kranz/compare/v0.8.2...HEAD
[0.8.2]: https://github.com/kranz-org/kranz/compare/v0.8.1...v0.8.2
[0.8.1]: https://github.com/kranz-org/kranz/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/kranz-org/kranz/compare/v0.7.2...v0.8.0
[0.7.2]: https://github.com/kranz-org/kranz/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/kranz-org/kranz/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/kranz-org/kranz/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/kranz-org/kranz/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/kranz-org/kranz/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/kranz-org/kranz/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/kranz-org/kranz/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/kranz-org/kranz/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/kranz-org/kranz/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/kranz-org/kranz/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/kranz-org/kranz/releases/tag/v0.1.0
