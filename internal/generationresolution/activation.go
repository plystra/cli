// Package generationresolution computes the deterministic build-time
// generation dependency closure on top of ordinary canonical provider
// resolution.
package generationresolution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/providerresolution"
)

const maximumRequirementSourceSize = 1024

var (
	// ErrResolve reports that the generation activation closure could not reach
	// one valid stable provider and extension selection.
	ErrResolve = errors.New("resolve generation activation closure")
	// ErrActivationCycle reports a cycle through extension namespace activations.
	ErrActivationCycle = errors.New("generation activation cycle")
	// ErrSelectExtension reports a selected activation provider without the
	// matching generation extension owned by that same plugin.
	ErrSelectExtension = errors.New("select generation activation extension")
	// ErrConvergence reports failure to stabilize within the finite monotonic bound.
	ErrConvergence = errors.New("generation activation closure did not converge")
	// ErrInvariant reports an internally inconsistent successful dependency pass.
	ErrInvariant = errors.New("generation activation resolution invariant")
)

// Input is one activation-resolution pass input. Requirements are root or
// already-derived exact requirements; activation-derived requirements are
// added internally and never written back into user configuration.
type Input struct {
	Requirements []providerresolution.Requirement
	Candidates   []providerresolution.Candidate
	Choices      []providerresolution.Choice
	Activations  generationactivation.Catalog
}

// SelectedActivation records one namespace handled by a selected extension and
// every required contract that caused the activation.
type SelectedActivation struct {
	namespace  string
	capability capabilityid.Identifier
	uses       []generationactivation.NamespaceUse
}

// Namespace returns the exact extension namespace.
func (a SelectedActivation) Namespace() string { return a.namespace }

// Capability returns the exact ordinary activation Capability.
func (a SelectedActivation) Capability() capabilityid.Identifier {
	return a.capability
}

// Uses returns defensive source-Capability provenance in canonical order.
func (a SelectedActivation) Uses() []generationactivation.NamespaceUse {
	return append([]generationactivation.NamespaceUse(nil), a.uses...)
}

// SelectedExtension is one selected provider plugin's eligible generation
// package. Unselected providers never appear here.
type SelectedExtension struct {
	pluginID    string
	api         string
	packagePath string
	source      string
	activations []SelectedActivation
}

// PluginID returns the selected extension owner.
func (e SelectedExtension) PluginID() string { return e.pluginID }

// API returns the exact generation protocol version.
func (e SelectedExtension) API() string { return e.api }

// Package returns the canonical plugin-relative generation package.
func (e SelectedExtension) Package() string { return e.packagePath }

// Source returns deterministic generation declaration provenance.
func (e SelectedExtension) Source() string { return e.source }

// Activations returns defensive namespace bindings in canonical order.
func (e SelectedExtension) Activations() []SelectedActivation {
	return append([]SelectedActivation(nil), e.activations...)
}

// Namespaces returns the exact namespaces passed to this extension helper.
func (e SelectedExtension) Namespaces() []string {
	result := make([]string, len(e.activations))
	for index, activation := range e.activations {
		result[index] = activation.namespace
	}
	return result
}

// Result is one immutable stable activation closure.
type Result struct {
	providerResolution providerresolution.Result
	activationSet      generationactivation.RequirementSet
	extensions         []SelectedExtension
	passes             int
}

// ProviderResolution returns the final exact requirement and provider mapping.
func (r Result) ProviderResolution() providerresolution.Result {
	return r.providerResolution
}

// ActivationRequirements returns the final namespace-derived requirement set.
func (r Result) ActivationRequirements() generationactivation.RequirementSet {
	return r.activationSet
}

// Extensions returns defensive selected-extension views in Plugin ID order.
func (r Result) Extensions() []SelectedExtension {
	return append([]SelectedExtension(nil), r.extensions...)
}

// Passes returns the number of provider/discovery passes needed for stability.
func (r Result) Passes() int { return r.passes }

type generatedRequirementKey struct {
	capability capabilityid.Identifier
	namespace  string
	source     capabilityid.Identifier
}

// Resolve expands namespace activations monotonically, reusing ordinary
// provider resolution after every expansion. The finite bound follows from the
// visible provider and activation declaration sets: every non-final pass adds
// at least one previously unseen activation cause.
func Resolve(input Input) (Result, error) {
	catalog, err := providerresolution.NewCatalog(cloneCandidates(input.Candidates))
	if err != nil {
		currentChoices := choicesForRequirements(input.Requirements, input.Choices)
		_, resolutionErr := providerresolution.Resolve(providerresolution.Input{
			Requirements: input.Requirements,
			Candidates:   input.Candidates,
			Choices:      currentChoices,
		})
		if resolutionErr == nil {
			resolutionErr = err
		}
		return Result{}, fmt.Errorf("%w: pass 1: %w", ErrResolve, resolutionErr)
	}
	return resolveWithProviderCatalog(input, catalog)
}

func resolveWithProviderCatalog(input Input, catalog providerresolution.Catalog) (Result, error) {
	requirements := cloneRequirements(input.Requirements)
	choices := append([]providerresolution.Choice(nil), input.Choices...)
	generated := make(map[generatedRequirementKey]struct{})

	maximumPasses := len(requirements) + len(input.Candidates) + len(input.Activations.Associations()) + 2
	for pass := 1; pass <= maximumPasses; pass++ {
		// Explicit choices for requirements introduced by a later activation or
		// generation pass remain dormant until that exact Capability is current.
		// providerresolution still validates each choice as soon as it applies.
		currentChoices := choicesForRequirements(requirements, choices)
		resolved, err := catalog.Resolve(requirements, currentChoices)
		if err != nil {
			return Result{}, fmt.Errorf("%w: pass %d: %w", ErrResolve, pass, err)
		}
		activations, err := input.Activations.DiscoverRequirements(resolved)
		if err != nil {
			return Result{}, fmt.Errorf("%w: pass %d: %w", ErrResolve, pass, err)
		}
		if cycle := findActivationCycle(activations); cycle != nil {
			return Result{}, fmt.Errorf("%w: pass %d: %w", ErrResolve, pass, cycle)
		}

		added := 0
		for _, activation := range activations.Requirements() {
			for _, use := range activation.Uses() {
				key := generatedRequirementKey{
					capability: activation.Capability(),
					namespace:  use.Namespace(),
					source:     use.SourceCapability(),
				}
				if _, exists := generated[key]; exists {
					continue
				}
				generated[key] = struct{}{}
				for _, source := range use.RequirementSources() {
					requirements = append(requirements, providerresolution.Requirement{
						Capability: activation.Capability().String(),
						Source:     activationRequirementSource(use, source),
					})
				}
				added++
			}
		}
		if added != 0 {
			continue
		}

		extensions, err := selectExtensions(input.Activations, activations, resolved)
		if err != nil {
			return Result{}, err
		}
		return Result{
			providerResolution: resolved,
			activationSet:      activations,
			extensions:         extensions,
			passes:             pass,
		}, nil
	}
	return Result{}, fmt.Errorf(
		"%w: %w after %d passes derived from %d roots, %d provider candidates, and %d activation associations",
		ErrResolve,
		ErrConvergence,
		maximumPasses,
		len(input.Requirements),
		len(input.Candidates),
		len(input.Activations.Associations()),
	)
}

func choicesForRequirements(requirements []providerresolution.Requirement, choices []providerresolution.Choice) []providerresolution.Choice {
	required := make(map[capabilityid.Identifier]struct{}, len(requirements))
	for _, requirement := range requirements {
		if requirement.Capability != "" {
			if id, err := capabilityid.Parse(requirement.Capability); err == nil {
				required[id] = struct{}{}
			}
		}
		if len(requirement.Contract) != 0 {
			if id, err := capabilitymeta.ParseID(requirement.Contract); err == nil {
				required[id] = struct{}{}
			}
		}
	}
	result := make([]providerresolution.Choice, 0, len(choices))
	for _, choice := range choices {
		id, err := capabilityid.Parse(choice.Capability)
		if err != nil {
			// Let providerresolution return its canonical invalid-input diagnostic.
			result = append(result, choice)
			continue
		}
		if _, applies := required[id]; applies {
			result = append(result, choice)
		}
	}
	return result
}

func cloneRequirements(inputs []providerresolution.Requirement) []providerresolution.Requirement {
	result := make([]providerresolution.Requirement, len(inputs))
	for index, input := range inputs {
		result[index] = providerresolution.Requirement{
			Capability: input.Capability,
			Contract:   append([]byte(nil), input.Contract...),
			Source:     input.Source,
		}
	}
	return result
}

func cloneCandidates(inputs []providerresolution.Candidate) []providerresolution.Candidate {
	result := make([]providerresolution.Candidate, len(inputs))
	for index, input := range inputs {
		result[index] = providerresolution.Candidate{
			PluginID: input.PluginID,
			Contract: append([]byte(nil), input.Contract...),
			Source:   input.Source,
		}
	}
	return result
}

func activationRequirementSource(use generationactivation.NamespaceUse, cause providerresolution.RequirementSource) providerresolution.RequirementSource {
	value := "extensions." + use.Namespace() + " on " + use.SourceCapability().String()
	if len(value) > maximumRequirementSourceSize {
		sum := sha256.Sum256([]byte(value))
		suffix := "...#sha256:" + hex.EncodeToString(sum[:])
		value = value[:maximumRequirementSourceSize-len(suffix)] + suffix
	}
	return providerresolution.RequirementSource{
		Kind:             providerresolution.RequirementActivation,
		Reference:        value,
		ModulePath:       cause.ModulePath,
		Path:             cause.Path,
		Line:             cause.Line,
		Column:           cause.Column,
		Namespace:        use.Namespace(),
		SourceCapability: use.SourceCapability().String(),
	}
}

type extensionBuilder struct {
	pluginID    string
	api         string
	packagePath string
	source      string
	activations []SelectedActivation
}

func selectExtensions(catalog generationactivation.Catalog, requirements generationactivation.RequirementSet, resolution providerresolution.Result) ([]SelectedExtension, error) {
	builders := make(map[string]*extensionBuilder)
	var issues []error
	for _, requirement := range requirements.Requirements() {
		provider, exists := resolution.SelectedProvider(requirement.Capability())
		if !exists {
			issues = append(issues, fmt.Errorf(
				"%w: activation Capability %s has no selected ordinary provider",
				ErrInvariant,
				requirement.Capability(),
			))
			continue
		}
		usesByNamespace := make(map[string][]generationactivation.NamespaceUse)
		for _, use := range requirement.Uses() {
			usesByNamespace[use.Namespace()] = append(usesByNamespace[use.Namespace()], use)
		}
		namespaces := make([]string, 0, len(usesByNamespace))
		for namespace := range usesByNamespace {
			namespaces = append(namespaces, namespace)
		}
		sort.Strings(namespaces)
		for _, namespace := range namespaces {
			extension, err := catalog.Select(namespace, provider.PluginID())
			if err != nil {
				issues = append(issues, fmt.Errorf(
					"%w: namespace %q activation Capability %s selected plugin %q: %w",
					ErrSelectExtension,
					namespace,
					requirement.Capability(),
					provider.PluginID(),
					err,
				))
				continue
			}
			builder, exists := builders[extension.PluginID()]
			if !exists {
				builder = &extensionBuilder{
					pluginID:    extension.PluginID(),
					api:         extension.API(),
					packagePath: extension.Package(),
					source:      extension.Source(),
				}
				builders[extension.PluginID()] = builder
			} else if builder.api != extension.API() || builder.packagePath != extension.Package() || builder.source != extension.Source() {
				issues = append(issues, fmt.Errorf(
					"%w: selected plugin %q has inconsistent generation declarations across namespaces",
					ErrInvariant,
					extension.PluginID(),
				))
				continue
			}
			builder.activations = append(builder.activations, SelectedActivation{
				namespace:  namespace,
				capability: requirement.Capability(),
				uses:       append([]generationactivation.NamespaceUse(nil), usesByNamespace[namespace]...),
			})
		}
	}
	if len(issues) != 0 {
		return nil, closureError(issues)
	}

	plugins := make([]string, 0, len(builders))
	for plugin := range builders {
		plugins = append(plugins, plugin)
	}
	sort.Strings(plugins)
	extensions := make([]SelectedExtension, len(plugins))
	for index, plugin := range plugins {
		builder := builders[plugin]
		sort.Slice(builder.activations, func(left, right int) bool {
			if builder.activations[left].namespace != builder.activations[right].namespace {
				return builder.activations[left].namespace < builder.activations[right].namespace
			}
			return builder.activations[left].capability.String() < builder.activations[right].capability.String()
		})
		extensions[index] = SelectedExtension{
			pluginID:    builder.pluginID,
			api:         builder.api,
			packagePath: builder.packagePath,
			source:      builder.source,
			activations: append([]SelectedActivation(nil), builder.activations...),
		}
	}
	return extensions, nil
}

// ActivationEdge is one dependency from a metadata-bearing required contract
// to the ordinary Capability that activates its namespace interpreter.
type ActivationEdge struct {
	source             capabilityid.Identifier
	target             capabilityid.Identifier
	namespace          string
	requirementSources []string
}

// Source returns the metadata-bearing required Capability.
func (e ActivationEdge) Source() capabilityid.Identifier { return e.source }

// Target returns the namespace's activation Capability.
func (e ActivationEdge) Target() capabilityid.Identifier { return e.target }

// Namespace returns the extension namespace introducing this edge.
func (e ActivationEdge) Namespace() string { return e.namespace }

// RequirementSources returns source requirement provenance.
func (e ActivationEdge) RequirementSources() []string {
	return append([]string(nil), e.requirementSources...)
}

// ActivationCycleError contains one complete deterministic activation cycle.
type ActivationCycleError struct {
	edges []ActivationEdge
}

// Edges returns a defensive closed path in traversal order.
func (e *ActivationCycleError) Edges() []ActivationEdge {
	if e == nil {
		return nil
	}
	return append([]ActivationEdge(nil), e.edges...)
}

func (e *ActivationCycleError) Error() string {
	if e == nil {
		return ErrActivationCycle.Error()
	}
	var message strings.Builder
	message.WriteString(ErrActivationCycle.Error())
	message.WriteString(": ")
	for index, edge := range e.edges {
		if index == 0 {
			message.WriteString(edge.source.String())
		}
		fmt.Fprintf(
			&message,
			" --extensions.%s from [%s]--> %s",
			edge.namespace,
			strings.Join(edge.requirementSources, ", "),
			edge.target,
		)
	}
	message.WriteString("; correction: remove or version the activation dependency cycle; activation order cannot break semantic cycles")
	return message.String()
}

// Unwrap supports errors.Is with ErrActivationCycle.
func (*ActivationCycleError) Unwrap() error { return ErrActivationCycle }

func findActivationCycle(requirements generationactivation.RequirementSet) *ActivationCycleError {
	adjacency := make(map[capabilityid.Identifier][]ActivationEdge)
	nodeSet := make(map[capabilityid.Identifier]struct{})
	seenEdges := make(map[string]struct{})
	for _, requirement := range requirements.Requirements() {
		for _, use := range requirement.Uses() {
			edge := ActivationEdge{
				source:             use.SourceCapability(),
				target:             requirement.Capability(),
				namespace:          use.Namespace(),
				requirementSources: requirementSourceLabels(use.RequirementSources()),
			}
			key := edge.source.String() + "\x00" + edge.target.String() + "\x00" + edge.namespace
			if _, duplicate := seenEdges[key]; duplicate {
				continue
			}
			seenEdges[key] = struct{}{}
			adjacency[edge.source] = append(adjacency[edge.source], edge)
			nodeSet[edge.source] = struct{}{}
			nodeSet[edge.target] = struct{}{}
		}
	}
	nodes := make([]capabilityid.Identifier, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].String() < nodes[right].String() })
	for node := range adjacency {
		edges := adjacency[node]
		sort.Slice(edges, func(left, right int) bool {
			if edges[left].target != edges[right].target {
				return edges[left].target.String() < edges[right].target.String()
			}
			return edges[left].namespace < edges[right].namespace
		})
		adjacency[node] = edges
	}

	const (
		unvisited uint8 = iota
		visiting
		visited
	)
	state := make(map[capabilityid.Identifier]uint8, len(nodes))
	stackNodes := make([]capabilityid.Identifier, 0, len(nodes))
	stackEdges := make([]ActivationEdge, 0, len(nodes))
	var cycle []ActivationEdge
	var visit func(capabilityid.Identifier) bool
	visit = func(node capabilityid.Identifier) bool {
		state[node] = visiting
		stackNodes = append(stackNodes, node)
		for _, edge := range adjacency[node] {
			switch state[edge.target] {
			case unvisited:
				stackEdges = append(stackEdges, edge)
				if visit(edge.target) {
					return true
				}
				stackEdges = stackEdges[:len(stackEdges)-1]
			case visiting:
				position := len(stackNodes) - 1
				for position >= 0 && stackNodes[position] != edge.target {
					position--
				}
				cycle = append([]ActivationEdge(nil), stackEdges[position:]...)
				cycle = append(cycle, edge)
				return true
			}
		}
		stackNodes = stackNodes[:len(stackNodes)-1]
		state[node] = visited
		return false
	}
	for _, node := range nodes {
		if state[node] == unvisited && visit(node) {
			return &ActivationCycleError{edges: cycle}
		}
	}
	return nil
}

func requirementSourceLabels(sources []providerresolution.RequirementSource) []string {
	values := make([]string, len(sources))
	for index, source := range sources {
		values[index] = source.String()
	}
	return values
}

func closureError(issues []error) error {
	sort.SliceStable(issues, func(left, right int) bool {
		return issues[left].Error() < issues[right].Error()
	})
	return &ClosureError{issues: append([]error(nil), issues...)}
}

// ClosureError contains every independently invalid selected-extension result.
type ClosureError struct {
	issues []error
}

// Issues returns defensive causes in deterministic diagnostic order.
func (e *ClosureError) Issues() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.issues...)
}

func (e *ClosureError) Error() string {
	if e == nil {
		return ErrResolve.Error()
	}
	var message strings.Builder
	message.WriteString(ErrResolve.Error())
	for _, issue := range e.issues {
		message.WriteString("; ")
		message.WriteString(issue.Error())
	}
	return message.String()
}

// Unwrap supports errors.Is and errors.As for the overall and specific causes.
func (e *ClosureError) Unwrap() []error {
	if e == nil {
		return []error{ErrResolve}
	}
	causes := make([]error, 1, len(e.issues)+1)
	causes[0] = ErrResolve
	causes = append(causes, e.issues...)
	return causes
}
