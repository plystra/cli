// Package generationactivation indexes canonical namespace activation
// associations and selects only the extension owned by an ordinary selected
// activation-Capability provider.
package generationactivation

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/pluginmeta"
)

var (
	// ErrCatalog reports invalid or conflicting visible activation declarations.
	ErrCatalog = errors.New("build generation activation catalog")
	// ErrInvalidDeclaration reports generation provenance that is incomplete or
	// inconsistent with a normalized plugin declaration.
	ErrInvalidDeclaration = errors.New("invalid generation activation declaration")
	// ErrAssociationConflict reports one namespace associated with different
	// exact activation Capabilities.
	ErrAssociationConflict = errors.New("conflicting generation activation association")
	// ErrMissingAssociation reports metadata for a namespace with no visible
	// activation declaration.
	ErrMissingAssociation = errors.New("missing generation activation association")
	// ErrSelectedProviderExtension reports an activation provider that does not
	// own a matching compatible generation extension.
	ErrSelectedProviderExtension = errors.New("selected provider has no matching generation extension")
)

// Declaration adds one normalized plugin generation declaration and its
// module-relative or module-version provenance to the visible catalog.
type Declaration struct {
	PluginID   string
	Source     string
	Generation pluginmeta.Generation
}

type normalizedDeclaration struct {
	pluginID   string
	source     string
	generation pluginmeta.Generation
}

// Extension identifies one plugin package eligible to interpret a namespace
// only when that plugin is selected for the association's Capability.
type Extension struct {
	pluginID    string
	api         string
	packagePath string
	source      string
}

// PluginID returns the canonical owning Plugin ID.
func (e Extension) PluginID() string { return e.pluginID }

// API returns the exact versioned generation API.
func (e Extension) API() string { return e.api }

// Package returns the canonical plugin-relative generation package.
func (e Extension) Package() string { return e.packagePath }

// Source returns deterministic declaration provenance suitable for diagnostics.
func (e Extension) Source() string { return e.source }

// Association binds one extension namespace to one exact canonical activation
// Capability and all compatible candidate-provider extensions.
type Association struct {
	namespace  string
	capability capabilityid.Identifier
	extensions []Extension
}

// Namespace returns the lower-kebab extension namespace.
func (a Association) Namespace() string { return a.namespace }

// Capability returns the exact canonical activation Capability.
func (a Association) Capability() capabilityid.Identifier { return a.capability }

// Extensions returns defensive copies sorted by Plugin ID.
func (a Association) Extensions() []Extension {
	return append([]Extension(nil), a.extensions...)
}

// Catalog is one immutable namespace-sorted activation association index.
type Catalog struct {
	associations []Association
	byNamespace  map[string]int
}

// New validates and deterministically indexes every visible generation
// declaration. An empty declaration set is valid.
func New(inputs []Declaration) (Catalog, error) {
	declarations := make([]normalizedDeclaration, len(inputs))
	for index, input := range inputs {
		if err := pluginid.Validate(input.PluginID); err != nil {
			return Catalog{}, fmt.Errorf("%w: %w: declarations[%d].plugin_id %q is not canonical", ErrCatalog, ErrInvalidDeclaration, index, input.PluginID)
		}
		if input.Source == "" || len(input.Source) > 1024 || !utf8.ValidString(input.Source) || strings.ContainsAny(input.Source, "\x00\r\n") {
			return Catalog{}, fmt.Errorf("%w: %w: declarations[%d].source must be non-empty valid single-line UTF-8, at most 1024 bytes", ErrCatalog, ErrInvalidDeclaration, index)
		}
		generation := input.Generation
		if generation.API() == "" || generation.Package() == "" || len(generation.Activations()) == 0 {
			return Catalog{}, fmt.Errorf("%w: %w: plugin %q at %q has an incomplete generation declaration", ErrCatalog, ErrInvalidDeclaration, input.PluginID, input.Source)
		}
		for _, activation := range generation.Activations() {
			if strings.HasPrefix(activation.Capability().Name(), "kernel.") {
				return Catalog{}, fmt.Errorf(
					"%w: %w: plugin %q at %q associates namespace %q with intrinsic Capability %s; generation extensions must activate through an ordinary canonical Capability provided by the selected plugin",
					ErrCatalog,
					ErrInvalidDeclaration,
					input.PluginID,
					input.Source,
					activation.Namespace(),
					activation.Capability(),
				)
			}
		}
		declarations[index] = normalizedDeclaration{pluginID: input.PluginID, source: input.Source, generation: generation}
	}
	sort.Slice(declarations, func(left, right int) bool {
		if declarations[left].pluginID != declarations[right].pluginID {
			return declarations[left].pluginID < declarations[right].pluginID
		}
		return declarations[left].source < declarations[right].source
	})
	for index := 1; index < len(declarations); index++ {
		if declarations[index].pluginID == declarations[index-1].pluginID {
			return Catalog{}, fmt.Errorf(
				"%w: %w: plugin %q has generation declarations at both %q and %q",
				ErrCatalog,
				ErrInvalidDeclaration,
				declarations[index].pluginID,
				declarations[index-1].source,
				declarations[index].source,
			)
		}
	}

	byNamespace := make(map[string][]ConflictCandidate)
	for _, declaration := range declarations {
		generation := declaration.generation
		for _, activation := range generation.Activations() {
			namespace := activation.Namespace()
			byNamespace[namespace] = append(byNamespace[namespace], ConflictCandidate{
				pluginID:    declaration.pluginID,
				capability:  activation.Capability(),
				api:         generation.API(),
				packagePath: generation.Package(),
				source:      declaration.source,
			})
		}
	}
	namespaces := make([]string, 0, len(byNamespace))
	for namespace := range byNamespace {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	associations := make([]Association, 0, len(namespaces))
	for _, namespace := range namespaces {
		candidates := byNamespace[namespace]
		sort.Slice(candidates, func(left, right int) bool {
			if candidates[left].pluginID != candidates[right].pluginID {
				return candidates[left].pluginID < candidates[right].pluginID
			}
			return candidates[left].source < candidates[right].source
		})
		capability := candidates[0].capability
		conflict := false
		for _, candidate := range candidates[1:] {
			if candidate.capability != capability {
				conflict = true
				break
			}
		}
		if conflict {
			return Catalog{}, fmt.Errorf("%w: %w", ErrCatalog, &AssociationConflictError{namespace: namespace, candidates: candidates})
		}
		extensions := make([]Extension, len(candidates))
		for index, candidate := range candidates {
			extensions[index] = Extension{
				pluginID:    candidate.pluginID,
				api:         candidate.api,
				packagePath: candidate.packagePath,
				source:      candidate.source,
			}
		}
		associations = append(associations, Association{namespace: namespace, capability: capability, extensions: extensions})
	}
	index := make(map[string]int, len(associations))
	for position, association := range associations {
		index[association.namespace] = position
	}
	return Catalog{associations: associations, byNamespace: index}, nil
}

// Associations returns immutable association views sorted by namespace.
func (c Catalog) Associations() []Association {
	return append([]Association(nil), c.associations...)
}

// Association returns one exact namespace association.
func (c Catalog) Association(namespace string) (Association, bool) {
	position, exists := c.byNamespace[namespace]
	if !exists {
		return Association{}, false
	}
	return c.associations[position], true
}

// Select returns only the extension owned by the plugin selected through
// ordinary provider resolution for the namespace's activation Capability.
func (c Catalog) Select(namespace, selectedProvider string) (Extension, error) {
	association, exists := c.Association(namespace)
	if !exists {
		return Extension{}, fmt.Errorf("%w: namespace %q has no visible activation declaration", ErrMissingAssociation, namespace)
	}
	if err := pluginid.Validate(selectedProvider); err != nil {
		return Extension{}, fmt.Errorf("%w: selected provider %q for namespace %q is not a canonical Plugin ID", ErrSelectedProviderExtension, selectedProvider, namespace)
	}
	position := sort.Search(len(association.extensions), func(index int) bool {
		return association.extensions[index].pluginID >= selectedProvider
	})
	if position < len(association.extensions) && association.extensions[position].pluginID == selectedProvider {
		return association.extensions[position], nil
	}
	candidates := make([]string, len(association.extensions))
	for index, extension := range association.extensions {
		candidates[index] = extension.pluginID
	}
	return Extension{}, fmt.Errorf(
		"%w: selected provider %q for activation Capability %s does not declare namespace %q; compatible extension providers: [%s]",
		ErrSelectedProviderExtension,
		selectedProvider,
		association.capability,
		namespace,
		strings.Join(candidates, ", "),
	)
}

// ConflictCandidate records one complete declaration involved in a namespace
// association conflict.
type ConflictCandidate struct {
	pluginID    string
	capability  capabilityid.Identifier
	api         string
	packagePath string
	source      string
}

// PluginID returns the declaring Plugin ID.
func (c ConflictCandidate) PluginID() string { return c.pluginID }

// Capability returns the declared activation Capability.
func (c ConflictCandidate) Capability() capabilityid.Identifier { return c.capability }

// API returns the declared generation API.
func (c ConflictCandidate) API() string { return c.api }

// Package returns the declared plugin-relative generation package.
func (c ConflictCandidate) Package() string { return c.packagePath }

// Source returns declaration provenance.
func (c ConflictCandidate) Source() string { return c.source }

// AssociationConflictError contains every visible candidate for one
// ambiguously associated extension namespace.
type AssociationConflictError struct {
	namespace  string
	candidates []ConflictCandidate
}

// Namespace returns the conflicting extension namespace.
func (e *AssociationConflictError) Namespace() string {
	if e == nil {
		return ""
	}
	return e.namespace
}

// Candidates returns defensive copies in Plugin ID order.
func (e *AssociationConflictError) Candidates() []ConflictCandidate {
	if e == nil {
		return nil
	}
	return append([]ConflictCandidate(nil), e.candidates...)
}

func (e *AssociationConflictError) Error() string {
	if e == nil {
		return ErrAssociationConflict.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s: namespace %q names different activation Capabilities", ErrAssociationConflict, e.namespace)
	for _, candidate := range e.candidates {
		fmt.Fprintf(
			&message,
			"; plugin %q at %q declares %s (api %q, package %q)",
			candidate.pluginID,
			candidate.source,
			candidate.capability,
			candidate.api,
			candidate.packagePath,
		)
	}
	fmt.Fprintf(&message, "; correction: every provider interpreting extensions.%s must activate through one exact canonical Capability", e.namespace)
	return message.String()
}

// Unwrap supports errors.Is with ErrAssociationConflict.
func (*AssociationConflictError) Unwrap() error { return ErrAssociationConflict }
