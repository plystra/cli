// Package interfacecontract validates the type-checked Go shape of an Interface.
package interfacecontract

import (
	"errors"
	"fmt"
	"go/types"

	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfaceid"
)

// ErrInvalid reports a Go declaration that cannot be a canonical Interface contract.
var ErrInvalid = errors.New("invalid Interface contract")

// Contract is the normalized identity of one type-checked Interface operation.
type Contract struct {
	id           interfaceid.Identifier
	packagePath  string
	methodName   string
	requestName  string
	responseName string
}

// ID returns the exact canonical Interface ID.
func (c Contract) ID() interfaceid.Identifier { return c.id }

// PackagePath returns the canonical Go package import path.
func (c Contract) PackagePath() string { return c.packagePath }

// MethodName returns the single exported operation method name.
func (c Contract) MethodName() string { return c.methodName }

// RequestName returns the exported same-package request struct name.
func (c Contract) RequestName() string { return c.requestName }

// ResponseName returns the exported same-package response struct name.
func (c Contract) ResponseName() string { return c.responseName }

// Validate verifies one parsed declaration against its type-checked Go package.
func Validate(declaration interfacedecl.Declaration, checkedPackage *types.Package) (Contract, error) {
	if checkedPackage == nil {
		return Contract{}, invalid(declaration, "type-checked Go package is required")
	}
	object := checkedPackage.Scope().Lookup(declaration.TypeName())
	typeName, ok := object.(*types.TypeName)
	if !ok {
		return Contract{}, invalid(declaration, "declared type Interface is missing from the checked package")
	}
	named, ok := typeName.Type().(*types.Named)
	if !ok {
		return Contract{}, invalid(declaration, "type Interface must be a defined Go type")
	}
	interfaceType, ok := named.Underlying().(*types.Interface)
	if !ok {
		return Contract{}, invalid(declaration, "type Interface must be a Go interface")
	}
	interfaceType = interfaceType.Complete()
	if !interfaceType.IsMethodSet() {
		return Contract{}, invalid(declaration, "type Interface must be a method-only Go interface")
	}
	if interfaceType.NumEmbeddeds() != 0 {
		return Contract{}, invalid(declaration, "type Interface must declare its operation without embedding another interface")
	}
	if interfaceType.NumExplicitMethods() != 1 {
		return Contract{}, invalid(declaration, fmt.Sprintf("type Interface must declare exactly one operation method, found %d", interfaceType.NumExplicitMethods()))
	}

	method := interfaceType.ExplicitMethod(0)
	if !method.Exported() {
		return Contract{}, invalid(declaration, "Interface operation method must be exported")
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok {
		return Contract{}, invalid(declaration, "Interface operation must have a Go function signature")
	}
	if signature.Variadic() {
		return Contract{}, invalid(declaration, "Interface operation must not be variadic")
	}
	if signature.Params().Len() != 2 {
		return Contract{}, invalid(declaration, fmt.Sprintf("Interface operation must accept context.Context and one request, found %d parameters", signature.Params().Len()))
	}
	if !isContext(checkedPackage, signature.Params().At(0).Type()) {
		return Contract{}, invalid(declaration, "Interface operation first parameter must be context.Context")
	}
	requestName, err := messageTypeName(checkedPackage, signature.Params().At(1).Type(), "request")
	if err != nil {
		return Contract{}, invalid(declaration, err.Error())
	}

	if signature.Results().Len() != 2 {
		return Contract{}, invalid(declaration, fmt.Sprintf("Interface operation must return one response and error, found %d results", signature.Results().Len()))
	}
	responseName, err := messageTypeName(checkedPackage, signature.Results().At(0).Type(), "response")
	if err != nil {
		return Contract{}, invalid(declaration, err.Error())
	}
	if !types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) {
		return Contract{}, invalid(declaration, "Interface operation second result must be error")
	}

	return Contract{
		id:           declaration.ID(),
		packagePath:  checkedPackage.Path(),
		methodName:   method.Name(),
		requestName:  requestName,
		responseName: responseName,
	}, nil
}

func isContext(checkedPackage *types.Package, value types.Type) bool {
	for _, imported := range checkedPackage.Imports() {
		if imported.Path() != "context" {
			continue
		}
		object := imported.Scope().Lookup("Context")
		return object != nil && types.Identical(value, object.Type())
	}
	return false
}

func messageTypeName(checkedPackage *types.Package, value types.Type, role string) (string, error) {
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil {
		return "", fmt.Errorf("Interface %s must be a defined exported same-package struct", role)
	}
	if named.Obj().Pkg() != checkedPackage || !named.Obj().Exported() {
		return "", fmt.Errorf("Interface %s must be a defined exported same-package struct", role)
	}
	if named.TypeParams() != nil && named.TypeParams().Len() != 0 {
		return "", fmt.Errorf("Interface %s must not be generic", role)
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return "", fmt.Errorf("Interface %s must be a defined exported same-package struct", role)
	}
	return named.Obj().Name(), nil
}

func invalid(declaration interfacedecl.Declaration, message string) error {
	position := declaration.Position()
	return fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalid, position.Path, position.Line, position.Column, message)
}
