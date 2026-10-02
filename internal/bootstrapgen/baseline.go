package bootstrapgen

import (
	"encoding/json"
	"sort"

	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/runtimebaseline"
)

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
	contract, err := json.Marshal(struct {
		Schema               string          `json:"baseline_schema"`
		Module               string          `json:"module"`
		ApplicationModel     string          `json:"application_model"`
		Compatibility        json.RawMessage `json:"compatibility"`
		Constructors         []constructor   `json:"constructors"`
		RuntimeProcessFields []string        `json:"runtime_process_fields"`
	}{runtimebaseline.Schema, options.ModulePath, options.ApplicationModelCompatibility.ApplicationModelDigest(), options.ApplicationModelCompatibility.CanonicalJSON(), constructors, []string{"http.address", "timeouts.startup"}})
	if err != nil {
		return runtimebaseline.Document{}, ErrInvalidOptions
	}
	exports := append([]runtimebaseline.Export{}, options.DependencyExports...)
	sort.Slice(exports, func(i, j int) bool { return exports[i].Module < exports[j].Module })
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
	if len(bindings) != len(document.Defaults) { return fmt.Errorf("%w: constructor baseline membership changed; regenerate and rebuild", ErrRuntimeCompatibility) }
	for _, binding := range bindings {
		compiled, err := constructorconfig.DefaultsJSON(binding.schema)
		if err != nil || !bytes.Equal(compiled, document.Defaults[binding.symbol]) {
			return fmt.Errorf("%w: compiled constructor defaults and private baseline differ; regenerate and rebuild", ErrRuntimeCompatibility)
		}
	}
	return nil
}
`
