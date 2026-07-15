package pluginmeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/pluginmeta"
)

func TestParseID(t *testing.T) {
	t.Parallel()

	input := "provides: [email.send/v1]\nid: acme.email.smtp\nconfig:\n  host: {type: string}\n"
	got, err := pluginmeta.ParseID([]byte(input))
	if err != nil || got != "acme.email.smtp" {
		t.Fatalf("ParseID = %q, %v", got, err)
	}
	quoted, err := pluginmeta.ParseID([]byte("id: 'acme.email.smtp'\n"))
	if err != nil || quoted != got {
		t.Fatalf("ParseID quoted = %q, %v", quoted, err)
	}
}

func TestParseIDRejectsInvalidIdentityEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"[]\n",
		"id: acme.one\n---\nid: acme.two\n",
		"id: &identity acme.one\n",
		"id: &identity acme.one\nprovides: [*identity]\n",
		"1: value\n",
		"id: acme.one\nversion: 1.0.0\n",
		"id: acme.one\nid: acme.two\n",
		"provides: []\n",
		"id: 1\n",
		"id: Acme.One\n",
		strings.Repeat("x", pluginmeta.MaximumSize+1),
	}
	for _, input := range tests {
		input := input
		t.Run(testName(input), func(t *testing.T) {
			t.Parallel()
			got, err := pluginmeta.ParseID([]byte(input))
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) {
				t.Fatalf("ParseID error = %v, want ErrInvalidManifest", err)
			}
			if got != "" {
				t.Fatalf("invalid ParseID returned %q", got)
			}
		})
	}
}

func FuzzParseID(f *testing.F) {
	for _, seed := range []string{"id: acme.one\n", "[]\n", "id: &x acme.one\nrequires: [*x]\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		id, err := pluginmeta.ParseID([]byte(input))
		if err != nil {
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) {
				t.Fatalf("ParseID returned unexpected error: %v", err)
			}
			return
		}
		if id == "" {
			t.Fatal("ParseID returned an empty ID")
		}
	})
}

func testName(input string) string {
	if input == "" {
		return "empty"
	}
	if len(input) > 64 {
		return "large"
	}
	return strings.NewReplacer("\n", "_", " ", "_").Replace(input)
}
