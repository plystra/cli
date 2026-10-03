package resourceproviderdecl_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/resourceproviderdecl"
)

func TestParseFileReturnsResourceProviderDeclaration(t *testing.T) {
	t.Parallel()
	source := []byte("package database\n\n// New constructs a database provider.\n//plystra:implements-resource data.database/v12\nfunc New(cfg Config) (*Database, error) { return nil, nil }\n")
	before := bytes.Clone(source)
	declarations, err := resourceproviderdecl.ParseFile("database/provider.go", source)
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	d := declarations[0]
	if d.ID() != "data.database/v12" || d.PackageName() != "database" || d.FunctionName() != "New" || d.Position() != (resourceproviderdecl.Position{Path: "database/provider.go", Line: 5, Column: 6}) {
		t.Fatalf("declaration = %#v", d)
	}
	declarations[0] = resourceproviderdecl.Declaration{}
	again, err := resourceproviderdecl.ParseFile("database/provider.go", source)
	if err != nil || len(again) != 1 || again[0] != d || !bytes.Equal(source, before) {
		t.Fatalf("parser retained mutable state: %#v, %v", again, err)
	}
}

func TestParseFilePreservesIndependentConstructorsInSourceOrder(t *testing.T) {
	t.Parallel()
	source := "package database\n//plystra:implements-resource data.database/v1\nfunc NewZ() {}\n//plystra:implements-resource data.database/v1\nfunc NewA() {}\n//plystra:implements-resource storage.cache/v2\nfunc NewCache() {}\n"
	declarations, err := resourceproviderdecl.ParseFile("provider.go", []byte(source))
	if err != nil || len(declarations) != 3 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	for i, name := range []string{"NewZ", "NewA", "NewCache"} {
		if declarations[i].FunctionName() != name || declarations[i].Position().Line != 3+2*i {
			t.Fatalf("declaration %d = %#v", i, declarations[i])
		}
	}
	if declarations[0].ID() != declarations[1].ID() || declarations[2].ID() != "storage.cache/v2" {
		t.Fatalf("identities = %#v", declarations)
	}
}

func TestParseFileSeparatesDirectiveOwners(t *testing.T) {
	t.Parallel()
	for _, providerFirst := range []bool{false, true} {
		provider := "//plystra:implements-resource data.database/v1\nfunc NewDatabase() {}\n"
		ordinary := "//plystra:implements order.create/v1\n//plystra:implements order.read/v1\nfunc NewOrders() {}\n"
		constructors := ordinary + "\n" + provider
		if providerFirst {
			constructors = provider + "\n" + ordinary
		}
		source := []byte("package service\n//plystra:resource data.database/v1\ntype Resource interface{}\n//plystra:interface order.create/v1\ntype Interface interface{}\n\n" + constructors)
		resources, err := resourceproviderdecl.ParseFile("service.go", source)
		if err != nil || len(resources) != 1 || resources[0].FunctionName() != "NewDatabase" {
			t.Fatalf("Resource providers = %#v, %v", resources, err)
		}
		implementations, err := implementationdecl.ParseFile("service.go", source)
		if err != nil || len(implementations) != 1 || implementations[0].FunctionName() != "NewOrders" || len(implementations[0].ImplementedInterfaces()) != 2 {
			t.Fatalf("Implementations = %#v, %v", implementations, err)
		}
	}
}

func TestParseFileLeavesGoLoadingAndSignatureValidationToCaller(t *testing.T) {
	t.Parallel()
	for _, function := range []string{
		"func New() {}",
		"func New(cfg Unknown, dependencies ...Missing) int { return 0 }",
		"func New() (*Database, error)",
		"func \u00c9tablir() {}",
	} {
		source := "//go:build resource_provider_inactive\n\npackage provider\n//plystra:implements-resource data.database/v1\n" + function
		declarations, err := resourceproviderdecl.ParseFile("provider.go", []byte(source))
		if err != nil || len(declarations) != 1 {
			t.Fatalf("%s: %#v, %v", function, declarations, err)
		}
	}
	longID := "data." + strings.Repeat("long", 300) + "/v1"
	source := "package provider\r\n//plystra:implements-resource " + longID + "\r\nfunc New() {}\r\n"
	declarations, err := resourceproviderdecl.ParseFile("provider.go", []byte(source))
	if err != nil || len(declarations) != 1 || declarations[0].ID() != longID {
		t.Fatalf("long identity or CRLF changed: %#v, %v", declarations, err)
	}
}

func TestParseFileIgnoresNonProviderCommentsAndLiterals(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"// New constructs an ordinary value.\nfunc New() {}",
		"//plystra:implements order.create/v1\nfunc New() {}",
		"//plystra:implements invalid-ordinary-id\nfunc New() {}",
		"//plystra:resource data.database/v1\ntype Resource interface{}",
		"//plystra:interface order.create/v1\ntype Interface interface{}",
		"const line = \"//plystra:implements-resource invalid-private-marker\"",
		"const block = \"/*plystra:implements-resource invalid-private-marker*/\"",
		"const raw = `\n//plystra:implements-resource invalid-private-marker\n/*plystra:implements-resource another-private-marker*/\n`",
		"// Example: //plystra:implements-resource data.database/v1",
	} {
		declarations, err := resourceproviderdecl.ParseFile("provider.go", []byte("package provider\n"+body))
		if err != nil || len(declarations) != 0 {
			t.Fatalf("%s: %#v, %v", body, declarations, err)
		}
	}
}

func TestParseFileRejectsMalformedProviderDirectives(t *testing.T) {
	t.Parallel()
	for _, directive := range []string{
		"//plystra:implements-resource",
		"//plystra:implements-resource ",
		"//plystra:implements-resource\tdata.database/v1",
		"//plystra:implements-resource  data.database/v1",
		"//plystra:implements-resource data.database/v1 ",
		"//plystra:implements-resource data.database/v1\t",
		"//plystra:implements-resource data.database/v1 data.cache/v1",
		"//plystra:implements-resource data.database/v1\tdata.cache/v1",
		"//plystra:implements-resource data.database/v1,data.cache/v1",
		"//plystra:implements-resource data.database/v1 // private-marker",
		"//plystra:implements-resources data.database/v1",
		"//plystra:implements-resource=invalid-private-marker",
		"/*plystra:implements-resource data.database/v1*/",
		"/*plystra:implements-resource\ndata.database/v1\n*/",
		"//plystra:implements-resource invalid-private-marker",
		"//plystra:implements-resource database/v1",
		"//plystra:implements-resource Data.database/v1",
		"//plystra:implements-resource data.Database/v1",
		"//plystra:implements-resource data..database/v1",
		"//plystra:implements-resource data.database/v0",
		"//plystra:implements-resource data.database/v01",
		"//plystra:implements-resource data.database/V1",
		"//plystra:implements-resource data.database/v18446744073709551616",
		"//plystra:implements-resource data.database/v1/extra",
	} {
		t.Run(directive, func(t *testing.T) {
			err := rejected(t, "package provider\n"+directive+"\nfunc New() {}", 2)
			if strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("malformed directive leaked its payload: %v", err)
			}
		})
	}
}

func TestParseFileRejectsInvalidProviderTargets(t *testing.T) {
	t.Parallel()
	const directive = "//plystra:implements-resource data.database/v1\n"
	for _, test := range []struct {
		name, body string
		line       int
	}{
		{"duplicate ID", directive + directive + "func New() {}", 3},
		{"multiple IDs", directive + "//plystra:implements-resource storage.cache/v1\nfunc New() {}", 3},
		{"unattached", directive + "\nfunc New() {}", 2},
		{"end of file", directive, 2},
		{"type", directive + "type New struct{}", 2},
		{"literal", directive + "var New = func() {}", 2},
		{"method", "type Factory struct{}\n" + directive + "func (Factory) New() {}", 3},
		{"pointer method", "type Factory struct{}\n" + directive + "func (*Factory) New() {}", 3},
		{"interface method", "type Factory interface{\n" + directive + "New()\n}", 3},
		{"unexported", directive + "func newDatabase() {}", 2},
		{"underscore", directive + "func _New() {}", 2},
		{"blank", directive + "func _() {}", 2},
		{"generic", directive + "func New[T any]() {}", 2},
		{"body comment", "func New() {\n" + directive + "}", 3},
		{"trailing", "func New() {} " + directive, 2},
	} {
		t.Run(test.name, func(t *testing.T) { rejected(t, "package provider\n"+test.body, test.line) })
	}
}

func TestParseFileRejectsMixedDirectivesOnSameConstructor(t *testing.T) {
	t.Parallel()
	const provider = "//plystra:implements-resource data.database/v1\n"
	for _, other := range []string{
		"//plystra:implements order.create/v1\n",
		"//plystra:implements\n",
		"/*plystra:implements order.create/v1*/\n",
		"//plystra:resource data.database/v1\n",
		"/*plystra:resource data.database/v1*/\n",
		"//plystra:interface order.create/v1\n",
		"/*plystra:interface order.create/v1*/\n",
	} {
		for _, providerFirst := range []bool{false, true} {
			body, line := other+provider, 3
			if providerFirst {
				body, line = provider+other, 2
			}
			rejected(t, "package provider\n"+body+"func New() {}", line)
		}
	}
}

func TestParseFileRejectsWholeFileAndRedactsSyntaxErrors(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"package provider\nvar secret = invalid-private-marker",
		"package provider\n//plystra:implements-resource data.database/v1\nfunc New() {}\n//plystra:implements-resource\nfunc Other() {}",
		"package provider\n//plystra:implements-resource data.database/v1\nfunc New() {}\n//plystra:implements-resource data.database/v1\n",
	} {
		if strings.Contains(source, "invalid-private-marker") {
			source += " ?"
		}
		err := rejected(t, source, 0)
		if strings.Contains(err.Error(), "invalid-private-marker") {
			t.Fatalf("syntax error exposed source: %v", err)
		}
	}
	var invalid *resourceproviderdecl.InvalidError
	if invalid.Position() != (resourceproviderdecl.Position{}) || invalid.Error() != resourceproviderdecl.ErrInvalid.Error() || !errors.Is(invalid, resourceproviderdecl.ErrInvalid) {
		t.Fatal("nil error methods are inconsistent")
	}
}

func rejected(t testing.TB, source string, line int) error {
	t.Helper()
	const path = "database/provider.go"
	declarations, err := resourceproviderdecl.ParseFile(path, []byte(source))
	var invalid *resourceproviderdecl.InvalidError
	if len(declarations) != 0 || !errors.Is(err, resourceproviderdecl.ErrInvalid) || !errors.As(err, &invalid) || invalid.Position().Path != path || invalid.Position().Line < 1 || invalid.Position().Column < 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	if line != 0 && invalid.Position().Line != line {
		t.Fatalf("position = %#v, want line %d", invalid.Position(), line)
	}
	again, repeated := resourceproviderdecl.ParseFile(path, []byte(source))
	if !reflect.DeepEqual(declarations, again) || repeated == nil || err.Error() != repeated.Error() {
		t.Fatalf("nondeterministic rejection: %v / %v", err, repeated)
	}
	return err
}
