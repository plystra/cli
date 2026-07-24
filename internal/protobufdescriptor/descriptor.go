// Package protobufdescriptor renders deterministic CLI-owned Protobuf schemas
// and binary descriptor evidence from the normalized canonical projection and
// committed wire map.
package protobufdescriptor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/goname"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/sdkmodel"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	// DescriptorSetPath is the committed binary FileDescriptorSet evidence.
	DescriptorSetPath = "generated/proto/descriptor-set.pb"
	// ErrorDetailFileName is the shared generated safe-error schema included
	// whenever the selected application exposes at least one Connect operation.
	ErrorDetailFileName = "plystra/generated/transport/v1/error.proto"
	// ErrorDetailFullName is the exact closed detail type shared by generated
	// Connect handlers and JavaScript wrappers.
	ErrorDetailFullName = "plystra.generated.transport.v1.PlystraErrorDetail"
	// ProjectionSchema identifies the initial deterministic descriptor model.
	ProjectionSchema = "plystra.protobuf-descriptor/v1"
	generatedRoot    = "generated/proto/"
	maximumSetBytes  = 64 << 20
)

var (
	// ErrBuild reports invalid or inconsistent descriptor input.
	ErrBuild = errors.New("build Protobuf descriptor evidence")
	// ErrProjection reports disagreement between the canonical projection and
	// committed active wire assignments.
	ErrProjection = errors.New("invalid Protobuf descriptor projection")
	// ErrDescriptor reports an internally invalid generated descriptor set.
	ErrDescriptor = errors.New("invalid generated Protobuf descriptor set")
)

// File is one immutable generated schema or descriptor-evidence file.
type File struct {
	path string
	data []byte
}

// Path returns the slash-separated application-relative generated path.
func (f File) Path() string { return f.path }

// Data returns a defensive copy of the generated contents.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Evidence is one immutable deterministic schema and descriptor projection.
type Evidence struct {
	files       []File
	digest      string
	prepared    bool
	descriptors int
}

// Valid reports whether Build produced the evidence.
func (e Evidence) Valid() bool {
	return e.prepared && e.files != nil && validDigest(e.digest) && e.descriptors >= 0
}

// Files returns path-sorted defensive generated files. The binary descriptor
// set is present even when the selected Connect surface is empty.
func (e Evidence) Files() []File {
	result := make([]File, len(e.files))
	for index, file := range e.files {
		result[index] = File{path: file.path, data: append([]byte(nil), file.data...)}
	}
	return result
}

// DescriptorSet returns a defensive copy of the deterministic binary
// FileDescriptorSet owned by this evidence.
func (e Evidence) DescriptorSet() []byte {
	for _, file := range e.files {
		if file.path == DescriptorSetPath {
			return append([]byte(nil), file.data...)
		}
	}
	return nil
}

// Digest returns the SHA-256 digest of the deterministic binary descriptor set.
func (e Evidence) Digest() string { return e.digest }

// DescriptorCount returns the number of generated .proto files.
func (e Evidence) DescriptorCount() int { return e.descriptors }

// Build validates exact canonical-to-wire agreement, renders one canonical
// schema per exposed Capability plus one service-only schema per Alias, and
// serializes one deterministic self-contained FileDescriptorSet.
func Build(model protobufmodel.Model, wireMap protobufwiremap.Map) (Evidence, error) {
	if !model.Valid() {
		return Evidence{}, fmt.Errorf("%w: %w: normalized Protobuf model is absent", ErrBuild, ErrProjection)
	}
	if !wireMap.Valid() || wireMap.ProjectionDigest() != model.Digest() {
		return Evidence{}, fmt.Errorf("%w: %w: wire map is absent or does not match the normalized projection", ErrBuild, ErrProjection)
	}

	wireCapabilities := wireMap.ActiveCapabilities()
	if len(wireCapabilities) != len(model.Operations()) {
		return Evidence{}, fmt.Errorf("%w: %w: active wire map has %d Capabilities for %d canonical operations", ErrBuild, ErrProjection, len(wireCapabilities), len(model.Operations()))
	}
	wireByID := make(map[string]protobufwiremap.CapabilityProjection, len(wireCapabilities))
	for _, capability := range wireCapabilities {
		wireByID[capability.ID()] = capability
	}

	descriptors := make([]*descriptorpb.FileDescriptorProto, 0, len(model.Operations())+len(model.Aliases())+1)
	canonicalFiles := make(map[string]string, len(model.Operations()))
	usesStruct := false
	for _, operation := range model.Operations() {
		wire, exists := wireByID[operation.ID().String()]
		if !exists {
			return Evidence{}, fmt.Errorf("%w: %w: active wire map is missing Capability %s", ErrBuild, ErrProjection, operation.ID())
		}
		file, operationUsesStruct, err := canonicalDescriptor(operation, wire)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: Capability %s: %v", ErrBuild, operation.ID(), err)
		}
		descriptors = append(descriptors, file)
		canonicalFiles[operation.ID().String()] = file.GetName()
		usesStruct = usesStruct || operationUsesStruct
	}
	for _, alias := range model.Aliases() {
		canonicalFile, exists := canonicalFiles[alias.Target().String()]
		if !exists {
			return Evidence{}, fmt.Errorf("%w: %w: Alias %s target %s has no canonical descriptor", ErrBuild, ErrProjection, alias.ID(), alias.Target())
		}
		file, err := aliasDescriptor(alias, canonicalFile)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: Alias %s: %v", ErrBuild, alias.ID(), err)
		}
		descriptors = append(descriptors, file)
	}
	if len(model.Operations()) != 0 {
		descriptors = append(descriptors, errorDetailDescriptor())
	}
	sort.Slice(descriptors, func(left, right int) bool { return descriptors[left].GetName() < descriptors[right].GetName() })

	setFiles := append([]*descriptorpb.FileDescriptorProto(nil), descriptors...)
	if usesStruct {
		setFiles = append(setFiles, protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto))
		sort.Slice(setFiles, func(left, right int) bool { return setFiles[left].GetName() < setFiles[right].GetName() })
	}
	set := &descriptorpb.FileDescriptorSet{File: setFiles}
	if _, err := protodesc.NewFiles(set); err != nil {
		return Evidence{}, fmt.Errorf("%w: %w: validate descriptor graph: %v", ErrBuild, ErrDescriptor, err)
	}
	binary, err := (proto.MarshalOptions{Deterministic: true}).Marshal(set)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: %w: serialize descriptor set: %v", ErrBuild, ErrDescriptor, err)
	}
	if len(binary) > maximumSetBytes {
		return Evidence{}, fmt.Errorf("%w: %w: descriptor set exceeds %d bytes", ErrBuild, ErrDescriptor, maximumSetBytes)
	}

	files := make([]File, 0, len(descriptors)+1)
	for _, descriptor := range descriptors {
		source, err := renderSource(descriptor)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: %w: render %s: %v", ErrBuild, ErrDescriptor, descriptor.GetName(), err)
		}
		files = append(files, File{path: generatedRoot + descriptor.GetName(), data: source})
	}
	files = append(files, File{path: DescriptorSetPath, data: binary})
	sort.Slice(files, func(left, right int) bool { return files[left].path < files[right].path })
	return Evidence{files: files, digest: digest(binary), prepared: true, descriptors: len(descriptors)}, nil
}

func errorDetailDescriptor() *descriptorpb.FileDescriptorProto {
	fields := []struct {
		name     string
		jsonName string
		number   int32
	}{
		{name: "requested_capability_id", jsonName: "requestedCapabilityId", number: 1},
		{name: "canonical_capability_id", jsonName: "canonicalCapabilityId", number: 2},
		{name: "semantic_error_code", jsonName: "semanticErrorCode", number: 3},
		{name: "kernel_error_class", jsonName: "kernelErrorClass", number: 4},
		{name: "trace_id", jsonName: "traceId", number: 5},
	}
	message := &descriptorpb.DescriptorProto{Name: proto.String("PlystraErrorDetail")}
	for _, field := range fields {
		message.Field = append(message.Field, &descriptorpb.FieldDescriptorProto{
			Name:     proto.String(field.name),
			JsonName: proto.String(field.jsonName),
			Number:   proto.Int32(field.number),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		})
	}
	return &descriptorpb.FileDescriptorProto{
		Name:        proto.String(ErrorDetailFileName),
		Package:     proto.String("plystra.generated.transport.v1"),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{message},
	}
}

func canonicalDescriptor(operation protobufmodel.Operation, wire protobufwiremap.CapabilityProjection) (*descriptorpb.FileDescriptorProto, bool, error) {
	identity := operation.Identity()
	if wire.ID() != operation.ID().String() || wire.ContractDigest() != operation.ContractDigest() {
		return nil, false, fmt.Errorf("%w: canonical contract digest does not match active wire history", ErrProjection)
	}
	request, requestAuxiliary, requestEnums, requestUsesStruct, err := messageDescriptor(identity.Package(), identity.RequestType(), operation.Request(), wire.Request())
	if err != nil {
		return nil, false, fmt.Errorf("%w: request: %v", ErrProjection, err)
	}
	response, responseAuxiliary, responseEnums, responseUsesStruct, err := messageDescriptor(identity.Package(), identity.ResponseType(), operation.Response(), wire.Response())
	if err != nil {
		return nil, false, fmt.Errorf("%w: response: %v", ErrProjection, err)
	}
	messages := []*descriptorpb.DescriptorProto{request, response}
	messages = append(messages, requestAuxiliary...)
	messages = append(messages, responseAuxiliary...)
	sort.Slice(messages, func(left, right int) bool { return messages[left].GetName() < messages[right].GetName() })
	enums := append(requestEnums, responseEnums...)
	sort.Slice(enums, func(left, right int) bool { return enums[left].GetName() < enums[right].GetName() })
	file := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(descriptorName(identity.Package())),
		Package:     proto.String(identity.Package()),
		Syntax:      proto.String("proto3"),
		MessageType: messages,
		EnumType:    enums,
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String(identity.Service()),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String(identity.Method()),
				InputType:  proto.String(qualified(identity.RequestType())),
				OutputType: proto.String(qualified(identity.ResponseType())),
			}},
		}},
	}
	if requestUsesStruct || responseUsesStruct {
		file.Dependency = []string{"google/protobuf/struct.proto"}
	}
	return file, requestUsesStruct || responseUsesStruct, nil
}

func aliasDescriptor(alias protobufmodel.Alias, canonicalFile string) (*descriptorpb.FileDescriptorProto, error) {
	identity := alias.Identity()
	if identity.CanonicalID() != alias.Target().String() || alias.TargetContractDigest() == "" {
		return nil, fmt.Errorf("%w: canonical target identity is inconsistent", ErrProjection)
	}
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String(descriptorName(identity.Package())),
		Package:    proto.String(identity.Package()),
		Syntax:     proto.String("proto3"),
		Dependency: []string{canonicalFile},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String(identity.Service()),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String(identity.Method()),
				InputType:  proto.String(qualified(identity.RequestType())),
				OutputType: proto.String(qualified(identity.ResponseType())),
			}},
		}},
	}, nil
}

func messageDescriptor(packageName, qualifiedName string, fields []sdkmodel.Field, wire protobufwiremap.MessageProjection) (*descriptorpb.DescriptorProto, []*descriptorpb.DescriptorProto, []*descriptorpb.EnumDescriptorProto, bool, error) {
	messageName, err := unqualified(packageName, qualifiedName)
	if err != nil || wire.Name() != messageName {
		return nil, nil, nil, false, fmt.Errorf("message identity %q does not match wire identity %q", qualifiedName, wire.Name())
	}
	wireFields := make(map[string]protobufwiremap.FieldProjection, len(wire.Fields()))
	for _, field := range wire.Fields() {
		wireFields[field.CanonicalName()] = field
	}
	wireEnums := make(map[string]protobufwiremap.EnumProjection, len(wire.Enums()))
	for _, enum := range wire.Enums() {
		wireEnums[enum.CanonicalField()] = enum
	}
	if len(wireFields) != len(fields) {
		return nil, nil, nil, false, fmt.Errorf("wire map has %d fields for %d canonical fields", len(wireFields), len(fields))
	}

	message := &descriptorpb.DescriptorProto{Name: proto.String(messageName)}
	for _, number := range wire.ReservedNumbers() {
		start := int32(number)
		end := start + 1
		message.ReservedRange = append(message.ReservedRange, &descriptorpb.DescriptorProto_ReservedRange{Start: &start, End: &end})
	}
	message.ReservedName = wire.ReservedNames()
	auxiliary := make([]*descriptorpb.DescriptorProto, 0)
	enums := make([]*descriptorpb.EnumDescriptorProto, 0)
	usesStruct := false
	for _, canonicalField := range fields {
		assignment, exists := wireFields[canonicalField.Name()]
		if !exists {
			return nil, nil, nil, false, fmt.Errorf("wire map is missing canonical field %q", canonicalField.Name())
		}
		field, helpers, enum, fieldUsesStruct, err := fieldDescriptor(packageName, qualifiedName, canonicalField, assignment, wireEnums[canonicalField.Name()])
		if err != nil {
			return nil, nil, nil, false, err
		}
		message.Field = append(message.Field, field)
		auxiliary = append(auxiliary, helpers...)
		if enum != nil {
			enums = append(enums, enum)
		}
		usesStruct = usesStruct || fieldUsesStruct
		if field.GetProto3Optional() {
			oneofIndex := int32(len(message.OneofDecl))
			field.OneofIndex = &oneofIndex
			message.OneofDecl = append(message.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: proto.String("_" + assignment.Name())})
		}
	}
	if len(wireEnums) != countEnumFields(fields) {
		return nil, nil, nil, false, fmt.Errorf("wire map has %d active enums for %d canonical enum fields", len(wireEnums), countEnumFields(fields))
	}
	sort.Slice(message.Field, func(left, right int) bool { return message.Field[left].GetNumber() < message.Field[right].GetNumber() })
	return message, auxiliary, enums, usesStruct, nil
}

func fieldDescriptor(packageName, messageName string, field sdkmodel.Field, assignment protobufwiremap.FieldProjection, enum protobufwiremap.EnumProjection) (*descriptorpb.FieldDescriptorProto, []*descriptorpb.DescriptorProto, *descriptorpb.EnumDescriptorProto, bool, error) {
	if assignment.CanonicalName() != field.Name() || assignment.Name() == "" || assignment.Number() <= 0 {
		return nil, nil, nil, false, fmt.Errorf("field %q has an invalid wire assignment", field.Name())
	}
	descriptor := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(assignment.Name()),
		JsonName: proto.String(protobufidentity.FieldJSONName(field.Name())),
		Number:   proto.Int32(int32(assignment.Number())),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	if len(field.EnumJSON()) != 0 {
		if err := validateEnum(field, enum); err != nil {
			return nil, nil, nil, false, err
		}
		descriptor.Type = descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum()
		descriptor.TypeName = proto.String(qualified(enum.Identity()))
		descriptor.Proto3Optional = proto.Bool(true)
		return descriptor, nil, enumDescriptor(enum), false, nil
	}
	if enum.Identity() != "" {
		return nil, nil, nil, false, fmt.Errorf("non-enum field %q has active enum wire history", field.Name())
	}

	if field.Kind() == sdkmodel.KindArray {
		wrapperName := unqualifiedGenerated(messageName + goname.Field(field.Name()) + "List")
		descriptor.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		descriptor.TypeName = proto.String(qualified(packageName + "." + wrapperName))
		item := &descriptorpb.FieldDescriptorProto{
			Name:     proto.String("values"),
			JsonName: proto.String("values"),
			Number:   proto.Int32(1),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		}
		usesStruct, err := applyKind(item, field.Items())
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("field %q array items: %v", field.Name(), err)
		}
		return descriptor, []*descriptorpb.DescriptorProto{{Name: proto.String(wrapperName), Field: []*descriptorpb.FieldDescriptorProto{item}}}, nil, usesStruct, nil
	}
	usesStruct, err := applyKind(descriptor, field.Kind())
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("field %q: %v", field.Name(), err)
	}
	if descriptor.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		descriptor.Proto3Optional = proto.Bool(true)
	}
	return descriptor, nil, nil, usesStruct, nil
}

func enumDescriptor(value protobufwiremap.EnumProjection) *descriptorpb.EnumDescriptorProto {
	name := unqualifiedGenerated(value.Identity())
	enum := &descriptorpb.EnumDescriptorProto{Name: proto.String(name)}
	values := append([]protobufwiremap.EnumValueProjection{value.Sentinel()}, value.Members()...)
	sort.Slice(values, func(left, right int) bool {
		if values[left].Number() != values[right].Number() {
			return values[left].Number() < values[right].Number()
		}
		return values[left].Name() < values[right].Name()
	})
	for _, member := range values {
		enum.Value = append(enum.Value, &descriptorpb.EnumValueDescriptorProto{Name: proto.String(member.Name()), Number: proto.Int32(int32(member.Number()))})
	}
	for _, number := range value.ReservedNumbers() {
		start := int32(number)
		end := start
		enum.ReservedRange = append(enum.ReservedRange, &descriptorpb.EnumDescriptorProto_EnumReservedRange{Start: &start, End: &end})
	}
	enum.ReservedName = value.ReservedNames()
	return enum
}

func validateEnum(field sdkmodel.Field, value protobufwiremap.EnumProjection) error {
	if value.Identity() == "" || value.CanonicalField() != field.Name() || value.Kind() != field.Kind() || value.Sentinel().Number() != 0 {
		return fmt.Errorf("field %q has inconsistent enum wire history", field.Name())
	}
	canonical := field.EnumJSON()
	members := value.Members()
	if len(canonical) != len(members) {
		return fmt.Errorf("field %q enum wire history has %d members for %d canonical values", field.Name(), len(members), len(canonical))
	}
	wanted := make(map[string]struct{}, len(canonical))
	for _, member := range canonical {
		wanted[string(member)] = struct{}{}
	}
	for _, member := range members {
		if _, exists := wanted[string(member.CanonicalJSON())]; !exists {
			return fmt.Errorf("field %q enum wire history contains unknown canonical value %s", field.Name(), member.CanonicalJSON())
		}
	}
	return nil
}

func applyKind(field *descriptorpb.FieldDescriptorProto, kind sdkmodel.Kind) (bool, error) {
	switch kind {
	case sdkmodel.KindString:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	case sdkmodel.KindInteger:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_SINT64.Enum()
	case sdkmodel.KindNumber:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_DOUBLE.Enum()
	case sdkmodel.KindBoolean:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum()
	case sdkmodel.KindObject:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		field.TypeName = proto.String(".google.protobuf.Struct")
		return true, nil
	default:
		return false, fmt.Errorf("unsupported canonical kind %q", kind)
	}
	return false, nil
}

func countEnumFields(fields []sdkmodel.Field) int {
	count := 0
	for _, field := range fields {
		if len(field.EnumJSON()) != 0 {
			count++
		}
	}
	return count
}

func renderSource(file *descriptorpb.FileDescriptorProto) ([]byte, error) {
	if file.GetName() == "" || file.GetPackage() == "" || file.GetSyntax() != "proto3" {
		return nil, errors.New("descriptor identity is incomplete")
	}
	var output bytes.Buffer
	output.WriteString("// Code generated by Plystra CLI. DO NOT EDIT.\n")
	output.WriteString("// Descriptor schema: " + ProjectionSchema + "\n\n")
	output.WriteString("syntax = \"proto3\";\n\n")
	output.WriteString("package " + file.GetPackage() + ";\n")
	for _, dependency := range file.Dependency {
		output.WriteString("\nimport " + strconv.Quote(dependency) + ";\n")
	}
	for _, enum := range file.EnumType {
		if err := renderEnum(&output, enum); err != nil {
			return nil, err
		}
	}
	for _, message := range file.MessageType {
		if err := renderMessage(&output, message); err != nil {
			return nil, err
		}
	}
	for _, service := range file.Service {
		output.WriteString("\nservice " + service.GetName() + " {\n")
		for _, method := range service.Method {
			output.WriteString("  rpc " + method.GetName() + "(" + method.GetInputType() + ") returns (" + method.GetOutputType() + ");\n")
		}
		output.WriteString("}\n")
	}
	return output.Bytes(), nil
}

func renderEnum(output *bytes.Buffer, value *descriptorpb.EnumDescriptorProto) error {
	if value.GetName() == "" || len(value.Value) == 0 {
		return errors.New("enum descriptor is incomplete")
	}
	output.WriteString("\nenum " + value.GetName() + " {\n")
	for _, member := range value.Value {
		output.WriteString(fmt.Sprintf("  %s = %d;\n", member.GetName(), member.GetNumber()))
	}
	if len(value.ReservedRange) != 0 {
		parts := make([]string, len(value.ReservedRange))
		for index, reserved := range value.ReservedRange {
			if reserved.GetStart() != reserved.GetEnd() {
				parts[index] = fmt.Sprintf("%d to %d", reserved.GetStart(), reserved.GetEnd())
			} else {
				parts[index] = strconv.FormatInt(int64(reserved.GetStart()), 10)
			}
		}
		output.WriteString("  reserved " + strings.Join(parts, ", ") + ";\n")
	}
	if len(value.ReservedName) != 0 {
		quoted := make([]string, len(value.ReservedName))
		for index, name := range value.ReservedName {
			quoted[index] = strconv.Quote(name)
		}
		output.WriteString("  reserved " + strings.Join(quoted, ", ") + ";\n")
	}
	output.WriteString("}\n")
	return nil
}

func renderMessage(output *bytes.Buffer, value *descriptorpb.DescriptorProto) error {
	if value.GetName() == "" {
		return errors.New("message descriptor has no name")
	}
	output.WriteString("\nmessage " + value.GetName() + " {\n")
	if len(value.ReservedRange) != 0 {
		parts := make([]string, len(value.ReservedRange))
		for index, reserved := range value.ReservedRange {
			end := reserved.GetEnd() - 1
			if reserved.GetStart() != end {
				parts[index] = fmt.Sprintf("%d to %d", reserved.GetStart(), end)
			} else {
				parts[index] = strconv.FormatInt(int64(reserved.GetStart()), 10)
			}
		}
		output.WriteString("  reserved " + strings.Join(parts, ", ") + ";\n")
	}
	if len(value.ReservedName) != 0 {
		quoted := make([]string, len(value.ReservedName))
		for index, name := range value.ReservedName {
			quoted[index] = strconv.Quote(name)
		}
		output.WriteString("  reserved " + strings.Join(quoted, ", ") + ";\n")
	}
	for _, field := range value.Field {
		fieldType, label, err := sourceFieldType(value, field)
		if err != nil {
			return err
		}
		output.WriteString(fmt.Sprintf("  %s%s %s = %d [json_name = %s];\n", label, fieldType, field.GetName(), field.GetNumber(), strconv.Quote(field.GetJsonName())))
	}
	output.WriteString("}\n")
	return nil
}

func sourceFieldType(message *descriptorpb.DescriptorProto, field *descriptorpb.FieldDescriptorProto) (string, string, error) {
	if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED &&
		field.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		typeName := field.GetTypeName()
		nestedName := typeName[strings.LastIndex(typeName, ".")+1:]
		for _, nested := range message.NestedType {
			if nested.GetName() != nestedName || nested.GetOptions() == nil || !nested.GetOptions().GetMapEntry() {
				continue
			}
			if len(nested.Field) != 2 || nested.Field[0].GetName() != "key" || nested.Field[1].GetName() != "value" {
				return "", "", errors.New("map-entry descriptor is incomplete")
			}
			keyType, err := sourceType(nested.Field[0])
			if err != nil {
				return "", "", err
			}
			valueType, err := sourceType(nested.Field[1])
			if err != nil {
				return "", "", err
			}
			return "map<" + keyType + ", " + valueType + ">", "", nil
		}
	}
	fieldType, err := sourceType(field)
	if err != nil {
		return "", "", err
	}
	if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		return fieldType, "repeated ", nil
	}
	if field.GetProto3Optional() {
		return fieldType, "optional ", nil
	}
	return fieldType, "", nil
}

func sourceType(field *descriptorpb.FieldDescriptorProto) (string, error) {
	switch field.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return "bool", nil
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return "bytes", nil
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return "double", nil
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		return "float", nil
	case descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return "sint32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		return "sint64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		return "string", nil
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32:
		return "uint32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64:
		return "uint64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		if field.GetTypeName() == "" {
			return "", errors.New("message or enum field has no type name")
		}
		return field.GetTypeName(), nil
	default:
		return "", fmt.Errorf("unsupported descriptor field type %s", field.GetType())
	}
}

func descriptorName(packageName string) string {
	return strings.ReplaceAll(packageName, ".", "/") + "/capability.proto"
}

func qualified(value string) string {
	if strings.HasPrefix(value, ".") {
		return value
	}
	return "." + value
}

func unqualified(packageName, value string) (string, error) {
	prefix := packageName + "."
	name, found := strings.CutPrefix(value, prefix)
	if !found || name == "" || strings.Contains(name, ".") {
		return "", fmt.Errorf("identity %q is not directly inside package %q", value, packageName)
	}
	return name, nil
}

func unqualifiedGenerated(value string) string {
	if index := strings.LastIndexByte(value, '.'); index >= 0 {
		return value[index+1:]
	}
	return value
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
