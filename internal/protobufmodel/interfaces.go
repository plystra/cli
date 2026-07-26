package protobufmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/protobufidentity"
)

const (
	maximumProtobufFieldNumber uint64 = 536870911
	reservedFieldNumberStart   uint64 = 19000
	reservedFieldNumberEnd     uint64 = 19999
)

var (
	// ErrInterfaceBuild reports invalid or internally inconsistent authored
	// Interface projection input.
	ErrInterfaceBuild = errors.New("build Interface Protobuf projection model")
	// ErrInterfaceInput reports one invalid canonical Interface definition.
	ErrInterfaceInput = errors.New("invalid Interface Protobuf projection input")
)

// InterfaceInput contains the canonical authored facts required to project one
// externally exposed Interface. It contains no Implementation or runtime state.
type InterfaceInput struct {
	InterfaceID    interfaceid.Identifier
	PackagePath    string
	Source         string
	MetadataSource string
	Contract       interfacecontract.Contract
	ContractDigest string
	SemanticErrors []string
}

// InterfaceField is one immutable authored Go field plus its deterministic
// Protobuf and ProtoJSON identities.
type InterfaceField struct {
	goName       string
	protobufName string
	jsonName     string
	number       uint64
	required     bool
	fieldType    interfacecontract.Type
}

// GoName returns the exported authored Go field name.
func (f InterfaceField) GoName() string { return f.goName }

// ProtobufName returns the deterministic lower-snake-case Protobuf field name.
func (f InterfaceField) ProtobufName() string { return f.protobufName }

// JSONName returns the exact effective JSON field name owned by the Go contract.
func (f InterfaceField) JSONName() string { return f.jsonName }

// Number returns the exact stable authored field number.
func (f InterfaceField) Number() uint64 { return f.number }

// Required reports the authored required-field marker.
func (f InterfaceField) Required() bool { return f.required }

// Type returns the exact normalized closed-graph Go field type.
func (f InterfaceField) Type() interfacecontract.Type { return f.fieldType }

// InterfaceMessage is one immutable canonical Go message and its generated
// Protobuf message identity.
type InterfaceMessage struct {
	goName       string
	protobufName string
	fields       []InterfaceField
}

// GoName returns the exported authored Go message type name.
func (m InterfaceMessage) GoName() string { return m.goName }

// ProtobufName returns the unqualified generated Protobuf message name.
func (m InterfaceMessage) ProtobufName() string { return m.protobufName }

// Fields returns a defensive field-number-ordered view.
func (m InterfaceMessage) Fields() []InterfaceField {
	return append([]InterfaceField(nil), m.fields...)
}

// InterfaceOperation is one immutable exposed authored Interface contract.
type InterfaceOperation struct {
	id             interfaceid.Identifier
	packagePath    string
	source         string
	metadataSource string
	contractDigest string
	identity       protobufidentity.Identity
	methodName     string
	requestGoName  string
	responseGoName string
	messages       []InterfaceMessage
	messageNames   map[string]string
	semanticErrors []string
}

// ID returns the exact canonical Interface ID.
func (o InterfaceOperation) ID() interfaceid.Identifier { return o.id }

// PackagePath returns the authoritative Go Interface package import path.
func (o InterfaceOperation) PackagePath() string { return o.packagePath }

// Source returns stable module-qualified Go declaration provenance.
func (o InterfaceOperation) Source() string { return o.source }

// MetadataSource returns optional stable Interface metadata provenance.
func (o InterfaceOperation) MetadataSource() string { return o.metadataSource }

// ContractDigest returns the exact normalized authored Interface digest.
func (o InterfaceOperation) ContractDigest() string { return o.contractDigest }

// Identity returns the deterministic Protobuf package and message identity.
func (o InterfaceOperation) Identity() protobufidentity.Identity { return o.identity }

// MethodName returns the authored single-operation Go method name.
func (o InterfaceOperation) MethodName() string { return o.methodName }

// RequestGoName returns the authored request Go type name.
func (o InterfaceOperation) RequestGoName() string { return o.requestGoName }

// ResponseGoName returns the authored response Go type name.
func (o InterfaceOperation) ResponseGoName() string { return o.responseGoName }

// Messages returns every projected message in generated-name order.
func (o InterfaceOperation) Messages() []InterfaceMessage {
	result := make([]InterfaceMessage, len(o.messages))
	for index, message := range o.messages {
		result[index] = InterfaceMessage{
			goName:       message.goName,
			protobufName: message.protobufName,
			fields:       append([]InterfaceField(nil), message.fields...),
		}
	}
	return result
}

// ProtobufMessageName resolves one canonical same-package Go message name.
func (o InterfaceOperation) ProtobufMessageName(goName string) (string, bool) {
	value, exists := o.messageNames[goName]
	return value, exists
}

// SemanticErrors returns deterministic declared Interface error codes.
func (o InterfaceOperation) SemanticErrors() []string {
	return append([]string(nil), o.semanticErrors...)
}

// InterfaceModel is the immutable normalized canonical-Interface portion of
// the selected Protobuf projection. A disabled or empty model remains valid.
type InterfaceModel struct {
	enabled              bool
	operations           []InterfaceOperation
	historyOperations    []InterfaceOperation
	canonicalJSON        []byte
	historyCanonicalJSON []byte
	digest               string
	historyDigest        string
	prepared             bool
}

// Valid reports whether BuildInterfaces produced the model.
func (m InterfaceModel) Valid() bool {
	return m.prepared &&
		len(m.canonicalJSON) != 0 &&
		len(m.historyCanonicalJSON) != 0 &&
		validInterfaceDigest(m.digest) &&
		validInterfaceDigest(m.historyDigest)
}

// Enabled reports whether Connect projection was selected.
func (m InterfaceModel) Enabled() bool { return m.enabled }

// Operations returns exposed Interface operations in canonical ID order.
func (m InterfaceModel) Operations() []InterfaceOperation {
	result := make([]InterfaceOperation, len(m.operations))
	for index, operation := range m.operations {
		result[index] = cloneInterfaceOperation(operation)
	}
	return result
}

// HistoryOperations returns every visible authored Interface plus any active
// intrinsic Interface in canonical ID order. Only Operations participate in
// generated transport output.
func (m InterfaceModel) HistoryOperations() []InterfaceOperation {
	result := make([]InterfaceOperation, len(m.historyOperations))
	for index, operation := range m.historyOperations {
		result[index] = cloneInterfaceOperation(operation)
	}
	return result
}

// CanonicalJSON returns defensive deterministic projection evidence.
func (m InterfaceModel) CanonicalJSON() []byte {
	return append([]byte(nil), m.canonicalJSON...)
}

// Digest returns the SHA-256 digest of CanonicalJSON.
func (m InterfaceModel) Digest() string { return m.digest }

// HistoryCanonicalJSON returns defensive deterministic all-visible Interface
// history input. It is compatibility evidence, not active transport output.
func (m InterfaceModel) HistoryCanonicalJSON() []byte {
	return append([]byte(nil), m.historyCanonicalJSON...)
}

// HistoryDigest returns the SHA-256 digest of HistoryCanonicalJSON.
func (m InterfaceModel) HistoryDigest() string { return m.historyDigest }

type canonicalInterfaceModel struct {
	Version    int                           `json:"version"`
	Enabled    bool                          `json:"enabled"`
	Interfaces []canonicalInterfaceOperation `json:"interfaces"`
}

type canonicalInterfaceHistoryModel struct {
	Version    int                           `json:"version"`
	Interfaces []canonicalInterfaceOperation `json:"interfaces"`
}

type canonicalInterfaceOperation struct {
	InterfaceID    string                      `json:"interface_id"`
	PackagePath    string                      `json:"package_path"`
	Source         string                      `json:"source"`
	MetadataSource string                      `json:"metadata_source,omitempty"`
	ContractDigest string                      `json:"contract_digest"`
	Package        string                      `json:"protobuf_package"`
	RequestType    string                      `json:"request_type"`
	ResponseType   string                      `json:"response_type"`
	MethodName     string                      `json:"method_name"`
	Messages       []canonicalInterfaceMessage `json:"messages"`
	SemanticErrors []string                    `json:"semantic_errors"`
}

type canonicalInterfaceMessage struct {
	GoName       string                    `json:"go_name"`
	ProtobufName string                    `json:"protobuf_name"`
	Fields       []canonicalInterfaceField `json:"fields"`
}

type canonicalInterfaceField struct {
	GoName       string `json:"go_name"`
	ProtobufName string `json:"protobuf_name"`
	JSONName     string `json:"json_name"`
	Number       uint64 `json:"number"`
	Required     bool   `json:"required"`
	Type         string `json:"type"`
}

// BuildInterfaces validates and normalizes exposed canonical Interface
// contracts independently of discovery order. Inputs may come from authored
// Project packages or exact intrinsic Kernel packages selected for this model.
func BuildInterfaces(connect bool, inputs []InterfaceInput) (InterfaceModel, error) {
	if !connect {
		return finalizeInterfaceSelection(false, nil, nil)
	}
	return BuildInterfaceSelection(true, inputs, inputs)
}

// BuildInterfaceSelection validates active Connect Interfaces and the broader
// all-visible history set independently of discovery order. Active Interfaces
// must appear identically in history. A disabled Connect selection retains
// history while producing no active transport operations.
func BuildInterfaceSelection(
	connect bool,
	activeInputs []InterfaceInput,
	historyInputs []InterfaceInput,
) (InterfaceModel, error) {
	history, err := normalizeInterfaceInputs(historyInputs)
	if err != nil {
		return InterfaceModel{}, err
	}
	var active []InterfaceOperation
	if connect {
		active, err = normalizeInterfaceInputs(activeInputs)
		if err != nil {
			return InterfaceModel{}, err
		}
	}
	if err := validateActiveInterfaceHistory(active, history); err != nil {
		return InterfaceModel{}, fmt.Errorf("%w: %w: %v", ErrInterfaceBuild, ErrInterfaceInput, err)
	}
	return finalizeInterfaceSelection(connect, active, history)
}

func normalizeInterfaceInputs(inputs []InterfaceInput) ([]InterfaceOperation, error) {
	ordered := append([]InterfaceInput(nil), inputs...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].InterfaceID != ordered[right].InterfaceID {
			return ordered[left].InterfaceID.String() < ordered[right].InterfaceID.String()
		}
		return ordered[left].Source < ordered[right].Source
	})
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].InterfaceID == ordered[index].InterfaceID {
			return nil, fmt.Errorf("%w: %w: Interface %s appears more than once at %s and %s", ErrInterfaceBuild, ErrInterfaceInput, ordered[index].InterfaceID, ordered[index-1].Source, ordered[index].Source)
		}
	}

	surfaces := make([]protobufidentity.Surface, len(ordered))
	for index, input := range ordered {
		if input.InterfaceID.String() == "" {
			return nil, fmt.Errorf("%w: %w: inputs[%d] has an empty Interface ID", ErrInterfaceBuild, ErrInterfaceInput, index)
		}
		surfaces[index] = protobufidentity.Surface{
			PublicID:    input.InterfaceID.String(),
			CanonicalID: input.InterfaceID.String(),
		}
	}
	identities, err := protobufidentity.Build(surfaces)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInterfaceBuild, err)
	}
	identityByID := make(map[string]protobufidentity.Identity, len(ordered))
	for _, identity := range identities.Identities() {
		identityByID[identity.PublicID()] = identity
	}

	operations := make([]InterfaceOperation, len(ordered))
	for index, input := range ordered {
		operation, err := normalizeInterfaceInput(input, identityByID[input.InterfaceID.String()])
		if err != nil {
			return nil, fmt.Errorf("%w: %w: inputs[%d] Interface %s: %v", ErrInterfaceBuild, ErrInterfaceInput, index, input.InterfaceID, err)
		}
		operations[index] = operation
	}
	return operations, nil
}

func normalizeInterfaceInput(input InterfaceInput, identity protobufidentity.Identity) (InterfaceOperation, error) {
	contract := input.Contract
	if contract.ID() != input.InterfaceID {
		return InterfaceOperation{}, fmt.Errorf("contract identity %s does not match input", contract.ID())
	}
	if input.PackagePath == "" || contract.PackagePath() != input.PackagePath {
		return InterfaceOperation{}, fmt.Errorf("contract package %q does not match input package %q", contract.PackagePath(), input.PackagePath)
	}
	if identity.PublicID() != input.InterfaceID.String() || identity.CanonicalID() != input.InterfaceID.String() {
		return InterfaceOperation{}, errors.New("generated Interface identity is absent or inconsistent")
	}
	if err := validateInterfaceSource(input.Source); err != nil {
		return InterfaceOperation{}, fmt.Errorf("source: %v", err)
	}
	if input.MetadataSource != "" {
		if err := validateInterfaceSource(input.MetadataSource); err != nil {
			return InterfaceOperation{}, fmt.Errorf("metadata source: %v", err)
		}
	}
	if !validInterfaceDigest(input.ContractDigest) {
		return InterfaceOperation{}, errors.New("contract digest must be a lower-case SHA-256 digest")
	}
	if contract.RequestName() == contract.ResponseName() {
		return InterfaceOperation{}, fmt.Errorf("request and response must use distinct exported Go message types, both use %s", contract.RequestName())
	}

	requestType, err := unqualifiedIdentity(identity.Package(), identity.RequestType())
	if err != nil {
		return InterfaceOperation{}, fmt.Errorf("request identity: %v", err)
	}
	responseType, err := unqualifiedIdentity(identity.Package(), identity.ResponseType())
	if err != nil {
		return InterfaceOperation{}, fmt.Errorf("response identity: %v", err)
	}
	base := strings.TrimSuffix(requestType, "Request")
	messageNames := make(map[string]string, len(contract.Messages()))
	messageNames[contract.RequestName()] = requestType
	messageNames[contract.ResponseName()] = responseType
	for _, message := range contract.Messages() {
		if _, root := messageNames[message.Name()]; root {
			continue
		}
		messageNames[message.Name()] = base + message.Name()
	}

	messages := make([]InterfaceMessage, 0, len(contract.Messages()))
	protobufOwners := make(map[string]string, len(contract.Messages()))
	for _, message := range contract.Messages() {
		protobufName, exists := messageNames[message.Name()]
		if !exists || protobufName == "" {
			return InterfaceOperation{}, fmt.Errorf("message %s has no generated identity", message.Name())
		}
		if previous, duplicate := protobufOwners[protobufName]; duplicate {
			return InterfaceOperation{}, fmt.Errorf("messages %s and %s produce duplicate Protobuf name %s", previous, message.Name(), protobufName)
		}
		protobufOwners[protobufName] = message.Name()
		fields, err := normalizeInterfaceFields(message, messageNames)
		if err != nil {
			return InterfaceOperation{}, err
		}
		messages = append(messages, InterfaceMessage{
			goName:       message.Name(),
			protobufName: protobufName,
			fields:       fields,
		})
	}
	sort.Slice(messages, func(left, right int) bool {
		return messages[left].protobufName < messages[right].protobufName
	})

	errors := append([]string(nil), input.SemanticErrors...)
	sort.Strings(errors)
	for index, code := range errors {
		if code == "" || !utf8.ValidString(code) || strings.ContainsAny(code, "\x00\r\n") {
			return InterfaceOperation{}, fmt.Errorf("semantic_errors[%d] is invalid", index)
		}
		if index > 0 && errors[index-1] == code {
			return InterfaceOperation{}, fmt.Errorf("semantic_errors duplicates %q", code)
		}
	}
	return InterfaceOperation{
		id:             input.InterfaceID,
		packagePath:    input.PackagePath,
		source:         input.Source,
		metadataSource: input.MetadataSource,
		contractDigest: input.ContractDigest,
		identity:       identity,
		methodName:     contract.MethodName(),
		requestGoName:  contract.RequestName(),
		responseGoName: contract.ResponseName(),
		messages:       messages,
		messageNames:   messageNames,
		semanticErrors: errors,
	}, nil
}

func normalizeInterfaceFields(message interfacecontract.Message, messageNames map[string]string) ([]InterfaceField, error) {
	fields := message.Fields()
	result := make([]InterfaceField, len(fields))
	protobufOwners := make(map[string]string, len(fields))
	for index, field := range fields {
		protobufName, err := goFieldToProtobuf(field.Name())
		if err != nil {
			return nil, fmt.Errorf("message %s field %s: %v", message.Name(), field.Name(), err)
		}
		if previous, duplicate := protobufOwners[protobufName]; duplicate {
			return nil, fmt.Errorf("message %s fields %s and %s produce duplicate Protobuf name %s", message.Name(), previous, field.Name(), protobufName)
		}
		protobufOwners[protobufName] = field.Name()
		number := field.Number()
		if number > maximumProtobufFieldNumber || number >= reservedFieldNumberStart && number <= reservedFieldNumberEnd {
			return nil, fmt.Errorf("message %s field %s number %d is outside the available Protobuf field-number space", message.Name(), field.Name(), number)
		}
		if err := validateInterfaceType(field.Type(), messageNames); err != nil {
			return nil, fmt.Errorf("message %s field %s: %v", message.Name(), field.Name(), err)
		}
		jsonName := field.Name()
		if field.HasExplicitJSONName() {
			jsonName = field.JSONName()
		}
		result[index] = InterfaceField{
			goName:       field.Name(),
			protobufName: protobufName,
			jsonName:     jsonName,
			number:       number,
			required:     field.Required(),
			fieldType:    field.Type(),
		}
	}
	return result, nil
}

func validateInterfaceType(value interfacecontract.Type, messageNames map[string]string) error {
	switch value.Kind() {
	case interfacecontract.TypeBoolean,
		interfacecontract.TypeString,
		interfacecontract.TypeInt32,
		interfacecontract.TypeInt64,
		interfacecontract.TypeUint32,
		interfacecontract.TypeUint64,
		interfacecontract.TypeFloat32,
		interfacecontract.TypeFloat64,
		interfacecontract.TypeBytes,
		interfacecontract.TypeTimestamp,
		interfacecontract.TypeDuration:
		return nil
	case interfacecontract.TypeRepeated:
		element, exists := value.Element()
		if !exists {
			return errors.New("repeated type has no element")
		}
		return validateInterfaceType(element, messageNames)
	case interfacecontract.TypeMap:
		key, keyExists := value.Key()
		mapValue, valueExists := value.Value()
		if !keyExists || !valueExists {
			return errors.New("map type has no key or value")
		}
		if err := validateInterfaceType(key, messageNames); err != nil {
			return fmt.Errorf("map key: %v", err)
		}
		return validateInterfaceType(mapValue, messageNames)
	case interfacecontract.TypeMessage:
		name, exists := value.MessageName()
		if !exists || messageNames[name] == "" {
			return errors.New("message type has no canonical generated identity")
		}
		return nil
	default:
		return fmt.Errorf("unsupported canonical Interface type %q", value.Kind())
	}
}

func validateActiveInterfaceHistory(active, history []InterfaceOperation) error {
	historyByID := make(map[string]canonicalInterfaceOperation, len(history))
	for _, operation := range history {
		historyByID[operation.ID().String()] = canonicalizeInterfaceOperation(operation)
	}
	for _, operation := range active {
		identifier := operation.ID().String()
		historical, exists := historyByID[identifier]
		if !exists {
			return fmt.Errorf("active Interface %s is absent from all-visible history", identifier)
		}
		current := canonicalizeInterfaceOperation(operation)
		currentJSON, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("encode active Interface %s: %v", identifier, err)
		}
		historyJSON, err := json.Marshal(historical)
		if err != nil {
			return fmt.Errorf("encode historical Interface %s: %v", identifier, err)
		}
		if string(currentJSON) != string(historyJSON) {
			return fmt.Errorf("active Interface %s differs from its all-visible history input", identifier)
		}
	}
	return nil
}

func finalizeInterfaceSelection(
	enabled bool,
	operations []InterfaceOperation,
	historyOperations []InterfaceOperation,
) (InterfaceModel, error) {
	document := canonicalInterfaceModel{
		Version:    1,
		Enabled:    enabled,
		Interfaces: make([]canonicalInterfaceOperation, len(operations)),
	}
	for index, operation := range operations {
		document.Interfaces[index] = canonicalizeInterfaceOperation(operation)
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return InterfaceModel{}, fmt.Errorf("%w: encode canonical model: %v", ErrInterfaceBuild, err)
	}
	sum := sha256.Sum256(canonical)
	historyDocument := canonicalInterfaceHistoryModel{
		Version:    1,
		Interfaces: make([]canonicalInterfaceOperation, len(historyOperations)),
	}
	for index, operation := range historyOperations {
		historyDocument.Interfaces[index] = canonicalizeInterfaceOperation(operation)
	}
	historyCanonical, err := json.Marshal(historyDocument)
	if err != nil {
		return InterfaceModel{}, fmt.Errorf("%w: encode canonical history model: %v", ErrInterfaceBuild, err)
	}
	historySum := sha256.Sum256(historyCanonical)
	return InterfaceModel{
		enabled:              enabled,
		operations:           cloneInterfaceOperations(operations),
		historyOperations:    cloneInterfaceOperations(historyOperations),
		canonicalJSON:        canonical,
		historyCanonicalJSON: historyCanonical,
		digest:               "sha256:" + hex.EncodeToString(sum[:]),
		historyDigest:        "sha256:" + hex.EncodeToString(historySum[:]),
		prepared:             true,
	}, nil
}

func canonicalizeInterfaceOperation(operation InterfaceOperation) canonicalInterfaceOperation {
	messages := operation.Messages()
	messageRecords := make([]canonicalInterfaceMessage, len(messages))
	for messageIndex, message := range messages {
		fields := message.Fields()
		fieldRecords := make([]canonicalInterfaceField, len(fields))
		for fieldIndex, field := range fields {
			fieldRecords[fieldIndex] = canonicalInterfaceField{
				GoName:       field.GoName(),
				ProtobufName: field.ProtobufName(),
				JSONName:     field.JSONName(),
				Number:       field.Number(),
				Required:     field.Required(),
				Type:         field.Type().Canonical(),
			}
		}
		messageRecords[messageIndex] = canonicalInterfaceMessage{
			GoName:       message.GoName(),
			ProtobufName: message.ProtobufName(),
			Fields:       fieldRecords,
		}
	}
	identity := operation.Identity()
	return canonicalInterfaceOperation{
		InterfaceID:    operation.ID().String(),
		PackagePath:    operation.PackagePath(),
		Source:         operation.Source(),
		MetadataSource: operation.MetadataSource(),
		ContractDigest: operation.ContractDigest(),
		Package:        identity.Package(),
		RequestType:    identity.RequestType(),
		ResponseType:   identity.ResponseType(),
		MethodName:     operation.MethodName(),
		Messages:       messageRecords,
		SemanticErrors: operation.SemanticErrors(),
	}
}

func cloneInterfaceOperations(values []InterfaceOperation) []InterfaceOperation {
	result := make([]InterfaceOperation, len(values))
	for index, operation := range values {
		result[index] = cloneInterfaceOperation(operation)
	}
	return result
}

func cloneInterfaceOperation(operation InterfaceOperation) InterfaceOperation {
	messageNames := make(map[string]string, len(operation.messageNames))
	for name, generated := range operation.messageNames {
		messageNames[name] = generated
	}
	messages := operation.Messages()
	return InterfaceOperation{
		id:             operation.id,
		packagePath:    operation.packagePath,
		source:         operation.source,
		metadataSource: operation.metadataSource,
		contractDigest: operation.contractDigest,
		identity:       operation.identity,
		methodName:     operation.methodName,
		requestGoName:  operation.requestGoName,
		responseGoName: operation.responseGoName,
		messages:       messages,
		messageNames:   messageNames,
		semanticErrors: operation.SemanticErrors(),
	}
}

func goFieldToProtobuf(value string) (string, error) {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return "", errors.New("exported Go field name must start with an ASCII upper-case letter")
	}
	var output strings.Builder
	for index := 0; index < len(value); index++ {
		current := value[index]
		if !isASCIIAlphaNumeric(current) {
			return "", fmt.Errorf("Go field name %q cannot be represented by the initial Protobuf identifier mapping", value)
		}
		upper := current >= 'A' && current <= 'Z'
		if upper && index > 0 {
			previous := value[index-1]
			nextLower := lowerRunLength(value[index+1:]) > 1
			if previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9' || previous >= 'A' && previous <= 'Z' && nextLower {
				output.WriteByte('_')
			}
		}
		if upper {
			current += 'a' - 'A'
		}
		output.WriteByte(current)
	}
	return output.String(), nil
}

func lowerRunLength(value string) int {
	length := 0
	for length < len(value) && value[length] >= 'a' && value[length] <= 'z' {
		length++
	}
	return length
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func unqualifiedIdentity(packageName, value string) (string, error) {
	prefix := packageName + "."
	name, found := strings.CutPrefix(value, prefix)
	if !found || name == "" || strings.Contains(name, ".") {
		return "", fmt.Errorf("identity %q is not directly inside package %q", value, packageName)
	}
	return name, nil
}

func validateInterfaceSource(value string) error {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("provenance must be one bounded nonempty line")
	}
	return nil
}

func validInterfaceDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}
