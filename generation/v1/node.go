package generation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maximumGeneratedNodes         = 4096
	maximumGeneratedBindings      = 256
	maximumGeneratedStringBytes   = 4096
	maximumGeneratedContextBytes  = 64 << 10
	maximumGeneratedMetadataBytes = 4096
	maximumGeneratedTimeoutMillis = 5 * 60 * 1000
)

// GeneratedNodeKind identifies one closed CLI-owned generation operation.
type GeneratedNodeKind string

const (
	GeneratedNodeKindCapabilityCall     GeneratedNodeKind = "capability-call"
	GeneratedNodeKindContextDerivation  GeneratedNodeKind = "context-derivation"
	GeneratedNodeKindConditionalFailure GeneratedNodeKind = "conditional-failure"
	GeneratedNodeKindMetadataAttachment GeneratedNodeKind = "metadata-attachment"
	GeneratedNodeKindAuditEventCall     GeneratedNodeKind = "audit-event-call"
)

// GeneratedNode is one ordered structured operation. Exactly one operation
// member must be present. Extensions cannot supply source text, file paths, or
// arbitrary JSON through this protocol.
type GeneratedNode struct {
	ID                 string                       `json:"id"`
	CapabilityCall     *GeneratedCapabilityCall     `json:"capability_call,omitempty"`
	ContextDerivation  *GeneratedContextDerivation  `json:"context_derivation,omitempty"`
	ConditionalFailure *GeneratedConditionalFailure `json:"conditional_failure,omitempty"`
	MetadataAttachment *GeneratedMetadataAttachment `json:"metadata_attachment,omitempty"`
	AuditEventCall     *GeneratedAuditEventCall     `json:"audit_event_call,omitempty"`
	_                  struct{}
}

// GeneratedCallErrorMode defines explicit generated call failure behavior.
type GeneratedCallErrorMode string

const (
	// GeneratedCallFailClosed stops the current generated invocation when the
	// call fails.
	GeneratedCallFailClosed GeneratedCallErrorMode = "fail-closed"
	// GeneratedCallCapture exposes the call error to a later conditional-failure
	// node. A captured error must be consumed before the response is used.
	GeneratedCallCapture GeneratedCallErrorMode = "capture"
	// GeneratedCallContinue permits an explicit audit-event failure to be
	// recorded by runtime telemetry without failing the business invocation.
	GeneratedCallContinue GeneratedCallErrorMode = "continue"
)

// GeneratedCapabilityCall invokes one exact required canonical Capability.
// Request bindings are checked against its canonical request schema.
type GeneratedCapabilityCall struct {
	Capability          CapabilityID            `json:"capability"`
	Request             []GeneratedFieldBinding `json:"request"`
	TimeoutMilliseconds uint32                  `json:"timeout_ms"`
	OnError             GeneratedCallErrorMode  `json:"on_error"`
	_                   struct{}
}

// GeneratedContextPresence defines whether a derived value may be absent.
type GeneratedContextPresence string

const (
	GeneratedContextRequired GeneratedContextPresence = "required"
	GeneratedContextOptional GeneratedContextPresence = "optional"
)

// GeneratedValueType is one supported canonical Capability value type.
type GeneratedValueType string

const (
	GeneratedValueString  GeneratedValueType = "string"
	GeneratedValueInteger GeneratedValueType = "integer"
	GeneratedValueNumber  GeneratedValueType = "number"
	GeneratedValueBoolean GeneratedValueType = "boolean"
	GeneratedValueObject  GeneratedValueType = "object"
	GeneratedValueArray   GeneratedValueType = "array"
)

// GeneratedContextDerivation validates a value and stores it under one
// namespaced generated invocation-context key. MaximumBytes is mandatory,
// invalid present values fail closed, and Required also fails when absent.
// Sensitive may strengthen classification; credential-derived values are
// always normalized as sensitive.
type GeneratedContextDerivation struct {
	Key          string                   `json:"key"`
	Value        GeneratedValue           `json:"value"`
	Type         GeneratedValueType       `json:"type"`
	Items        GeneratedValueType       `json:"items,omitempty"`
	Presence     GeneratedContextPresence `json:"presence"`
	Sensitive    bool                     `json:"sensitive,omitempty"`
	MaximumBytes uint32                   `json:"maximum_bytes"`
	_            struct{}
}

// GeneratedConditionOperator is one closed fail-condition predicate.
type GeneratedConditionOperator string

const (
	GeneratedConditionMissing GeneratedConditionOperator = "is-missing"
	GeneratedConditionPresent GeneratedConditionOperator = "is-present"
	GeneratedConditionTrue    GeneratedConditionOperator = "is-true"
	GeneratedConditionFalse   GeneratedConditionOperator = "is-false"
	GeneratedConditionError   GeneratedConditionOperator = "is-error"
)

// GeneratedCondition evaluates one typed value without admitting an
// extension-defined expression language.
type GeneratedCondition struct {
	Operator GeneratedConditionOperator `json:"operator"`
	Value    GeneratedValue             `json:"value"`
	_        struct{}
}

// GeneratedConditionalFailure returns one declared semantic error when its
// closed condition is true.
type GeneratedConditionalFailure struct {
	Condition GeneratedCondition `json:"condition"`
	ErrorCode string             `json:"error_code"`
	Message   string             `json:"message"`
	_         struct{}
}

// GeneratedMetadataAttachment adds one bounded scalar to propagated opaque
// invocation metadata. Keys are namespaced to the contributing extension.
// Oversized runtime values fail closed and are never truncated.
type GeneratedMetadataAttachment struct {
	Key          string         `json:"key"`
	Value        GeneratedValue `json:"value"`
	MaximumBytes uint32         `json:"maximum_bytes"`
	_            struct{}
}

// GeneratedAuditEventCall makes one explicit bounded call to an ordinary
// canonical audit-event Capability. It is separate from Kernel telemetry.
type GeneratedAuditEventCall struct {
	Event               string                  `json:"event"`
	Capability          CapabilityID            `json:"capability"`
	Request             []GeneratedFieldBinding `json:"request"`
	TimeoutMilliseconds uint32                  `json:"timeout_ms"`
	OnError             GeneratedCallErrorMode  `json:"on_error"`
	_                   struct{}
}

// GeneratedFieldBinding assigns one typed value to a canonical request field.
// Bindings are structurally unordered and normalize by field name.
type GeneratedFieldBinding struct {
	Field  string         `json:"field"`
	Value  GeneratedValue `json:"value"`
	target GeneratedFieldTarget
	_      struct{}
}

// GeneratedFieldTarget is the immutable canonical target-field shape attached
// to a binding during output normalization. Raw extension output has no target
// until the CLI validates it against the called Capability contract.
type GeneratedFieldTarget struct {
	typeName   GeneratedValueType
	items      GeneratedValueType
	required   bool
	enumerated bool
}

// Target returns the canonical request-field shape established by output
// normalization. It reports false for an unnormalized binding.
func (b GeneratedFieldBinding) Target() (GeneratedFieldTarget, bool) {
	return b.target, b.target.typeName != ""
}

// Type returns the canonical scalar, object, or array type.
func (t GeneratedFieldTarget) Type() GeneratedValueType { return t.typeName }

// Items returns the canonical array item type, or an empty value for a
// non-array field.
func (t GeneratedFieldTarget) Items() GeneratedValueType { return t.items }

// Required reports whether the target request field is required.
func (t GeneratedFieldTarget) Required() bool { return t.required }

// Enumerated reports whether the target request field uses a named generated
// enum type.
func (t GeneratedFieldTarget) Enumerated() bool { return t.enumerated }

// GeneratedValue is one closed value union. Exactly one member must be set.
type GeneratedValue struct {
	Literal    *GeneratedLiteral         `json:"literal,omitempty"`
	Invocation *GeneratedInvocationValue `json:"invocation,omitempty"`
	Node       *GeneratedNodeValue       `json:"node,omitempty"`
	shape      GeneratedValueShape
	_          struct{}
}

// GeneratedValueShape is the immutable canonical type and presence metadata
// attached to a value during output normalization. Raw extension output has no
// shape until the CLI validates its source.
type GeneratedValueShape struct {
	typeName   GeneratedValueType
	items      GeneratedValueType
	optional   bool
	errorValue bool
	sensitive  bool
	enumerated bool
	valid      bool
}

// Shape returns the canonical value shape established by output
// normalization. It reports false for an unnormalized value.
func (v GeneratedValue) Shape() (GeneratedValueShape, bool) { return v.shape, v.shape.valid }

// Type returns the canonical scalar, object, or array type. It is empty for an
// error value.
func (s GeneratedValueShape) Type() GeneratedValueType { return s.typeName }

// Items returns the canonical array item type, or an empty value otherwise.
func (s GeneratedValueShape) Items() GeneratedValueType { return s.items }

// Optional reports whether the value may be absent at runtime.
func (s GeneratedValueShape) Optional() bool { return s.optional }

// Error reports whether the value is an error rather than contract data.
func (s GeneratedValueShape) Error() bool { return s.errorValue }

// Sensitive reports whether the value carries sensitive data.
func (s GeneratedValueShape) Sensitive() bool { return s.sensitive }

// Enumerated reports whether the value uses a named generated enum type.
func (s GeneratedValueShape) Enumerated() bool { return s.enumerated }

// GeneratedLiteral is one typed scalar literal. Exactly one member must be
// set, including a pointer to false, zero, or an empty string when intended.
type GeneratedLiteral struct {
	String  *string `json:"string,omitempty"`
	Integer *int64  `json:"integer,omitempty"`
	Boolean *bool   `json:"boolean,omitempty"`
	_       struct{}
}

// StringValue constructs one string literal value.
func StringValue(value string) GeneratedValue {
	return GeneratedValue{Literal: &GeneratedLiteral{String: &value}}
}

// IntegerValue constructs one signed integer literal value.
func IntegerValue(value int64) GeneratedValue {
	return GeneratedValue{Literal: &GeneratedLiteral{Integer: &value}}
}

// BooleanValue constructs one boolean literal value.
func BooleanValue(value bool) GeneratedValue {
	return GeneratedValue{Literal: &GeneratedLiteral{Boolean: &value}}
}

// GeneratedInvocationValueSource identifies one CLI-owned invocation input.
type GeneratedInvocationValueSource string

const (
	GeneratedInvocationRequestField      GeneratedInvocationValueSource = "request-field"
	GeneratedInvocationResponseField     GeneratedInvocationValueSource = "response-field"
	GeneratedInvocationError             GeneratedInvocationValueSource = "invocation-error"
	GeneratedInvocationContextValue      GeneratedInvocationValueSource = "context-value"
	GeneratedInvocationAdapterCredential GeneratedInvocationValueSource = "adapter-credential"
)

// GeneratedInvocationValue references one supported current-invocation input.
// Type, Items, and Sensitive are supplied only for opaque context values;
// request and response field types are inferred from the source Capability
// contract, while adapter credentials are always sensitive.
type GeneratedInvocationValue struct {
	Source    GeneratedInvocationValueSource `json:"source"`
	Name      string                         `json:"name,omitempty"`
	Type      GeneratedValueType             `json:"type,omitempty"`
	Items     GeneratedValueType             `json:"items,omitempty"`
	Sensitive bool                           `json:"sensitive,omitempty"`
	_         struct{}
}

// GeneratedNodeOutput identifies one typed output of an earlier node in the
// same contribution.
type GeneratedNodeOutput string

const (
	GeneratedNodeResponse GeneratedNodeOutput = "response"
	GeneratedNodeError    GeneratedNodeOutput = "error"
	GeneratedNodeDerived  GeneratedNodeOutput = "derived-value"
)

// GeneratedNodeValue references a backward-only output from an earlier node.
type GeneratedNodeValue struct {
	ID     string              `json:"id"`
	Output GeneratedNodeOutput `json:"output"`
	Field  string              `json:"field,omitempty"`
	_      struct{}
}

// NormalizedGeneratedNode is one immutable validated operation. Node order is
// semantic and is never changed during normalization.
type NormalizedGeneratedNode struct {
	id        string
	kind      GeneratedNodeKind
	node      GeneratedNode
	valueInfo generatedValueInfo
}

// ID returns the contribution-local stable node identifier.
func (n NormalizedGeneratedNode) ID() string { return n.id }

// Kind returns the exact closed operation kind.
func (n NormalizedGeneratedNode) Kind() GeneratedNodeKind { return n.kind }

// CapabilityCall returns a defensive operation copy when Kind matches.
func (n NormalizedGeneratedNode) CapabilityCall() (GeneratedCapabilityCall, bool) {
	if n.node.CapabilityCall == nil {
		return GeneratedCapabilityCall{}, false
	}
	return cloneGeneratedCapabilityCall(*n.node.CapabilityCall), true
}

// ContextDerivation returns a defensive operation copy when Kind matches.
func (n NormalizedGeneratedNode) ContextDerivation() (GeneratedContextDerivation, bool) {
	if n.node.ContextDerivation == nil {
		return GeneratedContextDerivation{}, false
	}
	return cloneGeneratedContextDerivation(*n.node.ContextDerivation), true
}

// ConditionalFailure returns a defensive operation copy when Kind matches.
func (n NormalizedGeneratedNode) ConditionalFailure() (GeneratedConditionalFailure, bool) {
	if n.node.ConditionalFailure == nil {
		return GeneratedConditionalFailure{}, false
	}
	return cloneGeneratedConditionalFailure(*n.node.ConditionalFailure), true
}

// MetadataAttachment returns a defensive operation copy when Kind matches.
func (n NormalizedGeneratedNode) MetadataAttachment() (GeneratedMetadataAttachment, bool) {
	if n.node.MetadataAttachment == nil {
		return GeneratedMetadataAttachment{}, false
	}
	return cloneGeneratedMetadataAttachment(*n.node.MetadataAttachment), true
}

// AuditEventCall returns a defensive operation copy when Kind matches.
func (n NormalizedGeneratedNode) AuditEventCall() (GeneratedAuditEventCall, bool) {
	if n.node.AuditEventCall == nil {
		return GeneratedAuditEventCall{}, false
	}
	return cloneGeneratedAuditEventCall(*n.node.AuditEventCall), true
}

func (n NormalizedGeneratedNode) canonicalNode() GeneratedNode {
	return cloneGeneratedNode(n.node)
}

type generatedSchemaField struct {
	Type     GeneratedValueType `json:"type"`
	Items    GeneratedValueType `json:"items,omitempty"`
	Required bool               `json:"required,omitempty"`
	Enum     []json.RawMessage  `json:"enum,omitempty"`
}

type generatedContractSchema struct {
	Request  map[string]generatedSchemaField `json:"request"`
	Response map[string]generatedSchemaField `json:"response"`
	Errors   []string                        `json:"errors"`
}

type generatedValueInfo struct {
	typeName   GeneratedValueType
	items      GeneratedValueType
	optional   bool
	isError    bool
	sensitive  bool
	enumerated bool
}

type generatedNodeNormalizer struct {
	context                Context
	namespace              string
	source                 CapabilityID
	point                  GenerationPoint
	field                  string
	sourceSchema           generatedContractSchema
	previous               map[string]NormalizedGeneratedNode
	contextValues          map[string]generatedValueInfo
	contextKeys            map[string]int
	metadataKeys           map[string]int
	captured               map[string]bool
	invocationErrorHandled bool
}

func normalizeGeneratedNodes(context Context, contribution Contribution, field string) ([]NormalizedGeneratedNode, error) {
	if len(contribution.Nodes) > maximumGeneratedNodes {
		return nil, invalidOutput("%s.nodes contains %d nodes; maximum is %d", field, len(contribution.Nodes), maximumGeneratedNodes)
	}
	source, _ := context.Capability(contribution.Source)
	sourceSchema, err := decodeGeneratedContract(source)
	if err != nil {
		return nil, invalidOutput("%s.source %q has an unreadable canonical contract: %v", field, contribution.Source.String(), err)
	}
	normalizer := generatedNodeNormalizer{
		context:       context,
		namespace:     contribution.Namespace,
		source:        contribution.Source,
		point:         contribution.Point,
		field:         field + ".nodes",
		sourceSchema:  sourceSchema,
		previous:      make(map[string]NormalizedGeneratedNode, len(contribution.Nodes)),
		contextValues: make(map[string]generatedValueInfo),
		contextKeys:   make(map[string]int),
		metadataKeys:  make(map[string]int),
		captured:      make(map[string]bool),
	}
	nodes := make([]NormalizedGeneratedNode, 0, len(contribution.Nodes))
	for index, input := range contribution.Nodes {
		nodeField := fmt.Sprintf("%s[%d]", normalizer.field, index)
		if !validStableID(input.ID) {
			return nil, invalidOutput("%s.id %q is not a stable lower-kebab identifier", nodeField, input.ID)
		}
		if previous, duplicate := normalizer.previous[input.ID]; duplicate {
			return nil, invalidOutput("%s.id duplicates earlier node %q of kind %q", nodeField, previous.ID(), previous.Kind())
		}
		node, err := normalizer.normalizeNode(nodeField, input)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
		normalizer.previous[node.ID()] = node
	}
	unhandled := make([]string, 0)
	for id, handled := range normalizer.captured {
		if !handled {
			unhandled = append(unhandled, id)
		}
	}
	if len(unhandled) != 0 {
		sort.Strings(unhandled)
		return nil, invalidOutput("%s capability-call node %q captures failure without a later is-error conditional-failure", normalizer.field, unhandled[0])
	}
	return nodes, nil
}

func (n *generatedNodeNormalizer) normalizeNode(field string, input GeneratedNode) (NormalizedGeneratedNode, error) {
	count := 0
	if input.CapabilityCall != nil {
		count++
	}
	if input.ContextDerivation != nil {
		count++
	}
	if input.ConditionalFailure != nil {
		count++
	}
	if input.MetadataAttachment != nil {
		count++
	}
	if input.AuditEventCall != nil {
		count++
	}
	if count != 1 {
		return NormalizedGeneratedNode{}, invalidOutput("%s must contain exactly one generated operation; found %d", field, count)
	}
	normalized := GeneratedNode{ID: input.ID}
	var kind GeneratedNodeKind
	var err error
	switch {
	case input.CapabilityCall != nil:
		kind = GeneratedNodeKindCapabilityCall
		var operation GeneratedCapabilityCall
		operation, err = n.normalizeCapabilityCall(field+".capability_call", input.ID, *input.CapabilityCall)
		normalized.CapabilityCall = &operation
	case input.ContextDerivation != nil:
		kind = GeneratedNodeKindContextDerivation
		var operation GeneratedContextDerivation
		operation, err = n.normalizeContextDerivation(field+".context_derivation", len(n.previous), *input.ContextDerivation)
		normalized.ContextDerivation = &operation
	case input.ConditionalFailure != nil:
		kind = GeneratedNodeKindConditionalFailure
		var operation GeneratedConditionalFailure
		operation, err = n.normalizeConditionalFailure(field+".conditional_failure", *input.ConditionalFailure)
		normalized.ConditionalFailure = &operation
	case input.MetadataAttachment != nil:
		kind = GeneratedNodeKindMetadataAttachment
		var operation GeneratedMetadataAttachment
		operation, err = n.normalizeMetadataAttachment(field+".metadata_attachment", len(n.previous), *input.MetadataAttachment)
		normalized.MetadataAttachment = &operation
	case input.AuditEventCall != nil:
		kind = GeneratedNodeKindAuditEventCall
		var operation GeneratedAuditEventCall
		operation, err = n.normalizeAuditEventCall(field+".audit_event_call", *input.AuditEventCall)
		normalized.AuditEventCall = &operation
	}
	if err != nil {
		return NormalizedGeneratedNode{}, err
	}
	valueInfo := generatedValueInfo{}
	if normalized.ContextDerivation != nil {
		valueInfo = n.contextValues[normalized.ContextDerivation.Key]
	}
	return NormalizedGeneratedNode{id: input.ID, kind: kind, node: normalized, valueInfo: valueInfo}, nil
}

func (n *generatedNodeNormalizer) normalizeCapabilityCall(field, id string, input GeneratedCapabilityCall) (GeneratedCapabilityCall, error) {
	capability, schema, err := n.callTarget(field+".capability", input.Capability, true)
	if err != nil {
		return GeneratedCapabilityCall{}, err
	}
	if input.OnError != GeneratedCallFailClosed && input.OnError != GeneratedCallCapture {
		return GeneratedCapabilityCall{}, invalidOutput("%s.on_error %q must be %q or %q", field, input.OnError, GeneratedCallFailClosed, GeneratedCallCapture)
	}
	if err := validateGeneratedTimeout(field+".timeout_ms", input.TimeoutMilliseconds); err != nil {
		return GeneratedCapabilityCall{}, err
	}
	bindings, err := n.normalizeBindings(field+".request", input.Request, schema.Request, true)
	if err != nil {
		return GeneratedCapabilityCall{}, err
	}
	if input.OnError == GeneratedCallCapture {
		n.captured[id] = false
	}
	return GeneratedCapabilityCall{Capability: capability, Request: bindings, TimeoutMilliseconds: input.TimeoutMilliseconds, OnError: input.OnError}, nil
}

func (n *generatedNodeNormalizer) normalizeContextDerivation(field string, index int, input GeneratedContextDerivation) (GeneratedContextDerivation, error) {
	if err := validateGeneratedOwnedKey(field+".key", n.namespace, input.Key); err != nil {
		return GeneratedContextDerivation{}, err
	}
	if previous, duplicate := n.contextKeys[input.Key]; duplicate {
		return GeneratedContextDerivation{}, invalidOutput("%s.key %q duplicates context key from %s[%d]", field, input.Key, n.field, previous)
	}
	want, err := generatedDeclaredType(field, input.Type, input.Items)
	if err != nil {
		return GeneratedContextDerivation{}, err
	}
	value, got, err := n.normalizeValue(field+".value", input.Value)
	if err != nil {
		return GeneratedContextDerivation{}, err
	}
	if !generatedTypesCompatible(got, want) || got.isError {
		return GeneratedContextDerivation{}, invalidOutput("%s.value has type %s, want %s", field, describeGeneratedType(got), describeGeneratedType(want))
	}
	if input.Presence != GeneratedContextRequired && input.Presence != GeneratedContextOptional {
		return GeneratedContextDerivation{}, invalidOutput("%s.presence %q must be %q or %q", field, input.Presence, GeneratedContextRequired, GeneratedContextOptional)
	}
	if input.MaximumBytes == 0 || input.MaximumBytes > maximumGeneratedContextBytes {
		return GeneratedContextDerivation{}, invalidOutput("%s.maximum_bytes %d must be between 1 and %d", field, input.MaximumBytes, maximumGeneratedContextBytes)
	}
	if size, known := generatedLiteralSize(value); known && size > int(input.MaximumBytes) {
		return GeneratedContextDerivation{}, invalidOutput("%s.value encodes to %d bytes, exceeding maximum_bytes %d", field, size, input.MaximumBytes)
	}
	result := want
	result.optional = input.Presence == GeneratedContextOptional
	result.sensitive = got.sensitive || input.Sensitive
	n.contextValues[input.Key] = result
	n.contextKeys[input.Key] = index
	return GeneratedContextDerivation{Key: input.Key, Value: value, Type: input.Type, Items: input.Items, Presence: input.Presence, Sensitive: result.sensitive, MaximumBytes: input.MaximumBytes}, nil
}

func (n *generatedNodeNormalizer) normalizeConditionalFailure(field string, input GeneratedConditionalFailure) (GeneratedConditionalFailure, error) {
	value, info, err := n.normalizeValue(field+".condition.value", input.Condition.Value)
	if err != nil {
		return GeneratedConditionalFailure{}, err
	}
	switch input.Condition.Operator {
	case GeneratedConditionMissing, GeneratedConditionPresent:
		if info.isError || !info.optional {
			return GeneratedConditionalFailure{}, invalidOutput("%s.condition operator %q requires an optional non-error value; got %s", field, input.Condition.Operator, describeGeneratedType(info))
		}
	case GeneratedConditionTrue, GeneratedConditionFalse:
		if info.isError || info.optional || info.typeName != GeneratedValueBoolean {
			return GeneratedConditionalFailure{}, invalidOutput("%s.condition operator %q requires a present boolean; got %s", field, input.Condition.Operator, describeGeneratedType(info))
		}
	case GeneratedConditionError:
		if !info.isError {
			return GeneratedConditionalFailure{}, invalidOutput("%s.condition operator %q requires a captured or invocation error; got %s", field, input.Condition.Operator, describeGeneratedType(info))
		}
		if value.Node != nil {
			n.captured[value.Node.ID] = true
		} else if value.Invocation != nil && value.Invocation.Source == GeneratedInvocationError {
			n.invocationErrorHandled = true
		}
	default:
		return GeneratedConditionalFailure{}, invalidOutput("%s.condition.operator %q is not supported", field, input.Condition.Operator)
	}
	if !validGeneratedFieldName(input.ErrorCode) {
		return GeneratedConditionalFailure{}, invalidOutput("%s.error_code %q is not canonical lower snake case", field, input.ErrorCode)
	}
	if !containsString(n.sourceSchema.Errors, input.ErrorCode) {
		return GeneratedConditionalFailure{}, invalidOutput("%s.error_code %q is not declared by source Capability %q", field, input.ErrorCode, n.source.String())
	}
	if input.Message == "" {
		return GeneratedConditionalFailure{}, invalidOutput("%s.message must be non-empty", field)
	}
	if err := validateGeneratedText(field+".message", input.Message, maximumGeneratedStringBytes); err != nil {
		return GeneratedConditionalFailure{}, err
	}
	return GeneratedConditionalFailure{Condition: GeneratedCondition{Operator: input.Condition.Operator, Value: value}, ErrorCode: input.ErrorCode, Message: input.Message}, nil
}

func (n *generatedNodeNormalizer) normalizeMetadataAttachment(field string, index int, input GeneratedMetadataAttachment) (GeneratedMetadataAttachment, error) {
	if err := validateGeneratedOwnedKey(field+".key", n.namespace, input.Key); err != nil {
		return GeneratedMetadataAttachment{}, err
	}
	if previous, duplicate := n.metadataKeys[input.Key]; duplicate {
		return GeneratedMetadataAttachment{}, invalidOutput("%s.key %q duplicates metadata key from %s[%d]", field, input.Key, n.field, previous)
	}
	value, info, err := n.normalizeValue(field+".value", input.Value)
	if err != nil {
		return GeneratedMetadataAttachment{}, err
	}
	if info.isError || info.optional || info.sensitive || info.typeName == GeneratedValueObject || info.typeName == GeneratedValueArray {
		return GeneratedMetadataAttachment{}, invalidOutput("%s.value must be a present scalar that is non-sensitive; got %s", field, describeGeneratedType(info))
	}
	if input.MaximumBytes == 0 || input.MaximumBytes > maximumGeneratedMetadataBytes {
		return GeneratedMetadataAttachment{}, invalidOutput("%s.maximum_bytes %d must be between 1 and %d", field, input.MaximumBytes, maximumGeneratedMetadataBytes)
	}
	if size, known := generatedLiteralSize(value); known && size > int(input.MaximumBytes) {
		return GeneratedMetadataAttachment{}, invalidOutput("%s.value encodes to %d bytes, exceeding maximum_bytes %d", field, size, input.MaximumBytes)
	}
	n.metadataKeys[input.Key] = index
	return GeneratedMetadataAttachment{Key: input.Key, Value: value, MaximumBytes: input.MaximumBytes}, nil
}

func (n *generatedNodeNormalizer) normalizeAuditEventCall(field string, input GeneratedAuditEventCall) (GeneratedAuditEventCall, error) {
	if !validStableID(input.Event) {
		return GeneratedAuditEventCall{}, invalidOutput("%s.event %q is not a stable lower-kebab identifier", field, input.Event)
	}
	capability, schema, err := n.callTarget(field+".capability", input.Capability, false)
	if err != nil {
		return GeneratedAuditEventCall{}, err
	}
	if input.OnError != GeneratedCallFailClosed && input.OnError != GeneratedCallContinue {
		return GeneratedAuditEventCall{}, invalidOutput("%s.on_error %q must be %q or %q", field, input.OnError, GeneratedCallFailClosed, GeneratedCallContinue)
	}
	if err := validateGeneratedTimeout(field+".timeout_ms", input.TimeoutMilliseconds); err != nil {
		return GeneratedAuditEventCall{}, err
	}
	bindings, err := n.normalizeBindings(field+".request", input.Request, schema.Request, false)
	if err != nil {
		return GeneratedAuditEventCall{}, err
	}
	return GeneratedAuditEventCall{Event: input.Event, Capability: capability, Request: bindings, TimeoutMilliseconds: input.TimeoutMilliseconds, OnError: input.OnError}, nil
}

func (n *generatedNodeNormalizer) callTarget(field string, id CapabilityID, allowIntrinsic bool) (CapabilityID, generatedContractSchema, error) {
	view, exists := n.context.Capability(id)
	if !exists || !containsCapabilityID(n.context.requirements, id) {
		return CapabilityID{}, generatedContractSchema{}, invalidOutput("%s %q is not a current required canonical Capability", field, id.String())
	}
	if view.Intrinsic() {
		if !allowIntrinsic {
			return CapabilityID{}, generatedContractSchema{}, invalidOutput("%s %q is intrinsic; explicit audit events must use an ordinary provider Capability", field, id.String())
		}
	} else if _, selected := n.context.SelectedProvider(id); !selected {
		return CapabilityID{}, generatedContractSchema{}, invalidOutput("%s %q has no selected ordinary provider", field, id.String())
	}
	schema, err := decodeGeneratedContract(view)
	if err != nil {
		return CapabilityID{}, generatedContractSchema{}, invalidOutput("%s %q has an unreadable canonical contract: %v", field, id.String(), err)
	}
	return id, schema, nil
}

func (n *generatedNodeNormalizer) normalizeBindings(field string, inputs []GeneratedFieldBinding, schema map[string]generatedSchemaField, allowSensitive bool) ([]GeneratedFieldBinding, error) {
	if len(inputs) > maximumGeneratedBindings {
		return nil, invalidOutput("%s contains %d bindings; maximum is %d", field, len(inputs), maximumGeneratedBindings)
	}
	bindings := make([]GeneratedFieldBinding, 0, len(inputs))
	seen := make(map[string]int, len(inputs))
	for index, input := range inputs {
		bindingField := fmt.Sprintf("%s[%d]", field, index)
		if !validGeneratedFieldName(input.Field) {
			return nil, invalidOutput("%s.field %q is not canonical lower snake case", bindingField, input.Field)
		}
		target, exists := schema[input.Field]
		if !exists {
			return nil, invalidOutput("%s.field %q is not declared by the target Capability request", bindingField, input.Field)
		}
		if previous, duplicate := seen[input.Field]; duplicate {
			return nil, invalidOutput("%s.field duplicates binding %q from %s[%d]", bindingField, input.Field, field, previous)
		}
		value, source, err := n.normalizeValue(bindingField+".value", input.Value)
		if err != nil {
			return nil, err
		}
		want := generatedValueInfo{typeName: target.Type, items: target.Items}
		if source.isError || !generatedTypesCompatible(source, want) {
			return nil, invalidOutput("%s.value has type %s, but target field %q requires %s", bindingField, describeGeneratedType(source), input.Field, describeGeneratedType(want))
		}
		if source.sensitive && !allowSensitive {
			return nil, invalidOutput("%s.value is sensitive and cannot enter an audit-event request", bindingField)
		}
		if target.Required && source.optional {
			return nil, invalidOutput("%s.value may be absent, but target field %q is required; derive a required value first", bindingField, input.Field)
		}
		seen[input.Field] = index
		bindings = append(bindings, GeneratedFieldBinding{
			Field: input.Field,
			Value: value,
			target: GeneratedFieldTarget{
				typeName:   target.Type,
				items:      target.Items,
				required:   target.Required,
				enumerated: len(target.Enum) != 0,
			},
		})
	}
	required := make([]string, 0)
	for name, target := range schema {
		if target.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	for _, name := range required {
		if _, exists := seen[name]; !exists {
			return nil, invalidOutput("%s omits required target request field %q", field, name)
		}
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].Field < bindings[right].Field })
	return bindings, nil
}

func (n *generatedNodeNormalizer) normalizeValue(field string, input GeneratedValue) (GeneratedValue, generatedValueInfo, error) {
	count := 0
	if input.Literal != nil {
		count++
	}
	if input.Invocation != nil {
		count++
	}
	if input.Node != nil {
		count++
	}
	if count != 1 {
		return GeneratedValue{}, generatedValueInfo{}, invalidOutput("%s must contain exactly one typed value source; found %d", field, count)
	}
	if input.Literal != nil {
		literal, info, err := normalizeGeneratedLiteral(field+".literal", *input.Literal)
		if err != nil {
			return GeneratedValue{}, generatedValueInfo{}, err
		}
		value := GeneratedValue{Literal: &literal}
		value.shape = generatedShape(info)
		return value, info, nil
	}
	if input.Invocation != nil {
		invocation, info, err := n.normalizeInvocationValue(field+".invocation", *input.Invocation)
		if err != nil {
			return GeneratedValue{}, generatedValueInfo{}, err
		}
		value := GeneratedValue{Invocation: &invocation}
		value.shape = generatedShape(info)
		return value, info, nil
	}
	node, info, err := n.normalizeNodeValue(field+".node", *input.Node)
	if err != nil {
		return GeneratedValue{}, generatedValueInfo{}, err
	}
	value := GeneratedValue{Node: &node}
	value.shape = generatedShape(info)
	return value, info, nil
}

func generatedShape(info generatedValueInfo) GeneratedValueShape {
	return GeneratedValueShape{
		typeName:   info.typeName,
		items:      info.items,
		optional:   info.optional,
		errorValue: info.isError,
		sensitive:  info.sensitive,
		enumerated: info.enumerated,
		valid:      true,
	}
}

func normalizeGeneratedLiteral(field string, input GeneratedLiteral) (GeneratedLiteral, generatedValueInfo, error) {
	count := 0
	if input.String != nil {
		count++
	}
	if input.Integer != nil {
		count++
	}
	if input.Boolean != nil {
		count++
	}
	if count != 1 {
		return GeneratedLiteral{}, generatedValueInfo{}, invalidOutput("%s must contain exactly one scalar literal; found %d", field, count)
	}
	if input.String != nil {
		if err := validateGeneratedText(field+".string", *input.String, maximumGeneratedStringBytes); err != nil {
			return GeneratedLiteral{}, generatedValueInfo{}, err
		}
		value := *input.String
		return GeneratedLiteral{String: &value}, generatedValueInfo{typeName: GeneratedValueString}, nil
	}
	if input.Integer != nil {
		value := *input.Integer
		return GeneratedLiteral{Integer: &value}, generatedValueInfo{typeName: GeneratedValueInteger}, nil
	}
	value := *input.Boolean
	return GeneratedLiteral{Boolean: &value}, generatedValueInfo{typeName: GeneratedValueBoolean}, nil
}

func (n *generatedNodeNormalizer) normalizeInvocationValue(field string, input GeneratedInvocationValue) (GeneratedInvocationValue, generatedValueInfo, error) {
	result := input
	switch input.Source {
	case GeneratedInvocationRequestField:
		if input.Type != "" || input.Items != "" || input.Sensitive {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s type is inferred from the source request", field)
		}
		return generatedInvocationSchemaValue(field, result, input.Name, n.sourceSchema.Request)
	case GeneratedInvocationResponseField:
		if n.point != GenerationPointInvocationComplete && n.point != GenerationPointHTTPEgress {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s source %q is unavailable before canonical dispatch at point %q", field, input.Source, n.point)
		}
		if input.Type != "" || input.Items != "" || input.Sensitive {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s type is inferred from the source response", field)
		}
		if !n.invocationErrorHandled {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s uses the canonical response before an is-error conditional-failure handles the invocation error", field)
		}
		return generatedInvocationSchemaValue(field, result, input.Name, n.sourceSchema.Response)
	case GeneratedInvocationError:
		if n.point != GenerationPointInvocationComplete && n.point != GenerationPointHTTPEgress {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s source %q is unavailable before canonical dispatch at point %q", field, input.Source, n.point)
		}
		if input.Name != "" || input.Type != "" || input.Items != "" || input.Sensitive {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s invocation-error must not declare name, type, items, or sensitivity", field)
		}
		return result, generatedValueInfo{isError: true, optional: true}, nil
	case GeneratedInvocationContextValue:
		if !validStableID(input.Name) {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s.name %q is not a stable context key", field, input.Name)
		}
		declared, err := generatedDeclaredType(field, input.Type, input.Items)
		if err != nil {
			return GeneratedInvocationValue{}, generatedValueInfo{}, err
		}
		if known, exists := n.contextValues[input.Name]; exists && !generatedTypesCompatible(known, declared) {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s declares context key %q as %s, but the earlier derivation stores %s", field, input.Name, describeGeneratedType(declared), describeGeneratedType(known))
		}
		declared.optional = true
		declared.sensitive = input.Sensitive
		if known, exists := n.contextValues[input.Name]; exists {
			declared.optional = known.optional
			declared.sensitive = known.sensitive || input.Sensitive
		}
		result.Sensitive = declared.sensitive
		return result, declared, nil
	case GeneratedInvocationAdapterCredential:
		if n.point != GenerationPointHTTPIngress && n.point != GenerationPointInvocationPrepare {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s source %q is unavailable at point %q", field, input.Source, n.point)
		}
		if !validGeneratedFieldName(input.Name) {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s.name %q is not a canonical credential name", field, input.Name)
		}
		if input.Type != "" || input.Items != "" {
			return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s adapter credentials are typed strings", field)
		}
		result.Sensitive = true
		return result, generatedValueInfo{typeName: GeneratedValueString, optional: true, sensitive: true}, nil
	default:
		return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s.source %q is not supported", field, input.Source)
	}
}

func generatedInvocationSchemaValue(field string, input GeneratedInvocationValue, name string, schema map[string]generatedSchemaField) (GeneratedInvocationValue, generatedValueInfo, error) {
	if name == "" {
		return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s.name must identify one canonical field", field)
	}
	if !validGeneratedFieldName(name) {
		return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s.name %q is not a canonical field name", field, name)
	}
	value, exists := schema[name]
	if !exists {
		return GeneratedInvocationValue{}, generatedValueInfo{}, invalidOutput("%s.name %q is not declared by the source Capability", field, name)
	}
	return input, generatedValueInfo{typeName: value.Type, items: value.Items, optional: !value.Required, enumerated: len(value.Enum) != 0}, nil
}

func (n *generatedNodeNormalizer) normalizeNodeValue(field string, input GeneratedNodeValue) (GeneratedNodeValue, generatedValueInfo, error) {
	if !validStableID(input.ID) {
		return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s.id %q is not a stable node identifier", field, input.ID)
	}
	previous, exists := n.previous[input.ID]
	if !exists {
		return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s.id %q must reference an earlier node in the same contribution", field, input.ID)
	}
	switch previous.kind {
	case GeneratedNodeKindCapabilityCall:
		call := previous.node.CapabilityCall
		switch input.Output {
		case GeneratedNodeResponse:
			if call.OnError == GeneratedCallCapture && !n.captured[input.ID] {
				return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s uses response from captured call %q before an is-error conditional-failure", field, input.ID)
			}
			view, _ := n.context.Capability(call.Capability)
			schema, err := decodeGeneratedContract(view)
			if err != nil {
				return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s cannot inspect call response: %v", field, err)
			}
			if input.Field == "" {
				return input, generatedValueInfo{typeName: GeneratedValueObject}, nil
			}
			if !validGeneratedFieldName(input.Field) {
				return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s.field %q is not canonical lower snake case", field, input.Field)
			}
			output, exists := schema.Response[input.Field]
			if !exists {
				return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s.field %q is not declared by call %q response", field, input.Field, call.Capability.String())
			}
			return input, generatedValueInfo{typeName: output.Type, items: output.Items, optional: !output.Required, enumerated: len(output.Enum) != 0}, nil
		case GeneratedNodeError:
			if input.Field != "" {
				return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s.field must be empty for an error output", field)
			}
			if call.OnError != GeneratedCallCapture {
				return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s references error from fail-closed call %q", field, input.ID)
			}
			return input, generatedValueInfo{isError: true, optional: true}, nil
		default:
			return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s.output %q is not produced by capability-call node %q", field, input.Output, input.ID)
		}
	case GeneratedNodeKindContextDerivation:
		if input.Output != GeneratedNodeDerived || input.Field != "" {
			return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s must reference output %q without a field from context-derivation node %q", field, GeneratedNodeDerived, input.ID)
		}
		return input, previous.valueInfo, nil
	default:
		return GeneratedNodeValue{}, generatedValueInfo{}, invalidOutput("%s references node %q of kind %q, which has no value output", field, input.ID, previous.kind)
	}
}

func generatedDeclaredType(field string, typeName, items GeneratedValueType) (generatedValueInfo, error) {
	if !validGeneratedValueType(typeName) {
		return generatedValueInfo{}, invalidOutput("%s.type %q is not supported", field, typeName)
	}
	if typeName == GeneratedValueArray {
		if !validGeneratedValueType(items) || items == GeneratedValueArray {
			return generatedValueInfo{}, invalidOutput("%s.items %q must be a supported non-array type", field, items)
		}
	} else if items != "" {
		return generatedValueInfo{}, invalidOutput("%s.items is valid only for array values", field)
	}
	return generatedValueInfo{typeName: typeName, items: items}, nil
}

func validGeneratedValueType(value GeneratedValueType) bool {
	switch value {
	case GeneratedValueString, GeneratedValueInteger, GeneratedValueNumber, GeneratedValueBoolean, GeneratedValueObject, GeneratedValueArray:
		return true
	default:
		return false
	}
}

func generatedTypesCompatible(got, want generatedValueInfo) bool {
	if got.isError || want.isError {
		return got.isError == want.isError
	}
	if got.typeName == GeneratedValueInteger && want.typeName == GeneratedValueNumber {
		return true
	}
	return got.typeName == want.typeName && got.items == want.items
}

func describeGeneratedType(value generatedValueInfo) string {
	if value.isError {
		return "error"
	}
	name := string(value.typeName)
	if value.typeName == GeneratedValueArray {
		name += "<" + string(value.items) + ">"
	}
	if value.optional {
		name = "optional " + name
	}
	if value.sensitive {
		name = "sensitive " + name
	}
	return name
}

func decodeGeneratedContract(view CapabilityView) (generatedContractSchema, error) {
	var schema generatedContractSchema
	if err := json.Unmarshal(view.ContractJSON(), &schema); err != nil {
		return generatedContractSchema{}, err
	}
	if schema.Request == nil || schema.Response == nil || schema.Errors == nil {
		return generatedContractSchema{}, fmt.Errorf("contract is missing request, response, or errors")
	}
	return schema, nil
}

func validateGeneratedTimeout(field string, value uint32) error {
	if value == 0 || value > maximumGeneratedTimeoutMillis {
		return invalidOutput("%s %d must be between 1 and %d", field, value, maximumGeneratedTimeoutMillis)
	}
	return nil
}

func validateGeneratedOwnedKey(field, namespace, value string) error {
	if !validStableID(value) || !strings.HasPrefix(value, namespace+".") {
		return invalidOutput("%s %q must be a stable key prefixed by extensions.%s ownership as %q", field, value, namespace, namespace+".")
	}
	return nil
}

func validateGeneratedText(field, value string, maximum int) error {
	if len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return invalidOutput("%s must be valid UTF-8, at most %d bytes, and contain no NUL", field, maximum)
	}
	return nil
}

func validGeneratedFieldName(value string) bool {
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func generatedLiteralSize(value GeneratedValue) (int, bool) {
	if value.Literal == nil {
		return 0, false
	}
	var literal any
	switch {
	case value.Literal.String != nil:
		literal = *value.Literal.String
	case value.Literal.Integer != nil:
		literal = *value.Literal.Integer
	case value.Literal.Boolean != nil:
		literal = *value.Literal.Boolean
	default:
		return 0, false
	}
	encoded, err := json.Marshal(literal)
	if err != nil {
		return 0, false
	}
	return len(encoded), true
}

func cloneGeneratedNode(input GeneratedNode) GeneratedNode {
	result := GeneratedNode{ID: input.ID}
	if input.CapabilityCall != nil {
		value := cloneGeneratedCapabilityCall(*input.CapabilityCall)
		result.CapabilityCall = &value
	}
	if input.ContextDerivation != nil {
		value := cloneGeneratedContextDerivation(*input.ContextDerivation)
		result.ContextDerivation = &value
	}
	if input.ConditionalFailure != nil {
		value := cloneGeneratedConditionalFailure(*input.ConditionalFailure)
		result.ConditionalFailure = &value
	}
	if input.MetadataAttachment != nil {
		value := cloneGeneratedMetadataAttachment(*input.MetadataAttachment)
		result.MetadataAttachment = &value
	}
	if input.AuditEventCall != nil {
		value := cloneGeneratedAuditEventCall(*input.AuditEventCall)
		result.AuditEventCall = &value
	}
	return result
}

func cloneGeneratedCapabilityCall(input GeneratedCapabilityCall) GeneratedCapabilityCall {
	input.Request = cloneGeneratedBindings(input.Request)
	return input
}

func cloneGeneratedContextDerivation(input GeneratedContextDerivation) GeneratedContextDerivation {
	input.Value = cloneGeneratedValue(input.Value)
	return input
}

func cloneGeneratedConditionalFailure(input GeneratedConditionalFailure) GeneratedConditionalFailure {
	input.Condition.Value = cloneGeneratedValue(input.Condition.Value)
	return input
}

func cloneGeneratedMetadataAttachment(input GeneratedMetadataAttachment) GeneratedMetadataAttachment {
	input.Value = cloneGeneratedValue(input.Value)
	return input
}

func cloneGeneratedAuditEventCall(input GeneratedAuditEventCall) GeneratedAuditEventCall {
	input.Request = cloneGeneratedBindings(input.Request)
	return input
}

func cloneGeneratedBindings(inputs []GeneratedFieldBinding) []GeneratedFieldBinding {
	result := make([]GeneratedFieldBinding, len(inputs))
	for index, input := range inputs {
		result[index] = GeneratedFieldBinding{Field: input.Field, Value: cloneGeneratedValue(input.Value), target: input.target}
	}
	return result
}

func cloneGeneratedValue(input GeneratedValue) GeneratedValue {
	result := GeneratedValue{shape: input.shape}
	if input.Literal != nil {
		literal := GeneratedLiteral{}
		if input.Literal.String != nil {
			value := *input.Literal.String
			literal.String = &value
		}
		if input.Literal.Integer != nil {
			value := *input.Literal.Integer
			literal.Integer = &value
		}
		if input.Literal.Boolean != nil {
			value := *input.Literal.Boolean
			literal.Boolean = &value
		}
		result.Literal = &literal
	}
	if input.Invocation != nil {
		value := *input.Invocation
		result.Invocation = &value
	}
	if input.Node != nil {
		value := *input.Node
		result.Node = &value
	}
	return result
}
