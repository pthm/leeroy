package session

import "testing"

func TestActOptionsValidatedBeforeMovement(t *testing.T) {
	actWorld(t, actBase, `
    assert(act.best == nil)
    local invalid = {
      function() act.go_to{x=1,y=2,typo=true} end,
      function() act.go_to{x=1} end,
      function() act.go_to{x=1,y=2,tries=0} end,
      function() act.go_to{x=1,y=2,on_attacked=true} end,
      function() act.travel{points={{1,2},{3,4,typo=5}}} end,
      function() act.travel{points={}} end,
      function() act.travel{points={[1]={1,2},[3]={3,4}}} end,
      function() act.approach{unit=123} end,
      function() act.follow{name="Tester",distance=0/0} end,
      function() act.recover{safe="false"} end,
      function() act.corpse_run{typo=true} end,
      function() act.rest({}, {max_s=1}) end,
    }
    for _, f in ipairs(invalid) do local ok, err = pcall(f); assert(not ok and tostring(err):find("act%."), tostring(err)) end
    assert(#moves == 0)
    local r = act.travel{points={{1,2,0},{x=3,y=4,z=0}}, tolerance=1}
    assert(r.ok and #moves == 2)
  `)
}
