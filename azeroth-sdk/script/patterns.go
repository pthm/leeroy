package script

// go-lua ships string.find without pattern support (it asserts) and no match/gmatch/gsub.
// Lua 5.x pattern matching comes from gopher-lua's standalone pm package; the string
// functions are thin Lua wrappers around one Go primitive, string._pfind.

import (
	lua "github.com/Shopify/go-lua"
	"github.com/yuin/gopher-lua/pm"
)

// pfind(s, pattern, init) behaves like string.find with patterns: start, end, captures...
func pfind(l *lua.State) int {
	s, p := lua.CheckString(l, 1), lua.CheckString(l, 2)
	init := lua.OptInteger(l, 3, 1)
	if init > len(s)+1 {
		l.PushNil()
		return 1
	}
	mds, err := pm.Find(p, []byte(s), init-1, 1)
	if err != nil {
		lua.Errorf(l, "%s", err.Error())
	}
	if len(mds) == 0 {
		l.PushNil()
		return 1
	}
	md := mds[0]
	l.PushInteger(md.Capture(0) + 1)
	l.PushInteger(md.Capture(1))
	for i := 2; i < md.CaptureLength(); i += 2 {
		if md.IsPosCapture(i) {
			l.PushInteger(md.Capture(i))
		} else {
			l.PushString(s[md.Capture(i):md.Capture(i+1)])
		}
	}
	return md.CaptureLength()/2 + 1
}

const patternPrelude = `
local native_find, pfind, unpack, concat = string.find, string._pfind, table.unpack, table.concat
local function start(s, i)
  i = i or 1
  if i < 0 then i = math.max(#s + i + 1, 1) elseif i == 0 then i = 1 end
  return i
end
-- captures of a pfind result r, or the whole match when the pattern has none
local function caps(s, r)
  if #r > 2 then return unpack(r, 3) end
  return s:sub(r[1], r[2])
end
function string.find(s, p, init, plain)
  init = start(s, init)
  if plain then return native_find(s, p, init, true) end
  return pfind(s, p, init)
end
function string.match(s, p, init)
  local r = {pfind(s, p, start(s, init))}
  if r[1] == nil then return nil end
  return caps(s, r)
end
function string.gmatch(s, p)
  local pos = 1
  return function()
    if pos > #s + 1 then return nil end
    local r = {pfind(s, p, pos)}
    if r[1] == nil then pos = #s + 2; return nil end
    pos = r[2] >= r[1] and r[2] + 1 or r[1] + 1
    return caps(s, r)
  end
end
function string.gsub(s, p, repl, n)
  local out, pos, count, rt = {}, 1, 0, type(repl)
  n = n or math.huge
  while count < n and pos <= #s + 1 do
    local r = {pfind(s, p, pos)}
    if r[1] == nil then break end
    local st, en = r[1], r[2]
    local whole = s:sub(st, en)
    local v
    if rt == "string" or rt == "number" then
      repl = tostring(repl)
      local t, i = {}, 1
      while i <= #repl do
        local c = repl:sub(i, i)
        if c == "%" then
          i = i + 1
          c = repl:sub(i, i)
          local d = tonumber(c)
          if d == 0 or (d == 1 and #r == 2) then c = whole
          elseif d then c = tostring(r[d + 2]) end
        end
        t[#t + 1] = c
        i = i + 1
      end
      v = concat(t)
    elseif rt == "table" then v = repl[(caps(s, r))]
    else v = repl(caps(s, r)) end
    if v == nil or v == false then v = whole end
    out[#out + 1] = s:sub(pos, st - 1)
    out[#out + 1] = tostring(v)
    count = count + 1
    if en >= st then pos = en + 1 else
      out[#out + 1] = s:sub(st, st)
      pos = st + 1
    end
  end
  out[#out + 1] = s:sub(pos)
  return concat(out), count
end
`

func installPatterns(l *lua.State) {
	l.Global("string")
	l.PushGoFunction(pfind)
	l.SetField(-2, "_pfind")
	l.Pop(1)
	if err := lua.DoString(l, patternPrelude); err != nil {
		panic(err)
	}
}
