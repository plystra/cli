package javascriptgen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/sdkmodel"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TransportOptions binds the JavaScript wrapper to the same canonical
// Protobuf projection, stable wire map, and descriptor graph used by the
// generated Connect handlers.
type TransportOptions struct {
	Projection          protobufmodel.Model
	InterfaceProjection protobufmodel.InterfaceModel
	WireMap             protobufwiremap.Map
	DescriptorSet       []byte
}

type transportOperation struct {
	serviceType  string
	methodName   string
	requestType  string
	responseType string
	request      messageCodec
	response     messageCodec
}

type messageCodec struct {
	fields []fieldCodec
}

type fieldCodec struct {
	canonicalName    string
	protobufJSONName string
	kind             sdkmodel.Kind
	items            sdkmodel.Kind
	required         bool
	enum             []enumCodec
}

type enumCodec struct {
	canonical    json.RawMessage
	protobufName string
}

func bindTransport(operations []renderedOperation, options TransportOptions) ([]renderedOperation, error) {
	if !options.Projection.Valid() {
		return nil, fmt.Errorf("%w: normalized Protobuf transport projection is absent", ErrRender)
	}
	if len(operations) != 0 && !options.Projection.Enabled() {
		return nil, fmt.Errorf("%w: JavaScript operations require an enabled Connect transport projection", ErrRender)
	}
	interfaceProjection := options.InterfaceProjection
	if !interfaceProjection.Valid() {
		normalized, err := protobufmodel.BuildInterfaces(options.Projection.Enabled(), nil)
		if err != nil {
			return nil, fmt.Errorf("%w: normalize empty Interface projection: %v", ErrRender, err)
		}
		interfaceProjection = normalized
	}
	interfaceOperations := interfaceProjection.Operations()
	if len(interfaceOperations) != 0 && !interfaceProjection.Enabled() {
		return nil, fmt.Errorf("%w: JavaScript Interface methods require an enabled Connect transport projection", ErrRender)
	}
	if !options.WireMap.Matches(options.Projection, interfaceProjection) {
		return nil, fmt.Errorf("%w: Protobuf wire map is absent or does not match the Interface and legacy transport projections", ErrRender)
	}
	expected, err := protobufdescriptor.BuildWithInterfaces(options.Projection, options.WireMap, interfaceProjection)
	if err != nil {
		return nil, fmt.Errorf("%w: rebuild JavaScript Connect descriptors: %v", ErrRender, err)
	}
	if !bytes.Equal(options.DescriptorSet, expected.DescriptorSet()) {
		return nil, fmt.Errorf("%w: JavaScript Connect descriptors do not exactly match the normalized Protobuf projection and wire map", ErrRender)
	}

	canonical := make(map[string]protobufmodel.Operation, len(options.Projection.Operations()))
	for _, operation := range options.Projection.Operations() {
		canonical[operation.ID().String()] = operation
	}
	aliases := make(map[string]protobufmodel.Alias, len(options.Projection.Aliases()))
	for _, alias := range options.Projection.Aliases() {
		aliases[alias.ID().String()] = alias
	}
	wire := make(map[string]protobufwiremap.CapabilityProjection, len(options.WireMap.ActiveCapabilities()))
	for _, capability := range options.WireMap.ActiveCapabilities() {
		wire[capability.ID()] = capability
	}

	result := append([]renderedOperation(nil), operations...)
	identities := make([]protobufidentity.Identity, 0, len(result)+len(interfaceOperations))
	for index := range result {
		operation := &result[index]
		projected, exists := canonical[operation.target.String()]
		if !exists || projected.ContractDigest() != operation.operation.ContractDigest() {
			return nil, fmt.Errorf("%w: JavaScript operation %s has no matching canonical Protobuf contract", ErrRender, operation.id)
		}
		assignment, exists := wire[operation.target.String()]
		if !exists || assignment.ContractDigest() != operation.operation.ContractDigest() {
			return nil, fmt.Errorf("%w: JavaScript operation %s has no matching active Protobuf wire assignment", ErrRender, operation.id)
		}

		identity := projected.Identity()
		if operation.isAlias() {
			alias, aliasExists := aliases[operation.id.String()]
			if !aliasExists || alias.Target() != operation.target || alias.TargetContractDigest() != operation.operation.ContractDigest() {
				return nil, fmt.Errorf("%w: JavaScript Alias %s has no matching Protobuf Alias projection", ErrRender, operation.id)
			}
			identity = alias.Identity()
		}
		request, err := buildMessageCodec(operation.operation.Request(), assignment.Request())
		if err != nil {
			return nil, fmt.Errorf("%w: JavaScript operation %s request transport: %v", ErrRender, operation.id, err)
		}
		response, err := buildMessageCodec(operation.operation.Response(), assignment.Response())
		if err != nil {
			return nil, fmt.Errorf("%w: JavaScript operation %s response transport: %v", ErrRender, operation.id, err)
		}
		operation.transport = transportOperation{
			serviceType:  identity.Package() + "." + identity.Service(),
			methodName:   identity.Method(),
			requestType:  identity.RequestType(),
			responseType: identity.ResponseType(),
			request:      request,
			response:     response,
		}
		identities = append(identities, identity)
	}
	for _, operation := range interfaceOperations {
		identities = append(identities, operation.Identity())
	}
	if err := validateDescriptorSet(options.DescriptorSet, identities); err != nil {
		return nil, fmt.Errorf("%w: JavaScript Connect descriptors: %v", ErrRender, err)
	}
	return result, nil
}

func buildMessageCodec(fields []sdkmodel.Field, projection protobufwiremap.MessageProjection) (messageCodec, error) {
	projectedFields := projection.Fields()
	if len(projectedFields) != len(fields) {
		return messageCodec{}, fmt.Errorf("wire message %s has %d fields for %d canonical fields", projection.Name(), len(projectedFields), len(fields))
	}
	fieldsByCanonical := make(map[string]protobufwiremap.FieldProjection, len(projectedFields))
	for _, field := range projectedFields {
		fieldsByCanonical[field.CanonicalName()] = field
	}
	enumsByCanonical := make(map[string]protobufwiremap.EnumProjection, len(projection.Enums()))
	for _, enum := range projection.Enums() {
		enumsByCanonical[enum.CanonicalField()] = enum
	}

	result := messageCodec{fields: make([]fieldCodec, len(fields))}
	for index, field := range fields {
		projected, exists := fieldsByCanonical[field.Name()]
		if !exists {
			return messageCodec{}, fmt.Errorf("wire message %s omits canonical field %q", projection.Name(), field.Name())
		}
		codec := fieldCodec{
			canonicalName:    field.Name(),
			protobufJSONName: protobufidentity.FieldJSONName(projected.Name()),
			kind:             field.Kind(),
			items:            field.Items(),
			required:         field.Required(),
		}
		values := field.EnumJSON()
		if len(values) != 0 {
			enum, enumExists := enumsByCanonical[field.Name()]
			if !enumExists || enum.Kind() != field.Kind() {
				return messageCodec{}, fmt.Errorf("wire message %s omits the canonical enum for field %q", projection.Name(), field.Name())
			}
			members := make(map[string]string, len(enum.Members()))
			for _, member := range enum.Members() {
				members[string(member.CanonicalJSON())] = member.Name()
			}
			codec.enum = make([]enumCodec, len(values))
			for valueIndex, value := range values {
				name, memberExists := members[string(value)]
				if !memberExists {
					return messageCodec{}, fmt.Errorf("wire message %s enum for field %q omits canonical value %s", projection.Name(), field.Name(), value)
				}
				codec.enum[valueIndex] = enumCodec{canonical: append(json.RawMessage(nil), value...), protobufName: name}
			}
		}
		result.fields[index] = codec
	}
	return result, nil
}

func validateDescriptorSet(data []byte, identities []protobufidentity.Identity) error {
	var set descriptorpb.FileDescriptorSet
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, &set); err != nil {
		return fmt.Errorf("decode descriptor set: %v", err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return fmt.Errorf("resolve descriptor graph: %v", err)
	}
	if len(identities) != 0 {
		descriptor, findErr := files.FindDescriptorByName(protoreflect.FullName(protobufdescriptor.ErrorDetailFullName))
		message, ok := descriptor.(protoreflect.MessageDescriptor)
		if findErr != nil || !ok || !validErrorDetailDescriptor(message) {
			return fmt.Errorf("safe error detail %s is absent or inconsistent", protobufdescriptor.ErrorDetailFullName)
		}
	}
	for _, identity := range identities {
		serviceName := protoreflect.FullName(identity.Package() + "." + identity.Service())
		descriptor, findErr := files.FindDescriptorByName(serviceName)
		if findErr != nil {
			return fmt.Errorf("service %s is absent", serviceName)
		}
		service, ok := descriptor.(protoreflect.ServiceDescriptor)
		if !ok {
			return fmt.Errorf("descriptor %s is not a service", serviceName)
		}
		method := service.Methods().ByName(protoreflect.Name(identity.Method()))
		if method == nil || method.IsStreamingClient() || method.IsStreamingServer() {
			return fmt.Errorf("service %s has no unary method %s", serviceName, identity.Method())
		}
		if string(method.Input().FullName()) != identity.RequestType() || string(method.Output().FullName()) != identity.ResponseType() {
			return fmt.Errorf("procedure %s has inconsistent request or response descriptors", identity.Procedure())
		}
	}
	return nil
}

func validErrorDetailDescriptor(message protoreflect.MessageDescriptor) bool {
	if message == nil || string(message.FullName()) != protobufdescriptor.ErrorDetailFullName || message.Fields().Len() != 5 {
		return false
	}
	want := []struct {
		name   protoreflect.Name
		number protoreflect.FieldNumber
	}{
		{name: "requested_interface_id", number: 1},
		{name: "canonical_interface_id", number: 2},
		{name: "semantic_error_code", number: 3},
		{name: "kernel_error_class", number: 4},
		{name: "trace_id", number: 5},
	}
	for _, expected := range want {
		field := message.Fields().ByNumber(expected.number)
		if field == nil || field.Name() != expected.name || field.Kind() != protoreflect.StringKind || field.Cardinality() != protoreflect.Optional || field.HasPresence() {
			return false
		}
	}
	return true
}

func renderDescriptorSource(descriptorSet []byte) []byte {
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "import {")
	fmt.Fprintln(&source, "  createFileRegistry,")
	fmt.Fprintln(&source, "  fromBinary,")
	fmt.Fprintln(&source, "  type DescMessage,")
	fmt.Fprintln(&source, "  type DescMethodUnary,")
	fmt.Fprintln(&source, "} from \"@bufbuild/protobuf\";")
	fmt.Fprintln(&source, "import {")
	fmt.Fprintln(&source, "  FileDescriptorSetSchema,")
	fmt.Fprintln(&source, "  type FileDescriptorProto,")
	fmt.Fprintln(&source, "} from \"@bufbuild/protobuf/wkt\";")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "const descriptorSetBase64 = %s;\n", jsString(base64.StdEncoding.EncodeToString(descriptorSet)))
	fmt.Fprintln(&source, "const descriptorSet = fromBinary(")
	fmt.Fprintln(&source, "  FileDescriptorSetSchema,")
	fmt.Fprintln(&source, "  decodeBase64(descriptorSetBase64),")
	fmt.Fprintln(&source, ");")
	fmt.Fprintln(&source, "descriptorSet.file = orderDescriptorFiles(descriptorSet.file);")
	fmt.Fprintln(&source, "const registry = createFileRegistry(descriptorSet);")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "/** @internal */")
	fmt.Fprintln(&source, "export function resolveUnaryMethod(")
	fmt.Fprintln(&source, "  serviceType: string,")
	fmt.Fprintln(&source, "  methodName: string,")
	fmt.Fprintln(&source, "  requestType: string,")
	fmt.Fprintln(&source, "  responseType: string,")
	fmt.Fprintln(&source, "): DescMethodUnary {")
	fmt.Fprintln(&source, "  const service = registry.getService(serviceType);")
	fmt.Fprintln(&source, "  const method = service?.methods.find((candidate) => candidate.name === methodName);")
	fmt.Fprintln(&source, "  if (")
	fmt.Fprintln(&source, "    method === undefined ||")
	fmt.Fprintln(&source, "    method.methodKind !== \"unary\" ||")
	fmt.Fprintln(&source, "    method.input.typeName !== requestType ||")
	fmt.Fprintln(&source, "    method.output.typeName !== responseType")
	fmt.Fprintln(&source, "  ) {")
	fmt.Fprintln(&source, "    throw new Error(\"generated Plystra Connect descriptors are inconsistent\");")
	fmt.Fprintln(&source, "  }")
	fmt.Fprintln(&source, "  return method as DescMethodUnary;")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "/** @internal */")
	fmt.Fprintln(&source, "export function resolveMessage(typeName: string): DescMessage {")
	fmt.Fprintln(&source, "  const message = registry.getMessage(typeName);")
	fmt.Fprintln(&source, "  if (message === undefined || message.typeName !== typeName) {")
	fmt.Fprintln(&source, "    throw new Error(\"generated Plystra Connect descriptors are inconsistent\");")
	fmt.Fprintln(&source, "  }")
	fmt.Fprintln(&source, "  return message;")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "function orderDescriptorFiles(")
	fmt.Fprintln(&source, "  files: readonly FileDescriptorProto[],")
	fmt.Fprintln(&source, "): FileDescriptorProto[] {")
	fmt.Fprintln(&source, "  const pending = new Map<string, FileDescriptorProto>();")
	fmt.Fprintln(&source, "  for (const file of files) {")
	fmt.Fprintln(&source, "    if (file.name === undefined || file.name === \"\" || pending.has(file.name)) {")
	fmt.Fprintln(&source, "      throw new Error(\"generated Plystra Connect descriptor set is inconsistent\");")
	fmt.Fprintln(&source, "    }")
	fmt.Fprintln(&source, "    pending.set(file.name, file);")
	fmt.Fprintln(&source, "  }")
	fmt.Fprintln(&source, "  const ordered: FileDescriptorProto[] = [];")
	fmt.Fprintln(&source, "  while (pending.size > 0) {")
	fmt.Fprintln(&source, "    let progressed = false;")
	fmt.Fprintln(&source, "    for (const name of [...pending.keys()].sort()) {")
	fmt.Fprintln(&source, "      const file = pending.get(name);")
	fmt.Fprintln(&source, "      if (file === undefined || file.dependency.some((dependency) => pending.has(dependency))) {")
	fmt.Fprintln(&source, "        continue;")
	fmt.Fprintln(&source, "      }")
	fmt.Fprintln(&source, "      pending.delete(name);")
	fmt.Fprintln(&source, "      ordered.push(file);")
	fmt.Fprintln(&source, "      progressed = true;")
	fmt.Fprintln(&source, "    }")
	fmt.Fprintln(&source, "    if (!progressed) {")
	fmt.Fprintln(&source, "      throw new Error(\"generated Plystra Connect descriptor imports are cyclic\");")
	fmt.Fprintln(&source, "    }")
	fmt.Fprintln(&source, "  }")
	fmt.Fprintln(&source, "  return ordered;")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "function decodeBase64(value: string): Uint8Array {")
	fmt.Fprintln(&source, "  let decoded: string;")
	fmt.Fprintln(&source, "  try {")
	fmt.Fprintln(&source, "    decoded = globalThis.atob(value);")
	fmt.Fprintln(&source, "  } catch {")
	fmt.Fprintln(&source, "    throw new Error(\"generated Plystra Connect descriptor set is invalid\");")
	fmt.Fprintln(&source, "  }")
	fmt.Fprintln(&source, "  return Uint8Array.from(decoded, (character) => character.charCodeAt(0));")
	fmt.Fprintln(&source, "}")
	return []byte(source.String())
}

func renderMethodBinding(source *strings.Builder, operation transportOperation) {
	renderMethodResolver(source, operation)
	renderMessageCodec(source, "requestCodec", operation.request)
	renderMessageCodec(source, "responseCodec", operation.response)
	fmt.Fprintln(source)
}

func renderMethodResolver(source *strings.Builder, operation transportOperation) {
	fmt.Fprintln(source, "const method = resolveUnaryMethod(")
	fmt.Fprintf(source, "  %s,\n", jsString(operation.serviceType))
	fmt.Fprintf(source, "  %s,\n", jsString(operation.methodName))
	fmt.Fprintf(source, "  %s,\n", jsString(operation.requestType))
	fmt.Fprintf(source, "  %s,\n", jsString(operation.responseType))
	fmt.Fprintln(source, ");")
}

func renderMessageCodec(source *strings.Builder, name string, codec messageCodec) {
	fmt.Fprintf(source, "const %s: MessageCodec = {\n", name)
	fmt.Fprintln(source, "  fields: [")
	for _, field := range codec.fields {
		fmt.Fprintln(source, "    {")
		fmt.Fprintf(source, "      canonicalName: %s,\n", jsString(field.canonicalName))
		fmt.Fprintf(source, "      protobufJSONName: %s,\n", jsString(field.protobufJSONName))
		fmt.Fprintf(source, "      kind: %s,\n", jsString(string(field.kind)))
		if field.items != "" {
			fmt.Fprintf(source, "      items: %s,\n", jsString(string(field.items)))
		}
		fmt.Fprintf(source, "      required: %t,\n", field.required)
		if len(field.enum) != 0 {
			fmt.Fprintln(source, "      enum: [")
			for _, member := range field.enum {
				fmt.Fprintf(source, "        { canonical: %s, protobufName: %s },\n", typescriptScalarLiteral(field.kind, member.canonical), jsString(member.protobufName))
			}
			fmt.Fprintln(source, "      ],")
		}
		fmt.Fprintln(source, "    },")
	}
	fmt.Fprintln(source, "  ],")
	fmt.Fprintln(source, "};")
}
