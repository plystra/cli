package implementationdecl_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationdecl"
)

func TestParseFileReturnsExportedConstructorDeclaration(t *testing.T) {
	t.Parallel()

	source := []byte(`package rbac

// New constructs the RBAC implementation.
//plystra:implements authz.check/v1
//plystra:implements authz.explain/v1
func New() (*Service, error) { return &Service{}, nil }

type Service struct{}
`)
	declarations, err := implementationdecl.ParseFile("rbac/service.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(declarations) != 1 {
		t.Fatalf("declarations = %d, want 1", len(declarations))
	}
	declaration := declarations[0]
	if declaration.PackageName() != "rbac" || declaration.FunctionName() != "New" {
		t.Fatalf("declaration = %#v", declaration)
	}
	if position := declaration.Position(); position != (implementationdecl.Position{Path: "rbac/service.go", Line: 6, Column: 6}) {
		t.Fatalf("constructor position = %#v", position)
	}
	implemented := declaration.ImplementedInterfaces()
	if len(implemented) != 2 || implemented[0].ID().String() != "authz.check/v1" || implemented[1].ID().String() != "authz.explain/v1" {
		t.Fatalf("implemented Interfaces = %#v", implemented)
	}
	if position := implemented[0].Position(); position != (implementationdecl.Position{Path: "rbac/service.go", Line: 4, Column: 1}) {
		t.Fatalf("first directive position = %#v", position)
	}
	if position := implemented[1].Position(); position != (implementationdecl.Position{Path: "rbac/service.go", Line: 5, Column: 1}) {
		t.Fatalf("second directive position = %#v", position)
	}

	implemented[0] = implementationdecl.ImplementedInterface{}
	if declaration.ImplementedInterfaces()[0].ID().String() != "authz.check/v1" {
		t.Fatal("ImplementedInterfaces returned mutable declaration storage")
	}
}

func TestParseFileAcceptsExplicitlyNamedExportedConstructors(t *testing.T) {
	t.Parallel()

	source := []byte(`package mail

//plystra:implements email.send/v1
func NewSMTP() (*Service, error) { return &Service{}, nil }

//plystra:implements email.send/v1
func NewMemory() (*Service, error) { return &Service{}, nil }

type Service struct{}
`)
	declarations, err := implementationdecl.ParseFile("service.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(declarations) != 2 {
		t.Fatalf("declarations = %d, want 2", len(declarations))
	}
	if got, want := []string{declarations[0].FunctionName(), declarations[1].FunctionName()}, []string{"NewSMTP", "NewMemory"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("constructor names = %v, want %v", got, want)
	}
}

func TestParseFileIgnoresOrdinaryGoSource(t *testing.T) {
	t.Parallel()

	declarations, err := implementationdecl.ParseFile("service.go", []byte("package service\n\n// New constructs a service.\nfunc New() *Service { return &Service{} }\ntype Service struct{}\n"))
	if err != nil || len(declarations) != 0 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
}

func TestParseFileRejectsInvalidDirectives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "missing ID",
			source: `package test
//plystra:implements
func New() (*Service, error) { return nil, nil }
`,
			want: "expected //plystra:implements <interface-id>",
		},
		{
			name: "extra token",
			source: `package test
//plystra:implements order.create/v1 extra
func New() (*Service, error) { return nil, nil }
`,
			want: "expected one canonical Interface ID",
		},
		{
			name: "invalid ID",
			source: `package test
//plystra:implements order/v1
func New() (*Service, error) { return nil, nil }
`,
			want: "invalid Interface ID",
		},
		{
			name: "block comment",
			source: `package test
/*plystra:implements order.create/v1*/
func New() (*Service, error) { return nil, nil }
`,
			want: "expected //plystra:implements <interface-id>",
		},
		{
			name: "unattached",
			source: `package test
//plystra:implements order.create/v1

func New() (*Service, error) { return nil, nil }
`,
			want: "must immediately document",
		},
		{
			name: "non-function",
			source: `package test
//plystra:implements order.create/v1
var New = func() {}
`,
			want: "must immediately document",
		},
		{
			name: "method",
			source: `package test
type Factory struct{}
//plystra:implements order.create/v1
func (Factory) New() (*Service, error) { return nil, nil }
`,
			want: "package-level constructor function",
		},
		{
			name: "unexported function",
			source: `package test
//plystra:implements order.create/v1
func newService() (*Service, error) { return nil, nil }
`,
			want: "constructor function must be exported",
		},
		{
			name: "duplicate Interface",
			source: `package test
//plystra:implements order.create/v1
//plystra:implements order.create/v1
func New() (*Service, error) { return nil, nil }
`,
			want: "declares Interface order.create/v1 more than once",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			declarations, err := implementationdecl.ParseFile("implementation.go", []byte(test.source))
			if !errors.Is(err, implementationdecl.ErrInvalid) || len(declarations) != 0 {
				t.Fatalf("ParseFile = %#v, %v", declarations, err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile error %q does not contain %q", err, test.want)
			}
		})
	}
}

func TestParseFileRejectsInvalidGoSyntax(t *testing.T) {
	t.Parallel()

	declarations, err := implementationdecl.ParseFile("implementation.go", []byte("package test\nfunc New("))
	if !errors.Is(err, implementationdecl.ErrInvalid) || len(declarations) != 0 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
}

func FuzzParseFile(f *testing.F) {
	for _, seed := range []string{
		"package test\n//plystra:implements order.create/v1\nfunc New() (*Service, error) { return nil, nil }\n",
		"package test\nfunc New() *Service { return nil }\n",
		"package test\n//plystra:implements order/v1\nfunc New() (*Service, error) { return nil, nil }\n",
		"not go source",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		declarations, err := implementationdecl.ParseFile("fuzz.go", []byte(source))
		if err != nil {
			if !errors.Is(err, implementationdecl.ErrInvalid) {
				t.Fatalf("ParseFile returned unexpected error: %v", err)
			}
			return
		}
		for _, declaration := range declarations {
			if declaration.PackageName() == "" || declaration.FunctionName() == "" || declaration.Position().Path != "fuzz.go" || len(declaration.ImplementedInterfaces()) == 0 {
				t.Fatalf("invalid declaration: %#v", declaration)
			}
			for _, implemented := range declaration.ImplementedInterfaces() {
				if implemented.ID().String() == "" || implemented.Position().Path != "fuzz.go" {
					t.Fatalf("invalid implemented Interface: %#v", implemented)
				}
			}
		}
	})
}
