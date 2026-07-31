#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
generator="${script_dir}/generate-homebrew-formula.sh"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

checksums="${work_dir}/checksums.txt"
formula="${work_dir}/kranz.rb"

cat >"$checksums" <<'CHECKSUMS'
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  kranz_1.2.3_Darwin_arm64.tar.gz
bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  kranz_1.2.3_Darwin_x86_64.tar.gz
cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  kranz_1.2.3_Linux_arm64.tar.gz
dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  kranz_1.2.3_Linux_x86_64.tar.gz
CHECKSUMS

"$generator" v1.2.3 example/kranz "$checksums" "$formula"

grep -Fq 'kranz_1.2.3_Darwin_arm64.tar.gz' "$formula"
grep -Fq 'kranz_1.2.3_Darwin_x86_64.tar.gz' "$formula"
grep -Fq 'kranz_1.2.3_Linux_arm64.tar.gz' "$formula"
grep -Fq 'kranz_1.2.3_Linux_x86_64.tar.gz' "$formula"
grep -Fq 'sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' "$formula"
grep -Fq 'sha256 "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"' "$formula"
grep -Fq 'sha256 "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"' "$formula"
grep -Fq 'sha256 "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"' "$formula"
grep -Fq 'bin.install "kranz"' "$formula"

if grep -Eq '^[[:space:]]+version "' "$formula"; then
  echo "generated formula must let Homebrew infer the version from its URL" >&2
  exit 1
fi

if grep -Fq 'depends_on "go"' "$formula"; then
  echo "generated formula must not depend on Go" >&2
  exit 1
fi
if grep -Fq '/archive/refs/tags/' "$formula"; then
  echo "generated formula must not download source archives" >&2
  exit 1
fi

grep -v 'Linux_x86_64' "$checksums" >"${checksums}.incomplete"
if "$generator" 1.2.3 example/kranz "${checksums}.incomplete" "$formula" >/dev/null 2>&1; then
  echo "generator accepted an incomplete checksums file" >&2
  exit 1
fi
