package implementationinventory

import (
	"fmt"
	"go/types"
)

// ConcreteType identifies the exact defined non-interface pointer type returned
// by an Implementation constructor.
type ConcreteType struct {
	packagePath string
	typeName    string
	pointer     *types.Pointer
}

// PackagePath returns the Go import path that defines the concrete type.
func (c ConcreteType) PackagePath() string { return c.packagePath }

// TypeName returns the defined concrete type name without pointer syntax or
// generic type arguments.
func (c ConcreteType) TypeName() string { return c.typeName }

// String returns the fully qualified pointer type, including any concrete
// generic type arguments, or an empty string for the zero value.
func (c ConcreteType) String() string {
	if c.pointer == nil {
		return ""
	}
	return types.TypeString(c.pointer, func(pkg *types.Package) string {
		return pkg.Path()
	})
}

func validateConstructorResult(function *types.Func) (ConcreteType, error) {
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return ConcreteType{}, fmt.Errorf("compiled constructor is not a Go function")
	}
	if parameters := signature.TypeParams(); parameters != nil && parameters.Len() != 0 {
		return ConcreteType{}, fmt.Errorf("constructor must not declare type parameters")
	}
	results := signature.Results()
	if results.Len() != 2 {
		return ConcreteType{}, fmt.Errorf("constructor must return exactly one concrete pointer and error, found %d results", results.Len())
	}
	pointer, ok := types.Unalias(results.At(0).Type()).(*types.Pointer)
	if !ok {
		return ConcreteType{}, fmt.Errorf("constructor first result must be a pointer to a defined concrete type")
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return ConcreteType{}, fmt.Errorf("constructor first result must point to a defined concrete type")
	}
	if _, isInterface := named.Underlying().(*types.Interface); isInterface {
		return ConcreteType{}, fmt.Errorf("constructor first result must point to a concrete type, not a Go interface")
	}
	predeclaredError := types.Universe.Lookup("error")
	if predeclaredError == nil || !types.Identical(types.Unalias(results.At(1).Type()), predeclaredError.Type()) {
		return ConcreteType{}, fmt.Errorf("constructor second result must be the predeclared error type")
	}
	return ConcreteType{
		packagePath: named.Obj().Pkg().Path(),
		typeName:    named.Obj().Name(),
		pointer:     types.NewPointer(named),
	}, nil
}
