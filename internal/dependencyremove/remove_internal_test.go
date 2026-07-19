package dependencyremove

import "testing"

func TestValidateModulePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  string
		ok    bool
	}{
		{value: "example.com/acme/platform", want: "example.com/acme/platform", ok: true},
		{value: "github.com/acme/platform", want: "github.com/acme/platform", ok: true},
		{value: ""},
		{value: " example.com/acme/platform"},
		{value: "example.com/acme/platform "},
		{value: "example.com/acme/platform@v1.0.0"},
		{value: "example.com/acme/platform@none"},
		{value: "example.com/acme/platform\n"},
		{value: "-u"},
		{value: "../platform"},
		{value: "Example.com/acme/platform"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := validateModulePath(test.value)
			if (err == nil) != test.ok || got != test.want {
				t.Fatalf("validateModulePath(%q) = %q, %v; want %q, ok %t", test.value, got, err, test.want, test.ok)
			}
		})
	}
}
