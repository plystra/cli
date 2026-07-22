package interfaceid_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/interfaceid"
)

func TestParseExactIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		name  string
		major uint64
	}{
		{value: "order.create/v1", name: "order.create", major: 1},
		{value: "authn.session.verify/v12", name: "authn.session.verify", major: 12},
		{value: "workspace.invite-member/v2", name: "workspace.invite-member", major: 2},
		{value: "workflow.retry--now-/v3", name: "workflow.retry--now-", major: 3},
		{value: "storage.object.put/v18446744073709551615", name: "storage.object.put", major: ^uint64(0)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			identifier, err := interfaceid.Parse(test.value)
			if err != nil {
				t.Fatalf("Parse(%q): %v", test.value, err)
			}
			if identifier.Name() != test.name || identifier.Major() != test.major || identifier.String() != test.value {
				t.Fatalf("Parse(%q) = name %q, major %d, string %q", test.value, identifier.Name(), identifier.Major(), identifier.String())
			}
		})
	}
}

func TestParseRejectsNonCanonicalIdentifiers(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"order",
		"Order.create/v1",
		"order.Create/v1",
		"order_create/v1",
		"order..create/v1",
		"order.-create/v1",
		"order.1create/v1",
		"order.create_/v1",
		" order.create/v1",
		"order.create/v1 ",
		"订单.create/v1",
		"order.create/V1",
		"order.create/v0",
		"order.create/v01",
		"order.create/v-1",
		"order.create/v18446744073709551616",
		"order.create/v1/extra",
	}
	for _, value := range invalid {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			identifier, err := interfaceid.Parse(value)
			if !errors.Is(err, interfaceid.ErrInvalid) || identifier.String() != "" {
				t.Errorf("Parse(%q) = %q, %v", value, identifier.String(), err)
			}
		})
	}
}

func TestNewAndZeroValue(t *testing.T) {
	t.Parallel()

	identifier, err := interfaceid.New("order.create", 2)
	if err != nil || identifier.String() != "order.create/v2" {
		t.Fatalf("New = %q, %v", identifier.String(), err)
	}
	for _, test := range []struct {
		name  string
		major uint64
	}{{name: "order", major: 1}, {name: "order.create", major: 0}} {
		invalid, err := interfaceid.New(test.name, test.major)
		if !errors.Is(err, interfaceid.ErrInvalid) || invalid.String() != "" {
			t.Fatalf("New(%q, %d) = %q, %v", test.name, test.major, invalid.String(), err)
		}
	}

	var zero interfaceid.Identifier
	if zero.Name() != "" || zero.Major() != 0 || zero.String() != "" {
		t.Fatalf("zero value is not empty: %#v", zero)
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"order.create/v1",
		"authn.session.verify/v12",
		"workspace.invite-member/v2",
		"workflow.retry--now-/v3",
		"bad",
		"order.create/v0",
		"订单.create/v1",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		identifier, err := interfaceid.Parse(value)
		if err != nil {
			if !errors.Is(err, interfaceid.ErrInvalid) {
				t.Fatalf("Parse(%q) returned unexpected error: %v", value, err)
			}
			return
		}
		if identifier.String() != value {
			t.Fatalf("round trip = %q, want %q", identifier.String(), value)
		}
		reparsed, err := interfaceid.Parse(identifier.String())
		if err != nil || reparsed != identifier {
			t.Fatalf("reparse = %#v, %v; want %#v", reparsed, err, identifier)
		}
	})
}
