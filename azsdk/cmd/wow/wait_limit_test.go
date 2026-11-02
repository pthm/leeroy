package main

import "testing"

func TestRuntimeWaitLimit(t *testing.T) {
	t.Setenv("WOW_MAX_WAIT", "25")
	if maxWaitSeconds() != 25 || !validWait(20) || validWait(26) {
		t.Fatal("limit not applied")
	}
	for _, args := range [][]string{{"wait", "26"}, {"-w", "26", "return 1"}, {"-c", "26", "return 1"}} {
		if run(args) != 1 {
			t.Fatal("oversized wait accepted", args)
		}
	}
	t.Setenv("WOW_MAX_WAIT", "broken")
	if maxWaitSeconds() != 600 {
		t.Fatal("invalid override")
	}
}
