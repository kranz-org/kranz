# Contributing to Kranz

Thank you for improving Kranz. Keep changes focused, user-facing text in English, and configuration behavior backward compatible unless the change is explicitly documented as breaking.

## Development

Kranz requires the Go version declared in `go.mod`.

```bash
git clone https://github.com/kranz-org/kranz.git
cd kranz
make verify
make lint
make build
```

CI runs the same two gates. `make lint` downloads the golangci-lint version pinned in the `Makefile`, so no separate install is needed.

For TUI changes, test both light and dark terminal profiles, narrow terminals down to 64×14, keyboard input, and clickable controls. Add regression tests for lifecycle, persistence, or rendering bugs.

## Documentation

The documentation site is VitePress. `npm run docs:dev` serves it locally,
`npm run docs:build` is the CI gate, and `npm run docs:check-links` validates
every relative link.

Terminal recordings are generated, not captured by hand. Each one has a tape in
`docs/assets/tapes/`, so a recording can be reproduced after the interface
changes:

```bash
make build
vhs docs/assets/tapes/prerequisites.tape
```

Tapes assume they are run from the repository root with a fresh binary in
`./bin`. Keep them short and keep the terminal size consistent with the
existing tapes so the site's frames match.

`examples/reference/kranz.yaml` is included verbatim by the annotated
configuration page and is loaded and validated by a test. Update it when the
schema changes rather than editing the rendered documentation.

## Pull requests

- Create a dedicated branch for every change, including small fixes and
  documentation. Do not commit or push task work directly to `main`.
- Open a pull request, wait for required CI, and merge through GitHub. Prefer
  squash merge unless the individual commits are intentionally meaningful.
- Use a short conventional commit subject such as `feat:`, `fix:`, `docs:`, or `refactor:`.
- Explain the outcome, compatibility impact, and validation performed.
- Update `README.md` and `CHANGELOG.md` for user-visible behavior.
- Never include credentials, private repository URLs, or captured application data.

Minor versions are developed as a sequence of task-sized branches merged into
`main`; do not keep a long-lived version branch as a second integration branch.
Create `release/vX.Y.Z` only when the completed scope is ready for final release
preparation and stabilization. Published history is not rewritten for cosmetic
cleanup.

## Releases

Maintainers release from a clean `main` branch using Semantic Versioning. The
complete public-repository setup and recovery procedure lives in
[`docs/RELEASING.md`](docs/RELEASING.md). The normal release command is:

```bash
./scripts/tag-release.sh 0.1.0
git push origin v0.1.0
```

The tag starts the GitHub release workflow. It verifies the source, builds reproducible Darwin/Linux archives, publishes checksums and provenance, generates `kranz.rb`, and optionally updates a Homebrew tap.

To enable automatic tap updates after the project is public, create a `homebrew-tap` repository and configure:

- Repository variable `HOMEBREW_TAP_REPOSITORY`, for example `kranz-org/homebrew-tap`.
- Repository secret `HOMEBREW_TAP_GITHUB_TOKEN`, containing a fine-grained token
  limited to that tap with `Contents: Read and write` permission.

The source repository's `origin` does not affect this configuration.
