#!/bin/sh
set -eu

# No argument: live stream with an initial burst for immediate scrolling.
# A positive argument: a finite report for action-run history.
limit=${1:-0}
event=0
while [ "$limit" -eq 0 ] || [ "$event" -lt "$limit" ]; do
  event=$((event + 1))
  case $((event % 8)) in
    0) level=ERROR; message='synthetic request failed' ;;
    3) level=WARN; message='retry scheduled' ;;
    5) level=DEBUG; message='cache lookup' ;;
    *) level=INFO; message='request complete' ;;
  esac
  timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  printf '%s %-5s event=%03d message="%s" duration=%dms peer=192.0.2.10 url=https://example.test/orders/%d\n' \
    "$timestamp" "$level" "$event" "$message" "$((event * 7))" "$event"
  if [ "$limit" -eq 0 ] && [ "$event" -ge 24 ]; then
    sleep 1
  fi
done

