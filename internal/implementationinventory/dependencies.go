package implementationinventory

import (
	"fmt"
	"go/types"

	"github.com/plystra/cli/internal/interfaceid"
	"golang.org/x/mod/module"
)

// InterfaceInput identifies one already validated visible canonical Interface
// package for constructor dependency matching.
type InterfaceInput struct {
	ID          interfaceid.Identifier
	PackagePath string
}

// RequiredInterface is one exact canonical Interface constructor parameter.
type RequiredInterface struct {
	id                interfaceid.Identifier
	packagePath       string
	parameterName     string
	parameterPosition int
}

// ID returns the exact required Interface ID.
func (r RequiredInterface) ID() interfaceid.Identifier { return r.id }

// PackagePath returns the canonical Interface package import path.
func (r RequiredInterface) PackagePath() string { return r.packagePath }

// ParameterName returns the authored Go parameter name, or an empty string for
// an unnamed parameter.
func (r RequiredInterface) ParameterName() string { return r.parameterName }

// ParameterPosition returns the one-based constructor parameter position.
func (r RequiredInterface) ParameterPosition() int { return r.parameterPosition }

func indexInterfacePackages(inputs []InterfaceInput) (map[string]interfaceid.Identifier, error) {
	result := make(map[string]interfaceid.Identifier, len(inputs))
	for _, input := range inputs {
		if input.ID.String() == "" {
			return nil, fmt.Errorf("%w: visible Interface has an empty ID", ErrInvalidInput)
		}
		if err := module.CheckImportPath(input.PackagePath); err != nil {
			return nil, fmt.Errorf("%w: visible Interface %s has invalid package path %q: %v", ErrInvalidInput, input.ID, input.PackagePath, err)
		}
		if existing, duplicate := result[input.PackagePath]; duplicate {
			return nil, fmt.Errorf("%w: package %s declares both Interface %s and %s", ErrInvalidInput, input.PackagePath, existing, input.ID)
		}
		result[input.PackagePath] = input.ID
	}
	return result, nil
}

func validateRequiredInterfaces(function *types.Func, hasConfig bool, interfaces map[string]interfaceid.Identifier) ([]RequiredInterface, error) {
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("compiled constructor is not a Go function")
	}
	parameters := signature.Params()
	required := make([]RequiredInterface, 0, parameters.Len())
	for index := 0; index < parameters.Len(); index++ {
		if hasConfig && index == 0 {
			continue
		}
		parameter := parameters.At(index)
		if deferredOptionalParameter(parameter.Type()) {
			continue
		}
		named, ok := types.Unalias(parameter.Type()).(*types.Named)
		if !ok || named.Obj() == nil || named.Obj().Pkg() == nil || named.Obj().Name() != "Interface" || !named.Obj().Exported() {
			return nil, fmt.Errorf("parameter %d must be a canonical Interface type or plystra.Optional[T]", index+1)
		}
		if _, ok := named.Underlying().(*types.Interface); !ok {
			return nil, fmt.Errorf("parameter %d type %s.Interface is not a Go interface", index+1, named.Obj().Pkg().Path())
		}
		packagePath := named.Obj().Pkg().Path()
		identifier, visible := interfaces[packagePath]
		if !visible {
			return nil, fmt.Errorf("parameter %d type %s.Interface is not a visible canonical Interface package", index+1, packagePath)
		}
		required = append(required, RequiredInterface{
			id:                identifier,
			packagePath:       packagePath,
			parameterName:     parameter.Name(),
			parameterPosition: index + 1,
		})
	}
	return required, nil
}

// Optional[T] receives its exact public package, arity, type-argument, and
// canonical-Interface validation in the next structural-conformance stage.
// This narrow recognition prevents required-parameter validation from
// preempting that independently verifiable outcome.
func deferredOptionalParameter(value types.Type) bool {
	named, ok := types.Unalias(value).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Name() == "Optional" && named.TypeArgs() != nil && named.TypeArgs().Len() == 1
}
