module github.com/pthm/wowbench/azeroth-sdk

go 1.26.0

require (
	github.com/o0olele/detour-go v0.2.3
	github.com/yuin/gopher-lua v1.1.2
)

// AzerothGhost's detour-go fork (fixes needed for AzerothCore navmesh tiles); upstream v0.2.3 panics on them.
replace github.com/o0olele/detour-go => github.com/walkline/detour-go v0.0.0-20260624174442-8056537019f8
