package bootstrapgen_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/runtimebaseline"
)

func templateOptions(t *testing.T) bootstrapgen.Options {
	t.Helper()
	provenance := bootstrapConfigurationProvenance(t, generation.ConfigurationModeDefault)
	return bootstrapgen.Options{
		ModulePath: "example.com/app", Template: "example.com/near",
		Templates: []runtimebaseline.Template{
			{Module: "example.com/z-old", Version: "v1.0.0", YAML: "config: {acme.app: {endpoint: PRIVATE_SENTINEL}}\n"},
			{Module: "example.com/near", Version: "v1.2.0", Template: "example.com/z-old", YAML: "{}\n"},
		},
		DefaultStartupTimeout:         time.Minute,
		ConfigurationProvenance:       provenance,
		ApplicationModelCompatibility: bootstrapModelCompatibility(t, provenance),
	}
}

func TestRuntimeBaselineBindsOrderedAncestryWithoutPrivateValues(t *testing.T) {
	options := templateOptions(t)
	baseline, err := bootstrapgen.RuntimeBaseline(options)
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Template  string
		Templates []struct{ Module, Version, Template string } `json:"template_ancestry"`
	}
	if err := json.Unmarshal(baseline.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Template != options.Template || len(contract.Templates) != 2 || contract.Templates[0].Module != "example.com/z-old" || contract.Templates[1].Template != "example.com/z-old" {
		t.Fatal("contract lost ordered ancestry")
	}
	if baseline.Templates[0].YAML != options.Templates[0].YAML {
		t.Fatal("private layer lost")
	}
	for _, forbidden := range []string{"PRIVATE_SENTINEL", "endpoint", `"yaml"`, "dependency_modules", "dependency_exports"} {
		if strings.Contains(string(baseline.Contract), forbidden) {
			t.Fatalf("contract contains %s", forbidden)
		}
	}
	first, err := bootstrapgen.Render(options)
	if err != nil {
		t.Fatal(err)
	}
	options.Templates[0].YAML = "config: {acme.app: {endpoint: REFRESHED_PRIVATE_VALUE}}\n"
	refreshed, err := bootstrapgen.RuntimeBaseline(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bootstrapgen.Render(options)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ContractID != refreshed.ContractID || !bytes.Equal(first, second) {
		t.Fatal("private refresh changed binary inputs")
	}
	if bytes.Contains(first, []byte("PRIVATE_SENTINEL")) {
		t.Fatal("generated source contains private template")
	}
	options.Templates[0].Version = "v1.1.0"
	changed, err := bootstrapgen.RuntimeBaseline(options)
	if err != nil || changed.ContractID == baseline.ContractID {
		t.Fatal("ancestry version did not change contract", err)
	}
}

func TestRuntimeBaselineRejectsInvalidAncestry(t *testing.T) {
	for name, change := range map[string]func(*bootstrapgen.Options){
		"missing":        func(o *bootstrapgen.Options) { o.Templates = nil },
		"missing root":   func(o *bootstrapgen.Options) { o.Template = "" },
		"root drift":     func(o *bootstrapgen.Options) { o.Template = "example.com/other" },
		"duplicate":      func(o *bootstrapgen.Options) { o.Templates[1].Module = o.Templates[0].Module },
		"current module": func(o *bootstrapgen.Options) { o.Templates[0].Module = o.ModulePath },
		"cycle":          func(o *bootstrapgen.Options) { o.Templates[0].Template = o.Templates[1].Module },
		"gap":            func(o *bootstrapgen.Options) { o.Templates[1].Template = "example.com/missing" },
		"reordered":      func(o *bootstrapgen.Options) { o.Templates[0], o.Templates[1] = o.Templates[1], o.Templates[0] },
		"invalid module": func(o *bootstrapgen.Options) { o.Templates[0].Module = "../PRIVATE_SENTINEL" },
	} {
		t.Run(name, func(t *testing.T) {
			options := templateOptions(t)
			change(&options)
			if _, err := bootstrapgen.RuntimeBaseline(options); !errors.Is(err, bootstrapgen.ErrInvalidOptions) || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("accepted ancestry or exposed input", err)
			}
		})
	}
}
