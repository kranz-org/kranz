#!/bin/sh
# Runs from the catalog's own `src` directory; proves that `dir: src` was
# resolved relative to repositories/catalog/kranz.yaml regardless of the
# process working directory.
set -eu
printf '%s from %s (cwd=%s)\n' "${1:-service}" "${CATALOG_SCOPE:-unset}" "$(pwd)"
sleep 3600
