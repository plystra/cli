package implementationinventory

import (
	"fmt"
	"go/token"
	"go/types"

	"github.com/plystra/cli/internal/interfaceid"
	"golang.org/x/mod/module"
)

// ResourceInput identifies one already validated visible canonical Resource
// package. Types must come from the same importer as the constructor packages.
type ResourceInput struct {
	ID          interfaceid.Identifier
	PackagePath string
	Types       *types.Package
}

// RequiredResource is one exact canonical Resource constructor parameter.
type RequiredResource struct {
	id                interfaceid.Identifier
	packagePath       string
	parameterName     string
	parameterPosition int
}

// ID returns the exact required Resource ID.
func (r RequiredResource) ID() interfaceid.Identifier { return r.id }

// PackagePath returns the canonical Resource package import path.
func (r RequiredResource) PackagePath() string { return r.packagePath }

// ParameterName returns the exact nonblank authored Go dependency identifier.
func (r RequiredResource) ParameterName() string { return r.parameterName }

// ParameterPosition returns the one-based constructor parameter position.
func (r RequiredResource) ParameterPosition() int { return r.parameterPosition }

type canonicalResourceDefinition struct {
	id           interfaceid.Identifier
	resourceType *types.Named
}

func indexResourcePackages(inputs []ResourceInput) (map[string]canonicalResourceDefinition, error) {
	packages := make(map[string]canonicalResourceDefinition, len(inputs))
	identities := make(map[interfaceid.Identifier]string, len(inputs))
	for _, input := range inputs {
		if input.ID.String() == "" {
			return nil, fmt.Errorf("%w: visible Resource has an empty ID", ErrInvalidInput)
		}
		if err := module.CheckImportPath(input.PackagePath); err != nil {
			return nil, fmt.Errorf("%w: visible Resource %s has invalid package path %q", ErrInvalidInput, input.ID, input.PackagePath)
		}
		if input.Types == nil || input.Types.Path() != input.PackagePath {
			return nil, fmt.Errorf("%w: visible Resource %s package %s has missing or mismatched compiled type information", ErrInvalidInput, input.ID, input.PackagePath)
		}
		object, ok := input.Types.Scope().Lookup("Resource").(*types.TypeName)
		if !ok || object.IsAlias() || object.Pkg() != input.Types {
			return nil, fmt.Errorf("%w: visible Resource %s package %s must define its exported Resource type", ErrInvalidInput, input.ID, input.PackagePath)
		}
		named, ok := object.Type().(*types.Named)
		if !ok || named.Obj() != object || named.TypeParams().Len() != 0 {
			return nil, fmt.Errorf("%w: visible Resource %s must be a non-generic defined type", ErrInvalidInput, input.ID)
		}
		contract, ok := named.Underlying().(*types.Interface)
		if !ok || !contract.Complete().IsMethodSet() {
			return nil, fmt.Errorf("%w: visible Resource %s must be an ordinary Go interface", ErrInvalidInput, input.ID)
		}
		if _, duplicate := packages[input.PackagePath]; duplicate {
			return nil, fmt.Errorf("%w: duplicate visible Resource package %s", ErrInvalidInput, input.PackagePath)
		}
		if previous, duplicate := identities[input.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate visible Resource %s in packages %s and %s", ErrInvalidInput, input.ID, previous, input.PackagePath)
		}
		packages[input.PackagePath] = canonicalResourceDefinition{id: input.ID, resourceType: named}
		identities[input.ID] = input.PackagePath
	}
	return packages, nil
}

func validateRequiredResources(function *types.Func, hasConfig bool, resources map[string]canonicalResourceDefinition) ([]RequiredResource, map[int]struct{}, error) {
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return nil, nil, fmt.Errorf("compiled constructor is not a Go function")
	}
	var required []RequiredResource
	positions := make(map[int]struct{})
	for index := 0; index < signature.Params().Len(); index++ {
		if index == 0 && hasConfig {
			continue
		}
		parameter := signature.Params().At(index)
		if !resourceReference(parameter.Type()) {
			continue
		}
		named, ok := types.Unalias(parameter.Type()).(*types.Named)
		if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
			return nil, nil, fmt.Errorf("parameter %d must be an exact canonical Resource value", index+1)
		}
		packagePath := named.Obj().Pkg().Path()
		canonical, visible := resources[packagePath]
		if !visible {
			return nil, nil, fmt.Errorf("parameter %d Resource is not a visible canonical contract", index+1)
		}
		// Go assignability would accept a copied interface with the same method
		// set. Only the shared importer's exact named contract is a dependency.
		if !types.Identical(named, canonical.resourceType) {
			return nil, nil, fmt.Errorf("parameter %d does not use its exact canonical Resource type", index+1)
		}
		if parameter.Name() == "_" || !token.IsIdentifier(parameter.Name()) {
			return nil, nil, fmt.Errorf("parameter %d must have an explicit nonblank Go identifier", index+1)
		}
		required = append(required, RequiredResource{
			id: canonical.id, packagePath: packagePath,
			parameterName: parameter.Name(), parameterPosition: index + 1,
		})
		positions[index] = struct{}{}
	}
	return required, positions, nil
}

func resourceReference(value types.Type) bool {
	switch typed := types.Unalias(value).(type) {
	case *types.Pointer:
		return resourceReference(typed.Elem())
	case *types.Slice:
		return resourceReference(typed.Elem())
	case *types.Array:
		return resourceReference(typed.Elem())
	case *types.Named:
		return typed.Obj() != nil && typed.Obj().Name() == "Resource"
	default:
		return false
	}
}
