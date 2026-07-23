package applicationmeta

import (
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
)

type interfaceSetCandidate struct {
	valueSources   map[string]struct{}
	removalSources map[string]struct{}
}

func composeInterfaceRequirementSet(dependencies []Dependency, current []InterfaceRequirement, currentRemovals []interfaceRemoval, records map[string]*provenanceRecord) ([]InterfaceRequirement, error) {
	inherited := make(map[interfaceid.Identifier]*interfaceSetCandidate)
	for _, dependency := range dependencies {
		for _, requirement := range dependency.Manifest.InterfaceRequirements() {
			path := fmt.Sprintf("interfaces.require[%q]", requirement.id.String())
			source := dependencySource(dependency, requirement.source)
			addProvenance(records, path, interfaceDeclarationDigest("interfaces.require", requirement.id, false), source, false)
			candidate := ensureInterfaceSetCandidate(inherited, requirement.id)
			candidate.valueSources[source] = struct{}{}
		}
		for _, removal := range dependency.Manifest.removedInterfaceReqs {
			path := fmt.Sprintf("interfaces.require[%q]", removal.id.String())
			source := dependencySource(dependency, removal.source)
			addProvenance(records, path, interfaceDeclarationDigest("interfaces.require", removal.id, true), source, true)
			candidate := ensureInterfaceSetCandidate(inherited, removal.id)
			candidate.removalSources[source] = struct{}{}
		}
	}
	values := make(map[interfaceid.Identifier]InterfaceRequirement)
	for _, requirement := range current {
		values[requirement.id] = requirement
	}
	removed := interfaceRemovalSet(currentRemovals)
	ids := sortedInterfaceCandidateIDs(inherited)
	for _, id := range ids {
		if _, replaced := values[id]; replaced {
			continue
		}
		if _, explicitlyRemoved := removed[id]; explicitlyRemoved {
			continue
		}
		candidate := inherited[id]
		if len(candidate.valueSources) > 0 && len(candidate.removalSources) > 0 {
			return nil, inheritedInterfaceSetConflict("interfaces.require", id, candidate)
		}
		if len(candidate.valueSources) > 0 {
			values[id] = InterfaceRequirement{id: id, source: sortedSet(candidate.valueSources)[0]}
		}
	}
	result := make([]InterfaceRequirement, 0, len(values))
	for _, requirement := range values {
		result = append(result, requirement)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, nil
}

func ensureInterfaceSetCandidate(values map[interfaceid.Identifier]*interfaceSetCandidate, id interfaceid.Identifier) *interfaceSetCandidate {
	candidate := values[id]
	if candidate == nil {
		candidate = &interfaceSetCandidate{valueSources: make(map[string]struct{}), removalSources: make(map[string]struct{})}
		values[id] = candidate
	}
	return candidate
}

func sortedInterfaceCandidateIDs(values map[interfaceid.Identifier]*interfaceSetCandidate) []interfaceid.Identifier {
	ids := make([]interfaceid.Identifier, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left].String() < ids[right].String() })
	return ids
}

func interfaceRemovalSet(values []interfaceRemoval) map[interfaceid.Identifier]struct{} {
	result := make(map[interfaceid.Identifier]struct{}, len(values))
	for _, value := range values {
		result[value.id] = struct{}{}
	}
	return result
}

func inheritedInterfaceSetConflict(path string, id interfaceid.Identifier, candidate *interfaceSetCandidate) error {
	return fmt.Errorf(
		"%w: %s[%q] is added by %s and removed by %s; explicitly add or remove that exact Interface in the current Project configuration",
		ErrInheritedConflict,
		path,
		id.String(),
		strings.Join(sortedSet(candidate.valueSources), ", "),
		strings.Join(sortedSet(candidate.removalSources), ", "),
	)
}

type implementationCandidate struct {
	choice  ImplementationChoice
	removed bool
	sources map[string]struct{}
}

func composeImplementationChoices(dependencies []Dependency, current []ImplementationChoice, currentRemovals []interfaceRemoval, records map[string]*provenanceRecord) ([]ImplementationChoice, error) {
	inherited := make(map[interfaceid.Identifier]map[string]*implementationCandidate)
	for _, dependency := range dependencies {
		for _, choice := range dependency.Manifest.ImplementationChoices() {
			path := fmt.Sprintf("interfaces.use[%q]", choice.interfaceID.String())
			source := dependencySource(dependency, choice.source)
			digest := implementationChoiceDigest(choice)
			addProvenance(records, path, digest, source, false)
			byDigest := inherited[choice.interfaceID]
			if byDigest == nil {
				byDigest = make(map[string]*implementationCandidate)
				inherited[choice.interfaceID] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				choice.source = source
				candidate = &implementationCandidate{choice: choice, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
			if source < candidate.choice.source {
				candidate.choice.source = source
			}
		}
		for _, removal := range dependency.Manifest.removedImplementationChoices {
			path := fmt.Sprintf("interfaces.use[%q]", removal.id.String())
			source := dependencySource(dependency, removal.source)
			digest := interfaceDeclarationDigest("interfaces.use", removal.id, true)
			addProvenance(records, path, digest, source, true)
			byDigest := inherited[removal.id]
			if byDigest == nil {
				byDigest = make(map[string]*implementationCandidate)
				inherited[removal.id] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				candidate = &implementationCandidate{removed: true, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
		}
	}
	selected := make(map[interfaceid.Identifier]ImplementationChoice)
	for _, choice := range current {
		selected[choice.interfaceID] = choice
	}
	removed := interfaceRemovalSet(currentRemovals)
	ids := make([]interfaceid.Identifier, 0, len(inherited))
	for id := range inherited {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left].String() < ids[right].String() })
	for _, id := range ids {
		candidates := inherited[id]
		if _, replaced := selected[id]; replaced {
			continue
		}
		if _, explicitlyRemoved := removed[id]; explicitlyRemoved {
			continue
		}
		if len(candidates) != 1 {
			return nil, inheritedImplementationConflict(id, candidates)
		}
		for _, candidate := range candidates {
			if !candidate.removed {
				selected[id] = candidate.choice
			}
		}
	}
	result := make([]ImplementationChoice, 0, len(selected))
	for _, choice := range selected {
		result = append(result, choice)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].interfaceID.String() < result[right].interfaceID.String()
	})
	return result, nil
}

func inheritedImplementationConflict(id interfaceid.Identifier, candidates map[string]*implementationCandidate) error {
	digests := make([]string, 0, len(candidates))
	for digest := range candidates {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	parts := make([]string, 0, len(digests))
	for _, digest := range digests {
		candidate := candidates[digest]
		declaration := candidate.choice.constructor.String()
		if candidate.removed {
			declaration = "<removed>"
		}
		parts = append(parts, fmt.Sprintf("%s from %s", declaration, strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: interfaces.use[%q] has incompatible Implementation declarations: %s; set or remove that exact key in the current Project configuration", ErrInheritedConflict, id.String(), strings.Join(parts, "; "))
}

type interfacePolicyCandidate struct {
	policy  InterfacePolicy
	removed bool
	sources map[string]struct{}
}

func composeInterfacePolicies(dependencies []Dependency, current []InterfacePolicy, currentRemovals []interfaceRemoval, records map[string]*provenanceRecord) ([]InterfacePolicy, error) {
	inherited := make(map[interfaceid.Identifier]map[string]*interfacePolicyCandidate)
	for _, dependency := range dependencies {
		for _, policy := range dependency.Manifest.InterfacePolicies() {
			path := interfacePolicyPath(policy.interfaceID)
			source := dependencySource(dependency, policy.source)
			digest := interfacePolicyDigest(policy)
			addProvenance(records, path, digest, source, false)
			byDigest := inherited[policy.interfaceID]
			if byDigest == nil {
				byDigest = make(map[string]*interfacePolicyCandidate)
				inherited[policy.interfaceID] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				policy.source = source
				candidate = &interfacePolicyCandidate{policy: policy, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
			if source < candidate.policy.source {
				candidate.policy.source = source
			}
		}
		for _, removal := range dependency.Manifest.removedInterfacePolicies {
			path := interfacePolicyPath(removal.id)
			source := dependencySource(dependency, removal.source)
			digest := interfacePolicyRemovalDigest(removal.id)
			addProvenance(records, path, digest, source, true)
			byDigest := inherited[removal.id]
			if byDigest == nil {
				byDigest = make(map[string]*interfacePolicyCandidate)
				inherited[removal.id] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				candidate = &interfacePolicyCandidate{removed: true, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
		}
	}

	selected := make(map[interfaceid.Identifier]InterfacePolicy)
	for _, policy := range current {
		selected[policy.interfaceID] = policy
	}
	removed := interfaceRemovalSet(currentRemovals)
	ids := make([]interfaceid.Identifier, 0, len(inherited))
	for id := range inherited {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left].String() < ids[right].String() })
	for _, id := range ids {
		if _, replaced := selected[id]; replaced {
			continue
		}
		if _, explicitlyRemoved := removed[id]; explicitlyRemoved {
			continue
		}
		candidates := inherited[id]
		if len(candidates) != 1 {
			return nil, inheritedInterfacePolicyConflict(id, candidates)
		}
		for _, candidate := range candidates {
			if !candidate.removed {
				selected[id] = candidate.policy
			}
		}
	}
	result := make([]InterfacePolicy, 0, len(selected))
	for _, policy := range selected {
		result = append(result, policy)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].interfaceID.String() < result[right].interfaceID.String()
	})
	return result, nil
}

func inheritedInterfacePolicyConflict(id interfaceid.Identifier, candidates map[string]*interfacePolicyCandidate) error {
	digests := make([]string, 0, len(candidates))
	for digest := range candidates {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	parts := make([]string, 0, len(digests))
	for _, digest := range digests {
		candidate := candidates[digest]
		declaration := candidate.policy.timeout.String()
		if candidate.removed {
			declaration = "<removed>"
		}
		parts = append(parts, fmt.Sprintf("%s from %s", declaration, strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: %s has incompatible timeout declarations: %s; set or remove that exact Interface policy in the current Project configuration", ErrInheritedConflict, interfacePolicyPath(id), strings.Join(parts, "; "))
}

func interfacePolicyPath(id interfaceid.Identifier) string {
	return fmt.Sprintf("interfaces.policies[%q].timeout", id.String())
}

func interfacePolicyDigest(policy InterfacePolicy) string {
	return digestStrings("interfaces.policies", policy.interfaceID.String(), "timeout", policy.timeout.String())
}

func interfacePolicyRemovalDigest(id interfaceid.Identifier) string {
	return digestStrings("interfaces.policies", id.String(), "timeout", "removed")
}

func interfaceDeclarationDigest(path string, id interfaceid.Identifier, removed bool) string {
	if removed {
		return digestStrings(path, id.String(), "removed")
	}
	return digestStrings(path, id.String())
}

func implementationChoiceDigest(choice ImplementationChoice) string {
	return digestStrings("interfaces.use", choice.interfaceID.String(), choice.constructor.String())
}
