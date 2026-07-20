package capabilitymeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
)

func TestParseIDReadsCompleteCapabilityEnvelope(t *testing.T) {
	t.Parallel()

	input := `description: Registers an account.
response:
  account_id: {type: string, required: true}
id: account.register/v2
request:
  email: {type: string, required: true}
errors: [already_exists]
` + querySemanticsYAML + `
extensions:
  authn: {authenticated: true}
`
	identifier, err := capabilitymeta.ParseID([]byte(input))
	if err != nil || identifier.String() != "account.register/v2" {
		t.Fatalf("ParseID = %q, %v", identifier, err)
	}
	quoted, err := capabilitymeta.ParseID([]byte("id: 'account.register/v2'\n" + querySemanticsYAML + "\n"))
	if err != nil || quoted != identifier {
		t.Fatalf("ParseID quoted = %q, %v", quoted, err)
	}
}

func TestParseIDRejectsInvalidIdentityEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"[]\n",
		"id: account.register/v1\n---\nid: account.register/v2\n",
		"id: &identity account.register/v1\n",
		"id: account.register/v1\nrequest: &fields {}\nresponse: *fields\n",
		"1: value\n",
		"id: account.register/v1\nversion: 1.0.0\n",
		"id: account.register/v1\nextensions: []\n",
		"id: account.register/v1\nid: account.register/v2\n",
		"request: {}\n",
		"id: 1\n",
		"id: Account.Register/v1\n",
		"id: account.register\n",
		strings.Repeat("x", capabilitymeta.MaximumSize+1),
	}
	for _, input := range tests {
		input := input
		t.Run(testName(input), func(t *testing.T) {
			t.Parallel()
			identifier, err := capabilitymeta.ParseID([]byte(input))
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) {
				t.Fatalf("ParseID error = %v, want ErrInvalidManifest", err)
			}
			if identifier.String() != "" {
				t.Fatalf("invalid ParseID returned %q", identifier)
			}
		})
	}
}

func FuzzParseID(f *testing.F) {
	for _, seed := range []string{
		"id: account.register/v1\n" + querySemanticsYAML + "\n",
		"id: account.register/v1\nrequest: {}\nresponse: {}\n" + querySemanticsYAML + "\n",
		"[]\n",
		"id: &x account.register/v1\nrequest: *x\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		identifier, err := capabilitymeta.ParseID([]byte(input))
		if err != nil {
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) {
				t.Fatalf("ParseID returned unexpected error: %v", err)
			}
			return
		}
		if identifier.String() == "" {
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
