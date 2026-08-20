#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
verifier="${script_dir}/verify-linux-packages.sh"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

dist_dir="${work_dir}/dist"
runtime="${work_dir}/container-runtime"
runtime_log="${work_dir}/runtime.log"
mkdir -p "$dist_dir"
touch \
  "${dist_dir}/kranz_0.8.0_linux_amd64.deb" \
  "${dist_dir}/kranz_0.8.0_linux_amd64.rpm" \
  "${dist_dir}/kranz_0.8.0_linux_arm64.deb" \
  "${dist_dir}/kranz_0.8.0_linux_arm64.rpm"

cat >"$runtime" <<'RUNTIME'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_RUNTIME_LOG:?}"
RUNTIME
chmod +x "$runtime"

FAKE_RUNTIME_LOG="$runtime_log" \
  RUNTIME="$runtime" \
  DIST_DIR="$dist_dir" \
  "$verifier" >/dev/null

test "$(grep -Fc -- '--platform linux/amd64' "$runtime_log")" -eq 2
grep -Fq 'kranz_0.8.0_linux_amd64.deb' "$runtime_log"
grep -Fq 'kranz_0.8.0_linux_amd64.rpm' "$runtime_log"

: >"$runtime_log"
FAKE_RUNTIME_LOG="$runtime_log" \
  RUNTIME="$runtime" \
  DIST_DIR="$dist_dir" \
  PACKAGE_ARCH=arm64 \
  "$verifier" >/dev/null

test "$(grep -Fc -- '--platform linux/arm64' "$runtime_log")" -eq 2
grep -Fq 'docker.io/library/debian:12' "$runtime_log"
grep -Fq 'fedora:43' "$runtime_log"
grep -Fq 'kranz_0.8.0_linux_arm64.deb' "$runtime_log"
grep -Fq 'kranz_0.8.0_linux_arm64.rpm' "$runtime_log"

if FAKE_RUNTIME_LOG="$runtime_log" \
  RUNTIME="$runtime" \
  DIST_DIR="$dist_dir" \
  PACKAGE_ARCH=s390x \
  "$verifier" >/dev/null 2>&1; then
  echo "package verifier accepted an unsupported architecture" >&2
  exit 1
fi
