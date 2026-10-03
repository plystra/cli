package resourceproviderdecl_test

import (
	"errors"
	"go/ast"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourceproviderdecl"
)

func FuzzParseFile(f *testing.F) {
	for _, source := range []string{
		"package provider\n//plystra:implements-resource data.database/v1\nfunc New() (*Database, error) {return nil, nil}",
		"package provider\n//plystra:implements-resource data.database/v1\nfunc New[T any]() {}",
		"package provider\n//plystra:implements-resource data.database/v1\n//plystra:implements order.create/v1\nfunc New() {}",
		"package provider\n//plystra:implements order.create/v1\nfunc NewOrders() {}\n//plystra:implements-resource data.database/v1\nfunc NewDatabase() {}",
		"package provider\n/*plystra:implements-resource data.database/v1*/\nfunc New() {}",
		"package provider\n//plystra:implements-resource",
		"package provider\nconst marker = `//plystra:implements-resource invalid`",
		"not go source",
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		const path = "provider.go"
		first, err := resourceproviderdecl.ParseFile(path, []byte(source))
		second, repeated := resourceproviderdecl.ParseFile(path, []byte(source))
		if !reflect.DeepEqual(first, second) || (err == nil) != (repeated == nil) {
			t.Fatal("Resource provider parsing is nondeterministic")
		}
		if err != nil {
			var invalid *resourceproviderdecl.InvalidError
			if len(first) != 0 || !errors.Is(err, resourceproviderdecl.ErrInvalid) || !errors.As(err, &invalid) || invalid.Position().Path != path || repeated.Error() != err.Error() {
				t.Fatalf("invalid parser failure: %v", err)
			}
			return
		}
		for _, declaration := range first {
			if _, err := interfaceid.Parse(declaration.ID()); err != nil || declaration.PackageName() == "" || !ast.IsExported(declaration.FunctionName()) || declaration.Position().Path != path {
				t.Fatalf("invalid declaration: %#v", declaration)
			}
		}
	})
}
