#!/bin/sh
set -eu

echo "checking schema version"
sleep "${MIGRATION_DELAY:-1}"
echo "applying safe example migration"
sleep "${MIGRATION_DELAY:-1}"
echo "schema is up to date"
