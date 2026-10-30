package session

import "testing"

func TestGoToFloorPostconditions(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"preserve explicit z", `world.ground_z=function() error("must not replace requested z") end`,
			`r=act.go_to{x=0,y=0,z=20};assert(r.ok and #moves==1 and moves[1].z==20 and r.data.vertical_gap==0)`},
		{"wrong floor despite raw success", `move.to=function() pos={x=10,y=0,z=10};return {ok=true} end`,
			`r=act.go_to{x=10,y=0,z=0};assert(not r.ok and r.error.code=="wrong_floor" and r.data.vertical_gap==10 and r.data.position.z==10)`},
		{"selected floor enforced", `world.ground_z=function() return 20 end;move.to=function() return {ok=true} end`,
			`r=act.go_to{x=0,y=0};assert(not r.ok and r.error.code=="wrong_floor" and r.data.destination.z==20)`},
		{"unknown floor", `world.ground_z=function() return nil end`,
			`r=act.go_to{x=10,y=0};assert(not r.ok and r.error.code=="wrong_floor" and #moves==0)`},
		{"wrong map before movement", `pos.map=1`,
			`r=act.go_to{x=10,y=0,z=0,map=0};assert(not r.ok and r.error.code=="wrong_map" and #moves==0)`},
		{"map changes during movement", `move.to=function() pos={x=10,y=0,z=0,map=1};return {ok=true} end`,
			`r=act.go_to{x=10,y=0,z=0};assert(not r.ok and r.error.code=="wrong_map")`},
		{"raw success outside tolerance", `move.to=function() return {ok=true} end`,
			`r=act.go_to{x=100,y=0,z=0};assert(not r.ok and r.error.code=="unreachable" and r.data.distance==100)`},
		{"control failure at destination", `move.to=function() pos={x=10,y=0,z=0};return {ok=false,error={code="preempted",message="recovery"}} end`,
			`r=act.go_to{x=10,y=0,z=0};assert(not r.ok and r.error.code=="preempted")`},
		{"unverified fallback", `move.to=function() pos={x=10,y=0,z=0};return {ok=true,data={no_path=true}} end`,
			`r=act.go_to{x=10,y=0,z=0};assert(not r.ok and r.error.code=="route_unreachable")`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, actBase+tc.stubs, tc.body) })
	}
}

const partialRouteStub = `
  checks=0
  world.route=function(points,opts)
   checks=checks+1
   assert(#moves==0 and #points==3 and points[1].z==0)
   return {ok=false,error={code="route_unreachable",message="second leg blocked"},data={
    status="partial",from={x=0,y=0,z=0},completed_legs=1,blocked_leg=2,
    waypoints={{x=0,y=0,z=0},{x=10,y=0,z=0},{x=15,y=0,z=0}}}}
  end
`

func TestTravelRequiresWholeRouteOrExplicitPrefix(t *testing.T) {
	actWorld(t, actBase+partialRouteStub, `
  r=act.travel{points={{10,0,0},{20,0,0},{30,0,0}}}
  assert(not r.ok and #moves==0 and checks==1 and r.data.waypoint==2 and r.data.completed_waypoints==0)
 `)
	actWorld(t, actBase+partialRouteStub, `
  r=act.travel{points={{10,0,0},{20,0,0},{30,0,0}},allow_partial=true,tolerance=1}
  assert(not r.ok and r.error.code=="route_unreachable" and r.data.partial_walked and #moves==2)
  assert(moves[1].x==10 and moves[2].x==15 and r.data.completed_waypoints==1 and r.data.waypoint==2)
 `)
}
func TestTravelValidationAndFailureEvidence(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"verified before moving", `checks=0;world.route=function(points,opts) checks=checks+1;assert(#moves==0 and #points==2 and opts.map==0);return {ok=true,data={status="verified",from=pos,completed_legs=2}} end`,
			`r=act.travel{points={{10,0,0},{20,0,5}}};assert(r.ok and checks==1 and #moves==2 and r.data.completed_waypoints==2 and r.data.status=="verified")`},
		{"explicit unverified 2D", `world.route=function() error("validation disabled") end`,
			`r=act.travel{points={{10,0},{20,0}},validate=false};assert(r.ok and r.data.status=="unverified" and #moves==2)`},
		{"late wrong floor", `move.to=function(x,y,z) moves[#moves+1]={x=x};pos={x=x,y=y,z=(x==20 and 10 or z)};return {ok=true} end`,
			`r=act.travel{points={{10,0,0},{20,0,0},{30,0,0}}};assert(not r.ok and r.error.code=="wrong_floor" and r.data.waypoint==2 and r.data.completed_waypoints==1 and #moves==2)`},
		{"all maps checked first", ``,
			`r=act.travel{points={{10,0,0},{x=20,y=0,z=0,map=1}}};assert(not r.ok and r.error.code=="wrong_map" and #moves==0)`},
		{"validation moves character", `world.route=function() pos={x=100,y=0,z=0};return {ok=true,data={status="verified",from={x=0,y=0,z=0},completed_legs=1}} end`,
			`r=act.travel{points={{10,0,0}}};assert(not r.ok and r.error.code=="route_stale" and #moves==0)`},
		{"timeout cannot authorize prefix", `world.route=function() return {ok=false,error={code="route_timeout",message="route deadline"},data={status="partial",completed_legs=1,blocked_leg=2,waypoints={{x=10,y=0,z=0}}}} end`,
			`r=act.travel{points={{10,0,0},{20,0,0}},allow_partial=true};assert(not r.ok and r.error.code=="route_timeout" and #moves==0)`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, actBase+tc.stubs, tc.body) })
	}
}
func TestTravelStrictOptions(t *testing.T) {
	actWorld(t, actBase, `
  local invalid={
   function() act.travel{points={{1,2}}} end,
   function() act.travel{points={{1,2,3}},validate="false"} end,
   function() act.travel{points={{1,2,3}},validate=false,allow_partial=true} end,
   function() act.travel{points={{x=1,y=2,z=3,map=0.5}}} end,
   function() act.go_to{x=1,y=2,z=3,vertical_tolerance=0} end,
   function() act.go_to{x=1,y=2,z=3,map=-1} end,
  }
  for _,f in ipairs(invalid) do assert(not pcall(f)) end
  assert(#moves==0)
 `)
}
