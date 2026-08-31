package protobufdescriptor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	timestampDependency = "google/protobuf/timestamp.proto"
	durationDependency  = "google/protobuf/duration.proto"
)

// BuildWithInterfaces adds canonical messages and one unary service projected
// from each exposed Interface package to the same deterministic descriptor
// evidence as the current application transport model.
func BuildWithInterfaces(model protobufmodel.Model, wireMap protobufwiremap.Map, interfaces protobufmodel.InterfaceModel) (Evidence, error) {
	if !interfaces.Valid() {
		return Evidence{}, fmt.Errorf("%w: %w: Interface Protobuf model is absent", ErrBuild, ErrProjection)
	}
	if interfaces.Enabled() != model.Enabled() {
		return Evidence{}, fmt.Errorf("%w: %w: Interface and legacy Protobuf transport selection disagree", ErrBuild, ErrProjection)
	}
	if !wireMap.Matches(model, interfaces) {
		return Evidence{}, fmt.Errorf("%w: %w: wire map is absent or does not match the normalized Interface and legacy projections", ErrBuild, ErrProjection)
	}
	base, err := Build(model, wireMap)
	if err != nil {
		return Evidence{}, err
	}
	operations := interfaces.Operations()
	if len(operations) == 0 {
		return base, nil
	}
	wireInterfaces := wireMap.ActiveInterfaces()
	if len(wireInterfaces) != len(operations) {
		return Evidence{}, fmt.Errorf("%w: %w: wire map has %d active Interfaces for %d projected Interfaces", ErrBuild, ErrProjection, len(wireInterfaces), len(operations))
	}
	wireByID := make(map[string]protobufwiremap.InterfaceProjection, len(wireInterfaces))
	for _, projection := range wireInterfaces {
		wireByID[projection.ID()] = projection
	}

	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(base.DescriptorSet(), &set); err != nil {
		return Evidence{}, fmt.Errorf("%w: %w: decode base descriptor set: %v", ErrBuild, ErrDescriptor, err)
	}
	knownFiles := make(map[string]*descriptorpb.FileDescriptorProto, len(set.File)+len(operations)+2)
	for _, file := range set.File {
		knownFiles[file.GetName()] = file
	}
	generatedNames := make(map[string]struct{}, base.descriptors+len(operations))
	for _, file := range base.files {
		if file.path == DescriptorSetPath {
			continue
		}
		name, found := strings.CutPrefix(file.path, generatedRoot)
		if !found || name == "" {
			return Evidence{}, fmt.Errorf("%w: %w: base descriptor source path %q is outside %s", ErrBuild, ErrDescriptor, file.path, generatedRoot)
		}
		generatedNames[name] = struct{}{}
	}
	if knownFiles[ErrorDetailFileName] == nil {
		knownFiles[ErrorDetailFileName] = errorDetailDescriptor()
		generatedNames[ErrorDetailFileName] = struct{}{}
	}
	legacyOperations := make(map[string]protobufmodel.Operation, len(model.Operations()))
	for _, operation := range model.Operations() {
		legacyOperations[operation.ID().String()] = operation
	}

	needTimestamp := false
	needDuration := false
	for _, operation := range operations {
		wire, exists := wireByID[operation.ID().String()]
		if !exists {
			return Evidence{}, fmt.Errorf("%w: %w: Interface %s is absent from active wire history", ErrBuild, ErrProjection, operation.ID())
		}
		file, usesTimestamp, usesDuration, err := interfaceDescriptor(operation, wire)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: Interface %s: %v", ErrBuild, operation.ID(), err)
		}
		if previous := knownFiles[file.GetName()]; previous != nil {
			return Evidence{}, fmt.Errorf("%w: %w: Interface %s descriptor path %s collides with %s", ErrBuild, ErrProjection, operation.ID(), file.GetName(), previous.GetPackage())
		}
		knownFiles[file.GetName()] = file
		generatedNames[file.GetName()] = struct{}{}
		if _, overlapsLegacy := legacyOperations[operation.ID().String()]; overlapsLegacy {
			if err := bridgeLegacyInterfaceDescriptor(knownFiles, operation, file.GetName()); err != nil {
				return Evidence{}, fmt.Errorf("%w: Interface %s: %v", ErrBuild, operation.ID(), err)
			}
		}
		needTimestamp = needTimestamp || usesTimestamp
		needDuration = needDuration || usesDuration
	}
	if needTimestamp {
		addDescriptorDependency(knownFiles, protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto))
	}
	if needDuration {
		addDescriptorDependency(knownFiles, protodesc.ToFileDescriptorProto(durationpb.File_google_protobuf_duration_proto))
	}

	set.File = set.File[:0]
	for _, file := range knownFiles {
		set.File = append(set.File, file)
	}
	sort.Slice(set.File, func(left, right int) bool { return set.File[left].GetName() < set.File[right].GetName() })
	if _, err := protodesc.NewFiles(&set); err != nil {
		return Evidence{}, fmt.Errorf("%w: %w: validate Interface descriptor graph: %v", ErrBuild, ErrDescriptor, err)
	}
	binary, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&set)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: %w: serialize Interface descriptor set: %v", ErrBuild, ErrDescriptor, err)
	}
	if len(binary) > maximumSetBytes {
		return Evidence{}, fmt.Errorf("%w: %w: descriptor set exceeds %d bytes", ErrBuild, ErrDescriptor, maximumSetBytes)
	}

	files := make([]File, 0, len(generatedNames)+1)
	for _, descriptor := range set.File {
		if _, generated := generatedNames[descriptor.GetName()]; !generated {
			continue
		}
		source, err := renderSource(descriptor)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: %w: render %s: %v", ErrBuild, ErrDescriptor, descriptor.GetName(), err)
		}
		files = append(files, File{path: generatedRoot + descriptor.GetName(), data: source})
	}
	files = append(files, File{path: DescriptorSetPath, data: binary})
	sort.Slice(files, func(left, right int) bool { return files[left].path < files[right].path })
	return Evidence{
		files:       files,
		digest:      digest(binary),
		prepared:    true,
		descriptors: len(generatedNames),
	}, nil
}

func bridgeLegacyInterfaceDescriptor(files map[string]*descriptorpb.FileDescriptorProto, operation protobufmodel.InterfaceOperation, interfaceFile string) error {
	identity := operation.Identity()
	legacyName := descriptorName(identity.Package())
	legacy := files[legacyName]
	if legacy == nil {
		return fmt.Errorf("%w: overlapping legacy descriptor %s is absent", ErrDescriptor, legacyName)
	}
	if legacy.GetPackage() != identity.Package() || len(legacy.Service) != 1 || len(legacy.Service[0].Method) != 1 {
		return fmt.Errorf("%w: overlapping legacy descriptor %s has an incompatible service boundary", ErrDescriptor, legacyName)
	}
	method := legacy.Service[0].Method[0]
	if method.GetInputType() != qualified(identity.RequestType()) || method.GetOutputType() != qualified(identity.ResponseType()) {
		return fmt.Errorf("%w: overlapping legacy descriptor %s has incompatible request or response identity", ErrDescriptor, legacyName)
	}

	legacy.MessageType = nil
	legacy.EnumType = nil
	legacy.Extension = nil
	legacy.Service = nil
	legacy.Dependency = []string{interfaceFile}
	legacy.PublicDependency = nil
	legacy.WeakDependency = nil
	for name, file := range files {
		if name == legacyName || name == interfaceFile {
			continue
		}
		for index, dependency := range file.Dependency {
			if dependency == legacyName {
				file.Dependency[index] = interfaceFile
			}
		}
	}
	return nil
}

func interfaceDescriptor(
	operation protobufmodel.InterfaceOperation,
	wire protobufwiremap.InterfaceProjection,
) (*descriptorpb.FileDescriptorProto, bool, bool, error) {
	identity := operation.Identity()
	if identity.PublicID() != operation.ID().String() || identity.CanonicalID() != operation.ID().String() {
		return nil, false, false, fmt.Errorf("%w: generated identity is inconsistent", ErrProjection)
	}
	requestMessage, err := unqualified(identity.Package(), identity.RequestType())
	if err != nil {
		return nil, false, false, fmt.Errorf("%w: request identity: %v", ErrProjection, err)
	}
	responseMessage, err := unqualified(identity.Package(), identity.ResponseType())
	if err != nil {
		return nil, false, false, fmt.Errorf("%w: response identity: %v", ErrProjection, err)
	}
	if wire.ID() != operation.ID().String() ||
		wire.ContractDigest() != operation.ContractDigest() ||
		wire.ProtobufPackage() != identity.Package() ||
		wire.Service() != identity.Service() ||
		wire.Method() != identity.Method() ||
		wire.Procedure() != identity.Procedure() ||
		wire.RequestMessage() != requestMessage ||
		wire.ResponseMessage() != responseMessage {
		return nil, false, false, fmt.Errorf("%w: active Interface wire identity or contract digest is inconsistent", ErrProjection)
	}
	wireMessages := make(map[string]protobufwiremap.MessageProjection, len(wire.Messages()))
	for _, message := range wire.Messages() {
		wireMessages[message.Name()] = message
	}
	messages := operation.Messages()
	if len(wireMessages) != len(messages) {
		return nil, false, false, fmt.Errorf("%w: wire map has %d messages for %d canonical messages", ErrProjection, len(wireMessages), len(messages))
	}
	descriptors := make([]*descriptorpb.DescriptorProto, len(messages))
	usesTimestamp := false
	usesDuration := false
	for index, message := range messages {
		wireMessage, exists := wireMessages[message.ProtobufName()]
		if !exists {
			return nil, false, false, fmt.Errorf("%w: message %s is absent from active wire history", ErrProjection, message.GoName())
		}
		descriptor, messageUsesTimestamp, messageUsesDuration, err := interfaceMessageDescriptor(operation, message, wireMessage)
		if err != nil {
			return nil, false, false, fmt.Errorf("%w: message %s: %v", ErrProjection, message.GoName(), err)
		}
		descriptors[index] = descriptor
		usesTimestamp = usesTimestamp || messageUsesTimestamp
		usesDuration = usesDuration || messageUsesDuration
	}
	sort.Slice(descriptors, func(left, right int) bool {
		return descriptors[left].GetName() < descriptors[right].GetName()
	})
	dependencies := make([]string, 0, 2)
	if usesDuration {
		dependencies = append(dependencies, durationDependency)
	}
	if usesTimestamp {
		dependencies = append(dependencies, timestampDependency)
	}
	return &descriptorpb.FileDescriptorProto{
		Name:        proto.String(interfaceDescriptorName(identity.Package())),
		Package:     proto.String(identity.Package()),
		Syntax:      proto.String("proto3"),
		Dependency:  dependencies,
		MessageType: descriptors,
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String(wire.Service()),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String(wire.Method()),
				InputType:  proto.String(qualified(identity.Package() + "." + wire.RequestMessage())),
				OutputType: proto.String(qualified(identity.Package() + "." + wire.ResponseMessage())),
			}},
		}},
	}, usesTimestamp, usesDuration, nil
}

func interfaceMessageDescriptor(
	operation protobufmodel.InterfaceOperation,
	message protobufmodel.InterfaceMessage,
	wire protobufwiremap.MessageProjection,
) (*descriptorpb.DescriptorProto, bool, bool, error) {
	if wire.CanonicalName() != message.GoName() || wire.Name() != message.ProtobufName() {
		return nil, false, false, fmt.Errorf("message identity does not match active wire history")
	}
	wireFields := make(map[string]protobufwiremap.FieldProjection, len(wire.Fields()))
	for _, field := range wire.Fields() {
		wireFields[field.Name()] = field
	}
	if len(wireFields) != len(message.Fields()) {
		return nil, false, false, fmt.Errorf("wire map has %d fields for %d canonical fields", len(wireFields), len(message.Fields()))
	}
	descriptor := &descriptorpb.DescriptorProto{Name: proto.String(message.ProtobufName())}
	for _, number := range wire.ReservedNumbers() {
		start := int32(number)
		end := start + 1
		descriptor.ReservedRange = append(descriptor.ReservedRange, &descriptorpb.DescriptorProto_ReservedRange{Start: &start, End: &end})
	}
	descriptor.ReservedName = wire.ReservedNames()
	usesTimestamp := false
	usesDuration := false
	for _, field := range message.Fields() {
		assignment, exists := wireFields[field.ProtobufName()]
		if !exists {
			return nil, false, false, fmt.Errorf("field %s is absent from active wire history", field.GoName())
		}
		projected, mapEntry, fieldUsesTimestamp, fieldUsesDuration, err := interfaceFieldDescriptor(operation, message, field, assignment)
		if err != nil {
			return nil, false, false, err
		}
		descriptor.Field = append(descriptor.Field, projected)
		if mapEntry != nil {
			descriptor.NestedType = append(descriptor.NestedType, mapEntry)
		}
		if projected.GetProto3Optional() {
			oneofIndex := int32(len(descriptor.OneofDecl))
			projected.OneofIndex = &oneofIndex
			descriptor.OneofDecl = append(descriptor.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: proto.String("_" + field.ProtobufName())})
		}
		usesTimestamp = usesTimestamp || fieldUsesTimestamp
		usesDuration = usesDuration || fieldUsesDuration
	}
	sort.Slice(descriptor.Field, func(left, right int) bool {
		return descriptor.Field[left].GetNumber() < descriptor.Field[right].GetNumber()
	})
	sort.Slice(descriptor.NestedType, func(left, right int) bool {
		return descriptor.NestedType[left].GetName() < descriptor.NestedType[right].GetName()
	})
	return descriptor, usesTimestamp, usesDuration, nil
}

func interfaceFieldDescriptor(
	operation protobufmodel.InterfaceOperation,
	message protobufmodel.InterfaceMessage,
	field protobufmodel.InterfaceField,
	wire protobufwiremap.FieldProjection,
) (*descriptorpb.FieldDescriptorProto, *descriptorpb.DescriptorProto, bool, bool, error) {
	if wire.CanonicalName() != field.GoName() ||
		wire.Name() != field.ProtobufName() ||
		wire.Number() != int(field.Number()) {
		return nil, nil, false, false, fmt.Errorf("field %s does not match active wire history", field.GoName())
	}
	descriptor := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(wire.Name()),
		JsonName: proto.String(field.JSONName()),
		Number:   proto.Int32(int32(wire.Number())),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	fieldType := field.Type()
	switch fieldType.Kind() {
	case interfacecontract.TypeRepeated:
		element, exists := fieldType.Element()
		if !exists {
			return nil, nil, false, false, fmt.Errorf("field %s repeated type has no element", field.GoName())
		}
		descriptor.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		usesTimestamp, usesDuration, err := applyInterfaceValueType(descriptor, operation, element)
		return descriptor, nil, usesTimestamp, usesDuration, err
	case interfacecontract.TypeMap:
		key, keyExists := fieldType.Key()
		value, valueExists := fieldType.Value()
		if !keyExists || !valueExists {
			return nil, nil, false, false, fmt.Errorf("field %s map type has no key or value", field.GoName())
		}
		entryName := field.GoName() + "Entry"
		entry := &descriptorpb.DescriptorProto{
			Name:    proto.String(entryName),
			Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
		}
		keyDescriptor := &descriptorpb.FieldDescriptorProto{
			Name:     proto.String("key"),
			JsonName: proto.String("key"),
			Number:   proto.Int32(1),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}
		valueDescriptor := &descriptorpb.FieldDescriptorProto{
			Name:     proto.String("value"),
			JsonName: proto.String("value"),
			Number:   proto.Int32(2),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}
		keyTimestamp, keyDuration, err := applyInterfaceValueType(keyDescriptor, operation, key)
		if err != nil {
			return nil, nil, false, false, fmt.Errorf("field %s map key: %v", field.GoName(), err)
		}
		valueTimestamp, valueDuration, err := applyInterfaceValueType(valueDescriptor, operation, value)
		if err != nil {
			return nil, nil, false, false, fmt.Errorf("field %s map value: %v", field.GoName(), err)
		}
		entry.Field = []*descriptorpb.FieldDescriptorProto{keyDescriptor, valueDescriptor}
		descriptor.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		descriptor.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		descriptor.TypeName = proto.String(qualified(operation.Identity().Package() + "." + message.ProtobufName() + "." + entryName))
		return descriptor, entry, keyTimestamp || valueTimestamp, keyDuration || valueDuration, nil
	default:
		usesTimestamp, usesDuration, err := applyInterfaceValueType(descriptor, operation, fieldType)
		if err != nil {
			return nil, nil, false, false, fmt.Errorf("field %s: %v", field.GoName(), err)
		}
		if field.Required() && descriptor.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
			descriptor.Proto3Optional = proto.Bool(true)
		}
		return descriptor, nil, usesTimestamp, usesDuration, nil
	}
}

func applyInterfaceValueType(field *descriptorpb.FieldDescriptorProto, operation protobufmodel.InterfaceOperation, value interfacecontract.Type) (bool, bool, error) {
	switch value.Kind() {
	case interfacecontract.TypeBoolean:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum()
	case interfacecontract.TypeString:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	case interfacecontract.TypeInt32:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_SINT32.Enum()
	case interfacecontract.TypeInt64:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_SINT64.Enum()
	case interfacecontract.TypeUint32:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()
	case interfacecontract.TypeUint64:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_UINT64.Enum()
	case interfacecontract.TypeFloat32:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_FLOAT.Enum()
	case interfacecontract.TypeFloat64:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_DOUBLE.Enum()
	case interfacecontract.TypeBytes:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
	case interfacecontract.TypeTimestamp:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		field.TypeName = proto.String(".google.protobuf.Timestamp")
		return true, false, nil
	case interfacecontract.TypeDuration:
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		field.TypeName = proto.String(".google.protobuf.Duration")
		return false, true, nil
	case interfacecontract.TypeMessage:
		goName, exists := value.MessageName()
		if !exists {
			return false, false, fmt.Errorf("message type has no canonical Go name")
		}
		protobufName, exists := operation.ProtobufMessageName(goName)
		if !exists {
			return false, false, fmt.Errorf("message type %s has no generated identity", goName)
		}
		field.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		field.TypeName = proto.String(qualified(operation.Identity().Package() + "." + protobufName))
	default:
		return false, false, fmt.Errorf("unsupported canonical Interface type %q", value.Kind())
	}
	return false, false, nil
}

func interfaceDescriptorName(packageName string) string {
	return strings.ReplaceAll(packageName, ".", "/") + "/interface.proto"
}

func addDescriptorDependency(files map[string]*descriptorpb.FileDescriptorProto, dependency *descriptorpb.FileDescriptorProto) {
	if files[dependency.GetName()] == nil {
		files[dependency.GetName()] = dependency
	}
}
