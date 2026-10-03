package resourcecontract_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/resourcecontract"
	"github.com/plystra/cli/internal/resourcedecl"
)

func contract(t testing.TB, source string) (resourcecontract.Contract, error) {
	t.Helper()
	return contractInPackage(t, "example.com/library/api", source)
}

func contractInPackage(t testing.TB, packagePath, source string) (resourcecontract.Contract, error) {
	t.Helper()
	source = "package api\n" + source
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "resource.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check(packagePath, fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	decls, err := resourcedecl.ParseFile("resource.go", []byte(source))
	if err != nil || len(decls) != 1 {
		t.Fatalf("declarations = %v, %v", decls, err)
	}
	return resourcecontract.Validate(decls[0], pkg)
}

func digest(t testing.TB, source string) string {
	t.Helper()
	c, err := contract(t, source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Digest(), "sha256:") || len(c.Digest()) != 71 {
		t.Fatalf("digest = %q", c.Digest())
	}
	return c.Digest()
}

const marker = "//plystra:resource data.database/v1\n"

func TestResourcePublicShapeChangesDigest(t *testing.T) {
	for _, test := range []struct{ name, before, after string }{
		{"public-field", "type Value struct { Data string };", "type Value struct { Data []byte };"},
		{"tag", "type Value struct { Data string `json:\"data\"` };", "type Value struct { Data string `json:\"other\"` };"},
		{"pointer", "type Value *string;", "type Value **string;"},
		{"array", "type Value [2]string;", "type Value [3]string;"},
		{"map", "type Value map[string]int;", "type Value map[int]string;"},
		{"channel", "type Value <-chan int;", "type Value chan<- int;"},
		{"signature", "type Value func(int, string) error;", "type Value func(string, int) error;"},
		{"variadic", "type Value func([]string);", "type Value func(...string);"},
		{"method", "type Value struct{}; func(Value) Read() string {return \"\"};", "type Value struct{}; func(Value) Read() []byte {return nil};"},
		{"pointer-method", "type Value struct{}; func(*Value) Read() string {return \"\"};", "type Value struct{}; func(*Value) Read() []byte {return nil};"},
		{"method-receiver", "type Value struct{}; func(Value) Read() {};", "type Value struct{}; func(*Value) Read() {};"},
		{"private-promoted-field", "type hidden struct{Data string}; type Value struct{hidden};", "type hidden struct{Data []byte}; type Value struct{hidden};"},
		{"recursive", "type Value struct{Next *Value; Data string};", "type Value struct{Next *Value; Data []byte};"},
		{"mutually-recursive", "type Value struct{Next *Other}; type Other struct{Next *Value; Data string};", "type Value struct{Next *Other}; type Other struct{Next *Value; Data int};"},
		{"phantom-type-argument", "type Box[T any] struct{}; type Value = Box[int];", "type Box[T any] struct{}; type Value = Box[string];"},
		{"instance-underlying", "type Box[T any] struct{Data T}; type Value = Box[string];", "type Box[T any] struct{Data []T}; type Value = Box[string];"},
		{"embeddedness", "type Inner struct{Data string}; type Value struct{Inner};", "type Inner struct{Data string}; type Value struct{Inner Inner};"},
		{"field-order", "type Value struct{A int; B string};", "type Value struct{B string; A int};"},
		{"interface-method", "type Value interface{Read() string};", "type Value interface{Read() []byte};"},
		{"sealed-interface", "type Value interface{Read(); sealed()};", "type Value interface{Read()};"},
		{"returned-close", "type Value interface{Close() error};", "type Value interface{Close()};"},
	} {
		t.Run(test.name, func(t *testing.T) {
			suffix := "\n" + marker + "type Resource interface { Get() Value }"
			if digest(t, test.before+suffix) == digest(t, test.after+suffix) {
				t.Fatal("changed public shape preserved digest")
			}
		})
	}
}

func TestResourceDigestIgnoresNonContractChanges(t *testing.T) {
	for _, test := range []struct{ name, before, after string }{
		{"alias", "type Value = string;", "type Value = Alias; type Alias = string;"},
		{"byte", "type Value = []byte;", "type Value = []uint8;"},
		{"rune", "type Value = rune;", "type Value = int32;"},
		{"any", "type Value = any;", "type Value = interface{};"},
		{"parameter-name", "type Value func(first string) (result int);", "type Value func(second string) (other int);"},
		{"private-field", "type Value struct{Data string; private int};", "type Value struct{secret bool; Data string; private []byte};"},
		{"private-method", "type Value struct{}; func(Value) internal() int {return 0};", "type Value struct{}; func(Value) internal() string {return \"\"};"},
		{"method-order", "type Value interface{A(); B()};", "type Value interface{B(); A()};"},
		{"embedding", "type Value interface{Read()};", "type Hidden interface{Read()}; type Value interface{Hidden};"},
		{"provider-lifecycle", "type Value int; type Provider struct{}; func(*Provider) Close() {};", "type Value int; type Provider struct{}; func(*Provider) Shutdown() {};"},
		{"private-embedded-name", "type inner struct{Data string}; type Value struct{inner};", "type renamed struct{Data string}; type Value struct{renamed};"},
	} {
		t.Run(test.name, func(t *testing.T) {
			suffix := "\n" + marker + "type Resource interface { Get() Value }"
			if digest(t, test.before+suffix) != digest(t, test.after+suffix) {
				t.Fatal("non-contract change altered digest")
			}
		})
	}
	base := marker + "type Resource interface{Read() string}"
	if digest(t, base) != digest(t, "\n// documentation only\n"+base) {
		t.Fatal("location/comment changed digest")
	}
	if digest(t, base) == digest(t, strings.Replace(base, "data.database/v1", "data.database/v2", 1)) {
		t.Fatal("identity did not affect digest")
	}
}

func TestResourceOrdinaryInfrastructureTypes(t *testing.T) {
	digest(t, "import \"context\"\n"+marker+`type Resource interface {
		Query(ctx context.Context, values map[string]any, callback func([]byte) error) (<-chan struct{Data [4]*int}, error)
	}`)
	for _, name := range []string{"Start", "Stop", "Shutdown", "Close"} {
		for _, embedded := range []bool{false, true} {
			body := marker + "type Resource interface {" + name + "()}"
			if embedded {
				body = "type Lifecycle interface{" + name + "()}\n" + marker + "type Resource interface{Lifecycle}"
			}
			if _, err := contract(t, body); !errors.Is(err, resourcecontract.ErrInvalid) || !strings.Contains(err.Error(), name) {
				t.Fatalf("lifecycle %s: %v", name, err)
			}
		}
	}
	if _, err := contract(t, marker+"type Resource interface { ~int }"); !errors.Is(err, resourcecontract.ErrInvalid) {
		t.Fatalf("constraint interface: %v", err)
	}
}

func TestResourceGenericMethodsAndPackageIdentity(t *testing.T) {
	base := "type Box[T any] struct{}; func(Box[T]) Get() T {var zero T; return zero}\n" + marker + "type Resource interface{Get() Box[int]}"
	concrete := strings.Replace(base, "Get() T {var zero T; return zero}", "Get() int {return 0}", 1)
	if digest(t, base) != digest(t, concrete) {
		t.Fatal("instantiated method signature did not substitute the type argument")
	}
	if digest(t, base) == digest(t, strings.Replace(concrete, "Get() int {return 0}", "Get() string {return \"\"}", 1)) {
		t.Fatal("instantiated public method change was hidden")
	}
	first, err := contractInPackage(t, "example.com/first/api", base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := contractInPackage(t, "example.com/second/api", base)
	if err != nil || first.Digest() == second.Digest() {
		t.Fatalf("package identity not retained: %v", err)
	}
	alias := "type Box[T any] struct{Value T}; type Alias[T any] = Box[T];\n" + marker + "type Resource interface{Get() Alias[int]}"
	if digest(t, alias) != digest(t, strings.Replace(alias, "Get() Alias[int]", "Get() Box[int]", 1)) {
		t.Fatal("generic alias altered digest")
	}
}

func TestResourceGenericRecursiveReferenceAtDepthLimit(t *testing.T) {
	body := "type Value[T any] struct{Next " + strings.Repeat("*", 58) + "Value[T]}\n" + marker + "type Resource interface{Get() Value[int]}"
	digest(t, body)
	if _, err := contract(t, strings.Replace(body, strings.Repeat("*", 58), strings.Repeat("*", 59), 1)); !errors.Is(err, resourcecontract.ErrInvalid) {
		t.Fatalf("one-over recursive depth accepted: %v", err)
	}
}

func TestResourceAnonymousPromotedMethodsAndRecursion(t *testing.T) {
	for _, receiver := range []string{"hidden", "*hidden"} {
		prefix := "type hidden struct{}; func(" + receiver + ") Read() "
		suffix := "\n" + marker + "type Resource interface{Value() struct{hidden}}"
		if digest(t, prefix+"int {return 0}"+suffix) == digest(t, prefix+"string {return \"\"}"+suffix) {
			t.Fatal("anonymous promoted method change preserved digest")
		}
	}
	body := "type hidden struct{}; func(hidden) Again() struct{hidden} {panic(0)}\n" + marker + "type Resource interface{Value() struct{hidden}}"
	digest(t, body)
	before := "type Public struct{Data int}; type hidden struct{};\n" + marker + "type Resource interface{Value() struct{Public;hidden}}"
	after := strings.Replace(before, "type hidden struct{}", "type hidden struct{Data string}", 1)
	if digest(t, before) == digest(t, after) {
		t.Fatal("new ambiguous exported selector preserved digest")
	}
	private := "type hidden struct{}; func(hidden) Again() struct{hidden; private int} {panic(0)}\n" + marker + "type Resource interface{Value() struct{hidden; private int}}"
	changed := strings.Replace(private, "Value() struct{hidden; private int}", "Value() struct{hidden; private string}", 1)
	if digest(t, private) != digest(t, changed) {
		t.Fatal("private-only anonymous recursion edit changed digest")
	}
}

func TestResourceDepthRechecksSharedTypeAtLongerPaths(t *testing.T) {
	for _, depth := range []int{60, 61} {
		// Root named=1, underlying=2, method signature=3, parameter=4.
		body := marker + "type Resource interface{ A(int); Z(" + strings.Repeat("*", depth) + "int) }"
		_, err := contract(t, body)
		if (err != nil) != (depth == 61) {
			t.Fatalf("depth %d: %v", depth+4, err)
		}
	}
	// A named type reached shallowly must still expand on a later deep path.
	body := "type Value struct{Data int}\n" + marker + "type Resource interface{ A(Value); Z(" + strings.Repeat("*", 59) + "Value) }"
	if _, err := contract(t, body); !errors.Is(err, resourcecontract.ErrInvalid) {
		t.Fatalf("shared named graph depth: %v", err)
	}
}

func TestResourceNodeBound(t *testing.T) {
	for _, parameters := range []string{"int", "int, int"} {
		var body strings.Builder
		body.WriteString(marker + "type Resource interface { Read(" + parameters + ") struct {\n")
		for i := 0; i < 32765; i++ {
			fmt.Fprintf(&body, "Field%d int\n", i)
		}
		body.WriteString("} }")
		_, err := contract(t, body.String())
		if (err != nil) != (parameters == "int, int") {
			t.Fatalf("node limit with parameters %s: %v", parameters, err)
		}
	}
}

func TestResourcePrivateEmbeddingDoesNotConsumePublicBudget(t *testing.T) {
	var private strings.Builder
	private.WriteString("type hidden struct{\n")
	for i := 0; i < 70000; i++ {
		fmt.Fprintf(&private, "private%d int\n", i)
	}
	private.WriteString("}\n" + marker + "type Resource interface {Value() struct{hidden}}")
	if digest(t, private.String()) != digest(t, "type hidden struct{}\n"+marker+"type Resource interface {Value() struct{hidden}}") {
		t.Fatal("discarded private fields changed public digest")
	}
}

func TestResourceLongPrivateRecursiveProjectionIsCanonical(t *testing.T) {
	var declarations strings.Builder
	for i := 0; i <= 70; i++ {
		next := i + 1
		if i == 70 {
			next = i
		}
		fmt.Fprintf(&declarations, "type hidden%d struct{}; func(hidden%d) Again() struct{hidden%d} {panic(0)}\n", i, i, next)
	}
	declarations.WriteString(marker + "type Resource interface{Value() struct{hidden0}}")
	single := "type hidden struct{}; func(hidden) Again() struct{hidden} {panic(0)}\n" + marker + "type Resource interface{Value() struct{hidden}}"
	if digest(t, declarations.String()) != digest(t, single) {
		t.Fatal("discarded comparison traversal changed canonical recursion")
	}
	changed := strings.Replace(declarations.String(), "func(hidden70) Again() struct{hidden70}", "func(hidden70) Different() struct{hidden70}", 1)
	if _, err := contract(t, changed); !errors.Is(err, resourcecontract.ErrInvalid) {
		t.Fatalf("genuinely deep public graph did not fail: %v", err)
	}
}
