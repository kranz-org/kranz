#!/bin/sh

python3 -u -m http.server 18305 --bind 127.0.0.1 &
child_pid=$!
echo "child listener PID ${child_pid}"
wait "${child_pid}"
