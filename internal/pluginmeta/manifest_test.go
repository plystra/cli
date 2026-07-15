package pluginmeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginmeta"
)

func TestParseIndexesCanonicalMetadata(t *testing.T) {
	t.Parallel()

	input := "provides: [profile.get/v2, account.register/v1]\nid: acme.app.account\nrequires: [audit.write/v1]\nconfig:\n  token: {type: secret}\n"
	metadata, err := pluginmeta.Parse([]byte(input))
	if err != nil || metadata.ID() != "acme.app.account" {
		t.Fatalf("Parse = %#v, %v", metadata, err)
	}
	if got := identifierStrings(metadata.Provides()); !reflect.DeepEqual(got, []string{"account.register/v1", "profile.get/v2"}) {
		t.Fatalf("Provides = %v", got)
	}
	provided := metadata.Provides()
	provided[0] = capabilityid.Identifier{}
	if metadata.Provides()[0].String() != "account.register/v1" {
		t.Fatal("Provides exposed mutable metadata")
	}
	quoted, err := pluginmeta.Parse([]byte("id: 'acme.app.account'\n"))
	if err != nil || quoted.ID() != metadata.ID() || len(quoted.Provides()) != 0 {
		t.Fatalf("Parse quoted = %#v, %v", quoted, err)
	}
}

func TestParseRejectsInvalidMetadataEnvelopes(t *testing.T) {
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
		"id: acme.one\nprovides: email.send/v1\n",
		"id: acme.one\nprovides: [1]\n",
		"id: acme.one\nprovides: [email.send]\n",
		"id: acme.one\nprovides: [email.send/v1, email.send/v1]\n",
		"id: acme.one\nrequires: [Audit.write/v1]\n",
		strings.Repeat("x", pluginmeta.MaximumSize+1),
	}
	for _, input := range tests {
		input := input
		t.Run(testName(input), func(t *testing.T) {
			t.Parallel()
			metadata, err := pluginmeta.Parse([]byte(input))
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) {
				t.Fatalf("Parse error = %v, want ErrInvalidManifest", err)
			}
			if metadata.ID() != "" || len(metadata.Provides()) != 0 {
				t.Fatalf("invalid Parse returned %#v", metadata)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"id: acme.one\n", "id: acme.one\nprovides: [email.send/v1]\n", "[]\n", "id: &x acme.one\nrequires: [*x]\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		metadata, err := pluginmeta.Parse([]byte(input))
		if err != nil {
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) {
				t.Fatalf("Parse returned unexpected error: %v", err)
			}
			return
		}
		if metadata.ID() == "" {
			t.Fatal("Parse returned metadata without an ID")
		}
		provided := metadata.Provides()
		for index := 1; index < len(provided); index++ {
			if provided[index-1].String() >= provided[index].String() {
				t.Fatalf("Provides are not uniquely sorted: %q then %q", provided[index-1], provided[index])
			}
		}
	})
}

func identifierStrings(identifiers []capabilityid.Identifier) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
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
