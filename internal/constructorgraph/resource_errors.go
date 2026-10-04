package constructorgraph

import (
	"errors"
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

var (
	// ErrInvalidResourceInstance reports an invalid name or selected provider.
	ErrInvalidResourceInstance = errors.New("invalid selected Resource instance")
	// ErrInvalidResourceBinding reports an invalid explicit address or target.
	ErrInvalidResourceBinding = errors.New("invalid explicit Resource binding")
	// ErrMissingResourceBinding reports a dependency without a compatible instance.
	ErrMissingResourceBinding = errors.New("missing required Resource binding")
	// ErrAmbiguousResourceBinding reports multiple implicit compatible instances.
	ErrAmbiguousResourceBinding = errors.New("ambiguous required Resource binding")
)

// ResourceInstanceError retains the exact instance selection and its sources.
type ResourceInstanceError struct {
	name     string
	provider constructorsymbol.Symbol
	sources  []ResourceSource
	detail   string
}

func (e *ResourceInstanceError) Name() string                       { return e.name }
func (e *ResourceInstanceError) Provider() constructorsymbol.Symbol { return e.provider }
func (e *ResourceInstanceError) Sources() []ResourceSource {
	return append([]ResourceSource(nil), e.sources...)
}
func (e *ResourceInstanceError) ModulePath() string { return e.source().ModulePath }
func (e *ResourceInstanceError) SourcePath() string { return e.source().Path }
func (e *ResourceInstanceError) Line() int          { return e.source().Line }
func (e *ResourceInstanceError) Column() int        { return e.source().Column }
func (e *ResourceInstanceError) source() ResourceSource {
	if len(e.sources) > 0 {
		return e.sources[0]
	}
	return ResourceSource{}
}
func (*ResourceInstanceError) Unwrap() error { return ErrInvalidResourceInstance }
func (e *ResourceInstanceError) Error() string {
	return fmt.Sprintf("%s: %s provider %s: %s from [%s]; correction: select one visible Resource provider for a valid instance name", ErrInvalidResourceInstance, e.name, e.provider, e.detail, resourceSourceSummary(e.sources))
}

// ResourceBindingError identifies an invalid explicit address or an unresolved
// active dependency, retaining candidates and safe source-bearing evidence.
type ResourceBindingError struct {
	condition  error
	dependency ResourceDependency
	candidates []ResourceNode
	detail     string
}

func (e *ResourceBindingError) Unwrap() error { return e.condition }
func (e *ResourceBindingError) Dependency() ResourceDependency {
	return cloneResourceDependencies([]ResourceDependency{e.dependency})[0]
}
func (e *ResourceBindingError) Namespace() ResourceConsumerNamespace { return e.dependency.namespace }
func (e *ResourceBindingError) Consumer() string                     { return e.dependency.consumer }
func (e *ResourceBindingError) Constructor() constructorsymbol.Symbol {
	return e.dependency.constructor
}
func (e *ResourceBindingError) ResourceID() interfaceid.Identifier { return e.dependency.resourceID }
func (e *ResourceBindingError) ParameterName() string              { return e.dependency.parameterName }
func (e *ResourceBindingError) ParameterPosition() int             { return e.dependency.parameterPosition }
func (e *ResourceBindingError) Target() string                     { return e.dependency.instanceName }
func (e *ResourceBindingError) Sources() []ResourceSource          { return e.dependency.Sources() }
func (e *ResourceBindingError) ModulePath() string                 { return e.source().ModulePath }
func (e *ResourceBindingError) SourcePath() string                 { return e.source().Path }
func (e *ResourceBindingError) Line() int                          { return e.source().Line }
func (e *ResourceBindingError) Column() int                        { return e.source().Column }
func (e *ResourceBindingError) Candidates() []ResourceNode         { return cloneResourceNodes(e.candidates) }
func (e *ResourceBindingError) source() ResourceSource {
	if e.dependency.declaration.Reference != "" {
		return e.dependency.declaration
	}
	if len(e.dependency.sources) > 0 {
		return e.dependency.sources[0]
	}
	return ResourceSource{}
}
func (e *ResourceBindingError) Error() string {
	var message strings.Builder
	fmt.Fprintf(&message, "%s: %s %s at %s", e.condition, e.Namespace(), e.Consumer(), e.source())
	if e.Constructor().String() != "" {
		fmt.Fprintf(&message, " constructor %s", e.Constructor())
	}
	if e.ResourceID().String() != "" {
		fmt.Fprintf(&message, " requires %s through parameter %d (%s)", e.ResourceID(), e.ParameterPosition(), e.ParameterName())
	} else if e.ParameterName() != "" {
		fmt.Fprintf(&message, " parameter %s", e.ParameterName())
	}
	if e.Target() != "" {
		fmt.Fprintf(&message, " -> %s", e.Target())
	}
	fmt.Fprintf(&message, "; %s from [%s]", e.detail, resourceSourceSummary(e.Sources()))
	if len(e.dependency.consumerSelectionSources) > 0 {
		fmt.Fprintf(&message, "; consumer selected from [%s]", resourceSourceSummary(e.dependency.consumerSelectionSources))
	}
	for _, candidate := range e.candidates {
		fmt.Fprintf(&message, "; instance %s provides %s using %s at %s selected from [%s]", candidate.name, candidate.resourceID, candidate.provider.Symbol(), candidate.provider.Source(), resourceSourceSummary(candidate.sources))
	}
	message.WriteString("; correction: select a compatible instance and bind the exact consumer parameter, or remove the invalid binding")
	return message.String()
}

// ResourceCycleError is a complete ordered cycle between named instances.
// Its last target is its first consumer, even when providers are reused.
type ResourceCycleError struct{ steps []ResourceDependency }

func (*ResourceCycleError) Unwrap() error                 { return ErrCycle }
func (e *ResourceCycleError) Steps() []ResourceDependency { return cloneResourceDependencies(e.steps) }
func (e *ResourceCycleError) Error() string {
	var message strings.Builder
	message.WriteString(ErrCycle.Error())
	for _, step := range e.steps {
		fmt.Fprintf(&message, "; instance %s provider %s at %s requires %s through parameter %d (%s) -> instance %s provider %s by %s binding from [%s] selected from [%s]", step.consumer, step.constructor, step.declaration, step.resourceID, step.parameterPosition, step.parameterName, step.instanceName, step.provider, step.reason, resourceSourceSummary(step.sources), resourceSourceSummary(step.selectionSources))
	}
	message.WriteString("; correction: remove a Resource dependency or bind it to an acyclic compatible instance")
	return message.String()
}

func resourceSourceSummary(sources []ResourceSource) string {
	values := make([]string, len(sources))
	for index, source := range sources {
		values[index] = source.Reference
	}
	return strings.Join(values, ", ")
}
