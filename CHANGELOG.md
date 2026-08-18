# Changelog

All notable changes to Kranz are documented here. The project follows [Semantic Versioning](https://semver.org/), and release notes are generated from conventional commit subjects.

## [Unreleased]

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

[Unreleased]: https://github.com/kranz-org/kranz/compare/v0.7.2...HEAD
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
