package session

import "testing"

const errandWorld = actBase + `
  npc = {guid = "npc-123", entry = 123, name = "Vendor", alive = true, distance = 2,
    roles = {"vendor", "repair", "trainer"}}
  mobs = {npc}
  units.get = function(g) if g == npc.guid then return npc end end
  calls = {}
  function raw(op, data, code)
    calls[#calls + 1] = op
    return {id = #calls, op = op, ok = code == nil, data = data,
      error = code and {code = code, message = "refused", next = "inspect state"}}
  end
  vendor = {
    items = function() return raw("vendor.items", {}) end,
    sell_junk = function() return raw("vendor.sell_junk", {money = 7}) end,
    repair = function() return raw("vendor.repair", {fixed = 1}) end,
    buy = function(_, item) return raw("vendor.buy", {item = {entry = item}}) end,
  }
  spells.known = function(sp) return sp == 1 end
  trainer = {learn = function(_, sp) return raw("trainer.learn", {spell = sp}, sp == 3 and "not_enough_money" or nil) end}
`

func TestActVendorRequiredSteps(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"buy only", "", `r = act.vendor{npc_entry = 123, buy = {{9}}}
          assert(r.ok and #r.steps == 2 and #r.data.bought == 1)
          assert(table.concat(calls, ",") == "vendor.items,vendor.buy")`},
		{"partial purchase", `vendor.buy = function() return raw("vendor.buy", nil, "not_enough_money") end`,
			`r = act.vendor{npc_guid = npc.guid, sell_junk = true, buy = {{9}, {10}}}
          assert(not r.ok and r.error.code == "not_enough_money" and r.error.next == "inspect state")
          assert(r.data.sold.money == 7 and #r.data.completed == 2 and #r.data.remaining == 2)
          assert(r.data.remaining[1].item_entry == 9 and r.steps[3].request_id == 3 and not r.steps[3].ok)
          assert(#calls == 3)`},
		{"failed sale", `vendor.sell_junk = function() return raw("vendor.sell_junk", {count = 1, failed = 1}, "cannot_sell") end`,
			`r = act.vendor{npc_entry = 123, sell_junk = true, repair = true, buy = {{9}}}
          assert(not r.ok and r.error.code == "cannot_sell" and r.data.sold.count == 1)
          assert(#calls == 1 and #r.data.remaining == 4)`},
		{"failed repair", `vendor.repair = function() return raw("vendor.repair", {fixed = 1, still_damaged = 1}, "not_repaired") end`,
			`r = act.vendor{npc_entry = 123, repair = true, buy = {{9}}}
          assert(not r.ok and r.data.repaired.fixed == 1 and #calls == 1)`},
		{"list unavailable", `vendor.items = function() return raw("vendor.items", nil, "not_available") end`,
			`r = act.vendor{npc_entry = 123, buy = {{9}}}
          assert(not r.ok and r.error.code == "not_available" and #calls == 1)`},
		{"precheck all roles", `npc.roles = {"vendor"}`, `r = act.vendor{npc_entry = 123, sell_junk = true, repair = true}
          assert(not r.ok and r.error.code == "wrong_npc" and #calls == 0)`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, errandWorld+tc.stubs, tc.body) })
	}
}

func TestActTrainPartialWork(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"stop on failure", `r = act.train{npc_entry = 123, spells = {1, 2, 3, 4}}
          assert(not r.ok and r.error.code == "not_enough_money")
          assert(r.data.already_known[1] == 1 and r.data.learned[1] == 2 and r.data.failed[1] == 3 and r.data.remaining[1] == 4)
          assert(#calls == 2 and #r.steps == 2)`},
		{"continue on failure", `r = act.train{npc_entry = 123, spells = {1, 2, 3, 4}, continue_on_error = true}
          assert(not r.ok and #r.data.learned == 2 and r.data.learned[2] == 4)
          assert(#r.data.failed == 1 and #r.data.remaining == 0 and #calls == 3)`},
		{"all known", `r = act.train{npc_entry = 123, spells = {1}}
          assert(r.ok and #r.data.already_known == 1 and #calls == 0)`},
		{"all learned", `r = act.train{npc_entry = 123, spells = {2, 4}}
          assert(r.ok and #r.data.learned == 2 and #r.data.failed == 0)`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, errandWorld, tc.body) })
	}
}

func TestActNPCSelectorsAndValidation(t *testing.T) {
	actWorld(t, errandWorld, `
      for _, options in ipairs({{}, {npc = 123}, {npc_entry = 123, npc_guid = npc.guid},
        {npc_guid = 123}, {npc_entry = "123"}, {npc_entry = 123, sell_junk = "true"},
        {npc_entry = 123, repair = true, buuy = {}}, {npc_entry = 123, buy = {{9, 0}}},
        {npc_entry = 123, buy = {{9, 1, 7}}}, {npc_entry = 123, buy = {[2] = {9}}}}) do
        local ok, err = pcall(act.vendor, options)
        assert(not ok and tostring(err):find("act.vendor") and tostring(err):find("accepted fields"))
      end
      for _, spells in ipairs({{}, {2, 2}, {"2"}, {[2] = 2}}) do
        local ok = pcall(act.train, {npc_entry = 123, spells = spells})
        assert(not ok)
      end
      assert(#calls == 0)
      r = act.reach_npc{npc_guid = "123"}
      assert(not r.ok and r.error.code == "npc_not_found") -- never reinterpret as entry 123
      r = act.reach_npc{npc_entry = 123}
      assert(r.ok and r.data.guid == npc.guid)
      r = act.talk{npc_entry = 123, fn = function() return raw("custom", nil, "refused") end}
      assert(not r.ok and r.error.code == "refused")
    `)
}
