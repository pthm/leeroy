# detour

Recast/Detour's navigation mesh queries in Go: the `detour` package of
[o0olele/detour-go](https://github.com/o0olele/detour-go) (MIT, `LICENSE`) as patched in walkline's fork
(`github.com/walkline/detour-go@v0.0.0-20260624174442-8056537019f8`, from AzerothGhost: fixes for AzerothCore's navmesh
tiles, on which upstream v0.2.3 panics). Copied here unchanged so the SDK's module needs no `replace` directive (which
`go install …@latest` refuses). Don't edit it in place without noting the change here.
