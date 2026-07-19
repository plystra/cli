package moduleargument

import "testing"

func TestParseQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  string
		path  string
		ok    bool
	}{
		{value: "example.com/acme/platform", want: "example.com/acme/platform", path: "example.com/acme/platform", ok: true},
		{value: "example.com/acme/platform@v1.4.2", want: "example.com/acme/platform@v1.4.2", path: "example.com/acme/platform", ok: true},
		{value: "example.com/acme/platform@latest", want: "example.com/acme/platform@latest", path: "example.com/acme/platform", ok: true},
		{value: "example.com/acme/platform@main", want: "example.com/acme/platform@main", path: "example.com/acme/platform", ok: true},
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
			got, path, err := ParseQuery(test.value)
			if (err == nil) != test.ok || got != test.want || path != test.path {
				t.Fatalf("ParseQuery(%q) = %q, %q, %v; want %q, %q, ok %t", test.value, got, path, err, test.want, test.path, test.ok)
			}
		})
	}
}

func TestParsePath(t *testing.T) {
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
			got, err := ParsePath(test.value)
			if (err == nil) != test.ok || got != test.want {
				t.Fatalf("ParsePath(%q) = %q, %v; want %q, ok %t", test.value, got, err, test.want, test.ok)
			}
		})
	}
}
