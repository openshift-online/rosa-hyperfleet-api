package controller

import (
	"encoding/hex"
	"testing"
)

func TestRandomDNSPrefix(t *testing.T) {
	prefix, err := randomDNSPrefix()
	if err != nil {
		t.Fatalf("randomDNSPrefix returned error: %v", err)
	}
	if len(prefix) != 4 {
		t.Fatalf("randomDNSPrefix = %q, want four hexadecimal characters", prefix)
	}
	decoded, err := hex.DecodeString(prefix)
	if err != nil {
		t.Fatalf("randomDNSPrefix = %q, not hexadecimal: %v", prefix, err)
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded prefix contains %d bytes, want 2", len(decoded))
	}
}
