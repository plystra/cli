package interfacecontract

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

// TypeKind is one exact transport-stable Go field shape in the initial
// Interface contract graph.
type TypeKind string

const (
	TypeBoolean   TypeKind = "boolean"
	TypeString    TypeKind = "string"
	TypeInt32     TypeKind = "int32"
	TypeInt64     TypeKind = "int64"
	TypeUint32    TypeKind = "uint32"
	TypeUint64    TypeKind = "uint64"
	TypeFloat32   TypeKind = "float32"
	TypeFloat64   TypeKind = "float64"
	TypeBytes     TypeKind = "bytes"
	TypeRepeated  TypeKind = "repeated"
	TypeMap       TypeKind = "map"
	TypeMessage   TypeKind = "message"
	TypeTimestamp TypeKind = "timestamp"
	TypeDuration  TypeKind = "duration"
)

// Type is one immutable normalized field type. Repeated values expose an
// element, maps expose a key and value, and message references expose one
// same-package message name.
type Type struct {
	kind        TypeKind
	element     *Type
	key         *Type
	value       *Type
	messageName string
}

// Kind returns the exact normalized field-type kind.
func (t Type) Kind() TypeKind { return t.kind }

// Element returns the repeated element type.
func (t Type) Element() (Type, bool) {
	if t.kind != TypeRepeated || t.element == nil {
		return Type{}, false
	}
	return *t.element, true
}

// Key returns the map key type.
func (t Type) Key() (Type, bool) {
	if t.kind != TypeMap || t.key == nil {
		return Type{}, false
	}
	return *t.key, true
}

// Value returns the map value type.
func (t Type) Value() (Type, bool) {
	if t.kind != TypeMap || t.value == nil {
		return Type{}, false
	}
	return *t.value, true
}

// MessageName returns the exported same-package message name referenced by a
// message field type.
func (t Type) MessageName() (string, bool) {
	if t.kind != TypeMessage || t.messageName == "" {
		return "", false
	}
	return t.messageName, true
}

// Canonical returns the deterministic normalized type identity used by later
// digest and projection stages.
func (t Type) Canonical() string {
	switch t.kind {
	case TypeRepeated:
		if element, exists := t.Element(); exists {
			return "repeated<" + element.Canonical() + ">"
		}
	case TypeMap:
		key, keyExists := t.Key()
		value, valueExists := t.Value()
		if keyExists && valueExists {
			return "map<" + key.Canonical() + "," + value.Canonical() + ">"
		}
	case TypeMessage:
		if name, exists := t.MessageName(); exists {
			return "message:" + name
		}
	default:
		return string(t.kind)
	}
	return ""
}

// Message is one immutable exported same-package struct in the canonical
// request or response graph.
type Message struct {
	name   string
	fields []Field
}

// Name returns the exported Go message type name.
func (m Message) Name() string { return m.name }

// Fields returns a defensive field-number-ordered view.
func (m Message) Fields() []Field { return append([]Field(nil), m.fields...) }

type messageState struct {
	message  Message
	complete bool
}

type messageFrame struct {
	named           *types.Named
	incomingPointer bool
}

type typeGraph struct {
	checkedPackage *types.Package
	messages       map[*types.Named]*messageState
	active         []messageFrame
	activeIndex    map[*types.Named]int
}

func newTypeGraph(checkedPackage *types.Package) *typeGraph {
	return &typeGraph{
		checkedPackage: checkedPackage,
		messages:       make(map[*types.Named]*messageState),
		activeIndex:    make(map[*types.Named]int),
	}
}

func (g *typeGraph) normalizeMessage(named *types.Named, role string) ([]Field, error) {
	return g.normalizeMessageEdge(named, role, false)
}

func (g *typeGraph) normalizeMessageEdge(named *types.Named, role string, incomingPointer bool) ([]Field, error) {
	if state, exists := g.messages[named]; exists {
		return append([]Field(nil), state.message.fields...), nil
	}
	object := named.Obj()
	if object == nil || object.Pkg() != g.checkedPackage || !object.Exported() {
		return nil, fmt.Errorf("Interface %s must be an exported same-package message struct", role)
	}
	if named.TypeParams() != nil && named.TypeParams().Len() != 0 {
		return nil, fmt.Errorf("Interface %s message %s must not be generic", role, object.Name())
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("Interface %s must be an exported same-package message struct", role)
	}

	state := &messageState{
		message: Message{name: object.Name()},
	}
	g.messages[named] = state
	g.activeIndex[named] = len(g.active)
	g.active = append(g.active, messageFrame{named: named, incomingPointer: incomingPointer})
	defer func() {
		delete(g.activeIndex, named)
		g.active = g.active[:len(g.active)-1]
	}()
	fields, err := g.normalizeFields(structure, role)
	if err != nil {
		delete(g.messages, named)
		return nil, err
	}
	state.message.fields = fields
	state.complete = true
	return append([]Field(nil), fields...), nil
}

func (g *typeGraph) normalizeFields(structure *types.Struct, role string) ([]Field, error) {
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

		fieldType, pointerDepth, err := g.normalizeFieldType(field.Type(), role+" field "+field.Name())
		if err != nil {
			return nil, err
		}
		fields = append(fields, Field{
			name:            field.Name(),
			number:          number,
			required:        required,
			jsonName:        jsonName,
			hasExplicitJSON: hasExplicitJSON,
			fieldType:       fieldType,
			pointerDepth:    pointerDepth,
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

func (g *typeGraph) normalizeFieldType(value types.Type, fieldPath string) (Type, uint8, error) {
	value = types.Unalias(value)
	original := value
	pointerDepth := 0
	for {
		pointer, ok := value.(*types.Pointer)
		if !ok {
			break
		}
		pointerDepth++
		value = types.Unalias(pointer.Elem())
	}
	if pointerDepth > 2 {
		//lint:ignore ST1005 Interface is the canonical product term in this diagnostic.
		return Type{}, 0, fmt.Errorf("Interface %s uses unsupported pointer depth %d in %s; at most two direct pointer layers are allowed", fieldPath, pointerDepth, stableTypeString(original))
	}
	normalized, err := g.normalizeType(value, fieldPath, pointerDepth != 0)
	if err != nil {
		return Type{}, 0, err
	}
	return normalized, uint8(pointerDepth), nil
}

func (g *typeGraph) normalizeType(value types.Type, fieldPath string, pointerMessageEdge bool) (Type, error) {
	value = types.Unalias(value)
	switch current := value.(type) {
	case *types.Basic:
		kind, supported := basicTypeKind(current.Kind())
		if !supported {
			return Type{}, fmt.Errorf("Interface %s uses unsupported Go scalar type %s", fieldPath, current.Name())
		}
		return Type{kind: kind}, nil
	case *types.Slice:
		element := types.Unalias(current.Elem())
		if basic, ok := element.(*types.Basic); ok && basic.Kind() == types.Uint8 {
			return Type{kind: TypeBytes}, nil
		}
		normalized, err := g.normalizeType(element, fieldPath+" repeated element", false)
		if err != nil {
			return Type{}, err
		}
		if normalized.kind == TypeRepeated || normalized.kind == TypeMap {
			return Type{}, fmt.Errorf("Interface %s uses unsupported nested collection type %s", fieldPath, stableTypeString(value))
		}
		return Type{kind: TypeRepeated, element: typePointer(normalized)}, nil
	case *types.Map:
		key, err := g.normalizeType(current.Key(), fieldPath+" map key", false)
		if err != nil {
			return Type{}, err
		}
		if !validMapKey(key.kind) {
			return Type{}, fmt.Errorf("Interface %s uses unsupported map key type %s", fieldPath, stableTypeString(current.Key()))
		}
		value, err := g.normalizeType(current.Elem(), fieldPath+" map value", false)
		if err != nil {
			return Type{}, err
		}
		if value.kind == TypeRepeated || value.kind == TypeMap {
			return Type{}, fmt.Errorf("Interface %s uses unsupported collection-valued map type %s", fieldPath, stableTypeString(current))
		}
		return Type{kind: TypeMap, key: typePointer(key), value: typePointer(value)}, nil
	case *types.Named:
		if kind, supported := wellKnownTypeKind(current); supported {
			return Type{kind: kind}, nil
		}
		object := current.Obj()
		if object == nil {
			return Type{}, fmt.Errorf("Interface %s uses unsupported unnamed Go type %s", fieldPath, stableTypeString(value))
		}
		if object.Pkg() != g.checkedPackage {
			return Type{}, fmt.Errorf("Interface %s uses unsupported external defined type %s", fieldPath, stableTypeString(value))
		}
		if !object.Exported() {
			return Type{}, fmt.Errorf("Interface %s uses unexported message type %s", fieldPath, object.Name())
		}
		if current.TypeParams() != nil && current.TypeParams().Len() != 0 {
			return Type{}, fmt.Errorf("Interface %s uses generic message type %s", fieldPath, object.Name())
		}
		if _, ok := current.Underlying().(*types.Struct); !ok {
			return Type{}, fmt.Errorf("Interface %s uses unsupported defined non-message type %s", fieldPath, object.Name())
		}
		if err := g.normalizeMessageReference(current, "message "+object.Name(), fieldPath, pointerMessageEdge); err != nil {
			return Type{}, err
		}
		return Type{kind: TypeMessage, messageName: object.Name()}, nil
	case *types.Pointer:
		return Type{}, fmt.Errorf("Interface %s uses unsupported pointer placement in %s; pointers are allowed only directly at a message-field boundary", fieldPath, stableTypeString(value))
	case *types.Array:
		return Type{}, fmt.Errorf("Interface %s uses unsupported fixed array type %s", fieldPath, stableTypeString(value))
	case *types.Struct:
		return Type{}, fmt.Errorf("Interface %s uses unsupported anonymous struct type", fieldPath)
	case *types.Interface:
		return Type{}, fmt.Errorf("Interface %s uses unsupported interface-valued field type", fieldPath)
	case *types.Signature:
		return Type{}, fmt.Errorf("Interface %s uses unsupported function field type", fieldPath)
	case *types.Chan:
		return Type{}, fmt.Errorf("Interface %s uses unsupported channel field type", fieldPath)
	default:
		return Type{}, fmt.Errorf("Interface %s uses unsupported Go field type %s", fieldPath, stableTypeString(value))
	}
}

func (g *typeGraph) normalizeMessageReference(named *types.Named, role, fieldPath string, pointerEdge bool) error {
	if index, active := g.activeIndex[named]; active {
		pointerCycle := pointerEdge
		for frame := index + 1; frame < len(g.active) && !pointerCycle; frame++ {
			pointerCycle = g.active[frame].incomingPointer
		}
		if pointerCycle {
			path := make([]string, 0, len(g.active)-index+1)
			for frame := index; frame < len(g.active); frame++ {
				path = append(path, g.active[frame].named.Obj().Name())
			}
			path = append(path, named.Obj().Name())
			//lint:ignore ST1005 Interface is the canonical product term in this diagnostic.
			return fmt.Errorf("Interface %s creates unsupported pointer-to-message cycle %s", fieldPath, strings.Join(path, " -> "))
		}
		return nil
	}
	_, err := g.normalizeMessageEdge(named, role, pointerEdge)
	return err
}

func (g *typeGraph) normalizedMessages() []Message {
	messages := make([]Message, 0, len(g.messages))
	for _, state := range g.messages {
		if state.complete {
			messages = append(messages, Message{
				name:   state.message.name,
				fields: append([]Field(nil), state.message.fields...),
			})
		}
	}
	sort.Slice(messages, func(left, right int) bool { return messages[left].name < messages[right].name })
	return messages
}

func validatePointerMessageCycles(messages []Message) error {
	byName := make(map[string]Message, len(messages))
	for _, message := range messages {
		byName[message.name] = message
	}
	for _, message := range messages {
		for _, field := range message.fields {
			if field.pointerDepth == 0 || field.fieldType.kind != TypeMessage {
				continue
			}
			target := field.fieldType.messageName
			path, found := messageReferencePath(target, message.name, byName, make(map[string]bool, len(messages)))
			if !found {
				continue
			}
			cycle := append([]string{message.name}, path...)
			//lint:ignore ST1005 Interface is the canonical product term in this diagnostic.
			return fmt.Errorf(
				"Interface message %s field %s creates unsupported pointer-to-message cycle %s",
				message.name,
				field.name,
				strings.Join(cycle, " -> "),
			)
		}
	}
	return nil
}

func messageReferencePath(current, target string, messages map[string]Message, visiting map[string]bool) ([]string, bool) {
	if current == target {
		return []string{current}, true
	}
	if visiting[current] {
		return nil, false
	}
	visiting[current] = true
	defer delete(visiting, current)
	for _, field := range messages[current].fields {
		referenced, exists := referencedMessageName(field.fieldType)
		if !exists {
			continue
		}
		path, found := messageReferencePath(referenced, target, messages, visiting)
		if found {
			return append([]string{current}, path...), true
		}
	}
	return nil, false
}

func referencedMessageName(fieldType Type) (string, bool) {
	switch fieldType.kind {
	case TypeMessage:
		return fieldType.messageName, fieldType.messageName != ""
	case TypeRepeated:
		if fieldType.element != nil && fieldType.element.kind == TypeMessage {
			return fieldType.element.messageName, fieldType.element.messageName != ""
		}
	case TypeMap:
		if fieldType.value != nil && fieldType.value.kind == TypeMessage {
			return fieldType.value.messageName, fieldType.value.messageName != ""
		}
	}
	return "", false
}

func basicTypeKind(kind types.BasicKind) (TypeKind, bool) {
	switch kind {
	case types.Bool:
		return TypeBoolean, true
	case types.String:
		return TypeString, true
	case types.Int32:
		return TypeInt32, true
	case types.Int64:
		return TypeInt64, true
	case types.Uint32:
		return TypeUint32, true
	case types.Uint64:
		return TypeUint64, true
	case types.Float32:
		return TypeFloat32, true
	case types.Float64:
		return TypeFloat64, true
	default:
		return "", false
	}
}

func wellKnownTypeKind(named *types.Named) (TypeKind, bool) {
	object := named.Obj()
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "time" {
		return "", false
	}
	switch object.Name() {
	case "Time":
		return TypeTimestamp, true
	case "Duration":
		return TypeDuration, true
	default:
		return "", false
	}
}

func validMapKey(kind TypeKind) bool {
	switch kind {
	case TypeBoolean, TypeString, TypeInt32, TypeInt64, TypeUint32, TypeUint64:
		return true
	default:
		return false
	}
}

func stableTypeString(value types.Type) string {
	return types.TypeString(value, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
}

func typePointer(value Type) *Type {
	copy := value
	return &copy
}
