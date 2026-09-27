#!/bin/sh
# Builds the web app and the server binary (./taskmaster) with the app embedded.
set -eu
cd "$(dirname "$0")/.."
(cd web && npm ci --no-audit --no-fund && npm run build)
go build -o taskmaster ./cmd/taskmaster
echo "built ./taskmaster"
