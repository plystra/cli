package implementationinventory

import (
	"fmt"
	"go/types"

	"github.com/plystra/cli/internal/interfaceid"
	"golang.org/x/mod/module"
)

const kernelOptionalPackagePath = "github.com/plystra/kernel"

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

// OptionalInterface is one exact plystra.Optional[T] constructor parameter
// whose T is a visible canonical Interface.
type OptionalInterface struct {
	id                interfaceid.Identifier
	packagePath       string
	parameterName     string
	parameterPosition int
}

// ID returns the exact optional Interface ID.
func (o OptionalInterface) ID() interfaceid.Identifier { return o.id }

// PackagePath returns the canonical Interface package import path used as T.
func (o OptionalInterface) PackagePath() string { return o.packagePath }

// ParameterName returns the authored Go parameter name, or an empty string for
// an unnamed parameter.
func (o OptionalInterface) ParameterName() string { return o.parameterName }

// ParameterPosition returns the one-based constructor parameter position.
func (o OptionalInterface) ParameterPosition() int { return o.parameterPosition }

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

func validateRequiredInterfaces(function *types.Func, hasConfig bool, optionalPositions map[int]struct{}, interfaces map[string]interfaceid.Identifier) ([]RequiredInterface, error) {
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
		if _, optional := optionalPositions[index]; optional {
			continue
		}
		parameter := parameters.At(index)
		identifier, packagePath, err := canonicalInterface(parameter.Type(), interfaces)
		if err != nil {
			return nil, fmt.Errorf("parameter %d must be a canonical Interface type or plystra.Optional[T]: %v", index+1, err)
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

func validateOptionalInterfaces(function *types.Func, hasConfig bool, interfaces map[string]interfaceid.Identifier) ([]OptionalInterface, map[int]struct{}, error) {
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return nil, nil, fmt.Errorf("compiled constructor is not a Go function")
	}
	parameters := signature.Params()
	optional := make([]OptionalInterface, 0, parameters.Len())
	positions := make(map[int]struct{})
	for index := 0; index < parameters.Len(); index++ {
		if hasConfig && index == 0 {
			continue
		}
		parameter := parameters.At(index)
		reference, present := optionalReference(parameter.Type())
		if !present {
			continue
		}
		positions[index] = struct{}{}
		if reference.indirect {
			return nil, nil, fmt.Errorf("parameter %d plystra.Optional[T] must be passed as a value", index+1)
		}
		named := reference.named
		if named.Obj() == nil || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != kernelOptionalPackagePath {
			return nil, nil, fmt.Errorf("parameter %d Optional must be %s.Optional[T]", index+1, kernelOptionalPackagePath)
		}
		if _, ok := named.Underlying().(*types.Struct); !ok {
			return nil, nil, fmt.Errorf("parameter %d %s.Optional must be the public Kernel struct", index+1, kernelOptionalPackagePath)
		}
		arguments := named.TypeArgs()
		if arguments == nil || arguments.Len() != 1 {
			return nil, nil, fmt.Errorf("parameter %d %s.Optional must have exactly one type argument", index+1, kernelOptionalPackagePath)
		}
		identifier, packagePath, err := canonicalInterface(arguments.At(0), interfaces)
		if err != nil {
			return nil, nil, fmt.Errorf("parameter %d plystra.Optional type argument must be a canonical Interface: %v", index+1, err)
		}
		optional = append(optional, OptionalInterface{
			id:                identifier,
			packagePath:       packagePath,
			parameterName:     parameter.Name(),
			parameterPosition: index + 1,
		})
	}
	return optional, positions, nil
}

type optionalTypeReference struct {
	named    *types.Named
	indirect bool
}

func optionalReference(value types.Type) (optionalTypeReference, bool) {
	var element types.Type
	switch typed := value.(type) {
	case *types.Pointer:
		element = typed.Elem()
	case *types.Slice:
		element = typed.Elem()
	case *types.Array:
		element = typed.Elem()
	}
	if element != nil {
		reference, present := optionalReference(element)
		if present {
			reference.indirect = true
			return reference, true
		}
		return optionalTypeReference{}, false
	}
	named, ok := types.Unalias(value).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Name() != "Optional" {
		return optionalTypeReference{}, false
	}
	return optionalTypeReference{named: named}, true
}

func canonicalInterface(value types.Type, interfaces map[string]interfaceid.Identifier) (interfaceid.Identifier, string, error) {
	named, ok := types.Unalias(value).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil || named.Obj().Name() != "Interface" || !named.Obj().Exported() {
		return interfaceid.Identifier{}, "", fmt.Errorf("type is not the exported named Interface")
	}
	if _, ok := named.Underlying().(*types.Interface); !ok {
		return interfaceid.Identifier{}, "", fmt.Errorf("type %s.Interface is not a Go interface", named.Obj().Pkg().Path())
	}
	packagePath := named.Obj().Pkg().Path()
	identifier, visible := interfaces[packagePath]
	if !visible {
		return interfaceid.Identifier{}, "", fmt.Errorf("type %s.Interface is not a visible canonical Interface package", packagePath)
	}
	return identifier, packagePath, nil
}
