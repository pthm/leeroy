package script

import (
	"testing"

	lua "github.com/Shopify/go-lua"
)

func TestPatterns(t *testing.T) {
	l := lua.NewState()
	lua.OpenLibraries(l)
	installPatterns(l)
	err := lua.DoString(l, `
		assert(("hello world"):find("o w") == 5)
		assert(("a.b"):find(".", 1, true) == 2)
		local s, e, c = ("key=val"):find("(%w+)=")
		assert(s == 1 and e == 4 and c == "key")
		assert(("x=10,y=20"):match("y=(%d+)") == "20")
		assert(("abc"):match("^b") == nil)
		local k, v = ("pos -9900.5,680"):match("(%-?[%d.]+),(%d+)")
		assert(k == "-9900.5" and v == "680", k)
		local t = {}
		for w in ("a1 b22 c333"):gmatch("%a(%d+)") do t[#t+1] = w end
		assert(#t == 3 and t[3] == "333")
		local n = 0
		for _ in string.gmatch("", "x*") do n = n + 1 end
		assert(n == 1)
		assert(("hello"):gsub("l", "L") == "heLLo")
		local r, c2 = ("a,b,c"):gsub(",", ";", 1)
		assert(r == "a;b,c" and c2 == 1)
		assert(("abc"):gsub("%w", "%0%0") == "aabbcc")
		assert(("k=v"):gsub("(%w)=(%w)", "%2=%1") == "v=k")
		assert(("$x $y"):gsub("%$(%w)", {x = "1"}) == "1 $y")
		assert(("ab"):gsub(".", function(c) return c:upper() end) == "AB")
		assert(("abc"):gsub("", "-") == "-a-b-c-")
		assert(("0a0b"):gsub("%x%x", "") == "")
	`)
	if err != nil {
		t.Fatal(err)
	}
}
