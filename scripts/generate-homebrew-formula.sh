#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 VERSION OWNER/REPOSITORY CHECKSUMS OUTPUT" >&2
  exit 2
fi

tag="$1"
repository="$2"
checksums_file="$3"
output="$4"
version="${tag#v}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "version must be semantic and may start with v: $tag" >&2
  exit 2
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "repository must use OWNER/REPOSITORY format: $repository" >&2
  exit 2
fi
if [[ ! -f "$checksums_file" ]]; then
  echo "checksums file does not exist: $checksums_file" >&2
  exit 2
fi

checksum_for() {
  local archive="$1"
  local checksum

  checksum="$(awk -v archive="$archive" '$2 == archive { print $1 }' "$checksums_file")"
  if [[ ! "$checksum" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "expected exactly one SHA-256 checksum for ${archive}" >&2
    exit 1
  fi
  printf '%s' "$checksum" | tr '[:upper:]' '[:lower:]'
}

darwin_arm64_archive="kranz_${version}_Darwin_arm64.tar.gz"
darwin_x86_64_archive="kranz_${version}_Darwin_x86_64.tar.gz"
linux_arm64_archive="kranz_${version}_Linux_arm64.tar.gz"
linux_x86_64_archive="kranz_${version}_Linux_x86_64.tar.gz"

darwin_arm64_sha256="$(checksum_for "$darwin_arm64_archive")"
darwin_x86_64_sha256="$(checksum_for "$darwin_x86_64_archive")"
linux_arm64_sha256="$(checksum_for "$linux_arm64_archive")"
linux_x86_64_sha256="$(checksum_for "$linux_x86_64_archive")"
release_url="https://github.com/${repository}/releases/download/v${version}"

mkdir -p "$(dirname "$output")"
cat >"$output" <<RUBY
class Kranz < Formula
  desc "Keyboard-first local service orchestrator with a terminal UI"
  homepage "https://github.com/${repository}"
  license "MIT"

  on_macos do
    on_arm do
      url "${release_url}/${darwin_arm64_archive}"
      sha256 "${darwin_arm64_sha256}"
    end

    on_intel do
      url "${release_url}/${darwin_x86_64_archive}"
      sha256 "${darwin_x86_64_sha256}"
    end
  end

  on_linux do
    on_arm do
      url "${release_url}/${linux_arm64_archive}"
      sha256 "${linux_arm64_sha256}"
    end

    on_intel do
      url "${release_url}/${linux_x86_64_archive}"
      sha256 "${linux_x86_64_sha256}"
    end
  end

  def install
    bin.install "kranz"
  end

  test do
    assert_match "kranz #{version}", shell_output("#{bin}/kranz --version")
  end
end
RUBY
