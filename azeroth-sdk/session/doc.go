// Package session runs one logged-in character: it authenticates and enters the world through package client,
// then runs the tick loop that drives the character's Lua engine (package script) and its movement (packages
// movement and navigation).
//
// Bot is the entry point: NewBot with a Config, then Run, which blocks until Stop or a fatal error. Each tick
// (Config.AITickMs, 200 ms by default) calls script.Engine.CallTick; the client never acts on its own. The rest of
// the package is the agent's Lua API (luaapi.go and the domain files: items, trade, talents, travel, combat log...),
// the decoding of server packets into typed events and action results (events.go, model.go, values from package
// types), and the game log (gamelog.go: -events' file also keeps the character's state, nearby units, transports and API
// calls; -diagnostics). Package api exposes a set of Bots over JSON-RPC.
package session
