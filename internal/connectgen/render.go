// Package connectgen renders deterministic procedure-specific Connect handlers
// over the CLI-owned Protobuf descriptor graph and governed typed Interface
// values.
package connectgen

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/goname"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/sdkmodel"
	"github.com/plystra/cli/internal/transportprovenance"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	// ConnectModulePath and ConnectModuleVersion identify the generated Go
	// runtime dependency supported by this CLI build.
	ConnectModulePath    = "connectrpc.com/connect"
	ConnectModuleVersion = "v1.20.0"
	// ProtobufModulePath and ProtobufModuleVersion identify the generated
	// dynamic-message runtime supported by this CLI build.
	ProtobufModulePath    = "google.golang.org/protobuf"
	ProtobufModuleVersion = "v1.36.11"

	schemaRuntimePath = "generated/go/internal/connectschema/schema_gen.go"
)

var (
	// ErrRender reports that the normalized Connect projection could not
	// produce deterministic compilable handlers.
	ErrRender = errors.New("render generated Connect handlers")
	// ErrProjection reports disagreement among the normalized model, wire map,
	// descriptor graph, and generated invocation plan.
	ErrProjection = errors.New("invalid generated Connect projection")
	// ErrDescriptor reports a missing or inconsistent method descriptor.
	ErrDescriptor = errors.New("invalid generated Connect method descriptor")
)

// File is one immutable generated Connect Go source file.
type File struct {
	path        string
	packageName string
	data        []byte
}

// Path returns the slash-separated application-relative generated path.
func (f File) Path() string { return f.path }

// PackageName returns the generated Go package identifier.
func (f File) PackageName() string { return f.packageName }

// Data returns a defensive copy of the generated source.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Render validates the exact descriptor graph and emits one shared descriptor
// runtime, one canonical handler per exposed Interface, and temporary legacy
// handlers for surfaces that have not yet migrated to an authored Interface.
// Canonical Interface handlers accept the same authored Interface value used by
// in-process callers; Implementation packages are not an input to this renderer.
func Render(
	modulePath string,
	model protobufmodel.Model,
	interfaceModel protobufmodel.InterfaceModel,
	wireMap protobufwiremap.Map,
	descriptorSet []byte,
	plan generationlowering.Plan,
	cors *applicationmeta.HTTPCORS,
	configurationProvenance transportprovenance.Provenance,
) ([]File, error) {
	if !configurationProvenance.Valid() {
		return nil, fmt.Errorf("%w: %w: selected configuration provenance is absent or invalid", ErrRender, ErrProjection)
	}
	if err := modulepath.CheckProject(modulePath); err != nil {
		return nil, fmt.Errorf("%w: %w: invalid application Go Module path %q", ErrRender, ErrProjection, modulePath)
	}
	if !model.Valid() {
		return nil, fmt.Errorf("%w: %w: normalized Protobuf model is absent", ErrRender, ErrProjection)
	}
	if !interfaceModel.Valid() || interfaceModel.Enabled() != model.Enabled() {
		return nil, fmt.Errorf("%w: %w: normalized Interface Protobuf model is absent or disagrees with the selected transport", ErrRender, ErrProjection)
	}
	if !wireMap.Matches(model, interfaceModel) {
		return nil, fmt.Errorf("%w: %w: wire map is absent or does not match the normalized Interface and legacy Protobuf models", ErrRender, ErrProjection)
	}
	if plan.ModulePath() != modulePath {
		return nil, fmt.Errorf("%w: %w: invocation plan module %q does not match %q", ErrRender, ErrProjection, plan.ModulePath(), modulePath)
	}
	var normalizedCORS *applicationmeta.HTTPCORS
	if cors != nil {
		normalized, err := applicationmeta.NormalizeHTTPCORS(*cors)
		if err != nil {
			return nil, fmt.Errorf("%w: %w: selected CORS policy: %v", ErrRender, ErrProjection, err)
		}
		normalizedCORS = &normalized
	}
	operations := model.Operations()
	aliases := model.Aliases()
	interfaceOperations := interfaceModel.Operations()
	if len(operations) == 0 && len(aliases) == 0 && len(interfaceOperations) == 0 {
		return []File{}, nil
	}
	if !model.Enabled() || !interfaceModel.Enabled() {
		return nil, fmt.Errorf("%w: %w: disabled Connect model contains public surfaces", ErrRender, ErrProjection)
	}

	registry, err := descriptorRegistry(descriptorSet)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRender, err)
	}
	interfaceByID := make(map[string]protobufmodel.InterfaceOperation, len(interfaceOperations))
	interfaceWireByID := make(map[string]protobufwiremap.InterfaceProjection, len(interfaceOperations))
	for _, projection := range wireMap.ActiveInterfaces() {
		interfaceWireByID[projection.ID()] = projection
	}
	interfaceMethodByID := make(map[string]protoreflect.MethodDescriptor, len(interfaceOperations))
	for _, operation := range interfaceOperations {
		identifier := operation.ID().String()
		if _, duplicate := interfaceByID[identifier]; duplicate {
			return nil, fmt.Errorf("%w: %w: Interface %s appears more than once", ErrRender, ErrProjection, operation.ID())
		}
		method, err := exactMethod(registry, operation.Identity())
		if err != nil {
			return nil, fmt.Errorf("%w: Interface %s: %w", ErrRender, operation.ID(), err)
		}
		projection, exists := interfaceWireByID[identifier]
		if !exists || projection.ContractDigest() != operation.ContractDigest() {
			return nil, fmt.Errorf("%w: %w: Interface %s has no matching active wire projection", ErrRender, ErrProjection, operation.ID())
		}
		interfaceByID[identifier] = operation
		interfaceMethodByID[identifier] = method
	}
	if len(interfaceWireByID) != len(interfaceOperations) {
		return nil, fmt.Errorf("%w: %w: active Interface wire projection count %d does not match Interface operation count %d", ErrRender, ErrProjection, len(interfaceWireByID), len(interfaceOperations))
	}

	legacyHandlerCount := len(aliases)
	for _, operation := range operations {
		if _, migrated := interfaceByID[operation.ID().String()]; !migrated {
			legacyHandlerCount++
		}
	}
	var errorDetail protoreflect.MessageDescriptor
	if legacyHandlerCount != 0 {
		errorDetail, err = exactErrorDetail(registry)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRender, err)
		}
	}
	wireByID := make(map[string]protobufwiremap.CapabilityProjection, len(operations))
	for _, projection := range wireMap.ActiveCapabilities() {
		wireByID[projection.ID()] = projection
	}
	operationByID := make(map[string]protobufmodel.Operation, len(operations))
	aliasesByTarget := make(map[string][]protobufmodel.Alias, len(operations))
	methodByPublicID := make(map[string]protoreflect.MethodDescriptor, len(operations)+len(aliases))
	for _, operation := range operations {
		identity := operation.Identity()
		method, err := exactMethod(registry, identity)
		if err != nil {
			return nil, fmt.Errorf("%w: Capability %s: %w", ErrRender, operation.ID(), err)
		}
		projection, exists := wireByID[operation.ID().String()]
		if !exists || projection.ContractDigest() != operation.ContractDigest() {
			return nil, fmt.Errorf("%w: %w: Capability %s has no matching active wire projection", ErrRender, ErrProjection, operation.ID())
		}
		operationByID[operation.ID().String()] = operation
		methodByPublicID[identity.PublicID()] = method
	}
	if len(wireByID) != len(operations) {
		return nil, fmt.Errorf("%w: %w: active wire projection count %d does not match canonical operation count %d", ErrRender, ErrProjection, len(wireByID), len(operations))
	}
	for _, alias := range aliases {
		identity := alias.Identity()
		method, err := exactMethod(registry, identity)
		if err != nil {
			return nil, fmt.Errorf("%w: Alias %s: %w", ErrRender, alias.ID(), err)
		}
		target, exists := operationByID[alias.Target().String()]
		if !exists || target.ContractDigest() != alias.TargetContractDigest() {
			return nil, fmt.Errorf("%w: %w: Alias %s has no exact canonical target %s", ErrRender, ErrProjection, alias.ID(), alias.Target())
		}
		methodByPublicID[identity.PublicID()] = method
		aliasesByTarget[alias.Target().String()] = append(aliasesByTarget[alias.Target().String()], alias)
	}

	files := make([]File, 0, 1+len(interfaceOperations)+legacyHandlerCount)
	schemaSource, err := renderSchemaRuntime(descriptorSet)
	if err != nil {
		return nil, fmt.Errorf("%w: descriptor runtime: %w", ErrRender, err)
	}
	files = append(files, File{path: schemaRuntimePath, packageName: "connectschema", data: schemaSource})
	for _, operation := range interfaceOperations {
		file, err := renderInterfaceCanonical(
			modulePath,
			operation,
			aliasesByTarget[operation.ID().String()],
			interfaceWireByID[operation.ID().String()],
			interfaceMethodByID[operation.ID().String()],
			normalizedCORS,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: Interface %s: %w", ErrRender, operation.ID(), err)
		}
		files = append(files, file)
	}
	for _, operation := range operations {
		if _, migrated := interfaceByID[operation.ID().String()]; migrated {
			continue
		}
		wire := wireByID[operation.ID().String()]
		file, err := renderCanonical(modulePath, operation, aliasesByTarget[operation.ID().String()], wire, methodByPublicID[operation.ID().String()], errorDetail, plan.RequiresHTTPPath(operation.ID()), normalizedCORS)
		if err != nil {
			return nil, fmt.Errorf("%w: Capability %s: %w", ErrRender, operation.ID(), err)
		}
		files = append(files, file)
	}
	for _, alias := range aliases {
		target := operationByID[alias.Target().String()]
		file, err := renderAlias(modulePath, alias, target, methodByPublicID[alias.ID().String()], normalizedCORS)
		if err != nil {
			return nil, fmt.Errorf("%w: Alias %s: %w", ErrRender, alias.ID(), err)
		}
		files = append(files, file)
	}
	sort.Slice(files, func(left, right int) bool { return files[left].path < files[right].path })
	for index := 1; index < len(files); index++ {
		if files[index-1].path == files[index].path {
			return nil, fmt.Errorf("%w: %w: generated path %q is duplicated", ErrRender, ErrProjection, files[index].path)
		}
	}
	return files, nil
}

func exactErrorDetail(registry *protoregistry.Files) (protoreflect.MessageDescriptor, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: descriptor registry is absent", ErrDescriptor)
	}
	descriptor, err := registry.FindDescriptorByName(protoreflect.FullName(protobufdescriptor.ErrorDetailFullName))
	if err != nil {
		return nil, fmt.Errorf("%w: safe error detail %s is absent", ErrDescriptor, protobufdescriptor.ErrorDetailFullName)
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok || message == nil || message.Fields().Len() != 5 {
		return nil, fmt.Errorf("%w: safe error detail %s is inconsistent", ErrDescriptor, protobufdescriptor.ErrorDetailFullName)
	}
	want := []struct {
		name   protoreflect.Name
		number protoreflect.FieldNumber
	}{
		{name: "requested_capability_id", number: 1},
		{name: "canonical_capability_id", number: 2},
		{name: "semantic_error_code", number: 3},
		{name: "kernel_error_class", number: 4},
		{name: "trace_id", number: 5},
	}
	for _, field := range want {
		descriptor := message.Fields().ByNumber(field.number)
		if descriptor == nil || descriptor.Name() != field.name || descriptor.Kind() != protoreflect.StringKind || descriptor.Cardinality() != protoreflect.Optional || descriptor.HasPresence() {
			return nil, fmt.Errorf("%w: safe error detail field %s is inconsistent", ErrDescriptor, field.name)
		}
	}
	return message, nil
}

func descriptorRegistry(data []byte) (*protoregistry.Files, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: descriptor set is empty", ErrDescriptor)
	}
	var set descriptorpb.FileDescriptorSet
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("%w: decode descriptor set: %v", ErrDescriptor, err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return nil, fmt.Errorf("%w: construct descriptor graph: %v", ErrDescriptor, err)
	}
	return files, nil
}

func exactMethod(registry *protoregistry.Files, identity interface {
	Package() string
	Service() string
	Method() string
	RequestType() string
	ResponseType() string
}) (protoreflect.MethodDescriptor, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: descriptor registry is absent", ErrDescriptor)
	}
	name := protoreflect.FullName(identity.Package() + "." + identity.Service() + "." + identity.Method())
	descriptor, err := registry.FindDescriptorByName(name)
	if err != nil {
		return nil, fmt.Errorf("%w: method %s is absent", ErrDescriptor, name)
	}
	method, ok := descriptor.(protoreflect.MethodDescriptor)
	if !ok || method == nil || method.Parent() == nil || method.Parent().FullName() != protoreflect.FullName(identity.Package()+"."+identity.Service()) {
		return nil, fmt.Errorf("%w: descriptor %s is not the expected method", ErrDescriptor, name)
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return nil, fmt.Errorf("%w: method %s is not unary", ErrDescriptor, name)
	}
	if method.Input() == nil || string(method.Input().FullName()) != identity.RequestType() {
		return nil, fmt.Errorf("%w: method %s request is %q instead of %q", ErrDescriptor, name, method.Input().FullName(), identity.RequestType())
	}
	if method.Output() == nil || string(method.Output().FullName()) != identity.ResponseType() {
		return nil, fmt.Errorf("%w: method %s response is %q instead of %q", ErrDescriptor, name, method.Output().FullName(), identity.ResponseType())
	}
	return method, nil
}

type preparedField struct {
	field    sdkmodel.Field
	wire     protobufwiremap.FieldProjection
	enum     protobufwiremap.EnumProjection
	goName   string
	enumType string
}

func prepareFields(section string, fields []sdkmodel.Field, wire protobufwiremap.MessageProjection) ([]preparedField, error) {
	wireFields := make(map[string]protobufwiremap.FieldProjection, len(wire.Fields()))
	for _, value := range wire.Fields() {
		wireFields[value.CanonicalName()] = value
	}
	wireEnums := make(map[string]protobufwiremap.EnumProjection, len(wire.Enums()))
	for _, value := range wire.Enums() {
		wireEnums[value.CanonicalField()] = value
	}
	if len(wireFields) != len(fields) {
		return nil, fmt.Errorf("%w: %s wire field count %d does not match canonical field count %d", ErrProjection, strings.ToLower(section), len(wireFields), len(fields))
	}
	result := make([]preparedField, len(fields))
	goNames := make(map[string]string, len(fields))
	for index, field := range fields {
		assignment, exists := wireFields[field.Name()]
		if !exists || assignment.Number() <= 0 || assignment.Name() == "" {
			return nil, fmt.Errorf("%w: %s field %q has no valid wire assignment", ErrProjection, strings.ToLower(section), field.Name())
		}
		goName := goname.Field(field.Name())
		if previous, duplicate := goNames[goName]; duplicate {
			return nil, fmt.Errorf("%w: %s fields %q and %q both generate %s", ErrProjection, strings.ToLower(section), previous, field.Name(), goName)
		}
		goNames[goName] = field.Name()
		enum := wireEnums[field.Name()]
		if len(field.EnumJSON()) != 0 {
			if enum.Identity() == "" || enum.Kind() != field.Kind() || len(enum.Members()) != len(field.EnumJSON()) {
				return nil, fmt.Errorf("%w: %s enum field %q has inconsistent wire history", ErrProjection, strings.ToLower(section), field.Name())
			}
			members := make(map[string]int, len(enum.Members()))
			for _, member := range enum.Members() {
				canonical := string(member.CanonicalJSON())
				if canonical == "" || member.Number() <= 0 {
					return nil, fmt.Errorf("%w: %s enum field %q has an invalid wire member", ErrProjection, strings.ToLower(section), field.Name())
				}
				if _, duplicate := members[canonical]; duplicate {
					return nil, fmt.Errorf("%w: %s enum field %q repeats wire member %s", ErrProjection, strings.ToLower(section), field.Name(), canonical)
				}
				members[canonical] = member.Number()
			}
			for _, value := range field.EnumJSON() {
				if _, exists := members[string(value)]; !exists {
					return nil, fmt.Errorf("%w: %s enum field %q has no wire assignment for %s", ErrProjection, strings.ToLower(section), field.Name(), value)
				}
			}
		} else if enum.Identity() != "" {
			return nil, fmt.Errorf("%w: %s non-enum field %q has active enum wire history", ErrProjection, strings.ToLower(section), field.Name())
		}
		result[index] = preparedField{
			field:    field,
			wire:     assignment,
			enum:     enum,
			goName:   goName,
			enumType: section + goName,
		}
	}
	return result, nil
}

func generatedDirectory(identifier generation.CapabilityID) (string, error) {
	parsed, err := capabilityid.Parse(identifier.String())
	if err != nil {
		return "", fmt.Errorf("%w: Capability ID %q is not canonical", ErrProjection, identifier.String())
	}
	components := append([]string{"generated", "go", "adapters", "connect"}, strings.Split(parsed.Name(), ".")...)
	components = append(components, "v"+fmt.Sprint(parsed.Major()))
	return path.Join(components...), nil
}

func methodName(method protoreflect.MethodDescriptor) string {
	if method == nil || method.Parent() == nil {
		return ""
	}
	return string(method.Parent().FullName()) + "." + string(method.Name())
}

func runtimeSchemaImport(modulePath string) string {
	return path.Join(modulePath, "generated/go/internal/connectschema")
}
