package implementationinventory

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"
)

// Configuration identifies the exact exported same-package Config struct used
// as an Implementation constructor's optional first parameter.
type Configuration struct {
	packagePath string
	typeName    string
	fields      []ConfigurationField
	named       *types.Named
}

// ConfigurationField is one immutable exported field in a constructor-owned
// Config type. Name is the canonical YAML key while GoName and TypeIdentity
// retain the exact authored Go field identity for later typed parsing.
type ConfigurationField struct {
	name         string
	goName       string
	typeIdentity string
}

// Name returns the canonical lower-snake-case YAML key.
func (f ConfigurationField) Name() string { return f.name }

// GoName returns the exact exported authored Go field name.
func (f ConfigurationField) GoName() string { return f.goName }

// TypeIdentity returns the deterministic fully qualified Go type identity.
func (f ConfigurationField) TypeIdentity() string { return f.typeIdentity }

// PackagePath returns the Go import path that owns Config.
func (c Configuration) PackagePath() string { return c.packagePath }

// TypeName returns the exact exported Go type name, Config.
func (c Configuration) TypeName() string { return c.typeName }

// Fields returns a defensive field-name-ordered copy of the compiled Config
// schema. Fields excluded with yaml:"-" and unexported implementation details
// do not participate in constructor configuration.
func (c Configuration) Fields() []ConfigurationField {
	return append([]ConfigurationField(nil), c.fields...)
}

// Lookup returns one field by exact canonical YAML name.
func (c Configuration) Lookup(name string) (ConfigurationField, bool) {
	index := sort.Search(len(c.fields), func(index int) bool {
		return c.fields[index].name >= name
	})
	if index >= len(c.fields) || c.fields[index].name != name {
		return ConfigurationField{}, false
	}
	return c.fields[index], true
}

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
		fields, err := compileConfigurationFields(named.Underlying().(*types.Struct))
		if err != nil {
			return Configuration{}, false, err
		}
		configuration = Configuration{packagePath: compiled.Path(), typeName: "Config", fields: fields, named: named}
		hasConfiguration = true
	}
	return configuration, hasConfiguration, nil
}

func compileConfigurationFields(structure *types.Struct) ([]ConfigurationField, error) {
	fields := make([]ConfigurationField, 0, structure.NumFields())
	seen := make(map[string]string, structure.NumFields())
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		rawTag := structure.Tag(index)
		yamlTag, tagged := reflect.StructTag(rawTag).Lookup("yaml")
		if !field.Exported() {
			if tagged && yamlTag != "-" {
				return nil, fmt.Errorf("unexported Config field %s must not declare a YAML key", field.Name())
			}
			continue
		}
		if field.Anonymous() {
			return nil, fmt.Errorf("embedded Config field %s is not supported; declare an explicit named field", field.Name())
		}
		name, ignored, err := configurationFieldName(field.Name(), yamlTag, tagged)
		if err != nil {
			return nil, fmt.Errorf("Config field %s: %v", field.Name(), err)
		}
		if ignored {
			continue
		}
		if previous, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("Config fields %s and %s declare duplicate YAML key %q", previous, field.Name(), name)
		}
		seen[name] = field.Name()
		fields = append(fields, ConfigurationField{
			name:         name,
			goName:       field.Name(),
			typeIdentity: configurationTypeIdentity(field.Type()),
		})
	}
	sort.Slice(fields, func(left, right int) bool {
		return fields[left].name < fields[right].name
	})
	return fields, nil
}

func configurationFieldName(goName, yamlTag string, tagged bool) (string, bool, error) {
	name := strings.ToLower(goName)
	if tagged {
		parts := strings.Split(yamlTag, ",")
		if len(parts) != 1 {
			return "", false, fmt.Errorf("YAML tag options are not supported; use yaml:%q", parts[0])
		}
		if parts[0] == "-" {
			return "", true, nil
		}
		if parts[0] != "" {
			name = parts[0]
		}
	}
	if !validConfigurationFieldName(name) {
		return "", false, fmt.Errorf("YAML key %q is not canonical lower snake case", name)
	}
	return name, false, nil
}

func configurationTypeIdentity(value types.Type) string {
	return types.TypeString(value, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
}

func validConfigurationFieldName(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousUnderscore := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousUnderscore = false
		case character == '_' && !previousUnderscore:
			previousUnderscore = true
		default:
			return false
		}
	}
	return !previousUnderscore
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
