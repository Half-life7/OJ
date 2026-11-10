#!/bin/sh
set -eu
/app/web &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT
for attempt in $(seq 1 30); do
  if wget -q -O /dev/null http://127.0.0.1:8080/ready; then
    echo 'Extension arm64 Web /ready passed under emulation with real MySQL/Redis'
    exit 0
  fi
  sleep 1
done
exit 1
