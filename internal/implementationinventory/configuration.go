package implementationinventory

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"
)

const (
	maximumConfigurationTypeDepth = 64
	maximumConfigurationFields    = 65_536
)

// ConfigurationValueKind is one closed authored Go shape accepted for
// constructor configuration schema compilation.
type ConfigurationValueKind string

const (
	ConfigurationValueString          ConfigurationValueKind = "string"
	ConfigurationValueBoolean         ConfigurationValueKind = "boolean"
	ConfigurationValueSignedInteger   ConfigurationValueKind = "signed-integer"
	ConfigurationValueUnsignedInteger ConfigurationValueKind = "unsigned-integer"
	ConfigurationValueNumber          ConfigurationValueKind = "number"
	ConfigurationValueDuration        ConfigurationValueKind = "duration"
	ConfigurationValueURL             ConfigurationValueKind = "url"
	ConfigurationValueSecret          ConfigurationValueKind = "secret"
	ConfigurationValueObject          ConfigurationValueKind = "object"
	ConfigurationValuePointer         ConfigurationValueKind = "pointer"
	ConfigurationValueList            ConfigurationValueKind = "list"
	ConfigurationValueMap             ConfigurationValueKind = "map"
)

// Valid reports whether the value belongs to the closed configuration kind
// vocabulary.
func (k ConfigurationValueKind) Valid() bool {
	switch k {
	case ConfigurationValueString,
		ConfigurationValueBoolean,
		ConfigurationValueSignedInteger,
		ConfigurationValueUnsignedInteger,
		ConfigurationValueNumber,
		ConfigurationValueDuration,
		ConfigurationValueURL,
		ConfigurationValueSecret,
		ConfigurationValueObject,
		ConfigurationValuePointer,
		ConfigurationValueList,
		ConfigurationValueMap:
		return true
	default:
		return false
	}
}

// ConfigurationValue is one immutable recursively compiled authored Go type.
// Objects expose their fixed field schema; pointers, lists, and string-keyed
// maps expose one element type. An array additionally retains its exact length.
type ConfigurationValue struct {
	kind         ConfigurationValueKind
	typeIdentity string
	element      *ConfigurationValue
	fields       []ConfigurationField
	arrayLength  int64
	isArray      bool
}

// Kind returns the closed normalized Go value kind.
func (v ConfigurationValue) Kind() ConfigurationValueKind { return v.kind }

// TypeIdentity returns the deterministic canonical Go type identity.
func (v ConfigurationValue) TypeIdentity() string { return v.typeIdentity }

// Element returns the immutable element for a pointer, list, or map.
func (v ConfigurationValue) Element() (ConfigurationValue, bool) {
	if v.element == nil {
		return ConfigurationValue{}, false
	}
	return *v.element, true
}

// Fields returns the defensive name-ordered object field schema.
func (v ConfigurationValue) Fields() []ConfigurationField {
	return append([]ConfigurationField(nil), v.fields...)
}

// ArrayLength returns an authored fixed array length. A false result
// distinguishes slices and every non-list kind from arrays, including [0]T.
func (v ConfigurationValue) ArrayLength() (int64, bool) {
	if v.kind != ConfigurationValueList || !v.isArray {
		return 0, false
	}
	return v.arrayLength, true
}

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
	name   string
	goName string
	value  ConfigurationValue
}

// Name returns the canonical lower-snake-case YAML key.
func (f ConfigurationField) Name() string { return f.name }

// GoName returns the exact exported authored Go field name.
func (f ConfigurationField) GoName() string { return f.goName }

// TypeIdentity returns the deterministic fully qualified Go type identity.
func (f ConfigurationField) TypeIdentity() string { return f.value.typeIdentity }

// Value returns the recursively compiled immutable Go value schema.
func (f ConfigurationField) Value() ConfigurationValue { return f.value }

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
		value, err := compileConfigurationValue(named, &configurationCompileState{active: make(map[types.Type]struct{})}, 0)
		if err != nil {
			return Configuration{}, false, err
		}
		if value.kind != ConfigurationValueObject {
			return Configuration{}, false, fmt.Errorf("Config must compile to an object")
		}
		fields := value.fields
		configuration = Configuration{packagePath: compiled.Path(), typeName: "Config", fields: fields, named: named}
		hasConfiguration = true
	}
	return configuration, hasConfiguration, nil
}

type configurationCompileState struct {
	active map[types.Type]struct{}
	fields int
}

func compileConfigurationFields(structure *types.Struct, state *configurationCompileState, depth int) ([]ConfigurationField, error) {
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
		state.fields++
		if state.fields > maximumConfigurationFields {
			return nil, fmt.Errorf("Config schema exceeds %d fields", maximumConfigurationFields)
		}
		value, err := compileConfigurationValue(field.Type(), state, depth+1)
		if err != nil {
			return nil, fmt.Errorf("Config field %s: %v", field.Name(), err)
		}
		seen[name] = field.Name()
		fields = append(fields, ConfigurationField{
			name:   name,
			goName: field.Name(),
			value:  value,
		})
	}
	sort.Slice(fields, func(left, right int) bool {
		return fields[left].name < fields[right].name
	})
	return fields, nil
}

func compileConfigurationValue(value types.Type, state *configurationCompileState, depth int) (ConfigurationValue, error) {
	if value == nil || state == nil || state.active == nil {
		return ConfigurationValue{}, fmt.Errorf("configuration Go type is unavailable")
	}
	if depth > maximumConfigurationTypeDepth {
		return ConfigurationValue{}, fmt.Errorf("configuration Go type exceeds %d levels", maximumConfigurationTypeDepth)
	}
	value = types.Unalias(value)
	identity := configurationTypeIdentity(value)
	if named, ok := value.(*types.Named); ok {
		if exactConfigurationNamedType(named, "time", "Duration") {
			return ConfigurationValue{kind: ConfigurationValueDuration, typeIdentity: identity}, nil
		}
		if exactConfigurationNamedType(named, "net/url", "URL") {
			return ConfigurationValue{kind: ConfigurationValueURL, typeIdentity: identity}, nil
		}
		if exactConfigurationNamedType(named, "github.com/plystra/kernel/configuration", "Secret") {
			return ConfigurationValue{kind: ConfigurationValueSecret, typeIdentity: identity}, nil
		}
		if _, cycle := state.active[named]; cycle {
			return ConfigurationValue{}, fmt.Errorf("configuration Go type %s is recursive", identity)
		}
		state.active[named] = struct{}{}
		compiled, err := compileConfigurationValue(named.Underlying(), state, depth)
		delete(state.active, named)
		if err != nil {
			return ConfigurationValue{}, err
		}
		compiled.typeIdentity = identity
		return compiled, nil
	}

	switch typed := value.(type) {
	case *types.Basic:
		kind, ok := configurationBasicKind(typed.Kind())
		if !ok {
			return ConfigurationValue{}, fmt.Errorf("configuration Go type %s is not supported", identity)
		}
		return ConfigurationValue{kind: kind, typeIdentity: identity}, nil
	case *types.Pointer:
		element, err := compileConfigurationValue(typed.Elem(), state, depth+1)
		if err != nil {
			return ConfigurationValue{}, err
		}
		if configurationContainsSecret(element) {
			return ConfigurationValue{}, fmt.Errorf("Secret configuration must be a direct named field, not %s", identity)
		}
		return ConfigurationValue{kind: ConfigurationValuePointer, typeIdentity: identity, element: &element}, nil
	case *types.Slice:
		element, err := compileConfigurationValue(typed.Elem(), state, depth+1)
		if err != nil {
			return ConfigurationValue{}, err
		}
		if configurationContainsSecret(element) {
			return ConfigurationValue{}, fmt.Errorf("Secret configuration must be a direct named field, not %s", identity)
		}
		return ConfigurationValue{kind: ConfigurationValueList, typeIdentity: identity, element: &element}, nil
	case *types.Array:
		element, err := compileConfigurationValue(typed.Elem(), state, depth+1)
		if err != nil {
			return ConfigurationValue{}, err
		}
		if configurationContainsSecret(element) {
			return ConfigurationValue{}, fmt.Errorf("Secret configuration must be a direct named field, not %s", identity)
		}
		return ConfigurationValue{kind: ConfigurationValueList, typeIdentity: identity, element: &element, arrayLength: typed.Len(), isArray: true}, nil
	case *types.Map:
		key := types.Unalias(typed.Key())
		basic, ok := key.Underlying().(*types.Basic)
		if !ok || basic.Kind() != types.String {
			return ConfigurationValue{}, fmt.Errorf("configuration map %s must use string keys", identity)
		}
		element, err := compileConfigurationValue(typed.Elem(), state, depth+1)
		if err != nil {
			return ConfigurationValue{}, err
		}
		if configurationContainsSecret(element) {
			return ConfigurationValue{}, fmt.Errorf("Secret configuration must be a direct named field, not %s", identity)
		}
		return ConfigurationValue{kind: ConfigurationValueMap, typeIdentity: identity, element: &element}, nil
	case *types.Struct:
		if _, cycle := state.active[typed]; cycle {
			return ConfigurationValue{}, fmt.Errorf("configuration Go type %s is recursive", identity)
		}
		state.active[typed] = struct{}{}
		fields, err := compileConfigurationFields(typed, state, depth)
		delete(state.active, typed)
		if err != nil {
			return ConfigurationValue{}, err
		}
		return ConfigurationValue{kind: ConfigurationValueObject, typeIdentity: identity, fields: fields}, nil
	default:
		return ConfigurationValue{}, fmt.Errorf("configuration Go type %s is not supported", identity)
	}
}

func exactConfigurationNamedType(value *types.Named, packagePath, typeName string) bool {
	if value == nil || value.Obj() == nil || value.Obj().Pkg() == nil {
		return false
	}
	return value.Obj().Pkg().Path() == packagePath && value.Obj().Name() == typeName
}

func configurationBasicKind(kind types.BasicKind) (ConfigurationValueKind, bool) {
	switch kind {
	case types.String:
		return ConfigurationValueString, true
	case types.Bool:
		return ConfigurationValueBoolean, true
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		return ConfigurationValueSignedInteger, true
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return ConfigurationValueUnsignedInteger, true
	case types.Float32, types.Float64:
		return ConfigurationValueNumber, true
	default:
		return "", false
	}
}

func configurationContainsSecret(value ConfigurationValue) bool {
	if value.kind == ConfigurationValueSecret {
		return true
	}
	if value.element != nil && configurationContainsSecret(*value.element) {
		return true
	}
	for _, field := range value.fields {
		if configurationContainsSecret(field.value) {
			return true
		}
	}
	return false
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
	value = types.Unalias(value)
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
