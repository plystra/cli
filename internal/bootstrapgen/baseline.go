package bootstrapgen

import (
	"encoding/json"
	"sort"

	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/runtimebaseline"
)

type baselineModule struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

type baselineConstructor struct {
	Symbol     string                    `json:"symbol"`
	Interfaces []string                  `json:"interfaces"`
	Schema     *constructorconfig.Schema `json:"schema"`
}

// RuntimeBaseline produces private build output from the same inputs as bootstrap.
func RuntimeBaseline(options Options) (runtimebaseline.Document, error) {
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
	exports := append([]runtimebaseline.Export{}, options.DependencyExports...)
	sort.Slice(exports, func(i, j int) bool { return exports[i].Module < exports[j].Module })
	modules := make([]baselineModule, len(exports))
	for i, export := range exports {
		if _, err := constructorsymbol.Parse(export.Module + ".New"); err != nil || export.Module == options.ModulePath || i > 0 && exports[i-1].Module == export.Module {
			return runtimebaseline.Document{}, ErrInvalidOptions
		}
		modules[i] = baselineModule{export.Module, export.Version}
	}
	contract, err := json.Marshal(struct {
		Schema               string                `json:"baseline_schema"`
		Module               string                `json:"module"`
		ApplicationModel     string                `json:"application_model"`
		Compatibility        json.RawMessage       `json:"compatibility"`
		Constructors         []constructor         `json:"constructors"`
		ConstructorInventory []baselineConstructor `json:"constructor_inventory"`
		DependencyModules    []baselineModule      `json:"dependency_modules"`
		RuntimeProcessFields []string              `json:"runtime_process_fields"`
	}{runtimebaseline.Schema, options.ModulePath, options.ApplicationModelCompatibility.ApplicationModelDigest(), options.ApplicationModelCompatibility.CanonicalJSON(), constructors, inventory, modules, []string{"http.address", "timeouts.startup"}})
	if err != nil {
		return runtimebaseline.Document{}, ErrInvalidOptions
	}
	return runtimebaseline.Document{Schema: runtimebaseline.Schema, ContractID: runtimebaseline.ContractID(contract), Contract: contract, Defaults: defaults, Exports: exports}, nil
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
	expected := make(map[string]bool)
	for symbol, entry := range inventory { if entry.Schema != nil { expected[symbol] = true } }
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
