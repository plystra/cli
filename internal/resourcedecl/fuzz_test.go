package resourcedecl_test

import (
	"testing"

	"github.com/plystra/cli/internal/resourcedecl"
)

func FuzzParseResourceFile(f *testing.F) {
	for _, source := range []string{"package api\n//plystra:resource data.database/v1\ntype Resource interface{}", "package api\n//plystra:resource", "package api\ntype(\n//plystra:resource a.b/v1\nResource interface{Read()}\n)"} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		first, err := resourcedecl.ParseFile("resource.go", []byte(source))
		second, again := resourcedecl.ParseFile("resource.go", []byte(source))
		if (err == nil) != (again == nil) || len(first) != len(second) {
			t.Fatal("Resource parsing is nondeterministic")
		}
		for i := range first {
			if first[i] != second[i] || first[i].ID() == "" || first[i].Position().Path != "resource.go" {
				t.Fatal("invalid parsed Resource")
			}
		}
	})
}
