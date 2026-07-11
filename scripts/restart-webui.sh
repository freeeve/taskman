#!/bin/bash
# Rebuild taskman, keep the PATH cli in lockstep, and (re)start the web UI.
# Usage: scripts/restart-webui.sh [addr]
# Default addr 127.0.0.1:8311; the server log lands in bin/webui.log.
# set -e keeps this fail-closed: a build failure aborts before the install
# and before the running server is touched.
set -e
cd "$(dirname "$0")/.."
addr="${1:-127.0.0.1:8311}"
mkdir -p bin
go build -o bin/taskman .
# The served binary and the cli agents invoke must not skew: a cli-authored
# flow (posing decisions) dies on the live system if PATH lags the server.
go install .
pkill -f "taskman serve -addr $addr" 2>/dev/null || true
sleep 0.3
nohup ./bin/taskman serve -addr "$addr" > bin/webui.log 2>&1 &
sleep 0.5
curl -sf -o /dev/null "http://$addr/" && echo "webui running on http://$addr"
