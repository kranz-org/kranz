#!/usr/bin/env bash
set -euo pipefail

version=${1:?usage: extract-release-notes.sh VERSION [CHANGELOG]}
changelog=${2:-CHANGELOG.md}
heading="## [${version}]"

awk -v heading="$heading" '
  index($0, heading) == 1 {
    found = 1
    next
  }
  found && /^## \[/ {
    exit
  }
  found {
    print
  }
  END {
    if (!found) {
      exit 1
    }
  }
' "$changelog"
