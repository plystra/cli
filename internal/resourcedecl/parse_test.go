package resourcedecl_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/resourcedecl"
)

func TestParseResourceDeclarations(t *testing.T) {
	for _, body := range []string{
		"//plystra:resource data.database/v12\ntype Resource interface{}",
		"type (\n//plystra:resource data.database/v12\nResource interface{}\n)",
		"//plystra:resource data.database/v12\ntype (Resource interface{})",
	} {
		decls, err := resourcedecl.ParseFile("api/resource.go", []byte("package api\n"+body))
		if err != nil || len(decls) != 1 {
			t.Fatalf("ParseFile = %v, %v", decls, err)
		}
		d := decls[0]
		if d.ID() != "data.database/v12" || d.PackageName() != "api" || d.Position().Path != "api/resource.go" || d.Position().Line < 2 || d.Position().Column != 1 {
			t.Fatalf("declaration = %#v", d)
		}
	}
}

func TestRejectResourceDeclarations(t *testing.T) {
	for _, body := range []string{
		"//plystra:resource\ntype Resource interface{}",
		"//plystra:resource data/v1\ntype Resource interface{}",
		"//plystra:resource data.db/v0\ntype Resource interface{}",
		"//plystra:resource Data.db/v1\ntype Resource interface{}",
		"//plystra:resource data.db/v01\ntype Resource interface{}",
		"//plystra:resource data.db/v1 \ntype Resource interface{}",
		"//plystra:resource  data.db/v1\ntype Resource interface{}",
		"//plystra:resource data.db/v1 extra\ntype Resource interface{}",
		"/*plystra:resource data.db/v1*/\ntype Resource interface{}",
		"//plystra:resource data.db/v1\n\ntype Resource interface{}",
		"//plystra:resource data.db/v1\nfunc New() {}",
		"//plystra:resource data.db/v1\ntype Other interface{}",
		"//plystra:resource data.db/v1\ntype Resource struct{}",
		"//plystra:resource data.db/v1\ntype Resource = interface{}",
		"//plystra:resource data.db/v1\ntype Resource[T any] interface{Get() T}",
		"//plystra:resource data.db/v1\n//plystra:resource data.db/v2\ntype Resource interface{}",
		"//plystra:resource data.db/v1\n//plystra:interface data.db/v1\ntype Resource interface{}",
		"//plystra:resource data.db/v1\ntype (Resource interface{}; Other int)",
	} {
		t.Run(body, func(t *testing.T) {
			_, err := resourcedecl.ParseFile("resource.go", []byte("package api\n"+body))
			var invalid *resourcedecl.InvalidError
			if !errors.Is(err, resourcedecl.ErrInvalid) || !errors.As(err, &invalid) || invalid.Position().Path != "resource.go" || invalid.Position().Line != 2 {
				t.Fatalf("error = %v", err)
			}
		})
	}
	decls, err := resourcedecl.ParseFile("ordinary.go", []byte("package ordinary\nconst marker = `//plystra:resource data.db/v1`\n"))
	if err != nil || len(decls) != 0 {
		t.Fatalf("string marker = %v, %v", decls, err)
	}
}
