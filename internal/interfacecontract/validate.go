// Package interfacecontract validates the type-checked Go shape of an Interface.
package interfacecontract

import (
	"errors"
	"fmt"
	"go/types"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfaceid"
)

// ErrInvalid reports a Go declaration that cannot be a canonical Interface contract.
var ErrInvalid = errors.New("invalid Interface contract")

// Contract is the normalized identity of one type-checked Interface operation.
type Contract struct {
	id             interfaceid.Identifier
	packagePath    string
	methodName     string
	requestName    string
	responseName   string
	requestFields  []Field
	responseFields []Field
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

// RequestFields returns the request fields in stable field-number order.
func (c Contract) RequestFields() []Field { return append([]Field(nil), c.requestFields...) }

// ResponseFields returns the response fields in stable field-number order.
func (c Contract) ResponseFields() []Field { return append([]Field(nil), c.responseFields...) }

// Field is one normalized canonical request or response field.
type Field struct {
	name            string
	number          uint64
	required        bool
	jsonName        string
	hasExplicitJSON bool
}

// Name returns the exported Go field name.
func (f Field) Name() string { return f.name }

// Number returns the stable positive field number.
func (f Field) Number() uint64 { return f.number }

// Required reports whether the canonical field is required.
func (f Field) Required() bool { return f.required }

// JSONName returns the explicitly declared JSON name, or an empty string.
func (f Field) JSONName() string { return f.jsonName }

// HasExplicitJSONName reports whether the Go field declares a nonempty JSON name.
func (f Field) HasExplicitJSONName() bool { return f.hasExplicitJSON }

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
	request, err := messageType(checkedPackage, signature.Params().At(1).Type(), "request")
	if err != nil {
		return Contract{}, invalid(declaration, err.Error())
	}
	requestFields, err := normalizeFields(request.structure, "request")
	if err != nil {
		return Contract{}, invalid(declaration, err.Error())
	}

	if signature.Results().Len() != 2 {
		return Contract{}, invalid(declaration, fmt.Sprintf("Interface operation must return one response and error, found %d results", signature.Results().Len()))
	}
	response, err := messageType(checkedPackage, signature.Results().At(0).Type(), "response")
	if err != nil {
		return Contract{}, invalid(declaration, err.Error())
	}
	responseFields, err := normalizeFields(response.structure, "response")
	if err != nil {
		return Contract{}, invalid(declaration, err.Error())
	}
	if !types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) {
		return Contract{}, invalid(declaration, "Interface operation second result must be error")
	}

	return Contract{
		id:             declaration.ID(),
		packagePath:    checkedPackage.Path(),
		methodName:     method.Name(),
		requestName:    request.name,
		responseName:   response.name,
		requestFields:  requestFields,
		responseFields: responseFields,
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

type canonicalMessage struct {
	name      string
	structure *types.Struct
}

func messageType(checkedPackage *types.Package, value types.Type, role string) (canonicalMessage, error) {
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil {
		return canonicalMessage{}, fmt.Errorf("Interface %s must be a defined exported same-package struct", role)
	}
	if named.Obj().Pkg() != checkedPackage || !named.Obj().Exported() {
		return canonicalMessage{}, fmt.Errorf("Interface %s must be a defined exported same-package struct", role)
	}
	if named.TypeParams() != nil && named.TypeParams().Len() != 0 {
		return canonicalMessage{}, fmt.Errorf("Interface %s must not be generic", role)
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return canonicalMessage{}, fmt.Errorf("Interface %s must be a defined exported same-package struct", role)
	}
	return canonicalMessage{name: named.Obj().Name(), structure: structure}, nil
}

func normalizeFields(structure *types.Struct, role string) ([]Field, error) {
	fields := make([]Field, 0, structure.NumFields())
	numbers := make(map[uint64]string, structure.NumFields())
	jsonNames := make(map[string]string, structure.NumFields())
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() || field.Embedded() {
			return nil, fmt.Errorf("Interface %s field %s must be an exported named field", role, field.Name())
		}
		tags, err := parseStructTags(structure.Tag(index))
		if err != nil {
			return nil, fmt.Errorf("Interface %s field %s: %w", role, field.Name(), err)
		}
		number, required, err := parsePlystraTag(tags)
		if err != nil {
			return nil, fmt.Errorf("Interface %s field %s: %w", role, field.Name(), err)
		}
		if previous, exists := numbers[number]; exists {
			return nil, fmt.Errorf("Interface %s fields %s and %s use duplicate field number %d", role, previous, field.Name(), number)
		}
		numbers[number] = field.Name()

		jsonName, hasExplicitJSON, err := parseJSONName(tags)
		if err != nil {
			return nil, fmt.Errorf("Interface %s field %s: %w", role, field.Name(), err)
		}
		effectiveJSONName := field.Name()
		if hasExplicitJSON {
			effectiveJSONName = jsonName
		}
		if previous, exists := jsonNames[effectiveJSONName]; exists {
			return nil, fmt.Errorf("Interface %s fields %s and %s use duplicate JSON name %q", role, previous, field.Name(), effectiveJSONName)
		}
		jsonNames[effectiveJSONName] = field.Name()

		fields = append(fields, Field{
			name:            field.Name(),
			number:          number,
			required:        required,
			jsonName:        jsonName,
			hasExplicitJSON: hasExplicitJSON,
		})
	}
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].number != fields[right].number {
			return fields[left].number < fields[right].number
		}
		return fields[left].name < fields[right].name
	})
	return fields, nil
}

func parseStructTags(tag string) (map[string]string, error) {
	values := make(map[string]string)
	entries := 0
	for tag != "" {
		if entries > 0 && tag[0] != ' ' {
			return nil, errors.New("invalid Go struct tag syntax: entries must be separated by spaces")
		}
		index := 0
		for index < len(tag) && tag[index] == ' ' {
			index++
		}
		tag = tag[index:]
		if tag == "" {
			break
		}

		index = 0
		for index < len(tag) && tag[index] > ' ' && tag[index] != ':' && tag[index] != '"' && tag[index] != 0x7f {
			index++
		}
		if index == 0 || index+1 >= len(tag) || tag[index] != ':' || tag[index+1] != '"' {
			return nil, errors.New("invalid Go struct tag syntax")
		}
		key := tag[:index]
		tag = tag[index+1:]

		index = 1
		for index < len(tag) && tag[index] != '"' {
			if tag[index] == '\\' {
				index++
			}
			index++
		}
		if index >= len(tag) {
			return nil, errors.New("invalid Go struct tag syntax")
		}
		quotedValue := tag[:index+1]
		tag = tag[index+1:]
		value, err := strconv.Unquote(quotedValue)
		if err != nil {
			return nil, errors.New("invalid Go struct tag syntax")
		}
		if _, exists := values[key]; exists {
			if key == "plystra" || key == "json" {
				return nil, fmt.Errorf("duplicate Go struct tag key %q", key)
			}
		} else {
			values[key] = value
		}
		entries++
	}
	return values, nil
}

func parsePlystraTag(tags map[string]string) (uint64, bool, error) {
	value, exists := tags["plystra"]
	if !exists {
		return 0, false, errors.New("missing plystra field-number tag")
	}
	parts := strings.Split(value, ",")
	if len(parts) == 0 || parts[0] == "" || parts[0][0] == '0' {
		return 0, false, errors.New("field number must be a canonical positive decimal integer")
	}
	number, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || number == 0 || strconv.FormatUint(number, 10) != parts[0] {
		return 0, false, errors.New("field number must be a canonical positive decimal integer")
	}
	required := false
	for _, option := range parts[1:] {
		if option != "required" {
			return 0, false, fmt.Errorf("unknown plystra field option %q", option)
		}
		if required {
			return 0, false, errors.New("duplicate required field option")
		}
		required = true
	}
	return number, required, nil
}

func parseJSONName(tags map[string]string) (string, bool, error) {
	value, exists := tags["json"]
	if !exists {
		return "", false, nil
	}
	name, _, _ := strings.Cut(value, ",")
	if name == "" {
		return "", false, nil
	}
	if name == "-" {
		return "", false, errors.New("canonical Interface fields cannot be omitted from JSON")
	}
	if !validJSONName(name) {
		return "", false, fmt.Errorf("invalid explicit JSON name %q", name)
	}
	return name, true, nil
}

func validJSONName(name string) bool {
	const permittedPunctuation = "!#$%&()*+-./:;<=>?@[]^_{|}~ "
	for _, character := range name {
		if strings.ContainsRune(permittedPunctuation, character) {
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func invalid(declaration interfacedecl.Declaration, message string) error {
	position := declaration.Position()
	return fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalid, position.Path, position.Line, position.Column, message)
}
