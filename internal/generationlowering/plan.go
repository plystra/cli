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
	case generation.GeneratedNodeKindContextDerivation:
		if _, ok := generated.ContextDerivation(); !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: context-derivation operation is absent", ErrInvalidContribution)
		}
		reserve(generation.GeneratedNodeDerived, "Derived")
		reserve(generation.GeneratedNodeError, "Error")
	case generation.GeneratedNodeKindConditionalFailure:
		if _, ok := generated.ConditionalFailure(); !ok {
			return Node{}, nil, nil, fmt.Errorf("%w: conditional-failure operation is absent", ErrInvalidContribution)
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
		return node, nil, identifiers, nil
	}
	reference, err := targetReference(modulePath, target)
	if err != nil {
		return Node{}, nil, nil, err
	}
	node.target = reference
	node.hasTarget = true
	return node, []ImportRequest{
		{
			Path:   reference.importPath,
			Name:   reference.importName,
			Source: provenance + " target client " + target.String(),
		},
		{
			Path:   reference.contractImportPath,
			Name:   reference.contractImportName,
			Source: provenance + " target contract " + target.String(),
		},
	}, identifiers, nil
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
