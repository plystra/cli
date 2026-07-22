package interfaceinventory

import (
	"errors"
	"testing"
)

func TestDecodePackagesRejectsDuplicateAndUnsafeRecords(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"ImportPath":"example.com/value"}{"ImportPath":"example.com/value"}`,
		`{"ImportPath":""}`,
		"{\"ImportPath\":\"example.com/value\",\"Export\":\"relative.a\"}",
		`{"ImportPath":"example.com/value"} trailing`,
	}
	for _, data := range tests {
		packages, exports, err := decodePackages([]byte(data))
		if !errors.Is(err, ErrInvalidOutput) || packages != nil || exports != nil {
			t.Fatalf("decodePackages(%q) = %#v, %#v, %v", data, packages, exports, err)
		}
	}
}

func FuzzDecodePackages(f *testing.F) {
	for _, seed := range []string{
		`{"ImportPath":"example.com/value"}`,
		`{"ImportPath":"context","Export":"C:\\cache\\context.a"}`,
		`{"ImportPath":"example.com/value"}{"ImportPath":"context"}`,
		`not JSON`,
		``,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		packages, exports, err := decodePackages([]byte(data))
		if err != nil {
			if !errors.Is(err, ErrInvalidOutput) || packages != nil || exports != nil {
				t.Fatalf("decodePackages returned inconsistent error: %#v, %#v, %v", packages, exports, err)
			}
			return
		}
		if len(packages) == 0 && len(data) != 0 {
			// Whitespace is the only nonempty input that can validly decode to no
			// package records.
			for _, character := range data {
				if character != ' ' && character != '\t' && character != '\r' && character != '\n' {
					t.Fatalf("nonempty JSON stream produced no packages: %q", data)
				}
			}
		}
		for importPath, exportPath := range exports {
			loaded, exists := packages[importPath]
			if !exists || loaded.Export != exportPath {
				t.Fatalf("export index %q=%q disagrees with packages %#v", importPath, exportPath, packages)
			}
		}
	})
}
