package resolutionevidence

import (
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
)

func TestValidateConfigurationSelectionRejectsMalformedProvenance(t *testing.T) {
	t.Parallel()

	valid := func() ConfigurationSelection {
		return ConfigurationSelection{
			mode:             generation.ConfigurationModeEnvironment,
			environment:      "production",
			rootPath:         "plystra.yaml",
			rootDigest:       internalConfigurationDigest("1"),
			selectedPath:     "plystra.production.yaml",
			selectedDigest:   internalConfigurationDigest("2"),
			dependencyDigest: internalConfigurationDigest("3"),
		}
	}
	tests := []struct {
		name   string
		mutate func(*ConfigurationSelection)
	}{
		{name: "mode", mutate: func(value *ConfigurationSelection) { value.mode = "invalid" }},
		{name: "environment", mutate: func(value *ConfigurationSelection) { value.environment = "../production" }},
		{name: "root path", mutate: func(value *ConfigurationSelection) { value.rootPath = "other.yaml" }},
		{name: "selected path", mutate: func(value *ConfigurationSelection) { value.selectedPath = `C:/private/plystra.production.yaml` }},
		{name: "root digest", mutate: func(value *ConfigurationSelection) { value.rootDigest = "sha256:ABC" }},
		{name: "selected digest", mutate: func(value *ConfigurationSelection) { value.selectedDigest = "" }},
		{name: "dependency digest", mutate: func(value *ConfigurationSelection) { value.dependencyDigest = internalConfigurationDigest("g") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection := valid()
			test.mutate(&selection)
			if err := validateConfigurationSelection(selection); err == nil {
				t.Fatalf("validateConfigurationSelection(%#v) succeeded", selection)
			}
		})
	}

	defaultSelection := valid()
	defaultSelection.mode = generation.ConfigurationModeDefault
	defaultSelection.environment = ""
	defaultSelection.selectedPath = defaultSelection.rootPath
	defaultSelection.selectedDigest = defaultSelection.rootDigest
	if err := validateConfigurationSelection(defaultSelection); err != nil {
		t.Fatalf("default selection: %v", err)
	}
	explicitSelection := valid()
	explicitSelection.mode = generation.ConfigurationModeExplicit
	explicitSelection.environment = ""
	explicitSelection.selectedPath = "deploy/customer config.yaml"
	if err := validateConfigurationSelection(explicitSelection); err != nil {
		t.Fatalf("explicit selection: %v", err)
	}
	if err := validateConfigurationSelectionState(ConfigurationSelection{}, false, []ConfigurationField{internalValidConfigurationField()}); err == nil {
		t.Fatal("configuration fields without selection were accepted")
	}
}

func TestValidateConfigurationFieldsRejectsMalformedEvidence(t *testing.T) {
	t.Parallel()

	modules := internalConfigurationModules()
	selection := internalConfigurationSelection()
	if err := validateConfigurationFields([]ConfigurationField{internalValidConfigurationField()}, modules, selection, true); err != nil {
		t.Fatalf("valid configuration field: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*ConfigurationField)
	}{
		{name: "path", mutate: func(field *ConfigurationField) { field.path = "http.unknown" }},
		{name: "field digest", mutate: func(field *ConfigurationField) { field.digest = "sha256:ABC" }},
		{name: "field summary", mutate: func(field *ConfigurationField) { field.summary = "private-value" }},
		{name: "field owner", mutate: func(field *ConfigurationField) { field.owner = "invalid" }},
		{name: "contribution owner", mutate: func(field *ConfigurationField) { field.contributors[0].owner = "invalid" }},
		{name: "contribution precedence", mutate: func(field *ConfigurationField) { field.contributors[0].precedence = 3 }},
		{name: "contribution digest", mutate: func(field *ConfigurationField) { field.contributors[0].digest = "invalid" }},
		{name: "contribution summary", mutate: func(field *ConfigurationField) { field.contributors[0].summary = "private-value" }},
		{name: "removal summary", mutate: func(field *ConfigurationField) { field.contributors[0].removed = true }},
		{name: "source module", mutate: func(field *ConfigurationField) { field.contributors[0].sources[0].module = "example.com/missing" }},
		{name: "source path", mutate: func(field *ConfigurationField) { field.contributors[0].sources[0].path = `C:/private/plystra.yaml` }},
		{name: "source kind", mutate: func(field *ConfigurationField) { field.contributors[0].sources[0].kind = "diagnostic" }},
		{name: "source line", mutate: func(field *ConfigurationField) { field.contributors[0].sources[0].line = 9 }},
		{name: "source column", mutate: func(field *ConfigurationField) { field.contributors[0].sources[0].column = 0 }},
		{name: "no contributors", mutate: func(field *ConfigurationField) { field.contributors = nil }},
		{name: "winning contribution", mutate: func(field *ConfigurationField) { field.contributors[0].effective = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := internalValidConfigurationField()
			test.mutate(&field)
			if err := validateConfigurationFields([]ConfigurationField{field}, modules, selection, true); err == nil {
				t.Fatalf("validateConfigurationFields(%#v) succeeded", field)
			}
		})
	}

	t.Run("duplicate sources", func(t *testing.T) {
		field := internalValidConfigurationField()
		field.contributors[0].sources = append(field.contributors[0].sources, field.contributors[0].sources[0])
		if err := validateConfigurationFields([]ConfigurationField{field}, modules, selection, true); err == nil {
			t.Fatal("duplicate sources were accepted")
		}
	})

	t.Run("contributor order", func(t *testing.T) {
		field := internalValidConfigurationField()
		dependency := ConfigurationContribution{
			owner:      ConfigurationOwnerDependency,
			precedence: 1,
			digest:     internalConfigurationDigest("2"),
			summary:    "redacted",
			sources: []Source{{
				module: "corp.example/platform",
				path:   "plystra.yaml",
				kind:   "configuration-value",
				line:   1,
				column: 1,
			}},
		}
		field.contributors = append(field.contributors, dependency)
		if err := validateConfigurationFields([]ConfigurationField{field}, modules, selection, true); err == nil {
			t.Fatal("reversed contribution order was accepted")
		}
	})

	t.Run("source order", func(t *testing.T) {
		field := internalValidConfigurationField()
		field.path = `config["acme.smtp"]["host"]`
		field.owner = ConfigurationOwnerDependency
		field.digest = internalConfigurationDigest("2")
		field.summary = "redacted"
		field.contributors = []ConfigurationContribution{{
			owner:      ConfigurationOwnerDependency,
			precedence: 1,
			digest:     field.digest,
			summary:    field.summary,
			effective:  true,
			sources: []Source{
				{module: "corp.example/platform", path: "plystra.yaml", kind: "configuration-value", line: 1, column: 1},
				{module: "aaa.example/platform", path: "plystra.yaml", kind: "configuration-value", line: 1, column: 1},
			},
		}}
		if err := validateConfigurationFields([]ConfigurationField{field}, modules, selection, true); err == nil {
			t.Fatal("reversed source order was accepted")
		}
	})
}

func TestEvidenceValidRejectsMalformedConfigurationSource(t *testing.T) {
	t.Parallel()

	evidence := Evidence{
		generationAPI:             generation.Version,
		selectedModelDigest:       internalConfigurationDigest("4"),
		buildModelDigest:          internalConfigurationDigest("5"),
		modules:                   internalConfigurationModules()[:1],
		configurationFields:       []ConfigurationField{internalValidConfigurationField()},
		configurationSelection:    internalConfigurationSelection(),
		hasConfigurationSelection: true,
		prepared:                  true,
	}
	canonical, err := encode(evidence)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	evidence.canonicalJSON = canonical
	evidence.digest = digest(canonical)
	if !evidence.Valid() {
		t.Fatal("valid evidence fixture was rejected")
	}
	evidence.configurationFields[0].contributors[0].sources[0].module = "example.com/missing"
	if evidence.Valid() {
		t.Fatal("Evidence.Valid accepted a configuration source outside participating Projects")
	}
}

func internalValidConfigurationField() ConfigurationField {
	digest := internalConfigurationDigest("1")
	return ConfigurationField{
		path:      "http.address",
		digest:    digest,
		summary:   "string",
		owner:     ConfigurationOwnerRoot,
		effective: true,
		contributors: []ConfigurationContribution{{
			owner:      ConfigurationOwnerRoot,
			precedence: 2,
			digest:     digest,
			summary:    "string",
			effective:  true,
			sources: []Source{{
				module: "example.com/app",
				path:   "plystra.yaml",
				kind:   "configuration-value",
				line:   1,
				column: 1,
			}},
		}},
	}
}

func internalConfigurationSelection() ConfigurationSelection {
	digest := internalConfigurationDigest("1")
	return ConfigurationSelection{
		mode:             generation.ConfigurationModeDefault,
		rootPath:         "plystra.yaml",
		rootDigest:       digest,
		selectedPath:     "plystra.yaml",
		selectedDigest:   digest,
		dependencyDigest: internalConfigurationDigest("3"),
	}
}

func internalConfigurationModules() []Module {
	return []Module{
		{path: "example.com/app", role: ModuleRoleCurrent, source: Source{module: "example.com/app", path: "plystra.yaml", kind: "project-marker", line: 1, column: 1}},
		{path: "example.com/platform", role: ModuleRoleDependency, selectedVersion: "v1.0.0", source: Source{module: "corp.example/platform", path: "plystra.yaml", kind: "project-marker", line: 1, column: 1}, replacement: Replacement{kind: ReplacementModule, modulePath: "corp.example/platform", version: "v1.0.0"}, hasReplacement: true},
		{path: "example.com/aaa", role: ModuleRoleDependency, selectedVersion: "v1.0.0", source: Source{module: "aaa.example/platform", path: "plystra.yaml", kind: "project-marker", line: 1, column: 1}, replacement: Replacement{kind: ReplacementModule, modulePath: "aaa.example/platform", version: "v1.0.0"}, hasReplacement: true},
	}
}

func internalConfigurationDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
