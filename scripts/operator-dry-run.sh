#!/bin/sh
# One-command dry-run. Builds a stable binary so the admin view and this run
# share the same SQLite directory (core stores it beside the executable).
set -eu
cd "$(dirname "$0")/../operator"
mkdir -p bin
rm -rf bin/db bin/etc data
go build -o bin/pulse ./cmd/pulse
exec ./bin/pulse demo "$@"
