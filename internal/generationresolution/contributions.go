package generationresolution

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
)

var (
	// ErrContributionGraph reports a failure to build one valid semantic plan
	// from all final selected-extension contributions.
	ErrContributionGraph = errors.New("resolve generation contribution graph")
	// ErrDuplicateContributionID reports a globally repeated contribution ID.
	ErrDuplicateContributionID = errors.New("duplicate generation contribution ID")
	// ErrContributionTokenProvider reports a missing or ambiguous token provider.
	ErrContributionTokenProvider = errors.New("invalid generation contribution token provider")
	// ErrContributionPointDependency reports a dependency that cannot run on
	// every path or runs in the wrong generation-point direction.
	ErrContributionPointDependency = errors.New("invalid generation point dependency")
	// ErrContributionCycle reports a closed token dependency path.
	ErrContributionCycle = errors.New("generation contribution cycle")
	// ErrUnorderedContributions reports semantic work at an ordered point whose
	// dependency declarations do not determine one execution order.
	ErrUnorderedContributions = errors.New("unordered generation contributions")
)

// ResolvedContribution is one final selected-extension contribution with its
// provider provenance. Result order is semantic execution order, not canonical
// serialization order.
type ResolvedContribution struct {
	pluginID     string
	contribution generation.NormalizedContribution
}

// PluginID returns the selected extension owner.
func (c ResolvedContribution) PluginID() string { return c.pluginID }

// ID returns the globally stable contribution identifier.
func (c ResolvedContribution) ID() string { return c.contribution.ID() }

// Namespace returns the interpreted extension namespace.
func (c ResolvedContribution) Namespace() string { return c.contribution.Namespace() }

// Source returns the canonical Capability whose metadata caused the output.
func (c ResolvedContribution) Source() generation.CapabilityID { return c.contribution.Source() }

// Point returns the versioned generation point.
func (c ResolvedContribution) Point() generation.GenerationPoint { return c.contribution.Point() }

// Requires returns defensive dependency-token copies in canonical order.
func (c ResolvedContribution) Requires() []generation.ContributionToken {
	return c.contribution.Requires()
}

// Provides returns defensive dependency-token copies in canonical order.
func (c ResolvedContribution) Provides() []generation.ContributionToken {
	return c.contribution.Provides()
}

// ContributionDependency is one token-labelled provider-to-consumer edge.
type ContributionDependency struct {
	provider ResolvedContribution
	consumer ResolvedContribution
	token    generation.ContributionToken
}

// Provider returns the contribution that provides Token.
func (d ContributionDependency) Provider() ResolvedContribution { return d.provider }

// Consumer returns the contribution that requires Token.
func (d ContributionDependency) Consumer() ResolvedContribution { return d.consumer }

// Token returns the exact semantic dependency token.
func (d ContributionDependency) Token() generation.ContributionToken { return d.token }

// ContributionCycleError contains one complete closed contribution/token path.
type ContributionCycleError struct {
	dependencies []ContributionDependency
}

// Dependencies returns defensive path entries in traversal order.
func (e *ContributionCycleError) Dependencies() []ContributionDependency {
	if e == nil {
		return nil
	}
	return append([]ContributionDependency(nil), e.dependencies...)
}

func (e *ContributionCycleError) Error() string {
	if e == nil || len(e.dependencies) == 0 {
		return ErrContributionCycle.Error()
	}
	var message strings.Builder
	message.WriteString(ErrContributionCycle.Error())
	message.WriteString(": ")
	message.WriteString(describeContribution(e.dependencies[0].provider))
	for _, dependency := range e.dependencies {
		fmt.Fprintf(
			&message,
			" --token %q--> %s",
			dependency.token,
			describeContribution(dependency.consumer),
		)
	}
	message.WriteString("; correction: remove the cycle or declare an acyclic token flow; discovery order cannot make the cycle valid")
	return message.String()
}

// Unwrap supports errors.Is with ErrContributionCycle.
func (*ContributionCycleError) Unwrap() error { return ErrContributionCycle }

func resolveContributionGraph(outputs []ExtensionOutput) ([]ResolvedContribution, error) {
	contributions := make([]ResolvedContribution, 0)
	byID := make(map[string][]ResolvedContribution)
	providersByToken := make(map[generation.ContributionToken][]ResolvedContribution)
	for _, output := range outputs {
		for _, contribution := range output.output.Contributions() {
			resolved := ResolvedContribution{
				pluginID:     output.pluginID,
				contribution: contribution,
			}
			contributions = append(contributions, resolved)
			byID[resolved.ID()] = append(byID[resolved.ID()], resolved)
			for _, token := range resolved.Provides() {
				providersByToken[token] = append(providersByToken[token], resolved)
			}
		}
	}

	if id, duplicates, exists := firstDuplicateContributionID(byID); exists {
		return nil, contributionGraphError(
			ErrDuplicateContributionID,
			"ID %q is returned by %s; contribution IDs are global across selected extensions",
			id,
			describeContributionList(duplicates),
		)
	}
	if token, providers, exists := firstMultiplyProvidedToken(providersByToken); exists {
		return nil, contributionGraphError(
			ErrContributionTokenProvider,
			"token %q is provided by %s; every required token must have exactly one provider",
			token,
			describeContributionList(providers),
		)
	}

	providerByToken := make(map[generation.ContributionToken]ResolvedContribution, len(providersByToken))
	for token, providers := range providersByToken {
		providerByToken[token] = providers[0]
	}
	diagnosticOrder := append([]ResolvedContribution(nil), contributions...)
	sort.Slice(diagnosticOrder, func(left, right int) bool {
		return contributionDiagnosticKey(diagnosticOrder[left]) < contributionDiagnosticKey(diagnosticOrder[right])
	})
	edges := make([]ContributionDependency, 0)
	for _, consumer := range diagnosticOrder {
		for _, token := range consumer.Requires() {
			provider, exists := providerByToken[token]
			if !exists {
				return nil, contributionGraphError(
					ErrContributionTokenProvider,
					"%s requires token %q, but no selected contribution provides it",
					describeContribution(consumer),
					token,
				)
			}
			edges = append(edges, ContributionDependency{
				provider: provider,
				consumer: consumer,
				token:    token,
			})
		}
	}

	adjacency := contributionAdjacency(edges)
	if cycle := findContributionCycle(contributions, adjacency); cycle != nil {
		return nil, fmt.Errorf("%w: %w", ErrContributionGraph, cycle)
	}
	for _, edge := range edges {
		if err := validateContributionPointDependency(edge); err != nil {
			return nil, err
		}
	}
	ordered, err := orderContributions(contributions, adjacency)
	if err != nil {
		return nil, err
	}
	return ordered, nil
}

func firstDuplicateContributionID(values map[string][]ResolvedContribution) (string, []ResolvedContribution, bool) {
	identifiers := make([]string, 0)
	for id, contributions := range values {
		if len(contributions) > 1 {
			identifiers = append(identifiers, id)
		}
	}
	if len(identifiers) == 0 {
		return "", nil, false
	}
	sort.Strings(identifiers)
	duplicates := append([]ResolvedContribution(nil), values[identifiers[0]]...)
	sortContributionsForDiagnostics(duplicates)
	return identifiers[0], duplicates, true
}

func firstMultiplyProvidedToken(values map[generation.ContributionToken][]ResolvedContribution) (generation.ContributionToken, []ResolvedContribution, bool) {
	tokens := make([]generation.ContributionToken, 0)
	for token, providers := range values {
		if len(providers) > 1 {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		return "", nil, false
	}
	sort.Slice(tokens, func(left, right int) bool { return tokens[left] < tokens[right] })
	providers := append([]ResolvedContribution(nil), values[tokens[0]]...)
	sortContributionsForDiagnostics(providers)
	return tokens[0], providers, true
}

func contributionAdjacency(edges []ContributionDependency) map[string][]ContributionDependency {
	adjacency := make(map[string][]ContributionDependency)
	for _, edge := range edges {
		id := edge.provider.ID()
		adjacency[id] = append(adjacency[id], edge)
	}
	for id, dependencies := range adjacency {
		sort.Slice(dependencies, func(left, right int) bool {
			return contributionDependencyDiagnosticKey(dependencies[left]) < contributionDependencyDiagnosticKey(dependencies[right])
		})
		adjacency[id] = dependencies
	}
	return adjacency
}

func findContributionCycle(contributions []ResolvedContribution, adjacency map[string][]ContributionDependency) *ContributionCycleError {
	nodes := append([]ResolvedContribution(nil), contributions...)
	sortContributionsForDiagnostics(nodes)
	const (
		contributionUnvisited uint8 = iota
		contributionVisiting
		contributionVisited
	)
	state := make(map[string]uint8, len(nodes))
	stackNodes := make([]string, 0, len(nodes))
	stackEdges := make([]ContributionDependency, 0, len(nodes))
	var cycle []ContributionDependency
	var visit func(ResolvedContribution) bool
	visit = func(node ResolvedContribution) bool {
		state[node.ID()] = contributionVisiting
		stackNodes = append(stackNodes, node.ID())
		for _, edge := range adjacency[node.ID()] {
			target := edge.consumer
			switch state[target.ID()] {
			case contributionUnvisited:
				stackEdges = append(stackEdges, edge)
				if visit(target) {
					return true
				}
				stackEdges = stackEdges[:len(stackEdges)-1]
			case contributionVisiting:
				position := len(stackNodes) - 1
				for position >= 0 && stackNodes[position] != target.ID() {
					position--
				}
				cycle = append([]ContributionDependency(nil), stackEdges[position:]...)
				cycle = append(cycle, edge)
				return true
			}
		}
		stackNodes = stackNodes[:len(stackNodes)-1]
		state[node.ID()] = contributionVisited
		return false
	}
	for _, node := range nodes {
		if state[node.ID()] == contributionUnvisited && visit(node) {
			return &ContributionCycleError{dependencies: cycle}
		}
	}
	return nil
}

func validateContributionPointDependency(dependency ContributionDependency) error {
	providerRank, providerKnown := generationPointRank(dependency.provider.Point())
	consumerRank, consumerKnown := generationPointRank(dependency.consumer.Point())
	if !providerKnown || !consumerKnown {
		return contributionGraphError(
			ErrContributionPointDependency,
			"token %q connects unsupported points %q and %q",
			dependency.token,
			dependency.provider.Point(),
			dependency.consumer.Point(),
		)
	}
	if providerRank > consumerRank {
		return contributionGraphError(
			ErrContributionPointDependency,
			"%s requires token %q from later %s; generation points run from http.ingress through invocation.prepare, invocation.complete, and http.egress",
			describeContribution(dependency.consumer),
			dependency.token,
			describeContribution(dependency.provider),
		)
	}
	if dependency.provider.Point() == generation.GenerationPointHTTPIngress &&
		(dependency.consumer.Point() == generation.GenerationPointInvocationPrepare || dependency.consumer.Point() == generation.GenerationPointInvocationComplete) {
		return contributionGraphError(
			ErrContributionPointDependency,
			"%s requires HTTP-only ingress token %q from %s, but invocation points also run for internal calls that have no http.ingress stage",
			describeContribution(dependency.consumer),
			dependency.token,
			describeContribution(dependency.provider),
		)
	}
	return nil
}

func orderContributions(contributions []ResolvedContribution, adjacency map[string][]ContributionDependency) ([]ResolvedContribution, error) {
	groups := make(map[generation.GenerationPoint][]ResolvedContribution)
	for _, contribution := range contributions {
		if _, known := generationPointRank(contribution.Point()); !known {
			return nil, contributionGraphError(ErrContributionPointDependency, "%s uses unsupported generation point %q", describeContribution(contribution), contribution.Point())
		}
		groups[contribution.Point()] = append(groups[contribution.Point()], contribution)
	}
	ordered := make([]ResolvedContribution, 0, len(contributions))
	for _, point := range []generation.GenerationPoint{
		generation.GenerationPointHTTPIngress,
		generation.GenerationPointInvocationPrepare,
		generation.GenerationPointInvocationComplete,
		generation.GenerationPointHTTPEgress,
	} {
		group := groups[point]
		if len(group) == 0 {
			continue
		}
		indegree := make(map[string]int, len(group))
		byID := make(map[string]ResolvedContribution, len(group))
		for _, contribution := range group {
			indegree[contribution.ID()] = 0
			byID[contribution.ID()] = contribution
		}
		for _, provider := range group {
			for _, dependency := range adjacency[provider.ID()] {
				if dependency.consumer.Point() == point {
					indegree[dependency.consumer.ID()]++
				}
			}
		}
		ready := make(map[string]ResolvedContribution)
		for id, contribution := range byID {
			if indegree[id] == 0 {
				ready[id] = contribution
			}
		}
		processed := 0
		for processed < len(group) {
			if len(ready) == 0 {
				return nil, contributionGraphError(ErrContributionCycle, "ordered point %q contains a dependency cycle", point)
			}
			if len(ready) > 1 {
				unordered := make([]ResolvedContribution, 0, len(ready))
				for _, contribution := range ready {
					unordered = append(unordered, contribution)
				}
				sortContributionsForDiagnostics(unordered)
				return nil, contributionGraphError(
					ErrUnorderedContributions,
					"ordered point %q has simultaneously ready semantic work %s; declare requires/provides dependencies that establish one order",
					point,
					describeContributionList(unordered),
				)
			}
			var current ResolvedContribution
			for _, contribution := range ready {
				current = contribution
			}
			ordered = append(ordered, current)
			delete(ready, current.ID())
			processed++
			for _, dependency := range adjacency[current.ID()] {
				if dependency.consumer.Point() == point {
					indegree[dependency.consumer.ID()]--
					if indegree[dependency.consumer.ID()] == 0 {
						ready[dependency.consumer.ID()] = byID[dependency.consumer.ID()]
					}
				}
			}
		}
	}
	return ordered, nil
}

func generationPointRank(point generation.GenerationPoint) (int, bool) {
	switch point {
	case generation.GenerationPointHTTPIngress:
		return 0, true
	case generation.GenerationPointInvocationPrepare:
		return 1, true
	case generation.GenerationPointInvocationComplete:
		return 2, true
	case generation.GenerationPointHTTPEgress:
		return 3, true
	default:
		return 0, false
	}
}

func sortContributionsForDiagnostics(contributions []ResolvedContribution) {
	sort.Slice(contributions, func(left, right int) bool {
		return contributionDiagnosticKey(contributions[left]) < contributionDiagnosticKey(contributions[right])
	})
}

func contributionDiagnosticKey(contribution ResolvedContribution) string {
	return strings.Join([]string{
		contribution.ID(),
		contribution.pluginID,
		contribution.Namespace(),
		contribution.Source().String(),
		string(contribution.Point()),
	}, "\x00")
}

func contributionDependencyDiagnosticKey(dependency ContributionDependency) string {
	return strings.Join([]string{
		dependency.provider.ID(),
		dependency.consumer.ID(),
		dependency.token.String(),
	}, "\x00")
}

func describeContribution(contribution ResolvedContribution) string {
	return fmt.Sprintf(
		"contribution %q from selected plugin %q (extensions.%s on %s at %s)",
		contribution.ID(),
		contribution.pluginID,
		contribution.Namespace(),
		contribution.Source().String(),
		contribution.Point(),
	)
}

func describeContributionList(contributions []ResolvedContribution) string {
	values := make([]string, len(contributions))
	for index, contribution := range contributions {
		values[index] = describeContribution(contribution)
	}
	return "[" + strings.Join(values, "; ") + "]"
}

func contributionGraphError(kind error, format string, arguments ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrContributionGraph, kind, fmt.Sprintf(format, arguments...))
}
