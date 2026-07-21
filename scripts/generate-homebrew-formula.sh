#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 VERSION OWNER/REPOSITORY OUTPUT" >&2
  exit 2
fi

tag="$1"
repository="$2"
output="$3"
version="${tag#v}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "version must be semantic and may start with v: $tag" >&2
  exit 2
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "repository must use OWNER/REPOSITORY format: $repository" >&2
  exit 2
fi

source_url="https://github.com/${repository}/archive/refs/tags/v${version}.tar.gz"
sha256="${SOURCE_SHA256:-}"
if [[ -z "$sha256" ]]; then
  sha256="$(curl --fail --silent --show-error --location "$source_url" | shasum -a 256 | awk '{print $1}')"
fi
if [[ ! "$sha256" =~ ^[0-9a-fA-F]{64}$ ]]; then
  echo "SOURCE_SHA256 must contain 64 hexadecimal characters" >&2
  exit 2
fi
sha256="$(printf '%s' "$sha256" | tr '[:upper:]' '[:lower:]')"

mkdir -p "$(dirname "$output")"
cat >"$output" <<RUBY
class Kranz < Formula
  desc "Keyboard-first local service orchestrator with a terminal UI"
  homepage "https://github.com/${repository}"
  url "${source_url}"
  sha256 "${sha256}"
  license "MIT"

  depends_on "go" => :build

  def install
    ldflags = %W[
      -s -w
      -X main.version=#{version}
      -X main.commit=v#{version}
      -X main.buildTime=release
    ]
    system "go", "build", *std_go_args(ldflags:), "./cmd/kranz"
  end

  test do
    assert_match "kranz #{version}", shell_output("#{bin}/kranz --version")
  end
end
RUBY
