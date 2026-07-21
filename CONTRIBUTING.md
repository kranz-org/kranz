# Contributing to Kranz

Thank you for improving Kranz. Keep changes focused, user-facing text in English, and configuration behavior backward compatible unless the change is explicitly documented as breaking.

## Development

Kranz requires the Go version declared in `go.mod`.

```bash
git clone https://github.com/kranz-org/kranz.git
cd kranz
make verify
make build
```

For TUI changes, test both light and dark terminal profiles, narrow terminals down to 64×14, keyboard input, and clickable controls. Add regression tests for lifecycle, persistence, or rendering bugs.

## Pull requests

- Use a short conventional commit subject such as `feat:`, `fix:`, `docs:`, or `refactor:`.
- Explain the outcome, compatibility impact, and validation performed.
- Update `README.md` and `CHANGELOG.md` for user-visible behavior.
- Never include credentials, private repository URLs, or captured application data.

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
