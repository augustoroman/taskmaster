#!/bin/sh
# Regenerates code from proto/: Go into gen/ and TypeScript into web/src/gen/.
# Requires protoc; the Go plugins come from the tool directives in go.mod and
# the TypeScript plugin from web/node_modules (npm install in web/ first).
set -eu
cd "$(dirname "$0")/.."
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
go build -o "$bin/" google.golang.org/protobuf/cmd/protoc-gen-go connectrpc.com/connect/cmd/protoc-gen-connect-go
rm -rf gen web/src/gen
mkdir -p gen web/src/gen
PATH="$bin:$PWD/web/node_modules/.bin:$PATH" protoc -I proto \
  --go_out=gen --go_opt=paths=source_relative \
  --connect-go_out=gen --connect-go_opt=paths=source_relative \
  --es_out=web/src/gen --es_opt=target=ts,import_extension=js \
  proto/taskmaster/v1/*.proto
