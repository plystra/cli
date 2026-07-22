package interfacedecl_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/interfacedecl"
)

func TestParseFileReturnsCanonicalDeclaration(t *testing.T) {
	t.Parallel()

	source := []byte(`package createv1

import "context"

// Interface creates an order.
//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`)
	declarations, err := interfacedecl.ParseFile("interfaces/order/create/v1/interface.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(declarations) != 1 {
		t.Fatalf("declarations = %d, want 1", len(declarations))
	}
	declaration := declarations[0]
	position := declaration.Position()
	if declaration.ID().String() != "order.create/v1" || declaration.PackageName() != "createv1" || declaration.TypeName() != "Interface" {
		t.Fatalf("declaration = %#v", declaration)
	}
	if position.Path != "interfaces/order/create/v1/interface.go" || position.Line != 6 || position.Column != 1 {
		t.Fatalf("position = %#v", position)
	}
}

func TestParseFileAcceptsTypeSpecificationDocumentation(t *testing.T) {
	t.Parallel()

	source := []byte(`package readv1

import "context"

type (
	//plystra:interface records.read/v2
	Interface interface {
		Read(context.Context, Request) (Response, error)
	}
	Request struct{}
	Response struct{}
)
`)
	declarations, err := interfacedecl.ParseFile("interface.go", source)
	if err != nil || len(declarations) != 1 || declarations[0].ID().String() != "records.read/v2" {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
}

func TestParseFileIgnoresOrdinaryGoSource(t *testing.T) {
	t.Parallel()

	declarations, err := interfacedecl.ParseFile("service.go", []byte("package service\n\n// Interface is an ordinary comment.\ntype Service struct{}\n"))
	if err != nil || len(declarations) != 0 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
}

func TestParseFileRejectsInvalidDirectives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "missing ID",
			source: `package test
//plystra:interface
type Interface interface{}
`,
		},
		{
			name: "extra token",
			source: `package test
//plystra:interface order.create/v1 extra
type Interface interface{}
`,
		},
		{
			name: "invalid ID",
			source: `package test
//plystra:interface order/v1
type Interface interface{}
`,
		},
		{
			name: "block comment",
			source: `package test
/*plystra:interface order.create/v1*/
type Interface interface{}
`,
		},
		{
			name: "unattached",
			source: `package test
//plystra:interface order.create/v1

type Interface interface{}
`,
		},
		{
			name: "wrong type name",
			source: `package test
//plystra:interface order.create/v1
type Creator interface{}
`,
		},
		{
			name: "not interface",
			source: `package test
//plystra:interface order.create/v1
type Interface struct{}
`,
		},
		{
			name: "type alias",
			source: `package test
type Other interface{}
//plystra:interface order.create/v1
type Interface = Other
`,
		},
		{
			name: "duplicate directive",
			source: `package test
//plystra:interface order.create/v1
//plystra:interface order.create/v1
type Interface interface{}
`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			declarations, err := interfacedecl.ParseFile("interface.go", []byte(test.source))
			if !errors.Is(err, interfacedecl.ErrInvalid) || len(declarations) != 0 {
				t.Fatalf("ParseFile = %#v, %v", declarations, err)
			}
		})
	}
}

func TestParseFileRejectsInvalidGoSyntax(t *testing.T) {
	t.Parallel()

	declarations, err := interfacedecl.ParseFile("interface.go", []byte("package test\ntype Interface interface {"))
	if !errors.Is(err, interfacedecl.ErrInvalid) || len(declarations) != 0 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
}

func FuzzParseFile(f *testing.F) {
	for _, seed := range []string{
		"package test\n//plystra:interface order.create/v1\ntype Interface interface{}\n",
		"package test\ntype Service struct{}\n",
		"package test\n//plystra:interface order/v1\ntype Interface interface{}\n",
		"not go source",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		declarations, err := interfacedecl.ParseFile("fuzz.go", []byte(source))
		if err != nil {
			if !errors.Is(err, interfacedecl.ErrInvalid) {
				t.Fatalf("ParseFile returned unexpected error: %v", err)
			}
			return
		}
		for _, declaration := range declarations {
			if declaration.ID().String() == "" || declaration.TypeName() != "Interface" || declaration.Position().Path != "fuzz.go" {
				t.Fatalf("invalid declaration: %#v", declaration)
			}
		}
	})
}
