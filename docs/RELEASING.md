# Releasing Kranz

Kranz uses Semantic Versioning and treats annotated `vMAJOR.MINOR.PATCH` Git
tags as the release source of truth. A tag triggers the GitHub Actions release
workflow; no release commit or generated binary is checked into the repository.

## One-time public repository setup

1. Confirm that the public `kranz-org/kranz` repository uses `main` as its
   default branch and contains only reviewed release-ready history.
2. Make `main` the default branch. Require the `Go (ubuntu-latest)` and
   `Go (macos-latest)` checks before merging, require pull requests, and prevent
   force pushes and branch deletion.
3. Enable private vulnerability reporting, Dependabot security updates, secret
   scanning, and push protection in the repository security settings.
4. Allow GitHub Actions to create releases and attestations. Keep the workflow's
   default token permissions restricted; the release job declares only the
   permissions it needs.
5. Create `kranz-org/homebrew-tap` if Homebrew installation should be available.
   Add repository variable `HOMEBREW_TAP_REPOSITORY=kranz-org/homebrew-tap` and a
   `HOMEBREW_TAP_GITHUB_TOKEN` repository secret containing a fine-grained token
   limited to that tap with `Contents: Read and write`. Without them, releases
   still include a standalone `kranz.rb`.
6. Keep GitHub's default `bug`, `enhancement`, and `documentation` labels, then
   create the release-note labels used by `.github/release.yml`:

   ```bash
   gh label create breaking --color D73A4A --description "Breaking change"
   gh label create dependencies --color 0366D6 --description "Dependency update"
   gh label create maintenance --color C5DEF5 --description "Internal maintenance"
   gh label create skip-changelog --color EDEDED --description "Exclude from release notes"
   ```

## Prepare a release

1. Move user-visible entries from `Unreleased` into a versioned section in
   `CHANGELOG.md`, including the release date.
2. Confirm the working tree is clean and `main` is up to date.
3. Run the same checks and local packaging used by CI:

   ```bash
   make verify
   make release-check
   make snapshot
   ./dist/kranz_darwin_arm64_v8.0/kranz --version # choose the local platform build
   ```

4. Create an annotated tag without pushing it automatically:

   ```bash
   make tag RELEASE_VERSION=0.1.0
   git show v0.1.0
   git push origin v0.1.0
   ```

## Verify the published release

The workflow must publish four Darwin/Linux archives, `checksums.txt`, and
`kranz.rb`. Verify the release before announcing it:

```bash
gh release view v0.1.0
gh release download v0.1.0 --pattern 'checksums.txt' --pattern '*.tar.gz'
shasum -a 256 -c checksums.txt
```

When the optional tap automation is configured, validate the formula in its
real tap context and install it on a clean machine:

```bash
brew audit --strict --new --online kranz-org/tap/kranz
brew install --build-from-source kranz-org/tap/kranz
brew test kranz-org/tap/kranz
```

## Failed releases

Do not move an existing tag. Fix the cause, update the changelog, and create a
new patch version. GitHub release artifacts and build provenance remain tied to
the immutable source tag.
