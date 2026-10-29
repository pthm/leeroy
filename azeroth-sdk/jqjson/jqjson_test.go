package jqjson

import "testing"

// jq 1.8's output for number literals with an exponent (jq -n -c <literal>)
func TestJqNumber(t *testing.T) {
	for in, want := range map[string]string{"1e3": "1E+3", "1E+3": "1E+3", "1e-3": "0.001", "1.5e-7": "1.5E-7",
		"12.5e1": "125", "-2.50e2": "-250", "1e21": "1E+21", "123e-2": "1.23", "1e-7": "1E-7", "8210.40": "8210.40"} {
		if got := jqNumber(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
