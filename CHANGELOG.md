# Changelog

All notable changes to Kranz are documented here. The project follows [Semantic Versioning](https://semver.org/), and release notes are generated from conventional commit subjects.

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

[0.5.0]: https://github.com/kranz-org/kranz/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/kranz-org/kranz/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/kranz-org/kranz/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/kranz-org/kranz/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/kranz-org/kranz/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/kranz-org/kranz/releases/tag/v0.1.0
