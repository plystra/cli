package bootstrapgen

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/runtimebaseline"
)

type baselineTemplate struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Template string `json:"template"`
}

type baselineConstructor struct {
	Symbol       string                       `json:"symbol"`
	Interfaces   []string                     `json:"interfaces"`
	Schema       *constructorconfig.Schema    `json:"schema"`
	Dependencies []baselineResourceDependency `json:"resource_dependencies"`
}

// RuntimeBaseline produces private build output from the same inputs as bootstrap.
func RuntimeBaseline(options Options) (runtimebaseline.Document, error) {
	if modulepath.CheckProject(options.ModulePath) != nil {
		return runtimebaseline.Document{}, ErrInvalidOptions
	}
	type constructor struct {
		Symbol       string                   `json:"symbol"`
		Schema       constructorconfig.Schema `json:"schema"`
		BuildVisible string                   `json:"build_visible"`
	}
	constructors := make([]constructor, 0, len(options.ConstructorConfigurations))
	defaults := make(map[string]json.RawMessage)
	for _, input := range options.ConstructorConfigurations {
		schema, digest, err := compileConstructorConfiguration(input)
		if err != nil {
			return runtimebaseline.Document{}, err
		}
		if _, duplicate := defaults[input.Symbol]; duplicate {
			return runtimebaseline.Document{}, ErrInvalidOptions
		}
		data, err := constructorconfig.DefaultsJSON(schema)
		if err != nil {
			return runtimebaseline.Document{}, ErrInvalidOptions
		}
		defaults[input.Symbol] = data
		constructors = append(constructors, constructor{input.Symbol, schema, digest})
	}
	sort.Slice(constructors, func(i, j int) bool { return constructors[i].Symbol < constructors[j].Symbol })
	inventory := make([]baselineConstructor, 0, len(options.ConstructorInventory))
	seen := make(map[string]bool)
	for _, implementation := range options.ConstructorInventory {
		symbol := implementation.Symbol().String()
		if _, err := constructorsymbol.Parse(symbol); err != nil || seen[symbol] {
			return runtimebaseline.Document{}, ErrInvalidOptions
		}
		seen[symbol] = true
		entry := baselineConstructor{Symbol: symbol, Interfaces: []string{}}
		for _, dependency := range implementation.RequiredResources() {
			entry.Dependencies = append(entry.Dependencies, baselineResourceDependency{dependency.ParameterName(), dependency.ID().String()})
		}
		for _, declaration := range implementation.Declaration().ImplementedInterfaces() {
			entry.Interfaces = append(entry.Interfaces, declaration.ID().String())
		}
		sort.Strings(entry.Interfaces)
		if configuration, exists := implementation.Configuration(); exists {
			schema := constructorconfig.Schema{Kind: "object", Fields: compileConstructorFields(configuration.Fields())}
			entry.Schema = &schema
			data, err := constructorconfig.DefaultsJSON(schema)
			if err != nil {
				return runtimebaseline.Document{}, ErrInvalidOptions
			}
			defaults[symbol] = data
		}
		inventory = append(inventory, entry)
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Symbol < inventory[j].Symbol })
	resourceInventory, resourceConfigurations, err := planResourceBaseline(options, defaults)
	if err != nil {
		return runtimebaseline.Document{}, err
	}
	instances, bindings, err := planResourceIdentities(options.ResourceInstances, options.ResourceBindings)
	if err != nil {
		return runtimebaseline.Document{}, err
	}
	projection := options.ApplicationModelCompatibility.document.Projection
	if options.ApplicationModelCompatibility.Valid() && (!slices.Equal(projection.ResourceInstances, instances) || !slices.Equal(projection.ResourceBindings, bindings)) {
		return runtimebaseline.Document{}, ErrInvalidOptions
	}
	templates := append([]runtimebaseline.Template{}, options.Templates...)
	ancestry := make([]baselineTemplate, len(templates))
	previous := ""
	modules := map[string]bool{options.ModulePath: true}
	for i, template := range templates {
		if modulepath.CheckProject(template.Module) != nil || modules[template.Module] || template.Template != previous {
			return runtimebaseline.Document{}, ErrInvalidOptions
		}
		modules[template.Module] = true
		previous = template.Module
		ancestry[i] = baselineTemplate{template.Module, template.Version, template.Template}
	}
	if options.Template != previous {
		return runtimebaseline.Document{}, ErrInvalidOptions
	}
	contract, err := json.Marshal(struct {
		Schema               string                          `json:"baseline_schema"`
		Module               string                          `json:"module"`
		ApplicationModel     string                          `json:"application_model"`
		Compatibility        json.RawMessage                 `json:"compatibility"`
		Constructors         []constructor                   `json:"constructors"`
		ConstructorInventory []baselineConstructor           `json:"constructor_inventory"`
		ResourceInventory    []baselineResourceProvider      `json:"resource_inventory"`
		Resources            []baselineResourceConfiguration `json:"resource_configurations"`
		Template             string                          `json:"template"`
		Templates            []baselineTemplate              `json:"template_ancestry"`
		RuntimeProcessFields []string                        `json:"runtime_process_fields"`
	}{runtimebaseline.Schema, options.ModulePath, options.ApplicationModelCompatibility.ApplicationModelDigest(), options.ApplicationModelCompatibility.CanonicalJSON(), constructors, inventory, resourceInventory, resourceConfigurations, options.Template, ancestry, []string{"http.address", "timeouts.startup"}})
	if err != nil {
		return runtimebaseline.Document{}, ErrInvalidOptions
	}
	return runtimebaseline.Document{Schema: runtimebaseline.Schema, ContractID: runtimebaseline.ContractID(contract), Contract: contract, Defaults: defaults, Templates: templates}, nil
}

const runtimeBaselineSupport = `
func validateRuntimeBaseline(document runtimebaseline.Document) error {
	if document.ContractID != compiledRuntimeContract {
		return fmt.Errorf("%w: runtime baseline and binary do not match; regenerate and rebuild with the same selector", ErrRuntimeCompatibility)
	}
	var configuration applicationassembly.ConstructorConfiguration
	bindings, err := runtimeConstructorBindings(&configuration)
	if err != nil { return err }
	inventory, err := runtimeConstructorInventory(document)
	if err != nil { return err }
	providers, err := runtimeResourceInventory(document)
	if err != nil { return err }
	expected := make(map[string]bool)
	for symbol, entry := range inventory { if entry.Schema != nil { expected[symbol] = true } }
	for symbol, entry := range providers { if entry.Schema != nil { expected[symbol] = true } }
	for _, binding := range bindings {
		expected[binding.symbol] = true
		compiled, err := constructorconfig.DefaultsJSON(binding.schema)
		if err != nil || !bytes.Equal(compiled, document.Defaults[binding.symbol]) {
			return fmt.Errorf("%w: compiled constructor defaults and private baseline differ; regenerate and rebuild", ErrRuntimeCompatibility)
		}
	}
	if len(expected) != len(document.Defaults) { return fmt.Errorf("%w: constructor baseline membership changed; regenerate and rebuild", ErrRuntimeCompatibility) }
	return nil
}

type runtimeConstructorInventoryEntry struct {
	Symbol string
	Interfaces []string
	Schema *constructorconfig.Schema
	Dependencies []runtimeResourceDependency ` + "`json:\"resource_dependencies\"`" + `
}

func runtimeConstructorInventory(document runtimebaseline.Document) (map[string]runtimeConstructorInventoryEntry, error) {
	var contract struct { Inventory []runtimeConstructorInventoryEntry ` + "`json:\"constructor_inventory\"`" + ` }
	if json.Unmarshal(document.Contract, &contract) != nil { return nil, runtimebaseline.ErrBaseline }
	inventory := make(map[string]runtimeConstructorInventoryEntry, len(contract.Inventory))
	for _, entry := range contract.Inventory {
		if entry.Schema != nil {
			if err := constructorconfig.RestoreDefaults(entry.Schema, document.Defaults[entry.Symbol]); err != nil { return nil, runtimebaseline.ErrBaseline }
		}
		inventory[entry.Symbol] = entry
	}
	return inventory, nil
}
`
