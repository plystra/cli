package bootstrapgen

import (
	"bytes"
	"encoding/json"
	"go/token"
	"sort"

	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/resourcename"
)

// ResourceInstanceInput identifies one selected instance independently of order.
// Options.ResourceOrder must come from the rendered assembly's Resources plan.
type ResourceInstanceInput struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// ResourceBindingInput records a resolved edge, including implicit selections.
// Dormant Implementation edges are validated from inventory but are not frozen.
type ResourceBindingInput struct {
	Namespace     string `json:"namespace"`
	Consumer      string `json:"consumer"`
	ParameterName string `json:"parameter"`
	Target        string `json:"target"`
}

// ResourceConfigurationInput is private planning input, never generated content.
type ResourceConfigurationInput struct {
	Name, Provider string
	Schema         implementationinventory.Configuration
	YAML           []byte
}

func (ResourceConfigurationInput) String() string   { return "<private-resource-configuration>" }
func (ResourceConfigurationInput) GoString() string { return "<private-resource-configuration>" }

// ResourceConfigurationDigest returns the schema-directed public identity.
// Runtime-only values, Secret references, and private defaults are excluded.
func ResourceConfigurationDigest(input ResourceConfigurationInput) (string, error) {
	if resourcename.Check(input.Name) != nil {
		return "", ErrInvalidOptions
	}
	if _, err := constructorsymbol.Parse(input.Provider); err != nil {
		return "", ErrInvalidOptions
	}
	_, digest, err := compileConstructorConfiguration(ConstructorConfigurationInput{Symbol: input.Provider, Schema: input.Schema, YAML: input.YAML})
	if err != nil {
		return "", err
	}
	return "sha256:" + digest, nil
}

type baselineResourceDependency struct {
	Parameter string `json:"parameter"`
	Resource  string `json:"resource"`
}

type baselineResourceProvider struct {
	Symbol       string                       `json:"symbol"`
	Resource     string                       `json:"resource"`
	Schema       *constructorconfig.Schema    `json:"schema"`
	Dependencies []baselineResourceDependency `json:"dependencies"`
}

type baselineResourceConfiguration struct {
	Name         string                   `json:"name"`
	Provider     string                   `json:"provider"`
	Schema       constructorconfig.Schema `json:"schema"`
	BuildVisible string                   `json:"build_visible"`
}

func planResourceIdentities(instances []ResourceInstanceInput, bindings []ResourceBindingInput) ([]ResourceInstanceInput, []ResourceBindingInput, error) {
	instances = append([]ResourceInstanceInput{}, instances...)
	bindings = append([]ResourceBindingInput{}, bindings...)
	byName := make(map[string]string, len(instances))
	for _, instance := range instances {
		if resourcename.Check(instance.Name) != nil || byName[instance.Name] != "" {
			return nil, nil, ErrInvalidOptions
		}
		if _, err := constructorsymbol.Parse(instance.Provider); err != nil {
			return nil, nil, ErrInvalidOptions
		}
		byName[instance.Name] = instance.Provider
	}
	seen := make(map[[3]string]bool, len(bindings))
	for _, binding := range bindings {
		key := [3]string{binding.Namespace, binding.Consumer, binding.ParameterName}
		if seen[key] || !token.IsIdentifier(binding.ParameterName) || binding.ParameterName == "_" || byName[binding.Target] == "" {
			return nil, nil, ErrInvalidOptions
		}
		seen[key] = true
		switch binding.Namespace {
		case "implementations":
			if _, err := constructorsymbol.Parse(binding.Consumer); err != nil {
				return nil, nil, ErrInvalidOptions
			}
		case "instances":
			if byName[binding.Consumer] == "" {
				return nil, nil, ErrInvalidOptions
			}
		default:
			return nil, nil, ErrInvalidOptions
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Name < instances[j].Name })
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Consumer != b.Consumer {
			return a.Consumer < b.Consumer
		}
		return a.ParameterName < b.ParameterName
	})
	return instances, bindings, nil
}

func planResourceBaseline(options Options, defaults map[string]json.RawMessage) ([]baselineResourceProvider, []baselineResourceConfiguration, error) {
	if _, err := planResourceOrder(options.ResourceInstances, options.ResourceOrder); err != nil {
		return nil, nil, err
	}
	providers := make([]baselineResourceProvider, 0, len(options.ResourceInventory))
	bySymbol := make(map[string]baselineResourceProvider)
	for _, provider := range options.ResourceInventory {
		symbol := provider.Symbol().String()
		if _, err := constructorsymbol.Parse(symbol); err != nil {
			return nil, nil, ErrInvalidOptions
		}
		if _, duplicate := bySymbol[symbol]; duplicate {
			return nil, nil, ErrInvalidOptions
		}
		entry := baselineResourceProvider{Symbol: symbol, Resource: provider.ID(), Dependencies: []baselineResourceDependency{}}
		for _, dependency := range provider.Dependencies() {
			entry.Dependencies = append(entry.Dependencies, baselineResourceDependency{dependency.ParameterName(), dependency.ID()})
		}
		if configuration, exists := provider.Configuration(); exists {
			schema := constructorconfig.Schema{Kind: "object", Fields: compileConstructorFields(configuration.Fields())}
			entry.Schema = &schema
			data, err := constructorconfig.DefaultsJSON(schema)
			if err != nil {
				return nil, nil, ErrInvalidOptions
			}
			if _, collision := defaults[symbol]; collision {
				return nil, nil, ErrInvalidOptions
			}
			defaults[symbol] = data
		}
		bySymbol[symbol] = entry
		providers = append(providers, entry)
	}
	instances, _, err := planResourceIdentities(options.ResourceInstances, options.ResourceBindings)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[string]ResourceConfigurationInput)
	for _, input := range options.ResourceConfigurations {
		if _, duplicate := byName[input.Name]; duplicate {
			return nil, nil, ErrInvalidOptions
		}
		byName[input.Name] = input
	}
	configurations := make([]baselineResourceConfiguration, 0, len(byName))
	for _, instance := range instances {
		provider, exists := bySymbol[instance.Provider]
		if !exists {
			return nil, nil, ErrInvalidOptions
		}
		input, configured := byName[instance.Name]
		if configured != (provider.Schema != nil) {
			return nil, nil, ErrInvalidOptions
		}
		if !configured {
			continue
		}
		if input.Provider != instance.Provider {
			return nil, nil, ErrInvalidOptions
		}
		schema, digest, err := compileConstructorConfiguration(ConstructorConfigurationInput{Symbol: input.Provider, Schema: input.Schema, YAML: input.YAML})
		if err != nil {
			return nil, nil, err
		}
		left, _ := json.Marshal(schema)
		right, _ := json.Marshal(provider.Schema)
		compiledDefaults, defaultsErr := constructorconfig.DefaultsJSON(schema)
		if !bytes.Equal(left, right) || defaultsErr != nil || !bytes.Equal(compiledDefaults, defaults[instance.Provider]) {
			return nil, nil, ErrInvalidOptions
		}
		configurations = append(configurations, baselineResourceConfiguration{instance.Name, instance.Provider, schema, digest})
		delete(byName, instance.Name)
	}
	if len(byName) != 0 {
		return nil, nil, ErrInvalidOptions
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Symbol < providers[j].Symbol })
	return providers, configurations, nil
}

func planResourceOrder(instances []ResourceInstanceInput, order []string) (map[string]int, error) {
	if len(instances) != len(order) {
		return nil, ErrInvalidOptions
	}
	indices := make(map[string]int, len(order))
	for i, name := range order {
		if resourcename.Check(name) != nil {
			return nil, ErrInvalidOptions
		}
		if _, duplicate := indices[name]; duplicate {
			return nil, ErrInvalidOptions
		}
		indices[name] = i
	}
	seen := make(map[string]bool, len(instances))
	for _, instance := range instances {
		if _, exists := indices[instance.Name]; !exists || seen[instance.Name] {
			return nil, ErrInvalidOptions
		}
		seen[instance.Name] = true
	}
	return indices, nil
}
