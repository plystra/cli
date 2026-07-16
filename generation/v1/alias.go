package generation

import (
	"fmt"
	"sort"
	"strings"
)

const maximumCapabilityAliasContributions = 4096

// CapabilityAliasContribution proposes one application-local alternate name
// for a direct canonical Capability target. The selected extension plugin is
// attached as source provenance by the CLI after protocol validation.
type CapabilityAliasContribution struct {
	ID         string       `json:"id"`
	Namespace  string       `json:"namespace"`
	Source     CapabilityID `json:"source"`
	Alias      CapabilityID `json:"alias"`
	Target     CapabilityID `json:"target"`
	Exposure   *Exposure    `json:"exposure,omitempty"`
	Deprecated string       `json:"deprecated,omitempty"`
	_          struct{}
}

// NormalizedCapabilityAliasContribution is one immutable validated extension
// proposal ready for CLI-owned final Alias-map merging.
type NormalizedCapabilityAliasContribution struct {
	id          string
	namespace   string
	source      CapabilityID
	alias       CapabilityID
	target      CapabilityID
	exposure    Exposure
	hasExposure bool
	deprecated  string
}

// ID returns the stable extension-local Alias contribution identifier.
func (c NormalizedCapabilityAliasContribution) ID() string { return c.id }

// Namespace returns the interpreted extension namespace.
func (c NormalizedCapabilityAliasContribution) Namespace() string { return c.namespace }

// Source returns the canonical Capability whose metadata caused the proposal.
func (c NormalizedCapabilityAliasContribution) Source() CapabilityID { return c.source }

// Alias returns the proposed application-local Capability ID.
func (c NormalizedCapabilityAliasContribution) Alias() CapabilityID { return c.alias }

// Target returns the direct canonical Capability target.
func (c NormalizedCapabilityAliasContribution) Target() CapabilityID { return c.target }

// Exposure returns an explicit normalized narrowing. A false result means the
// Alias inherits the target's exposure exactly.
func (c NormalizedCapabilityAliasContribution) Exposure() (Exposure, bool) {
	if !c.hasExposure {
		return Exposure{}, false
	}
	return c.exposure, c.hasExposure
}

// Deprecated returns the application-local deprecation message, if any.
func (c NormalizedCapabilityAliasContribution) Deprecated() string { return c.deprecated }

func normalizeCapabilityAliasContributions(context Context, inputs []CapabilityAliasContribution) ([]NormalizedCapabilityAliasContribution, error) {
	if len(inputs) > maximumCapabilityAliasContributions {
		return nil, invalidOutput(
			"alias_contributions contains %d entries; maximum is %d",
			len(inputs),
			maximumCapabilityAliasContributions,
		)
	}
	contributions := make([]NormalizedCapabilityAliasContribution, 0, len(inputs))
	seen := make(map[string]int, len(inputs))
	for index, input := range inputs {
		field := fmt.Sprintf("alias_contributions[%d]", index)
		if !validStableID(input.ID) {
			return nil, invalidOutput("%s.id %q is not a stable lower-kebab identifier", field, input.ID)
		}
		if previous, duplicate := seen[input.ID]; duplicate {
			return nil, invalidOutput("%s.id duplicates Alias contribution %q from alias_contributions[%d]", field, input.ID, previous)
		}
		if err := validateOutputSource(context, field, input.Namespace, input.Source); err != nil {
			return nil, err
		}
		if input.Alias.String() == "" {
			return nil, invalidOutput("%s.alias is not a canonical Capability ID", field)
		}
		if input.Target.String() == "" {
			return nil, invalidOutput("%s.target is not a canonical Capability ID", field)
		}
		if _, collision := context.Capability(input.Alias); collision {
			return nil, invalidOutput("%s.alias %q collides with a canonical Capability", field, input.Alias.String())
		}
		if strings.HasPrefix(input.Alias.Name(), "kernel.") {
			return nil, invalidOutput("%s.alias %q uses the reserved kernel.* canonical namespace", field, input.Alias.String())
		}
		target, exists := context.Capability(input.Target)
		if !exists {
			return nil, invalidOutput("%s.target %q is not a visible canonical Capability", field, input.Target.String())
		}
		if !containsCapabilityID(context.requirements, input.Target) {
			return nil, invalidOutput("%s.target %q is not a current canonical requirement", field, input.Target.String())
		}
		if input.Alias.Major() != input.Target.Major() {
			return nil, invalidOutput("%s.alias %q and target %q must use the same major version", field, input.Alias.String(), input.Target.String())
		}

		normalizedExposure := Exposure{}
		hasExposure := false
		if input.Exposure != nil {
			normalizedExposure = *input.Exposure
			if !exposureSubset(normalizedExposure, target.Exposure()) {
				return nil, invalidOutput("%s.exposure broadens target %q exposure", field, input.Target.String())
			}
			hasExposure = normalizedExposure != target.Exposure()
		}
		if len(input.Deprecated) > 1024 || strings.ContainsRune(input.Deprecated, '\x00') {
			return nil, invalidOutput("%s.deprecated must be at most 1024 bytes and contain no NUL", field)
		}

		seen[input.ID] = index
		contributions = append(contributions, NormalizedCapabilityAliasContribution{
			id:          input.ID,
			namespace:   input.Namespace,
			source:      input.Source,
			alias:       input.Alias,
			target:      input.Target,
			exposure:    normalizedExposure,
			hasExposure: hasExposure,
			deprecated:  input.Deprecated,
		})
	}
	sort.Slice(contributions, func(left, right int) bool {
		return contributions[left].id < contributions[right].id
	})
	return contributions, nil
}
