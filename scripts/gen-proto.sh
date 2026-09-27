#!/bin/sh
# Regenerates Go code from proto/. Requires protoc; the Go plugins come from
# the tool directives in go.mod.
set -eu
cd "$(dirname "$0")/.."
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
go build -o "$bin/" google.golang.org/protobuf/cmd/protoc-gen-go connectrpc.com/connect/cmd/protoc-gen-connect-go
rm -rf gen
mkdir -p gen
PATH="$bin:$PATH" protoc -I proto \
  --go_out=gen --go_opt=paths=source_relative \
  --connect-go_out=gen --connect-go_opt=paths=source_relative \
  proto/taskmaster/v1/*.proto
