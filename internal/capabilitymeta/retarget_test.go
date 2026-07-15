package capabilitymeta_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
)

func TestRetargetSchemaPreservesSourceMaterialAndWireSemantics(t *testing.T) {
	t.Parallel()

	source := []byte("# Account capability.\r\nid: 'account.register/v1' # Exact identity.\r\ndescription: Registers an account.\r\nrequest:\r\n  email: {type: string, required: true} # Login address.\r\nresponse: {}\r\nerrors: [already_exists]\r\n")
	original := append([]byte(nil), source...)
	target := mustIdentifier(t, "account.register/v2")
	got, err := capabilitymeta.RetargetSchema(source, target)
	if err != nil {
		t.Fatalf("RetargetSchema: %v", err)
	}
	want, err := os.ReadFile("testdata/account.register.v2.retargeted.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("retargeted source:\n got: %s\nwant: %s", got, want)
	}
	if bytes.Contains(got, []byte("\r\n")) || !bytes.Contains(got, []byte("# Account capability.")) || !bytes.Contains(got, []byte("# Exact identity.")) || !bytes.Contains(got, []byte("# Login address.")) || !bytes.Contains(got, []byte("Registers an account.")) {
		t.Fatalf("retargeted source did not preserve normalized source material:\n%s", got)
	}
	declared, err := capabilitymeta.ParseID(got)
	if err != nil || declared != target {
		t.Fatalf("ParseID(retargeted) = %s, %v", declared, err)
	}
	wantSemanticSource := bytes.Replace(source, []byte("account.register/v1"), []byte("account.register/v2"), 1)
	wantSchema, err := capabilitymeta.NormalizeSchema(wantSemanticSource)
	if err != nil {
		t.Fatalf("NormalizeSchema(want): %v", err)
	}
	gotSchema, err := capabilitymeta.NormalizeSchema(got)
	if err != nil || !bytes.Equal(gotSchema, wantSchema) {
		t.Fatalf("retargeted wire schema = %s, want %s, %v", gotSchema, wantSchema, err)
	}
	repeated, err := capabilitymeta.RetargetSchema(source, target)
	if err != nil || !bytes.Equal(repeated, got) {
		t.Fatalf("repeated retarget = %q, %v", repeated, err)
	}
	if !bytes.Equal(source, original) {
		t.Fatal("RetargetSchema mutated source bytes")
	}
}

func TestRetargetSchemaPreservesExactVersionBytesDefensively(t *testing.T) {
	t.Parallel()

	source := []byte("id: account.register/v1\r\nrequest: {}\r\n")
	got, err := capabilitymeta.RetargetSchema(source, mustIdentifier(t, "account.register/v1"))
	if err != nil || !bytes.Equal(got, source) {
		t.Fatalf("RetargetSchema(same ID) = %q, %v", got, err)
	}
	got[0] = 'x'
	if source[0] != 'i' {
		t.Fatal("RetargetSchema returned mutable source storage")
	}
}

func TestRetargetSchemaRejectsInvalidTargetsAndSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		data   string
		target capabilityid.Identifier
		also   error
	}{
		{name: "empty target", data: "id: account.register/v1\n", target: capabilityid.Identifier{}},
		{name: "different name", data: "id: account.register/v1\n", target: mustIdentifier(t, "profile.get/v2")},
		{name: "invalid source", data: "id: account.register/v1\nrequest:\n  email: {type: bytes}\n", target: mustIdentifier(t, "account.register/v2"), also: capabilitymeta.ErrInvalidManifest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := capabilitymeta.RetargetSchema([]byte(test.data), test.target)
			if !errors.Is(err, capabilitymeta.ErrRetargetSchema) || (test.also != nil && !errors.Is(err, test.also)) || got != nil {
				t.Fatalf("RetargetSchema = %q, %v", got, err)
			}
		})
	}
}

func mustIdentifier(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return identifier
}

func FuzzRetargetSchema(f *testing.F) {
	for _, seed := range []string{normalizationInput, "id: account.register/v1\n", "[]\n", "id: &x account.register/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	target := mustFuzzIdentifier(f, "account.register/v2")
	f.Fuzz(func(t *testing.T, input string) {
		got, err := capabilitymeta.RetargetSchema([]byte(input), target)
		if err != nil {
			if !errors.Is(err, capabilitymeta.ErrRetargetSchema) {
				t.Fatalf("RetargetSchema returned unexpected error: %v", err)
			}
			return
		}
		declared, err := capabilitymeta.ParseID(got)
		source, sourceErr := capabilitymeta.ParseID([]byte(input))
		if err != nil || declared != target || sourceErr != nil {
			t.Fatalf("retargeted declaration = %s, %v", declared, err)
		}
		if source == target && !bytes.Equal(got, []byte(input)) {
			t.Fatal("same-version retarget did not preserve exact bytes")
		}
		if source != target && strings.Contains(string(got), "\r\n") {
			t.Fatal("new-version retarget retained non-canonical line endings")
		}
	})
}

func mustFuzzIdentifier(f *testing.F, value string) capabilityid.Identifier {
	f.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		f.Fatalf("Parse(%q): %v", value, err)
	}
	return identifier
}
