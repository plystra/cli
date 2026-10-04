package implementationassemblygen

import (
	"crypto/sha256"
	"fmt"
	"go/token"
	"slices"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourcename"
	kernelinvocation "github.com/plystra/kernel/invocation"
	"golang.org/x/mod/module"
)

// ResourceDependencyInput is one resolved ordinary Go value parameter.
// InstanceName is the selected target, never a constructor-keyed singleton.
type ResourceDependencyInput struct {
	ResourceID        interfaceid.Identifier
	PackagePath       string
	InstanceName      string
	ParameterName     string
	ParameterPosition int
}

// ResourceInput is one selected named instance and its exact provider plan.
type ResourceInput struct {
	Name             string
	ResourceID       interfaceid.Identifier
	PackagePath      string
	ContractDigest   [sha256.Size]byte
	Provider         constructorsymbol.Symbol
	ModulePath       string
	ModuleVersion    string
	HasConfiguration bool
	Dependencies     []ResourceDependencyInput
}

func cloneResources(values []ResourceInput) []ResourceInput {
	result := append([]ResourceInput(nil), values...)
	for index := range result {
		result[index].Dependencies = append([]ResourceDependencyInput(nil), result[index].Dependencies...)
	}
	return result
}

func planResourceInstances(options Options) ([]ResourceInput, map[string]ResourceInput, error) {
	resources := cloneResources(options.Resources)
	slices.SortFunc(resources, func(a, b ResourceInput) int { return strings.Compare(a.Name, b.Name) })
	byName := make(map[string]ResourceInput, len(resources))
	contracts := make(map[string]ResourceInput)
	providers := make(map[string]ResourceInput)
	for _, resource := range resources {
		if resourcename.Check(resource.Name) != nil || resource.ResourceID.String() == "" || module.CheckImportPath(resource.PackagePath) != nil || resource.Provider.String() == "" || resource.ContractDigest == [sha256.Size]byte{} {
			return nil, nil, fmt.Errorf("%w: incomplete Resource instance", ErrInvalidInput)
		}
		if _, duplicate := byName[resource.Name]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate Resource instance %s", ErrConstructorGraph, resource.Name)
		}
		if resource.Provider.PackagePath() != resource.ModulePath && !strings.HasPrefix(resource.Provider.PackagePath(), resource.ModulePath+"/") {
			return nil, nil, fmt.Errorf("%w: Resource instance %s provider is outside its module", ErrInvalidInput, resource.Name)
		}
		if _, err := kernelinvocation.NewModuleBuild(resource.ModulePath, resource.ModuleVersion, options.ApplicationBuildIdentity); err != nil {
			return nil, nil, fmt.Errorf("%w: Resource instance %s module provenance: %v", ErrInvalidInput, resource.Name, err)
		}
		if previous, exists := contracts[resource.ResourceID.String()]; exists && (previous.PackagePath != resource.PackagePath || previous.ContractDigest != resource.ContractDigest) {
			return nil, nil, fmt.Errorf("%w: inconsistent Resource contract %s", ErrInvalidInput, resource.ResourceID)
		}
		if previous, exists := providers[resource.Provider.String()]; exists && !sameResourceProvider(previous, resource) {
			return nil, nil, fmt.Errorf("%w: inconsistent Resource provider %s", ErrInvalidInput, resource.Provider)
		}
		if err := validateParameterOrder(resource.HasConfiguration, nil, resource.Dependencies); err != nil {
			return nil, nil, fmt.Errorf("%w: Resource instance %s: %v", ErrConstructorGraph, resource.Name, err)
		}
		byName[resource.Name] = resource
		contracts[resource.ResourceID.String()] = resource
		providers[resource.Provider.String()] = resource
	}
	for _, resource := range resources {
		for _, dependency := range resource.Dependencies {
			if err := validateResourceDependency(dependency, byName); err != nil {
				return nil, nil, fmt.Errorf("%w: Resource instance %s: %v", ErrConstructorGraph, resource.Name, err)
			}
		}
	}
	var ordered []ResourceInput
	state := make(map[string]int)
	var stack []string
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("%w: Resource cycle %s", ErrConstructorGraph, strings.Join(append(append([]string(nil), stack...), name), " -> "))
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		stack = append(stack, name)
		resource := byName[name]
		for _, dependency := range resource.Dependencies {
			if err := visit(dependency.InstanceName); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		ordered = append(ordered, resource)
		return nil
	}
	for _, resource := range resources {
		if err := visit(resource.Name); err != nil {
			return nil, nil, err
		}
	}
	return ordered, byName, nil
}

func sameResourceProvider(a, b ResourceInput) bool {
	if a.ResourceID != b.ResourceID || a.PackagePath != b.PackagePath || a.ModulePath != b.ModulePath || a.ModuleVersion != b.ModuleVersion || a.HasConfiguration != b.HasConfiguration || len(a.Dependencies) != len(b.Dependencies) {
		return false
	}
	for index, dependency := range a.Dependencies {
		other := b.Dependencies[index]
		if dependency.ResourceID != other.ResourceID || dependency.PackagePath != other.PackagePath || dependency.ParameterName != other.ParameterName || dependency.ParameterPosition != other.ParameterPosition {
			return false
		}
	}
	return true
}

func validateResourceDependency(dependency ResourceDependencyInput, resources map[string]ResourceInput) error {
	if resourcename.Check(dependency.InstanceName) != nil || dependency.ResourceID.String() == "" || module.CheckImportPath(dependency.PackagePath) != nil {
		return fmt.Errorf("invalid Resource dependency")
	}
	target, exists := resources[dependency.InstanceName]
	if !exists || target.ResourceID != dependency.ResourceID || target.PackagePath != dependency.PackagePath {
		return fmt.Errorf("parameter %d (%s) requires an exact Resource %s at instance %s", dependency.ParameterPosition, dependency.ParameterName, dependency.ResourceID, dependency.InstanceName)
	}
	return nil
}

func validateParameterOrder(hasConfiguration bool, interfaces []DependencyInput, resources []ResourceDependencyInput) error {
	count := len(interfaces) + len(resources)
	start := 1
	if hasConfiguration {
		start = 2
	}
	positions := make(map[int]bool, count)
	check := func(name string, position, previous int) error {
		if name == "_" || !token.IsIdentifier(name) || position < start || position >= start+count || position <= previous || positions[position] {
			return fmt.Errorf("dependencies must have nonblank identifiers and contiguous authored positions")
		}
		positions[position] = true
		return nil
	}
	previous := 0
	for _, dependency := range interfaces {
		if err := check(dependency.ParameterName, dependency.ParameterPosition, previous); err != nil {
			return err
		}
		previous = dependency.ParameterPosition
	}
	previous = 0
	for _, dependency := range resources {
		if err := check(dependency.ParameterName, dependency.ParameterPosition, previous); err != nil {
			return err
		}
		previous = dependency.ParameterPosition
	}
	return nil
}

func renderResourceConstruction(source *strings.Builder, planned plan) map[string]int {
	indices := make(map[string]int, len(planned.resources))
	for index, resource := range planned.resources {
		provider := planned.imports[resource.Provider.PackagePath()]
		identity := "Resource instance " + resource.Name + " provider " + resource.Provider.String()
		fmt.Fprintf(source, "\tcurrentConstructor = %s\n", strconv.Quote(identity))
		arguments := make([]string, 0, len(resource.Dependencies)+1)
		if resource.HasConfiguration {
			arguments = append(arguments, fmt.Sprintf("configuration.ResourceConfig%d", index))
		}
		for _, dependency := range resource.Dependencies {
			arguments = append(arguments, fmt.Sprintf("resource%d", indices[dependency.InstanceName]))
		}
		fmt.Fprintf(source, "\tresourceValue%d, resourceError := %s.%s(%s)\n", index, provider, resource.Provider.FunctionName(), strings.Join(arguments, ", "))
		fmt.Fprintln(source, "\tcurrentConstructor = \"\"")
		fmt.Fprintf(source, "\tif instance, ok := any(resourceValue%d).(kernellifecycle.Instance); resourceValue%d != nil && ok {\n", index, index)
		fmt.Fprintf(source, "\t\tbinding, err := kernellifecycle.NewResourceBinding(%s, %s, instance)\n", strconv.Quote(resource.Name), strconv.Quote(resource.Provider.String()))
		fmt.Fprintf(source, "\t\tif err != nil { return InterfaceRuntime{}, fmt.Errorf(\"%%w: %s lifecycle: %%w\", ErrInterfaceAssembly, err) }\n", identity)
		fmt.Fprintln(source, "\t\tlifecycleBindings = append(lifecycleBindings, binding)")
		fmt.Fprintln(source, "\t}")
		fmt.Fprintf(source, "\tif resourceError != nil { return InterfaceRuntime{}, fmt.Errorf(\"%%w: %s failed\", ErrInterfaceAssembly) }\n", identity)
		fmt.Fprintf(source, "\tif resourceValue%d == nil { return InterfaceRuntime{}, fmt.Errorf(\"%%w: %s returned nil\", ErrInterfaceAssembly) }\n", index, identity)
		fmt.Fprintf(source, "\tvar resource%d %s.Resource = resourceValue%d\n", index, planned.imports[resource.PackagePath], index)
		fmt.Fprintf(source, "\t_ = resource%d\n", index)
		indices[resource.Name] = index
	}
	return indices
}
