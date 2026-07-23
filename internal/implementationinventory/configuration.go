package implementationinventory

import (
	"fmt"
	"go/types"
)

// Configuration identifies the exact exported same-package Config struct used
// as an Implementation constructor's optional first parameter.
type Configuration struct {
	packagePath string
	typeName    string
	named       *types.Named
}

// PackagePath returns the Go import path that owns Config.
func (c Configuration) PackagePath() string { return c.packagePath }

// TypeName returns the exact exported Go type name, Config.
func (c Configuration) TypeName() string { return c.typeName }

// String returns the fully qualified Go configuration type, or an empty string
// for the zero value.
func (c Configuration) String() string {
	if c.packagePath == "" || c.typeName == "" {
		return ""
	}
	return c.packagePath + "." + c.typeName
}

func validateConfiguration(compiled *types.Package, function *types.Func) (Configuration, bool, error) {
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return Configuration{}, false, fmt.Errorf("compiled constructor is not a Go function")
	}
	parameters := signature.Params()
	var configuration Configuration
	hasConfiguration := false
	for index := 0; index < parameters.Len(); index++ {
		reference, present := configReference(parameters.At(index).Type())
		if !present {
			continue
		}
		if index != 0 {
			return Configuration{}, false, fmt.Errorf("Config must be the first constructor parameter, found parameter %d", index+1)
		}
		if reference.indirect {
			return Configuration{}, false, fmt.Errorf("Config must be passed as a struct value, not through %s", types.TypeString(parameters.At(index).Type(), nil))
		}
		if reference.alias != nil {
			return Configuration{}, false, fmt.Errorf("Config must be a defined struct, not a type alias")
		}
		named := reference.named
		if named == nil || named.Obj() == nil || named.Obj().Pkg() != compiled {
			return Configuration{}, false, fmt.Errorf("Config must be defined by constructor package %s", compiled.Path())
		}
		if named.Obj().Name() != "Config" || !named.Obj().Exported() {
			return Configuration{}, false, fmt.Errorf("configuration type must be the exported same-package type Config")
		}
		if named.TypeParams() != nil && named.TypeParams().Len() != 0 || named.TypeArgs() != nil && named.TypeArgs().Len() != 0 {
			return Configuration{}, false, fmt.Errorf("Config must not be generic")
		}
		if _, ok := named.Underlying().(*types.Struct); !ok {
			return Configuration{}, false, fmt.Errorf("Config must be a struct")
		}
		configuration = Configuration{packagePath: compiled.Path(), typeName: "Config", named: named}
		hasConfiguration = true
	}
	return configuration, hasConfiguration, nil
}

type configTypeReference struct {
	named    *types.Named
	alias    *types.Alias
	indirect bool
}

func configReference(value types.Type) (configTypeReference, bool) {
	switch typed := value.(type) {
	case *types.Named:
		if typed.Obj() != nil && typed.Obj().Name() == "Config" {
			return configTypeReference{named: typed}, true
		}
	case *types.Alias:
		if typed.Obj() != nil && typed.Obj().Name() == "Config" {
			return configTypeReference{alias: typed}, true
		}
	case *types.Pointer:
		reference, present := configReference(typed.Elem())
		if present {
			reference.indirect = true
			return reference, true
		}
	case *types.Slice:
		reference, present := configReference(typed.Elem())
		if present {
			reference.indirect = true
			return reference, true
		}
	case *types.Array:
		reference, present := configReference(typed.Elem())
		if present {
			reference.indirect = true
			return reference, true
		}
	}
	return configTypeReference{}, false
}
