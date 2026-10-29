#!/bin/sh
# build.sh: build every LEEROY tool into bin/ (the bench, the viewer, azworld, accounts). The scripts that use them run
# this first; it's quick when nothing changed.
cd "$(dirname "$0")" && exec go build -o bin/ ./azeroth-sdk/cmd/... ./azworld ./azview ./azbench ./server/account
