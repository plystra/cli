// Package implementationadaptergen renders typed endpoint adapters for
// selected ordinary Go Implementations.
package implementationadaptergen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	kernelcapability "github.com/plystra/kernel/capability"
	"golang.org/x/mod/module"
)

const (
	kernelCapabilityPackage  = "github.com/plystra/kernel/capability"
	kernelInvocationPackage  = "github.com/plystra/kernel/invocation"
	maximumConcreteTypeBytes = 4096
)

var (
	// ErrRender reports invalid adapter input or generated Go source.
	ErrRender = errors.New("render Implementation adapters")
	// ErrInvalidInput reports incomplete or unsafe selected binding identity.
	ErrInvalidInput = errors.New("invalid Implementation adapter input")
	// ErrDuplicateInterface reports more than one selected adapter for an exact
	// Interface.
	ErrDuplicateInterface = errors.New("duplicate Interface Implementation adapter")
)

// Input is the complete selected binding and authored contract shape needed
// by one typed Implementation adapter.
type Input struct {
	InterfaceID    interfaceid.Identifier
	PackagePath    string
	MethodName     string
	RequestName    string
	ResponseName   string
	Constructor    constructorsymbol.Symbol
	ConcreteType   string
	SemanticErrors []string
}

// File is one immutable generated Implementation adapter source file.
type File struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	concrete    string
	path        string
	data        []byte
}

// InterfaceID returns the exact authored Interface represented by this
// adapter.
func (f File) InterfaceID() interfaceid.Identifier { return f.interfaceID }

// Constructor returns the exact selected Implementation constructor.
func (f File) Constructor() constructorsymbol.Symbol { return f.constructor }

// ConcreteType returns the selected constructor's exact inferred pointer type
// as deterministic provenance.
func (f File) ConcreteType() string { return f.concrete }

// Path returns the Project-relative generated source path.
func (f File) Path() string { return f.path }

// Data returns defensive gofmt-formatted generated source.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Render validates, canonicalizes, and renders one package per selected
// Interface binding in exact Interface-ID order. Each adapter owns the exact
// contract token used to construct its endpoint so later generated handles can
// share that same opaque Kernel definition.
func Render(inputs []Input) ([]File, error) {
	normalized := make([]Input, len(inputs))
	for index, input := range inputs {
		normalized[index] = input
		normalized[index].SemanticErrors = append([]string(nil), input.SemanticErrors...)
		if err := validateInput(normalized[index]); err != nil {
			return nil, fmt.Errorf("%w: input %d: %w", ErrRender, index, err)
		}
		slices.Sort(normalized[index].SemanticErrors)
		for errorIndex := 1; errorIndex < len(normalized[index].SemanticErrors); errorIndex++ {
			if normalized[index].SemanticErrors[errorIndex-1] == normalized[index].SemanticErrors[errorIndex] {
				return nil, fmt.Errorf(
					"%w: input %d: %w: semantic error %q is duplicated",
					ErrRender,
					index,
					ErrInvalidInput,
					normalized[index].SemanticErrors[errorIndex],
				)
			}
		}
	}
	sortInputs(normalized)
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].InterfaceID == normalized[index].InterfaceID {
			return nil, fmt.Errorf("%w: %w: %s", ErrRender, ErrDuplicateInterface, normalized[index].InterfaceID)
		}
	}

	files := make([]File, len(normalized))
	for index, input := range normalized {
		data, err := render(input)
		if err != nil {
			return nil, fmt.Errorf("%w: Interface %s: %v", ErrRender, input.InterfaceID, err)
		}
		files[index] = File{
			interfaceID: input.InterfaceID,
			constructor: input.Constructor,
			concrete:    input.ConcreteType,
			path:        outputPath(input.InterfaceID),
			data:        data,
		}
	}
	return files, nil
}

func validateInput(input Input) error {
	if input.InterfaceID.String() == "" {
		return fmt.Errorf("%w: Interface ID is required", ErrInvalidInput)
	}
	if module.CheckImportPath(input.PackagePath) != nil || input.PackagePath == kernelCapabilityPackage || input.PackagePath == kernelInvocationPackage {
		return fmt.Errorf("%w: Interface package %q is invalid", ErrInvalidInput, input.PackagePath)
	}
	for _, candidate := range []struct {
		name  string
		value string
	}{
		{name: "method", value: input.MethodName},
		{name: "request", value: input.RequestName},
		{name: "response", value: input.ResponseName},
	} {
		if !token.IsIdentifier(candidate.value) || !ast.IsExported(candidate.value) {
			return fmt.Errorf("%w: %s name %q must be an exported Go identifier", ErrInvalidInput, candidate.name, candidate.value)
		}
	}
	if input.Constructor.String() == "" {
		return fmt.Errorf("%w: selected constructor symbol is required", ErrInvalidInput)
	}
	if !validConcreteType(input.ConcreteType) {
		return fmt.Errorf("%w: concrete type provenance %q is invalid", ErrInvalidInput, input.ConcreteType)
	}
	for _, code := range input.SemanticErrors {
		if !kernelcapability.ValidSemanticErrorCode(code) {
			return fmt.Errorf("%w: semantic error code %q is invalid", ErrInvalidInput, code)
		}
	}
	return nil
}

func validConcreteType(value string) bool {
	if value == "" || len(value) > maximumConcreteTypeBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "*") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func sortInputs(inputs []Input) {
	slices.SortFunc(inputs, func(left, right Input) int {
		return strings.Compare(left.InterfaceID.String(), right.InterfaceID.String())
	})
}

func outputPath(identifier interfaceid.Identifier) string {
	segments := strings.Split(identifier.Name(), ".")
	segments = append([]string{"generated", "go", "adapters", "implementations"}, segments...)
	segments = append(segments, "v"+strconv.FormatUint(identifier.Major(), 10), "adapter_gen.go")
	return path.Join(segments...)
}

func render(input Input) ([]byte, error) {
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "package adapter")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "import (")
	fmt.Fprintln(&source, "\t\"context\"")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "\tcontract %s\n", strconv.Quote(input.PackagePath))
	fmt.Fprintf(&source, "\tkernelcapability %s\n", strconv.Quote(kernelCapabilityPackage))
	fmt.Fprintf(&source, "\tkernelinvocation %s\n", strconv.Quote(kernelInvocationPackage))
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "// InterfaceID is the exact authored Interface served by this adapter.\nconst InterfaceID = %s\n", strconv.Quote(input.InterfaceID.String()))
	fmt.Fprintf(&source, "// ConstructorSymbol is the exact selected Implementation constructor.\nconst ConstructorSymbol = %s\n", strconv.Quote(input.Constructor.String()))
	fmt.Fprintf(&source, "// ConcreteType records the exact inferred constructor result without naming it in generated code.\nconst ConcreteType = %s\n", strconv.Quote(input.ConcreteType))
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "var contractToken = kernelcapability.MustParseContractWithSemanticErrors[contract."+input.RequestName+", contract."+input.ResponseName+"](")
	fmt.Fprintln(&source, "\tInterfaceID,")
	for _, code := range input.SemanticErrors {
		fmt.Fprintf(&source, "\t%s,\n", strconv.Quote(code))
	}
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Contract returns the exact opaque contract token shared by this adapter's endpoint and generated handles.")
	fmt.Fprintf(&source, "func Contract() kernelcapability.Contract[contract.%s, contract.%s] {\n", input.RequestName, input.ResponseName)
	fmt.Fprintln(&source, "\treturn contractToken")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// NewEndpoint adapts the selected concrete Implementation through its authored Interface.")
	fmt.Fprintln(&source, "func NewEndpoint(implementation contract.Interface) (kernelinvocation.Endpoint, error) {")
	fmt.Fprintf(&source, "\treturn kernelinvocation.NewEndpoint(contractToken, func(ctx context.Context, request contract.%s) (contract.%s, error) {\n", input.RequestName, input.ResponseName)
	fmt.Fprintf(&source, "\t\treturn implementation.%s(ctx, request)\n", input.MethodName)
	fmt.Fprintln(&source, "\t})")
	fmt.Fprintln(&source, "}")

	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return formatted, nil
}
