package generationlowering

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/goname"
	"golang.org/x/mod/module"
)

var (
	// ErrLower reports failure to lower validated, semantically ordered
	// contributions into CLI-owned render-ready state.
	ErrLower = errors.New("lower generation contributions")
	// ErrInvalidContribution reports an inconsistent contribution sequence at
	// the lowering boundary.
	ErrInvalidContribution = errors.New("invalid contribution lowering input")
)

// ContributionView is the immutable contribution surface consumed by the
// lowerer. Both generation.NormalizedContribution and the resolution layer's
// selected-plugin contribution satisfy it.
type ContributionView interface {
	ID() string
	Namespace() string
	Source() generation.CapabilityID
	Point() generation.GenerationPoint
	Nodes() []generation.NormalizedGeneratedNode
}

// TargetReference is one exact generated canonical Capability client used by
// a typed call node.
type TargetReference struct {
	capability         generation.CapabilityID
	importPath         string
	importName         string
	contractImportPath string
	contractImportName string
	operation          string
}

// Capability returns the exact canonical call target.
func (r TargetReference) Capability() generation.CapabilityID { return r.capability }

// ImportPath returns the application-module generated client import path.
func (r TargetReference) ImportPath() string { return r.importPath }

// ImportName returns the deterministic local package identifier.
func (r TargetReference) ImportName() string { return r.importName }

// ContractImportPath returns the application-module generated canonical
// contract import path used to construct typed requests.
func (r TargetReference) ContractImportPath() string { return r.contractImportPath }

// ContractImportName returns the deterministic local canonical contract
// package identifier.
func (r TargetReference) ContractImportName() string { return r.contractImportName }

// Operation returns the generated client method name.
func (r TargetReference) Operation() string { return r.operation }

// Node is one lowered operation in contribution-local semantic order.
type Node struct {
	generated          generation.NormalizedGeneratedNode
	target             TargetReference
	hasTarget          bool
	responseIdentifier string
	errorIdentifier    string
	derivedIdentifier  string
	sourceIdentifier   string
	presenceIdentifier string
	bindingIdentifiers map[string]string
}

// ID returns the contribution-local stable node identifier.
func (n Node) ID() string { return n.generated.ID() }

// Kind returns the exact closed operation kind.
func (n Node) Kind() generation.GeneratedNodeKind { return n.generated.Kind() }

// Generated returns the immutable validated generation-protocol operation.
func (n Node) Generated() generation.NormalizedGeneratedNode { return n.generated }

// Target returns the generated canonical client reference for Capability-call
// and audit-event-call nodes.
func (n Node) Target() (TargetReference, bool) { return n.target, n.hasTarget }

// Identifier returns the CLI-owned execution identifier for an output role.
// Error identifiers also cover CLI validation or attachment failures that are
// not referenceable through the extension protocol.
func (n Node) Identifier(output generation.GeneratedNodeOutput) (string, bool) {
	var value string
	switch output {
	case generation.GeneratedNodeResponse:
		value = n.responseIdentifier
	case generation.GeneratedNodeError:
		value = n.errorIdentifier
	case generation.GeneratedNodeDerived:
		value = n.derivedIdentifier
	}
	return value, value != ""
}

// SourceIdentifier returns the CLI-owned local used while reading an optional
// generated invocation value.
func (n Node) SourceIdentifier() (string, bool) {
	return n.sourceIdentifier, n.sourceIdentifier != ""
}

// PresenceIdentifier returns the CLI-owned local that records whether an
// optional generated invocation value exists.
func (n Node) PresenceIdentifier() (string, bool) {
	return n.presenceIdentifier, n.presenceIdentifier != ""
}

// BindingIdentifier returns the CLI-owned local used to convert one request
// binding before a generated Capability call.
func (n Node) BindingIdentifier(field string) (string, bool) {
	value, ok := n.bindingIdentifiers[field]
	return value, ok
}

// Contribution is one immutable lowered semantic contribution.
type Contribution struct {
	pluginID  string
	id        string
	namespace string
	source    generation.CapabilityID
	point     generation.GenerationPoint
	nodes     []Node
}

// PluginID returns selected-provider provenance when the input exposes it.
func (c Contribution) PluginID() string { return c.pluginID }

// ID returns the globally stable contribution identifier.
func (c Contribution) ID() string { return c.id }

// Namespace returns the interpreted extension namespace.
func (c Contribution) Namespace() string { return c.namespace }

// Source returns the exact metadata-bearing canonical Capability.
func (c Contribution) Source() generation.CapabilityID { return c.source }

// Point returns the versioned application integration point.
func (c Contribution) Point() generation.GenerationPoint { return c.point }

// Nodes returns defensive operations in semantic order.
func (c Contribution) Nodes() []Node { return append([]Node(nil), c.nodes...) }

// Plan is one immutable render-ready contribution sequence and merged Go
// lexical scope. Contribution and node order is semantic and never sorted.
type Plan struct {
	modulePath    string
	scope         Scope
	contributions []Contribution
}

// ModulePath returns the application Go Module path used for generated client
// references.
func (p Plan) ModulePath() string { return p.modulePath }

// Scope returns the immutable merged Go lexical scope.
func (p Plan) Scope() Scope { return p.scope }

// Contributions returns defensive entries in semantic execution order.
func (p Plan) Contributions() []Contribution {
	result := make([]Contribution, len(p.contributions))
	for index, contribution := range p.contributions {
		result[index] = contribution
		result[index].nodes = append([]Node(nil), contribution.nodes...)
		for nodeIndex := range result[index].nodes {
			bindings := result[index].nodes[nodeIndex].bindingIdentifiers
			if len(bindings) == 0 {
				continue
			}
			result[index].nodes[nodeIndex].bindingIdentifiers = make(map[string]string, len(bindings))
			for field, identifier := range bindings {
				result[index].nodes[nodeIndex].bindingIdentifiers[field] = identifier
			}
		}
	}
	return result
}

// Lower converts validated contributions in their already-resolved semantic
// order into immutable render-ready references and identifiers. It never uses
// discovery order to reorder semantic work.
func Lower[C ContributionView](modulePath string, inputs []C) (Plan, error) {
	if err := module.CheckPath(modulePath); err != nil {
		return Plan{}, fmt.Errorf("%w: invalid application Go Module path %q: %v", ErrLower, modulePath, err)
	}

	contributions := make([]Contribution, 0, len(inputs))
	imports := make([]ImportRequest, 0)
	identifiers := make([]IdentifierRequest, 0)
	seen := make(map[string]int, len(inputs))
	for index, input := range inputs {
		id := input.ID()
		if previous, duplicate := seen[id]; duplicate {
			return Plan{}, fmt.Errorf(
				"%w: %w: contributions[%d] repeats ID %q from contributions[%d]",
				ErrLower,
				ErrInvalidContribution,
				index,
				id,
				previous,
			)
		}
		seen[id] = index

		pluginID := ""
		if selected, ok := any(input).(interface{ PluginID() string }); ok {
			pluginID = selected.PluginID()
		}
		generatedNodes := input.Nodes()
		contribution := Contribution{
			pluginID:  pluginID,
			id:        id,
			namespace: input.Namespace(),
			source:    input.Source(),
			point:     input.Point(),
			nodes:     make([]Node, 0, len(generatedNodes)),
		}
		for _, generated := range generatedNodes {
			node, nodeImports, nodeIdentifiers, err := lowerNode(modulePath, contribution, generated)
			if err != nil {
				return Plan{}, fmt.Errorf("%w: contribution %q node %q: %w", ErrLower, contribution.id, generated.ID(), err)
			}
			contribution.nodes = append(contribution.nodes, node)
			imports = append(imports, nodeImports...)
			identifiers = append(identifiers, nodeIdentifiers...)
		}
		contributions = append(contributions, contribution)
	}

	scope, err := BuildScope(imports, identifiers)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrLower, err)
	}
	return Plan{
		modulePath:    modulePath,
		scope:         scope,
		contributions: contributions,
	}, nil
}

func lowerNode(modulePath string, contribution Contribution, generated generation.NormalizedGeneratedNode) (Node, []ImportRequest, []IdentifierRequest, error) {
	node := Node{generated: generated}
	base := generatedIdentifierBase(contribution.id, generated.ID())
	provenance := nodeProvenance(contribution, generated)
	identifiers := make([]IdentifierRequest, 0, 2)
	imports := make([]ImportRequest, 0, 1)
	reserve := func(output generation.GeneratedNodeOutput, suffix string) {
		name := base + suffix
		identifiers = append(identifiers, IdentifierRequest{
			Name:   name,
			Source: provenance + " " + string(output),
		})
		switch output {
		case generation.GeneratedNodeResponse:
			node.responseIdentifier = name
		case generation.GeneratedNodeError:
			node.errorIdentifier = name
		case generation.GeneratedNodeDerived:
			node.derivedIdentifier = name
		}
	}

	var target generation.CapabilityID
	switch generated.Kind() {
	case generation.GeneratedNodeKindCapabilityCall:
		operation, ok := generated.CapabilityCall()
		if !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: capability-call operation is absent", ErrInvalidContribution)
		}
		target = operation.Capability
		identifiers = append(identifiers, invocationRuntimeIdentifiers()...)
		reserve(generation.GeneratedNodeResponse, "Response")
		reserve(generation.GeneratedNodeError, "Error")
		for _, binding := range operation.Request {
			if binding.Value.Node == nil || binding.Value.Node.Output != generation.GeneratedNodeResponse || binding.Value.Node.Field != "" {
				continue
			}
			name := generatedIdentifierBase(contribution.id, generated.ID(), "request", binding.Field)
			if node.bindingIdentifiers == nil {
				node.bindingIdentifiers = make(map[string]string)
			}
			node.bindingIdentifiers[binding.Field] = name
			identifiers = append(identifiers,
				IdentifierRequest{Name: name, Source: provenance + " whole-response request binding " + binding.Field},
			)
			identifiers = append(identifiers, valueConversionRuntimeIdentifiers()...)
		}
	case generation.GeneratedNodeKindContextDerivation:
		operation, ok := generated.ContextDerivation()
		if !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: context-derivation operation is absent", ErrInvalidContribution)
		}
		reserve(generation.GeneratedNodeDerived, "Derived")
		reserve(generation.GeneratedNodeError, "Error")
		if operation.Value.Invocation != nil && operation.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
			node.sourceIdentifier = base + "Source"
			node.presenceIdentifier = base + "Present"
			identifiers = append(identifiers,
				IdentifierRequest{Name: node.sourceIdentifier, Source: provenance + " optional source"},
				IdentifierRequest{Name: node.presenceIdentifier, Source: provenance + " optional presence"},
			)
		}
		if operation.Presence == generation.GeneratedContextOptional {
			identifiers = append(identifiers, contextRuntimeIdentifiers()...)
		}
		if operation.Value.Node != nil && operation.Value.Node.Output == generation.GeneratedNodeResponse && operation.Value.Node.Field == "" {
			identifiers = append(identifiers, valueConversionRuntimeIdentifiers()...)
		}
		imports = append(imports, ImportRequest{
			Path:   path.Join(modulePath, "generated/go/internal/invocationcontext"),
			Name:   "invocationcontext",
			Source: provenance + " generated invocation context",
		})
	case generation.GeneratedNodeKindConditionalFailure:
		operation, ok := generated.ConditionalFailure()
		if !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: conditional-failure operation is absent", ErrInvalidContribution)
		}
		identifiers = append(identifiers, conditionalRuntimeIdentifiers()...)
		if operation.Condition.Value.Invocation != nil && operation.Condition.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
			node.sourceIdentifier = base + "Source"
			node.presenceIdentifier = base + "Present"
			identifiers = append(identifiers,
				IdentifierRequest{Name: node.sourceIdentifier, Source: provenance + " optional source"},
				IdentifierRequest{Name: node.presenceIdentifier, Source: provenance + " optional presence"},
			)
			imports = append(imports, ImportRequest{
				Path:   path.Join(modulePath, "generated/go/internal/invocationcontext"),
				Name:   "invocationcontext",
				Source: provenance + " generated invocation context",
			})
		}
	case generation.GeneratedNodeKindMetadataAttachment:
		if _, ok := generated.MetadataAttachment(); !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: metadata-attachment operation is absent", ErrInvalidContribution)
		}
		reserve(generation.GeneratedNodeError, "Error")
	case generation.GeneratedNodeKindAuditEventCall:
		operation, ok := generated.AuditEventCall()
		if !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: audit-event-call operation is absent", ErrInvalidContribution)
		}
		target = operation.Capability
		identifiers = append(identifiers, invocationRuntimeIdentifiers()...)
		reserve(generation.GeneratedNodeError, "Error")
	default:
		return Node{}, nil, nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalidContribution, generated.Kind())
	}

	if target.String() == "" {
		return node, imports, identifiers, nil
	}
	reference, err := targetReference(modulePath, target)
	if err != nil {
		return Node{}, nil, nil, err
	}
	node.target = reference
	node.hasTarget = true
	imports = append(imports,
		ImportRequest{
			Path:   reference.importPath,
			Name:   reference.importName,
			Source: provenance + " target client " + target.String(),
		},
		ImportRequest{
			Path:   reference.contractImportPath,
			Name:   reference.contractImportName,
			Source: provenance + " target contract " + target.String(),
		},
	)
	return node, imports, identifiers, nil
}

func targetReference(modulePath string, target generation.CapabilityID) (TargetReference, error) {
	identifier, err := capabilityid.Parse(target.String())
	if err != nil {
		return TargetReference{}, fmt.Errorf("%w: target %q is not canonical: %v", ErrInvalidContribution, target.String(), err)
	}
	components := append([]string{modulePath, "generated", "go", "clients"}, strings.Split(identifier.Name(), ".")...)
	components = append(components, "v"+strconv.FormatUint(identifier.Major(), 10))
	contractComponents := append([]string{modulePath, "generated", "go", "contracts"}, strings.Split(identifier.Name(), ".")...)
	contractComponents = append(contractComponents, "v"+strconv.FormatUint(identifier.Major(), 10))
	importName := goname.Package(identifier)
	return TargetReference{
		capability:         target,
		importPath:         path.Join(components...),
		importName:         importName,
		contractImportPath: path.Join(contractComponents...),
		contractImportName: importName + "contract",
		operation:          goname.Operation(identifier),
	}, nil
}

func invocationRuntimeIdentifiers() []IdentifierRequest {
	return []IdentifierRequest{
		{Name: "plystraErrInvalidContext", Source: "generated invocation timeout runtime"},
		{Name: "plystraInvokeWithTimeout", Source: "generated invocation timeout runtime"},
		{Name: "plystraPointer", Source: "generated invocation typed binding runtime"},
		{Name: "plystraConvertOptional", Source: "generated invocation typed binding runtime"},
	}
}

func contextRuntimeIdentifiers() []IdentifierRequest {
	return []IdentifierRequest{
		{Name: "plystraPointer", Source: "generated invocation typed binding runtime"},
	}
}

func conditionalRuntimeIdentifiers() []IdentifierRequest {
	return []IdentifierRequest{
		{Name: "plystraConditionalError", Source: "generated invocation conditional failure runtime"},
	}
}

func valueConversionRuntimeIdentifiers() []IdentifierRequest {
	return []IdentifierRequest{
		{Name: "plystraErrInvalidGeneratedValue", Source: "generated invocation value conversion runtime"},
		{Name: "plystraConvertValue", Source: "generated invocation value conversion runtime"},
	}
}

func generatedIdentifierBase(parts ...string) string {
	var result strings.Builder
	result.WriteString("plystra")
	for _, part := range parts {
		words := strings.FieldsFunc(part, func(character rune) bool {
			return character == '.' || character == '-'
		})
		for _, word := range words {
			result.WriteString(goname.ExportedWord(word))
		}
	}
	return result.String()
}

func nodeProvenance(contribution Contribution, node generation.NormalizedGeneratedNode) string {
	parts := []string{"contribution", contribution.id, "namespace", contribution.namespace, "source", contribution.source.String()}
	if contribution.pluginID != "" {
		parts = append(parts, "plugin", contribution.pluginID)
	}
	parts = append(parts, "node", node.ID(), "kind", string(node.Kind()))
	return strings.Join(parts, " ")
}
