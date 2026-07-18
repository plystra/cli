package command

import (
	"reflect"
	"testing"
)

func TestParseCapabilityArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		want      capabilityArguments
		ok        bool
	}{
		{
			name:      "create inferred target",
			arguments: []string{"capability", "create", "records.create"},
			want:      capabilityArguments{action: "create", reference: "records.create"},
			ok:        true,
		},
		{
			name:      "create explicit target and confirmation",
			arguments: []string{"capability", "create", "records.create/v3", "--confirm", "--plugin", "acme.records"},
			want:      capabilityArguments{action: "create", reference: "records.create/v3", plugin: "acme.records", confirm: true},
			ok:        true,
		},
		{
			name:      "implement explicit target",
			arguments: []string{"capability", "implement", "email.send/v1", "--plugin", "mailer"},
			want:      capabilityArguments{action: "implement", reference: "email.send/v1", plugin: "mailer"},
			ok:        true,
		},
		{name: "missing command", arguments: nil},
		{name: "wrong root", arguments: []string{"plugin", "create", "records.create"}},
		{name: "unknown action", arguments: []string{"capability", "remove", "records.create/v1"}},
		{name: "missing reference", arguments: []string{"capability", "create"}},
		{name: "option reference", arguments: []string{"capability", "create", "--confirm"}},
		{name: "duplicate confirmation", arguments: []string{"capability", "create", "records.create/v3", "--confirm", "--confirm"}},
		{name: "implement confirmation", arguments: []string{"capability", "implement", "records.create/v1", "--confirm"}},
		{name: "missing plugin", arguments: []string{"capability", "create", "records.create", "--plugin"}},
		{name: "option plugin", arguments: []string{"capability", "create", "records.create", "--plugin", "--confirm"}},
		{name: "duplicate plugin", arguments: []string{"capability", "create", "records.create", "--plugin", "first", "--plugin", "second"}},
		{name: "unknown option", arguments: []string{"capability", "create", "records.create", "--force"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCapabilityArguments(test.arguments)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseCapabilityArguments(%q) = %#v, %t; want %#v, %t", test.arguments, got, ok, test.want, test.ok)
			}
		})
	}
}
