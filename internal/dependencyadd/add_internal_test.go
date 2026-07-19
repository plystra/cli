package dependencyadd

import "testing"

func TestValidateQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  string
		ok    bool
	}{
		{value: "example.com/acme/platform", want: "example.com/acme/platform", ok: true},
		{value: "example.com/acme/platform@v1.4.2", want: "example.com/acme/platform@v1.4.2", ok: true},
		{value: "example.com/acme/platform@latest", want: "example.com/acme/platform@latest", ok: true},
		{value: "example.com/acme/platform@main", want: "example.com/acme/platform@main", ok: true},
		{value: ""},
		{value: " example.com/acme/platform"},
		{value: "example.com/acme/platform "},
		{value: "example.com/acme/platform@"},
		{value: "example.com/acme/platform@none"},
		{value: "example.com/acme/platform\n@v1.0.0"},
		{value: "-u"},
		{value: "../platform@v1.0.0"},
		{value: "Example.com/acme/platform@v1.0.0"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, _, err := validateQuery(test.value)
			if (err == nil) != test.ok || got != test.want {
				t.Fatalf("validateQuery(%q) = %q, %v; want %q, ok %t", test.value, got, err, test.want, test.ok)
			}
		})
	}
}
