package command

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateInvocationIDProducesCanonicalUUIDV4(t *testing.T) {
	t.Parallel()

	invocationID, err := generateInvocationID()
	if err != nil {
		t.Fatalf("generate invocation ID: %v", err)
	}
	if len(invocationID) != 36 || invocationID[8] != '-' || invocationID[13] != '-' || invocationID[18] != '-' || invocationID[23] != '-' {
		t.Fatalf("invocation ID shape = %q", invocationID)
	}
	if invocationID != strings.ToLower(invocationID) {
		t.Fatalf("invocation ID is not canonical lowercase: %q", invocationID)
	}

	raw, err := hex.DecodeString(strings.ReplaceAll(invocationID, "-", ""))
	if err != nil || len(raw) != 16 {
		t.Fatalf("decode invocation ID %q: bytes %d, error %v", invocationID, len(raw), err)
	}
	if raw[6]>>4 != 4 {
		t.Fatalf("invocation ID version = %d, want 4", raw[6]>>4)
	}
	if raw[8]&0xc0 != 0x80 {
		t.Fatalf("invocation ID variant bits = %#x, want RFC 4122", raw[8]&0xc0)
	}
}
