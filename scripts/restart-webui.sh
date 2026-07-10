#!/bin/bash
# Rebuild taskman and (re)start the web UI. Usage: scripts/restart-webui.sh [addr]
# Default addr 127.0.0.1:8311; the server log lands in bin/webui.log.
set -e
cd "$(dirname "$0")/.."
addr="${1:-127.0.0.1:8311}"
mkdir -p bin
go build -o bin/taskman .
pkill -f "taskman serve -addr $addr" 2>/dev/null || true
sleep 0.3
nohup ./bin/taskman serve -addr "$addr" > bin/webui.log 2>&1 &
sleep 0.5
curl -sf -o /dev/null "http://$addr/" && echo "webui running on http://$addr"
