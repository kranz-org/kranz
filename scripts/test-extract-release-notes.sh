#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
extractor="${script_dir}/extract-release-notes.sh"
repository_root=$(CDPATH= cd -- "${script_dir}/.." && pwd)

notes=$("$extractor" 0.4.0 "${repository_root}/CHANGELOG.md")

grep -Fq '### Added' <<<"$notes"
grep -Fq 'Full line editing in the regex log search' <<<"$notes"

if grep -Fq '## [0.3.0]' <<<"$notes"; then
  echo "release notes must stop at the next changelog version" >&2
  exit 1
fi

if "$extractor" 9.9.9 "${repository_root}/CHANGELOG.md" >/dev/null 2>&1; then
  echo "a missing changelog version must fail" >&2
  exit 1
fi
