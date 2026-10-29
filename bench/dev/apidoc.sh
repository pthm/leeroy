#!/bin/sh
# bench/dev/apidoc.sh: regenerate the API reference and the TypeScript SDK types/client from the code
# (azeroth-sdk/docs/api-reference.md, sdk/ts/src/types.ts, sdk/ts/src/api.ts). Starts the GM dev character if it
# isn't running: the function registry comes from a live session. Run it after changing the Lua API or package types.
cd "$(dirname "$0")/../.." || exit 1
(cd azeroth-sdk && go build -o azsdk-dev ./cmd/azsdk) || exit 1
WOW_PORT=8112 bench/agent/bin/wow 'return 1' 2>/dev/null | grep -q result || sh bench/dev/gm.sh >/dev/null
cd azeroth-sdk && go run ./cmd/apigen -rpc http://127.0.0.1:8112/rpc && (cd sdk/ts && bunx -p typescript tsc -p .)
