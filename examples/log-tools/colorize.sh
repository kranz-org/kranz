#!/bin/sh
set -eu

# A plain stdin/stdout command, with no dependency on a particular log tool.
while IFS= read -r line; do
  case "$line" in
    *ERROR*) style='1;31' ;;
    *WARN*) style='33' ;;
    *DEBUG*) style='2;36' ;;
    *) style='32' ;;
  esac
  printf '\033[%sm%s\033[0m\n' "$style" "$line"
done

