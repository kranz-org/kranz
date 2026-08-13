#!/bin/sh

child_pid=""

cleanup() {
  if [ -n "${child_pid}" ]; then
    kill "${child_pid}" 2>/dev/null || true
  fi
  exit 0
}

trap cleanup INT TERM

while true; do
  python3 -u -m http.server 18307 --bind 127.0.0.1 &
  child_pid=$!
  echo "OPEN  tcp://127.0.0.1:18307 · child PID ${child_pid}"
  sleep 6

  kill "${child_pid}" 2>/dev/null || true
  wait "${child_pid}" 2>/dev/null || true
  child_pid=""
  echo "CLOSED tcp://127.0.0.1:18307"
  sleep 6
done
