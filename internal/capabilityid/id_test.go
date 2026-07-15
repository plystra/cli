package capabilityid_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
)

func TestParseExactIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		name  string
		major uint64
	}{
		{value: "email.send/v1", name: "email.send", major: 1},
		{value: "workspace.invite-member/v12", name: "workspace.invite-member", major: 12},
		{value: "authn.login.password/v1", name: "authn.login.password", major: 1},
		{value: "authn.login.oidc.complete/v1", name: "authn.login.oidc.complete", major: 1},
		{value: "authn.passkey.challenge.create/v1", name: "authn.passkey.challenge.create", major: 1},
		{value: "workflow.retry--now-/v2", name: "workflow.retry--now-", major: 2},
		{value: "storage.object.put/v18446744073709551615", name: "storage.object.put", major: ^uint64(0)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			identifier, err := capabilityid.Parse(test.value)
			if err != nil {
				t.Fatalf("Parse(%q): %v", test.value, err)
			}
			if identifier.Name() != test.name || identifier.Major() != test.major || identifier.String() != test.value {
				t.Fatalf("Parse(%q) = name %q, major %d, string %q", test.value, identifier.Name(), identifier.Major(), identifier.String())
			}
		})
	}
}

func TestParseReferenceAcceptsOptionalVersion(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"account.register", "account.register/v2"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			reference, err := capabilityid.ParseReference(value)
			if err != nil || reference.Name() != "account.register" || reference.String() != value {
				t.Fatalf("ParseReference(%q) = %#v, %v", value, reference, err)
			}
			exact, versioned := reference.Exact()
			if value == "account.register" {
				if reference.Major() != 0 || reference.Versioned() || versioned || exact.String() != "" {
					t.Fatalf("unversioned reference = major %d, versioned %t, exact %q/%t", reference.Major(), reference.Versioned(), exact.String(), versioned)
				}
				return
			}
			if reference.Major() != 2 || !reference.Versioned() || !versioned || exact.String() != value {
				t.Fatalf("versioned reference = major %d, versioned %t, exact %q/%t", reference.Major(), reference.Versioned(), exact.String(), versioned)
			}
		})
	}
}

func TestParsersRejectNonCanonicalValues(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"email",
		"Email.send",
		"email.Send",
		"email_send",
		"email..send",
		"email.-send",
		"email.1send",
		"email.send_",
		" email.send",
		"email.send ",
		"邮件.send",
		"email.send/V1",
		"email.send/v0",
		"email.send/v01",
		"email.send/v-1",
		"email.send/v18446744073709551616",
		"email.send/v1/extra",
	}
	for _, value := range invalid {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if identifier, err := capabilityid.Parse(value); !errors.Is(err, capabilityid.ErrInvalid) || identifier.String() != "" {
				t.Errorf("Parse(%q) = %q, %v", value, identifier.String(), err)
			}
			if reference, err := capabilityid.ParseReference(value); !errors.Is(err, capabilityid.ErrInvalid) || reference.String() != "" {
				t.Errorf("ParseReference(%q) = %q, %v", value, reference.String(), err)
			}
		})
	}
}

func TestNewAndZeroValues(t *testing.T) {
	t.Parallel()

	identifier, err := capabilityid.New("account.register", 2)
	if err != nil || identifier.String() != "account.register/v2" {
		t.Fatalf("New = %q, %v", identifier.String(), err)
	}
	for _, test := range []struct {
		name  string
		major uint64
	}{{name: "account", major: 1}, {name: "account.register", major: 0}} {
		if invalid, err := capabilityid.New(test.name, test.major); !errors.Is(err, capabilityid.ErrInvalid) || invalid.String() != "" {
			t.Fatalf("New(%q, %d) = %q, %v", test.name, test.major, invalid.String(), err)
		}
	}
	var identifierZero capabilityid.Identifier
	var referenceZero capabilityid.Reference
	if identifierZero.Name() != "" || identifierZero.Major() != 0 || identifierZero.String() != "" || referenceZero.Name() != "" || referenceZero.Major() != 0 || referenceZero.String() != "" || referenceZero.Versioned() {
		t.Fatalf("zero values are not empty: %#v, %#v", identifierZero, referenceZero)
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"email.send/v1", "workspace.invite-member/v12", "authn.login.oidc.complete/v1", "workflow.retry--now-/v2", "bad", "email.send/v0", "邮件.send/v1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		identifier, err := capabilityid.Parse(value)
		if err != nil {
			if !errors.Is(err, capabilityid.ErrInvalid) {
				t.Fatalf("Parse(%q) returned unexpected error: %v", value, err)
			}
			return
		}
		if identifier.String() != value {
			t.Fatalf("round trip = %q, want %q", identifier.String(), value)
		}
		reparsed, err := capabilityid.Parse(identifier.String())
		if err != nil || reparsed != identifier {
			t.Fatalf("reparse = %#v, %v; want %#v", reparsed, err, identifier)
		}
	})
}

func FuzzParseReference(f *testing.F) {
	for _, seed := range []string{"account.register", "account.register/v2", "authn.passkey.challenge.create", "workflow.retry--now-/v2", "bad", "account.register/v0", "账户.register"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		reference, err := capabilityid.ParseReference(value)
		if err != nil {
			if !errors.Is(err, capabilityid.ErrInvalid) {
				t.Fatalf("ParseReference(%q) returned unexpected error: %v", value, err)
			}
			return
		}
		if reference.String() != value {
			t.Fatalf("round trip = %q, want %q", reference.String(), value)
		}
		reparsed, err := capabilityid.ParseReference(reference.String())
		if err != nil || reparsed != reference {
			t.Fatalf("reparse = %#v, %v; want %#v", reparsed, err, reference)
		}
	})
}
