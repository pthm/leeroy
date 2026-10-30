package session

import "testing"

func TestCastCorrectionBoundsAndEvidence(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"specific refusal", `spells.check=function() return {usable=false,reason="no_power",power_needed=30} end`, `r=act.cast(133,"mob");assert(not r.ok and r.error.code=="no_power" and r.data.attempts==0 and r.data.check.power_needed==30)`},
		{"foreign movement", `move._acquire=function() return {ok=false,error={code="movement_busy",message="owned"}} end;move.stop=function() error("must not stop") end`, `r=act.cast(133,"mob");assert(not r.ok and r.error.code=="movement_busy")`},
		{"bounded facing", `spells.cast=function() return {ok=false,error={code="unit_not_infront",message="moved"}} end`, `r=act.cast(133,"mob");assert(not r.ok and r.data.attempts==2 and #r.data.corrections==2)`},
		{"default never approaches", `spells.check=function() return {usable=false,reason="out_of_range"} end`, `r=act.cast(133,"mob");assert(not r.ok and r.error.code=="out_of_range" and #moves==0)`},
		{"approach limit", `spells.check=function() return {usable=false,reason="out_of_range",range_yards=30} end;units.get=function() return {x=100,y=0,z=0} end`, `r=act.cast(133,"mob",{approach=true});assert(not r.ok and r.error.code=="move_limit" and #moves==0)`},
		{"explicit approach", `spells.check=function() return {usable=false,reason="out_of_range",range_yards=30} end;units.get=function() return {x=35,y=0,z=0} end;spells.cast=function() return {ok=true,data={spell=133}} end`, `r=act.cast(133,"mob",{approach=true});assert(r.ok and #moves==1 and moves[1].x==6 and r.data.action.spell==133)`},
		{"failed path", `spells.check=function() return {usable=false,reason="line_of_sight"} end;units.get=function() return {x=5,y=0,z=0} end;world.path=function() return {ok=false,error={code="route_unreachable",message="wall"}} end`, `r=act.cast(133,"mob",{approach=true});assert(not r.ok and #moves==0)`},
		{"deadline", `spells.cast=function() return {id=9999,pending=true} end`, `r=act.cast(133,"mob",{max_time_s=0.01});assert(not r.ok and r.error.code=="cast_timeout" and r.data.attempts==1)`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, actBase+tc.stubs, tc.body) })
	}
}
