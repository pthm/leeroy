#!/bin/sh
# build.sh: build every LEEROY tool into bin/ (azsdk and wow, azbench, azview, azworld, ...). The scripts that use them
# run this first; it's quick when nothing changed.
cd "$(dirname "$0")" && exec go build -o bin/ ./azsdk/cmd/... ./cmd/...
