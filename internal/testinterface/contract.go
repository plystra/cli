// Package testinterface builds canonical Go contracts for generator tests.
package testinterface

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
)

// Simple builds a request and response with one string field.
func Simple(t testing.TB, identifier, packagePath, method string) interfacecontract.Contract {
	t.Helper()
	return Parse(t, packagePath, fmt.Sprintf("package contract\nimport \"context\"\n//plystra:interface %s\ntype Interface interface { %s(context.Context, Request) (Response, error) }\ntype Request struct { Value string `plystra:\"1\"` }\ntype Response struct { Value string `plystra:\"1\"` }\n", identifier, method))
}

// Parse normalizes an authored contract through the same parser and validator
// used by Interface discovery.
func Parse(t testing.TB, packagePath, source string) interfacecontract.Contract {
	t.Helper()
	const name = "interface.go"
	declarations, err := interfacedecl.ParseFile(name, []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("parse Interface: %v (%d declarations)", err, len(declarations))
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, name, source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check(packagePath, files, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := interfacecontract.Validate(declarations[0], pkg)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}
