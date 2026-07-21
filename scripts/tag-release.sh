#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 VERSION" >&2
  exit 2
fi

version="${1#v}"
tag="v${version}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "version must follow semantic versioning: $1" >&2
  exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "working tree must be clean before creating a release tag" >&2
  exit 1
fi
if git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null; then
  echo "tag already exists: ${tag}" >&2
  exit 1
fi

make verify
git tag -a "$tag" -m "Kranz ${tag}"
echo "Created ${tag}. Review it, then publish with: git push origin ${tag}"
