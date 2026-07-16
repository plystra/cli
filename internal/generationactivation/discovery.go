package generationactivation

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/providerresolution"
)

var (
	// ErrDiscoverRequirements reports that extension metadata on the current
	// exact requirement set could not be mapped to activation Capabilities.
	ErrDiscoverRequirements = errors.New("discover generation activation requirements")
	// ErrInvalidResolution reports a result that does not contain the canonical
	// exact contracts promised by providerresolution.Result.
	ErrInvalidResolution = errors.New("invalid provider resolution for activation discovery")
)

// NamespaceUse records one required canonical Capability whose normalized
// contract uses an extension namespace.
type NamespaceUse struct {
	namespace          string
	sourceCapability   capabilityid.Identifier
	requirementSources []string
}

// Namespace returns the exact lower-kebab extension namespace.
func (u NamespaceUse) Namespace() string { return u.namespace }

// SourceCapability returns the required contract containing the namespace.
func (u NamespaceUse) SourceCapability() capabilityid.Identifier {
	return u.sourceCapability
}

// RequirementSources returns sorted provenance for the source requirement.
func (u NamespaceUse) RequirementSources() []string {
	return append([]string(nil), u.requirementSources...)
}

// ActivationRequirement is one ordinary canonical activation Capability to add
// to the next provider-resolution pass, with every cause retained.
type ActivationRequirement struct {
	capability capabilityid.Identifier
	uses       []NamespaceUse
}

// Capability returns the exact ordinary activation Capability.
func (r ActivationRequirement) Capability() capabilityid.Identifier {
	return r.capability
}

// Uses returns defensive namespace-use provenance in canonical order.
func (r ActivationRequirement) Uses() []NamespaceUse {
	return append([]NamespaceUse(nil), r.uses...)
}

// RequirementSet is one immutable deterministic set of activation requirements.
type RequirementSet struct {
	requirements []ActivationRequirement
	byCapability map[capabilityid.Identifier]int
}

// Requirements returns defensive values sorted by activation Capability ID.
func (s RequirementSet) Requirements() []ActivationRequirement {
	return append([]ActivationRequirement(nil), s.requirements...)
}

// Requirement returns one activation requirement by exact canonical ID.
func (s RequirementSet) Requirement(id capabilityid.Identifier) (ActivationRequirement, bool) {
	position, exists := s.byCapability[id]
	if !exists {
		return ActivationRequirement{}, false
	}
	return s.requirements[position], true
}

// DiscoverRequirements finds every extension namespace on the current exact
// requirement set and maps it through the catalog. It returns no partial set if
// any namespace lacks an unambiguous ordinary activation association.
func (c Catalog) DiscoverRequirements(resolution providerresolution.Result) (RequirementSet, error) {
	usesByActivation := make(map[capabilityid.Identifier][]NamespaceUse)
	missingByNamespace := make(map[string][]NamespaceUse)
	seenCapabilities := make(map[capabilityid.Identifier]struct{})
	var issues []error

	for _, capability := range resolution.Capabilities() {
		contract := capability.ContractJSON()
		canonical, err := capabilitymeta.NormalizeSchema(contract)
		if err != nil || !bytes.Equal(canonical, contract) {
			issues = append(issues, fmt.Errorf(
				"%w: Capability %s does not contain canonical exact contract JSON",
				ErrInvalidResolution,
				capability.ID(),
			))
			continue
		}
		metadata, err := capabilitymeta.Parse(contract)
		if err != nil || metadata.ID() != capability.ID() {
			issues = append(issues, fmt.Errorf(
				"%w: Capability %s contract identity is inconsistent",
				ErrInvalidResolution,
				capability.ID(),
			))
			continue
		}
		if _, duplicate := seenCapabilities[capability.ID()]; duplicate {
			issues = append(issues, fmt.Errorf(
				"%w: duplicate resolved Capability %s",
				ErrInvalidResolution,
				capability.ID(),
			))
			continue
		}
		seenCapabilities[capability.ID()] = struct{}{}

		for _, extension := range metadata.Extensions().Values() {
			use := NamespaceUse{
				namespace:          extension.Namespace(),
				sourceCapability:   capability.ID(),
				requirementSources: capability.Sources(),
			}
			association, exists := c.Association(extension.Namespace())
			if !exists {
				missingByNamespace[extension.Namespace()] = append(missingByNamespace[extension.Namespace()], use)
				continue
			}
			if strings.HasPrefix(association.Capability().Name(), "kernel.") {
				issues = append(issues, fmt.Errorf(
					"%w: namespace %q is associated with intrinsic Capability %s",
					ErrInvalidResolution,
					extension.Namespace(),
					association.Capability(),
				))
				continue
			}
			usesByActivation[association.Capability()] = append(usesByActivation[association.Capability()], use)
		}
	}

	missingNamespaces := make([]string, 0, len(missingByNamespace))
	for namespace := range missingByNamespace {
		missingNamespaces = append(missingNamespaces, namespace)
	}
	sort.Strings(missingNamespaces)
	for _, namespace := range missingNamespaces {
		uses := missingByNamespace[namespace]
		sortNamespaceUses(uses)
		issues = append(issues, &MissingAssociationError{
			namespace: namespace,
			uses:      append([]NamespaceUse(nil), uses...),
		})
	}
	if len(issues) != 0 {
		return RequirementSet{}, discoveryError(issues)
	}

	capabilities := make([]capabilityid.Identifier, 0, len(usesByActivation))
	for capability := range usesByActivation {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(left, right int) bool {
		return capabilities[left].String() < capabilities[right].String()
	})
	requirements := make([]ActivationRequirement, len(capabilities))
	index := make(map[capabilityid.Identifier]int, len(capabilities))
	for position, capability := range capabilities {
		uses := usesByActivation[capability]
		sortNamespaceUses(uses)
		requirements[position] = ActivationRequirement{
			capability: capability,
			uses:       append([]NamespaceUse(nil), uses...),
		}
		index[capability] = position
	}
	return RequirementSet{requirements: requirements, byCapability: index}, nil
}

func sortNamespaceUses(uses []NamespaceUse) {
	sort.Slice(uses, func(left, right int) bool {
		if uses[left].namespace != uses[right].namespace {
			return uses[left].namespace < uses[right].namespace
		}
		if uses[left].sourceCapability != uses[right].sourceCapability {
			return uses[left].sourceCapability.String() < uses[right].sourceCapability.String()
		}
		return strings.Join(uses[left].requirementSources, "\x00") < strings.Join(uses[right].requirementSources, "\x00")
	})
}

func discoveryError(issues []error) error {
	sort.SliceStable(issues, func(left, right int) bool {
		return issues[left].Error() < issues[right].Error()
	})
	return &DiscoveryError{issues: append([]error(nil), issues...)}
}

// DiscoveryError contains every independently missing or inconsistent
// activation association found in one pass.
type DiscoveryError struct {
	issues []error
}

// Issues returns defensive causes in deterministic diagnostic order.
func (e *DiscoveryError) Issues() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.issues...)
}

func (e *DiscoveryError) Error() string {
	if e == nil {
		return ErrDiscoverRequirements.Error()
	}
	var message strings.Builder
	message.WriteString(ErrDiscoverRequirements.Error())
	for _, issue := range e.issues {
		message.WriteString("; ")
		message.WriteString(issue.Error())
	}
	return message.String()
}

// Unwrap supports errors.Is and errors.As for the overall and specific causes.
func (e *DiscoveryError) Unwrap() []error {
	if e == nil {
		return []error{ErrDiscoverRequirements}
	}
	causes := make([]error, 1, len(e.issues)+1)
	causes[0] = ErrDiscoverRequirements
	causes = append(causes, e.issues...)
	return causes
}

// MissingAssociationError reports every required Capability using one
// namespace for which no generation activation declaration is visible.
type MissingAssociationError struct {
	namespace string
	uses      []NamespaceUse
}

// Namespace returns the unclaimed extension namespace.
func (e *MissingAssociationError) Namespace() string {
	if e == nil {
		return ""
	}
	return e.namespace
}

// Uses returns all affected required Capabilities in canonical order.
func (e *MissingAssociationError) Uses() []NamespaceUse {
	if e == nil {
		return nil
	}
	return append([]NamespaceUse(nil), e.uses...)
}

func (e *MissingAssociationError) Error() string {
	if e == nil {
		return ErrMissingAssociation.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s: extensions.%s is used by", ErrMissingAssociation, e.namespace)
	for _, use := range e.uses {
		fmt.Fprintf(
			&message,
			" %s required from [%s];",
			use.sourceCapability,
			strings.Join(use.requirementSources, ", "),
		)
	}
	fmt.Fprintf(
		&message,
		" correction: add an intended plugin generation declaration associating namespace %q with one exact ordinary activation Capability",
		e.namespace,
	)
	return message.String()
}

// Unwrap supports errors.Is with ErrMissingAssociation.
func (*MissingAssociationError) Unwrap() error { return ErrMissingAssociation }
