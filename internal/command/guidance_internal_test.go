package command

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseGuidanceArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		want      guidanceArguments
		ok        bool
	}{
		{arguments: []string{"guidance", "check"}, want: guidanceArguments{action: "check"}, ok: true},
		{arguments: []string{"guidance", "sync"}, want: guidanceArguments{action: "sync"}, ok: true},
		{arguments: []string{"guidance", "sync", "--replace-generated"}, want: guidanceArguments{action: "sync", replaceGenerated: true}, ok: true},
		{arguments: nil},
		{arguments: []string{"guidance"}},
		{arguments: []string{"check"}},
		{arguments: []string{"guidance", "check", "--replace-generated"}},
		{arguments: []string{"guidance", "sync", "--replace-generated", "--replace-generated"}},
		{arguments: []string{"guidance", "sync", "--unknown"}},
		{arguments: []string{"guidance", "unknown"}},
	}
	for _, test := range tests {
		test := test
		t.Run(guidanceArgumentTestName(test.arguments), func(t *testing.T) {
			t.Parallel()
			got, ok := parseGuidanceArguments(test.arguments)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseGuidanceArguments(%q) = %#v, %t; want %#v, %t", test.arguments, got, ok, test.want, test.ok)
			}
		})
	}
}

func guidanceArgumentTestName(arguments []string) string {
	if len(arguments) == 0 {
		return "empty"
	}
	return strings.Join(arguments, "-")
}
