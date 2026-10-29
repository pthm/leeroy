package main

import (
	"encoding/hex"
	"testing"
)

// Vector from the old TypeScript account.ts (salt bytes i*7+1).
func TestVerifier(t *testing.T) {
	salt := make([]byte, 32)
	for i := range salt {
		salt[i] = byte(i*7 + 1)
	}
	got := hex.EncodeToString(verifier(salt, "AGENT1", "secret"))
	if want := "d1a49a03e20c8e29d9ea4cc46b07ce16eefc809aec56d096f76eea126441f800"; got != want {
		t.Fatalf("verifier = %s, want %s", got, want)
	}
}
