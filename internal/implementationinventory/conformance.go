package implementationinventory

import (
	"errors"
	"fmt"
	"go/types"
)

func validateStructuralConformance(input Input, concrete ConcreteType, interfaces canonicalInterfaceIndex) error {
	for _, declaration := range input.Declaration.ImplementedInterfaces() {
		identifier := declaration.ID().String()
		definitions := interfaces.identities[identifier]
		switch len(definitions) {
		case 0:
			return fmt.Errorf("declared Interface %s at %s has no visible canonical Interface", identifier, declarationSource(input, declaration.Position().Path, declaration.Position().Line, declaration.Position().Column))
		case 1:
			// Continue below. Duplicate Interface identities remain the responsibility
			// of the complete-provenance identity validator.
		default:
			continue
		}

		canonical := definitions[0]
		target := canonical.interfaceType
		if imported := importedPackage(input.Types, canonical.packagePath); imported != nil {
			if input.Importer != nil {
				compiled, err := input.Importer.Import(canonical.packagePath)
				if err != nil {
					return fmt.Errorf("load canonical Interface %s from constructor import graph package %s", identifier, canonical.packagePath)
				}
				resolved, err := interfaceType(compiled)
				if err != nil {
					return fmt.Errorf("resolve canonical Interface %s from constructor import graph: %v", identifier, err)
				}
				target = resolved
			} else {
				resolved, err := interfaceType(imported)
				if err == nil {
					target = resolved
				}
			}
		}
		if types.AssignableTo(concrete.pointer, target) {
			continue
		}

		interfaceType := target.Underlying().(*types.Interface)
		missing, wrongType := types.MissingMethod(concrete.pointer, interfaceType, true)
		qualifiedInterface := canonical.packagePath + ".Interface"
		prefix := fmt.Sprintf("declared Interface %s at %s: concrete result %s is not assignable to %s", identifier, declarationSource(input, declaration.Position().Path, declaration.Position().Line, declaration.Position().Column), concrete.String(), qualifiedInterface)
		if missing == nil {
			return errors.New(prefix)
		}
		if !wrongType {
			return fmt.Errorf("%s: missing method %s", prefix, missing.Name())
		}
		actual, _, _ := types.LookupFieldOrMethod(concrete.pointer, true, input.Types, missing.Name())
		have := "<missing>"
		if actual != nil {
			have = qualifiedType(actual.Type())
		}
		return fmt.Errorf("%s: method %s has an incompatible signature: have %s; want %s", prefix, missing.Name(), have, qualifiedType(missing.Type()))
	}
	return nil
}

func importedPackage(root *types.Package, packagePath string) *types.Package {
	if root == nil {
		return nil
	}
	stack := []*types.Package{root}
	seen := make(map[*types.Package]struct{})
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current == nil {
			continue
		}
		if _, visited := seen[current]; visited {
			continue
		}
		seen[current] = struct{}{}
		if current.Path() == packagePath {
			return current
		}
		imports := current.Imports()
		for index := len(imports) - 1; index >= 0; index-- {
			stack = append(stack, imports[index])
		}
	}
	return nil
}

func interfaceType(compiled *types.Package) (*types.Named, error) {
	if compiled == nil {
		return nil, fmt.Errorf("compiled package is nil")
	}
	object := compiled.Scope().Lookup("Interface")
	if object == nil {
		return nil, fmt.Errorf("package %s has no Interface type", compiled.Path())
	}
	named, ok := object.Type().(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() != compiled || !named.Obj().Exported() {
		return nil, fmt.Errorf("package %s has no exported defined Interface type", compiled.Path())
	}
	underlying, ok := named.Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("package %s Interface is not a Go interface", compiled.Path())
	}
	underlying.Complete()
	return named, nil
}

func declarationSource(input Input, sourcePath string, line, column int) string {
	version := input.ModuleVersion
	if version == "" {
		version = "local"
	}
	return fmt.Sprintf("%s@%s/%s:%d:%d", input.ModulePath, version, sourcePath, line, column)
}

func qualifiedType(value types.Type) string {
	return types.TypeString(value, func(pkg *types.Package) string {
		return pkg.Path()
	})
}
