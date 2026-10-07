package constructorgraph

import (
	"fmt"
	"go/token"
	"sort"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourcename"
)

type resourceConsumer struct {
	namespace ResourceConsumerNamespace
	name      string
}

type resourceAddress struct {
	resourceConsumer
	parameter string
}

type resourceConsumerRecord struct {
	constructor  constructorsymbol.Symbol
	declaration  ResourceSource
	sources      []ResourceSource
	dependencies []ResourceDependency
}

type resourceGraphBuilder struct {
	instances map[string]ResourceNode
	generated map[string]GeneratedResourceNode
	consumers map[resourceConsumer]resourceConsumerRecord
	explicit  map[resourceAddress]ResourceDependency
	states    map[string]visitState
	stack     []ResourceDependency
	order     []ResourceNode
}

func newResourceGraphBuilder(input Input) (*resourceGraphBuilder, error) {
	builder := &resourceGraphBuilder{
		instances: make(map[string]ResourceNode),
		generated: make(map[string]GeneratedResourceNode),
		consumers: make(map[resourceConsumer]resourceConsumerRecord),
		explicit:  make(map[resourceAddress]ResourceDependency),
		states:    make(map[string]visitState),
	}
	instances := append([]ResourceInstanceInput(nil), input.ResourceInstances...)
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Name != instances[j].Name {
			return instances[i].Name < instances[j].Name
		}
		return instances[i].Provider.String() < instances[j].Provider.String()
	})
	for _, selected := range instances {
		sources, err := normalizeResourceSources(selected.Sources)
		if err != nil {
			return nil, fmt.Errorf("%w: Resource instance sources: %v", ErrInvalidInput, err)
		}
		failure := &ResourceInstanceError{sources: sources, provider: selected.Provider}
		if resourcename.Check(selected.Name) != nil {
			failure.detail = "invalid instance name"
			return nil, failure
		}
		failure.name = selected.Name
		provider, found := input.ResourceProviders.BySymbol(selected.Provider)
		if !found {
			failure.detail = "provider is not visible"
			return nil, failure
		}
		identifier, err := interfaceid.Parse(provider.ID())
		if err != nil {
			return nil, fmt.Errorf("%w: provider has an invalid Resource ID", ErrInvalidInput)
		}
		position := provider.Declaration().Position()
		declaration := ResourceSource{Reference: provider.Source(), ModulePath: provider.ModulePath(), Path: position.Path, Line: position.Line, Column: position.Column}
		if _, err := normalizeResourceSources([]ResourceSource{declaration}); err != nil {
			return nil, fmt.Errorf("%w: provider declaration: %v", ErrInvalidInput, err)
		}
		if previous, duplicate := builder.instances[selected.Name]; duplicate {
			if previous.provider.Symbol() != selected.Provider {
				failure.detail = fmt.Sprintf("conflicting selected providers %s and %s", previous.provider.Symbol(), selected.Provider)
				failure.sources = append(previous.Sources(), sources...)
				return nil, failure
			}
			sources, err = normalizeResourceSources(append(previous.Sources(), sources...))
			if err != nil {
				return nil, fmt.Errorf("%w: instance source conflict: %v", ErrInvalidInput, err)
			}
		}
		builder.instances[selected.Name] = ResourceNode{name: selected.Name, resourceID: identifier, provider: provider, sources: sources}
		consumer := resourceConsumer{ResourceConsumerInstance, selected.Name}
		dependencies := make([]ResourceDependency, 0, len(provider.Dependencies()))
		for _, dependency := range provider.Dependencies() {
			identifier, err := interfaceid.Parse(dependency.ID())
			if err != nil {
				return nil, fmt.Errorf("%w: provider dependency has an invalid Resource ID", ErrInvalidInput)
			}
			dependencies = append(dependencies, ResourceDependency{
				namespace:                consumer.namespace,
				consumer:                 consumer.name,
				constructor:              provider.Symbol(),
				declaration:              declaration,
				consumerSelectionSources: sources,
				resourceID:               identifier,
				packagePath:              dependency.PackagePath(),
				parameterName:            dependency.ParameterName(),
				parameterPosition:        dependency.ParameterPosition(),
			})
		}
		builder.consumers[consumer] = resourceConsumerRecord{constructor: provider.Symbol(), declaration: declaration, sources: sources, dependencies: dependencies}
	}
	generated := append([]GeneratedResourceInput(nil), input.GeneratedResources...)
	sort.Slice(generated, func(i, j int) bool {
		if generated[i].Name != generated[j].Name {
			return generated[i].Name < generated[j].Name
		}
		return generated[i].Constructor.String() < generated[j].Constructor.String()
	})
	for _, selected := range generated {
		if resourcename.Check(selected.Name) != nil || selected.MemberID == "" || selected.ResourceID.String() == "" || selected.PackagePath == "" || selected.TypeName == "" || selected.Constructor.String() == "" || selected.DatabaseResourceID.String() == "" || selected.DatabasePackagePath == "" || selected.DatabaseParameter == "" || selected.DatabaseParameterPosition < 1 {
			return nil, fmt.Errorf("%w: invalid generated Resource provider input", ErrInvalidInput)
		}
		sources, err := normalizeResourceSources(selected.Sources)
		if err != nil {
			return nil, fmt.Errorf("%w: generated Resource provider sources: %v", ErrInvalidInput, err)
		}
		if _, exists := builder.instances[selected.Name]; exists {
			return nil, fmt.Errorf("%w: generated Resource provider %q collides with selected instance", ErrInvalidInput, selected.Name)
		}
		if _, exists := builder.generated[selected.Name]; exists {
			return nil, fmt.Errorf("%w: generated Resource provider %q is duplicated", ErrInvalidInput, selected.Name)
		}
		declaration := sources[0]
		dependency := ResourceDependency{
			namespace: ResourceConsumerInstance, consumer: selected.Name, constructor: selected.Constructor,
			declaration: declaration, consumerSelectionSources: sources,
			resourceID: selected.DatabaseResourceID, packagePath: selected.DatabasePackagePath,
			parameterName: selected.DatabaseParameter, parameterPosition: selected.DatabaseParameterPosition,
		}
		builder.generated[selected.Name] = GeneratedResourceNode{
			memberID: selected.MemberID, name: selected.Name, resourceID: selected.ResourceID,
			packagePath: selected.PackagePath, typeName: selected.TypeName,
			constructor: selected.Constructor, sources: sources,
		}
		builder.consumers[resourceConsumer{ResourceConsumerInstance, selected.Name}] = resourceConsumerRecord{
			constructor: selected.Constructor, declaration: declaration, sources: sources, dependencies: []ResourceDependency{dependency},
		}
	}
	for _, implementation := range input.Implementations.Implementations() {
		consumer := resourceConsumer{ResourceConsumerImplementation, implementation.Symbol().String()}
		position := implementation.Declaration().Position()
		declaration := ResourceSource{Reference: implementation.Source(), ModulePath: implementation.ModulePath(), Path: implementation.SourcePath(), Line: position.Line, Column: position.Column}
		dependencies := make([]ResourceDependency, 0, len(implementation.RequiredResources()))
		for _, dependency := range implementation.RequiredResources() {
			dependencies = append(dependencies, ResourceDependency{
				namespace:         consumer.namespace,
				consumer:          consumer.name,
				constructor:       implementation.Symbol(),
				declaration:       declaration,
				resourceID:        dependency.ID(),
				packagePath:       dependency.PackagePath(),
				parameterName:     dependency.ParameterName(),
				parameterPosition: dependency.ParameterPosition(),
			})
		}
		builder.consumers[consumer] = resourceConsumerRecord{constructor: implementation.Symbol(), declaration: declaration, dependencies: dependencies}
	}
	bindings := append([]ResourceBindingInput(nil), input.ResourceBindings...)
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Consumer != b.Consumer {
			return a.Consumer < b.Consumer
		}
		if a.Parameter != b.Parameter {
			return a.Parameter < b.Parameter
		}
		return a.Target < b.Target
	})
	for _, binding := range bindings {
		if err := builder.addBinding(binding); err != nil {
			return nil, err
		}
	}
	return builder, nil
}

func (b *resourceGraphBuilder) addBinding(input ResourceBindingInput) error {
	sources, err := normalizeResourceSources(input.Sources)
	if err != nil {
		return fmt.Errorf("%w: Resource binding sources: %v", ErrInvalidInput, err)
	}
	failure := &ResourceBindingError{condition: ErrInvalidResourceBinding, dependency: ResourceDependency{sources: sources}}
	consumer := resourceConsumer{input.Namespace, input.Consumer}
	switch input.Namespace {
	case ResourceConsumerImplementation:
		symbol, err := constructorsymbol.Parse(input.Consumer)
		if err != nil {
			failure.detail = "invalid Implementation consumer identity"
			return failure
		}
		failure.dependency.constructor = symbol
	case ResourceConsumerInstance:
		if resourcename.Check(input.Consumer) != nil {
			failure.detail = "invalid consumer instance name"
			return failure
		}
	default:
		failure.detail = "invalid consumer namespace"
		return failure
	}
	failure.dependency.namespace, failure.dependency.consumer = input.Namespace, input.Consumer
	record, visible := b.consumers[consumer]
	if visible {
		failure.dependency.constructor = record.constructor
		failure.dependency.declaration = record.declaration
		failure.dependency.consumerSelectionSources = record.sources
	}
	if input.Parameter == "_" || !token.IsIdentifier(input.Parameter) {
		failure.detail = "invalid parameter identifier"
		return failure
	}
	failure.dependency.parameterName = input.Parameter
	if !visible {
		failure.detail = "consumer is not visible or selected"
		return failure
	}
	found := false
	var dependency ResourceDependency
	for _, declared := range record.dependencies {
		if declared.parameterName == input.Parameter {
			dependency, found = declared, true
			break
		}
	}
	if !found {
		failure.detail = "consumer has no Resource parameter with this exact identifier"
		return failure
	}
	dependency.sources, dependency.reason = sources, SelectionExplicit
	failure.dependency = dependency
	if resourcename.Check(input.Target) != nil {
		failure.detail = "invalid target instance name"
		return failure
	}
	dependency.instanceName = input.Target
	failure.dependency = dependency
	target, exists := b.instances[input.Target]
	if exists {
		dependency.provider, dependency.selectionSources = target.provider.Symbol(), target.Sources()
	} else if generated, generatedExists := b.generated[input.Target]; generatedExists && generated.resourceID == dependency.resourceID {
		if input.Namespace == ResourceConsumerImplementation {
			failure.detail = "generated Resource binding requires Data emission and installation"
			failure.generatedCandidates = []GeneratedResourceNode{generated}
			return failure
		}
		dependency.provider, dependency.selectionSources = generated.constructor, generated.Sources()
	} else {
		failure.detail = "target instance is not selected"
		return failure
	}
	failure.dependency = dependency
	if exists && target.resourceID != dependency.resourceID {
		failure.detail = "target does not provide the exact Resource contract"
		failure.candidates = []ResourceNode{target}
		return failure
	}
	address := resourceAddress{consumer, input.Parameter}
	if previous, duplicate := b.explicit[address]; duplicate {
		if previous.instanceName != input.Target {
			failure.detail = fmt.Sprintf("conflicting targets %s and %s for the same binding address", previous.instanceName, input.Target)
			failure.dependency.sources = append(previous.Sources(), sources...)
			return failure
		}
		dependency.sources, err = normalizeResourceSources(append(previous.Sources(), sources...))
		if err != nil {
			return fmt.Errorf("%w: binding source conflict: %v", ErrInvalidInput, err)
		}
	}
	b.explicit[address] = dependency
	return nil
}

func (b *resourceGraphBuilder) resolve(graph *Graph) error {
	names := make([]string, 0, len(b.instances))
	for name := range b.instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := b.visit(name); err != nil {
			return err
		}
	}
	graph.resourceConstruction = cloneResourceNodes(b.order)
	graph.resourceDependencies = make(map[constructorsymbol.Symbol][]ResourceDependency)
	for _, node := range graph.construction {
		consumer := resourceConsumer{ResourceConsumerImplementation, node.symbol.String()}
		for _, declared := range b.consumers[consumer].dependencies {
			dependency, err := b.bind(declared)
			if err != nil {
				if failure, ok := err.(*ResourceBindingError); ok {
					failure.path = node.path.clone()
				}
				return err
			}
			graph.resourceDependencies[node.symbol] = append(graph.resourceDependencies[node.symbol], dependency)
		}
	}
	generatedNames := make([]string, 0, len(b.generated))
	for name := range b.generated {
		generatedNames = append(generatedNames, name)
	}
	sort.Strings(generatedNames)
	for _, name := range generatedNames {
		if err := b.visitGenerated(name); err != nil {
			return err
		}
		node := b.generated[name]
		record := b.consumers[resourceConsumer{ResourceConsumerInstance, name}]
		for _, declared := range record.dependencies {
			dependency, err := b.bind(declared)
			if err != nil {
				return err
			}
			node.dependencies = append(node.dependencies, dependency)
			graph.resourceDependencies[node.constructor] = append(graph.resourceDependencies[node.constructor], dependency)
		}
		graph.generatedResources = append(graph.generatedResources, node)
	}
	return nil
}

func (b *resourceGraphBuilder) bind(declared ResourceDependency) (ResourceDependency, error) {
	address := resourceAddress{resourceConsumer{declared.namespace, declared.consumer}, declared.parameterName}
	if explicit, found := b.explicit[address]; found {
		return explicit, nil
	}
	var candidates []ResourceNode
	for _, instance := range b.instances {
		if instance.resourceID == declared.resourceID {
			candidates = append(candidates, instance)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })
	generated := make([]GeneratedResourceNode, 0)
	for _, node := range b.generated {
		if node.resourceID == declared.resourceID {
			generated = append(generated, node)
		}
	}
	sort.Slice(generated, func(i, j int) bool { return generated[i].name < generated[j].name })
	if len(candidates)+len(generated) != 1 {
		condition := ErrMissingResourceBinding
		if len(candidates)+len(generated) > 1 {
			condition = ErrAmbiguousResourceBinding
		}
		return ResourceDependency{}, &ResourceBindingError{condition: condition, dependency: declared, candidates: cloneResourceNodes(candidates), generatedCandidates: cloneGeneratedResourceNodes(generated), detail: "implicit binding requires exactly one compatible selected instance"}
	}
	if declared.namespace == ResourceConsumerImplementation && len(generated) != 0 {
		return ResourceDependency{}, &ResourceBindingError{condition: ErrInvalidResourceBinding, dependency: declared, generatedCandidates: cloneGeneratedResourceNodes(generated), detail: "generated Resource binding requires Data emission and installation"}
	}
	if len(candidates) == 1 {
		target := candidates[0]
		declared.instanceName, declared.provider, declared.reason = target.name, target.provider.Symbol(), SelectionUnique
		declared.selectionSources = target.Sources()
		return declared, nil
	}
	target := generated[0]
	declared.instanceName, declared.provider, declared.reason = target.name, target.constructor, SelectionUnique
	declared.selectionSources = target.Sources()
	return declared, nil
}

func (b *resourceGraphBuilder) visit(name string) error {
	if b.states[name] == visitDone {
		return nil
	}
	if b.states[name] == visitActive {
		for index, step := range b.stack {
			if step.consumer == name {
				return &ResourceCycleError{steps: cloneResourceDependencies(b.stack[index:])}
			}
		}
		return fmt.Errorf("%w: Resource cycle has no matching active instance", ErrInvalidInput)
	}
	b.states[name] = visitActive
	node := b.instances[name]
	for _, declared := range b.consumers[resourceConsumer{ResourceConsumerInstance, name}].dependencies {
		dependency, err := b.bind(declared)
		if err != nil {
			return err
		}
		b.stack = append(b.stack, dependency)
		if _, generated := b.generated[dependency.instanceName]; !generated {
			if err := b.visit(dependency.instanceName); err != nil {
				return err
			}
		} else if err := b.visitGenerated(dependency.instanceName); err != nil {
			return err
		}
		b.stack = b.stack[:len(b.stack)-1]
		node.dependencies = append(node.dependencies, dependency)
	}
	b.states[name] = visitDone
	b.order = append(b.order, node)
	return nil
}

func (b *resourceGraphBuilder) visitGenerated(name string) error {
	if b.states[name] == visitDone {
		return nil
	}
	if b.states[name] == visitActive {
		for index, step := range b.stack {
			if step.consumer == name {
				return &ResourceCycleError{steps: cloneResourceDependencies(b.stack[index:])}
			}
		}
		return fmt.Errorf("%w: generated Resource cycle has no matching active instance", ErrInvalidInput)
	}
	b.states[name] = visitActive
	record := b.consumers[resourceConsumer{ResourceConsumerInstance, name}]
	for _, declared := range record.dependencies {
		dependency, err := b.bind(declared)
		if err != nil {
			return err
		}
		b.stack = append(b.stack, dependency)
		if _, generated := b.generated[dependency.instanceName]; generated {
			if err := b.visitGenerated(dependency.instanceName); err != nil {
				return err
			}
		} else if err := b.visit(dependency.instanceName); err != nil {
			return err
		}
		b.stack = b.stack[:len(b.stack)-1]
	}
	b.states[name] = visitDone
	return nil
}

func normalizeResourceSources(values []ResourceSource) ([]ResourceSource, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("no source")
	}
	result := append([]ResourceSource(nil), values...)
	for _, source := range result {
		if _, err := normalizeSource(source.Reference); err != nil {
			return nil, err
		}
		if err := checkSourceLocation(source.ModulePath, source.Path, source.Line, source.Column); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Reference < result[j].Reference })
	write := 0
	for _, source := range result {
		if write > 0 && result[write-1].Reference == source.Reference {
			if result[write-1] != source {
				return nil, fmt.Errorf("conflicting typed source provenance")
			}
			continue
		}
		result[write], write = source, write+1
	}
	return result[:write], nil
}
