package command

import (
	"reflect"
	"testing"
)

func TestParseNewArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		want      newArguments
		ok        bool
	}{
		{name: "runnable", arguments: []string{"new", "example.com/acme/app"}, want: newArguments{modulePath: "example.com/acme/app"}, ok: true},
		{name: "library", arguments: []string{"new", "example.com/acme/app", "--library"}, want: newArguments{modulePath: "example.com/acme/app", library: true}, ok: true},
		{name: "plugin", arguments: []string{"new", "example.com/acme/app", "--plugin", "account"}, want: newArguments{modulePath: "example.com/acme/app", plugin: "account"}, ok: true},
		{name: "combined", arguments: []string{"new", "example.com/acme/app", "--plugin", "account", "--library"}, want: newArguments{modulePath: "example.com/acme/app", library: true, plugin: "account"}, ok: true},
		{name: "missing module", arguments: []string{"new"}},
		{name: "option as module", arguments: []string{"new", "--library"}},
		{name: "duplicate library", arguments: []string{"new", "example.com/acme/app", "--library", "--library"}},
		{name: "missing plugin", arguments: []string{"new", "example.com/acme/app", "--plugin"}},
		{name: "option as plugin", arguments: []string{"new", "example.com/acme/app", "--plugin", "--library"}},
		{name: "duplicate plugin", arguments: []string{"new", "example.com/acme/app", "--plugin", "account", "--plugin", "profile"}},
		{name: "unknown", arguments: []string{"new", "example.com/acme/app", "--unknown"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseNewArguments(test.arguments)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseNewArguments(%q) = %#v, %t; want %#v, %t", test.arguments, got, ok, test.want, test.ok)
			}
		})
	}
}
