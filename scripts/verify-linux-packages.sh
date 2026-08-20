#!/usr/bin/env bash
# Verify that the built .deb and .rpm install, work, and uninstall cleanly in
# stock images. It is kept out of CI's default path because it needs a
# container runtime; run it before a release from any machine that has one.
#
#   make snapshot
#   ./scripts/verify-linux-packages.sh
#
# Set RUNTIME=podman to use podman instead of docker.
set -euo pipefail

RUNTIME="${RUNTIME:-docker}"
DIST_DIR="${DIST_DIR:-dist}"

if ! command -v "$RUNTIME" >/dev/null 2>&1; then
  echo "verify-linux-packages: $RUNTIME is not installed" >&2
  exit 2
fi

find_package() {
  local extension="$1"
  local match
  match="$(find "$DIST_DIR" -maxdepth 1 -name "*linux_amd64.${extension}" -print -quit)"
  if [ -z "$match" ]; then
    echo "verify-linux-packages: no amd64 .${extension} in ${DIST_DIR}; run 'make snapshot' first" >&2
    exit 2
  fi
  printf '%s' "$match"
}

# The checks below are what a user actually does with an installed package:
# the binary is on PATH, it reports its own version, it reads a configuration,
# the shipped completion is where the shell looks for it, and removing the
# package takes all of that away again.
verify_in_image() {
  local image="$1" package="$2" install_cmd="$3" remove_cmd="$4"
  local package_name
  package_name="$(basename "$package")"

  echo "==> ${image}"
  "$RUNTIME" run --rm \
    -v "$(cd "$(dirname "$package")" && pwd)":/packages:ro \
    "$image" \
    /bin/sh -euc "
      ${install_cmd} /packages/${package_name}

      command -v kranz >/dev/null || { echo 'kranz is not on PATH'; exit 1; }
      kranz --version
      test -f /usr/share/bash-completion/completions/kranz || { echo 'bash completion missing'; exit 1; }
      test -f /usr/share/zsh/site-functions/_kranz || { echo 'zsh completion missing'; exit 1; }
      test -f /usr/share/doc/kranz/LICENSE || { echo 'LICENSE missing'; exit 1; }

      mkdir -p /tmp/project
      printf 'project: Packaged\nservices:\n  api:\n    command: sleep 60\n' > /tmp/project/kranz.yaml
      kranz -C /tmp/project config check >/dev/null || { echo 'config check failed'; exit 1; }
      kranz -C /tmp/project plan >/dev/null || { echo 'plan failed'; exit 1; }

      ${remove_cmd}
      if command -v kranz >/dev/null 2>&1; then echo 'kranz survived removal'; exit 1; fi
      if [ -f /usr/share/bash-completion/completions/kranz ]; then echo 'completion survived removal'; exit 1; fi

      echo 'OK'
    "
}

deb_package="$(find_package deb)"
rpm_package="$(find_package rpm)"

verify_in_image debian:12 "$deb_package" "apt-get update >/dev/null && apt-get install -y" "apt-get remove -y kranz >/dev/null"
verify_in_image ubuntu:24.04 "$deb_package" "apt-get update >/dev/null && apt-get install -y" "apt-get remove -y kranz >/dev/null"
verify_in_image fedora:41 "$rpm_package" "dnf install -y" "dnf remove -y kranz >/dev/null"
verify_in_image rockylinux:9 "$rpm_package" "dnf install -y" "dnf remove -y kranz >/dev/null"

echo
echo "All package images verified."
