package newproject

import (
	"testing"
)

func TestRelativeReplacementPath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: ".", want: true},
		{value: "..", want: true},
		{value: "./support", want: true},
		{value: "../support", want: true},
		{value: `.\support`, want: true},
		{value: `..\support`, want: true},
		{value: "/opt/support", want: false},
		{value: `C:\support`, want: false},
		{value: "example.com/acme/support", want: false},
	} {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			if got := relativeReplacementPath(test.value); got != test.want {
				t.Fatalf("relativeReplacementPath(%q) = %t, want %t", test.value, got, test.want)
			}
		})
	}
}
