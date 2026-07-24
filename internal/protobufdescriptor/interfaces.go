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

// BuildWithInterfaces adds message-only schemas projected from canonical
// authored Interface packages to the same deterministic descriptor evidence as
// the current application transport model. Connect services are added by their
// later dedicated generation boundary.
func BuildWithInterfaces(model protobufmodel.Model, wireMap protobufwiremap.Map, interfaces protobufmodel.InterfaceModel) (Evidence, error) {
	base, err := Build(model, wireMap)
	if err != nil {
		return Evidence{}, err
	}
	if !interfaces.Valid() {
		return Evidence{}, fmt.Errorf("%w: %w: Interface Protobuf model is absent", ErrBuild, ErrProjection)
	}
	if interfaces.Enabled() != model.Enabled() {
		return Evidence{}, fmt.Errorf("%w: %w: Interface and legacy Protobuf transport selection disagree", ErrBuild, ErrProjection)
	}
	operations := interfaces.Operations()
	if len(operations) == 0 {
		return base, nil
	}

	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(base.DescriptorSet(), &set); err != nil {
		return Evidence{}, fmt.Errorf("%w: %w: decode base descriptor set: %v", ErrBuild, ErrDescriptor, err)
	}
	knownFiles := make(map[string]*descriptorpb.FileDescriptorProto, len(set.File)+len(operations)+2)
	for _, file := range set.File {
		knownFiles[file.GetName()] = file
	}

	interfaceDescriptors := make([]*descriptorpb.FileDescriptorProto, len(operations))
	needTimestamp := false
	needDuration := false
	for index, operation := range operations {
		file, usesTimestamp, usesDuration, err := interfaceDescriptor(operation)
		if err != nil {
			return Evidence{}, fmt.Errorf("%w: Interface %s: %v", ErrBuild, operation.ID(), err)
		}
		if previous := knownFiles[file.GetName()]; previous != nil {
			return Evidence{}, fmt.Errorf("%w: %w: Interface %s descriptor path %s collides with %s", ErrBuild, ErrProjection, operation.ID(), file.GetName(), previous.GetPackage())
		}
		knownFiles[file.GetName()] = file
		interfaceDescriptors[index] = file
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

	files := make([]File, 0, len(base.files)+len(interfaceDescriptors))
	for _, file := range base.files {
		if file.path != DescriptorSetPath {
			files = append(files, File{path: file.path, data: append([]byte(nil), file.data...)})
		}
	}
	for _, descriptor := range interfaceDescriptors {
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
		descriptors: base.descriptors + len(interfaceDescriptors),
	}, nil
}

func interfaceDescriptor(operation protobufmodel.InterfaceOperation) (*descriptorpb.FileDescriptorProto, bool, bool, error) {
	identity := operation.Identity()
	if identity.PublicID() != operation.ID().String() || identity.CanonicalID() != operation.ID().String() {
		return nil, false, false, fmt.Errorf("%w: generated identity is inconsistent", ErrProjection)
	}
	messages := operation.Messages()
	descriptors := make([]*descriptorpb.DescriptorProto, len(messages))
	usesTimestamp := false
	usesDuration := false
	for index, message := range messages {
		descriptor, messageUsesTimestamp, messageUsesDuration, err := interfaceMessageDescriptor(operation, message)
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
	}, usesTimestamp, usesDuration, nil
}

func interfaceMessageDescriptor(operation protobufmodel.InterfaceOperation, message protobufmodel.InterfaceMessage) (*descriptorpb.DescriptorProto, bool, bool, error) {
	descriptor := &descriptorpb.DescriptorProto{Name: proto.String(message.ProtobufName())}
	usesTimestamp := false
	usesDuration := false
	for _, field := range message.Fields() {
		projected, mapEntry, fieldUsesTimestamp, fieldUsesDuration, err := interfaceFieldDescriptor(operation, message, field)
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
) (*descriptorpb.FieldDescriptorProto, *descriptorpb.DescriptorProto, bool, bool, error) {
	descriptor := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(field.ProtobufName()),
		JsonName: proto.String(field.JSONName()),
		Number:   proto.Int32(int32(field.Number())),
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
		if descriptor.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
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
