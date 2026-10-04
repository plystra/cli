package constructorgraph

import (
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

// ResourceBindingUnsupportedError identifies the first Resource parameter of
// a reachable constructor, preserving the Interface path that activated it.
type ResourceBindingUnsupportedError struct {
	implementation implementationinventory.Implementation
	dependency     implementationinventory.RequiredResource
	path           dependencyPath
}

func (e *ResourceBindingUnsupportedError) Constructor() constructorsymbol.Symbol {
	return e.implementation.Symbol()
}
func (e *ResourceBindingUnsupportedError) ResourceID() interfaceid.Identifier {
	return e.dependency.ID()
}
func (e *ResourceBindingUnsupportedError) ParameterName() string {
	return e.dependency.ParameterName()
}
func (e *ResourceBindingUnsupportedError) ParameterPosition() int {
	return e.dependency.ParameterPosition()
}
func (e *ResourceBindingUnsupportedError) ModulePath() string { return e.implementation.ModulePath() }
func (e *ResourceBindingUnsupportedError) SourcePath() string { return e.implementation.SourcePath() }
func (e *ResourceBindingUnsupportedError) Line() int {
	return e.implementation.Declaration().Position().Line
}
func (e *ResourceBindingUnsupportedError) Column() int {
	return e.implementation.Declaration().Position().Column
}
func (e *ResourceBindingUnsupportedError) Root() Root { return e.path.clone().root }
func (e *ResourceBindingUnsupportedError) Steps() []PathStep {
	return clonePathSteps(e.path.steps)
}
func (e *ResourceBindingUnsupportedError) RequirementSources() []RequirementSource {
	return append([]RequirementSource(nil), e.path.root.sources...)
}

func (e *ResourceBindingUnsupportedError) Error() string {
	if e == nil {
		return ErrResourceBindingUnsupported.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s: %s at %s requires %s through parameter %d (%s); reached from %s",
		ErrResourceBindingUnsupported, e.Constructor(), e.implementation.Source(), e.ResourceID(),
		e.ParameterPosition(), e.ParameterName(), e.path.root.interfaceID)
	for _, step := range e.path.steps {
		fmt.Fprintf(&message, "; %s parameter %d (%s) selects %s for %s",
			step.RequiringConstructor(), step.ParameterPosition(), step.ParameterName(),
			step.SelectedConstructor(), step.InterfaceID())
	}
	return message.String()
}

func (*ResourceBindingUnsupportedError) Unwrap() error { return ErrResourceBindingUnsupported }
