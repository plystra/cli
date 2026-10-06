package resolutionevidence_test

import (
	"bytes"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestSecretReferenceProvenanceDoesNotPublishPrivateEquality(t *testing.T) {
	t.Parallel()
	lookup := configurationSchemaLookup(t)
	manifest := func(reference string) applicationmeta.Manifest {
		return configurationManifest(t, "plystra.yaml", "config: {example.com/acme/smtp.New: {password: "+reference+"}}")
	}
	for _, local := range []string{"{env: PRIVATE_LOCAL}", "{$remove: true}"} {
		var first []byte
		for _, reference := range []string{"{env: PRIVATE_FIRST}", "{env: PRIVATE_SECOND}", "{file: /PRIVATE_FILE}"} {
			value := reference
			if local == "{$remove: true}" {
				value = local
			}
			root := manifest(value)
			composition, err := applicationmeta.Compose(nil, root, lookup)
			if err != nil {
				t.Fatal(err)
			}
			input := configurationEvidenceInput(t, generation.ConfigurationModeDefault, "", "plystra.yaml", composition,
				[]resolutionevidence.ConfigurationLayerInput{{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)}},
				[]resolutionevidence.ModuleInput{{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"}})
			evidence, err := resolutionevidence.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			field := configurationField(t, evidence, `config["example.com/acme/smtp.New"]["password"]`)
			if !field.Effective() || field.Owner() != resolutionevidence.ConfigurationOwnerRoot || field.Removed() != (local == "{$remove: true}") {
				t.Fatal("private equality changed current-Project ownership or removal")
			}
			contributors := field.Contributors()
			if len(contributors) != 1 || !contributors[0].Effective() || len(contributors[0].Sources()) != 1 {
				t.Fatal("private equality changed current-project ownership")
			}
			if first != nil && !bytes.Equal(first, evidence.CanonicalJSON()) {
				t.Fatal("private reference kind, target, or equality changed public provenance")
			}
			first = evidence.CanonicalJSON()
		}
	}
}

func TestBuildRecordsCurrentProjectConfigurationProvenance(t *testing.T) {
	t.Parallel()

	lookup := configurationSchemaLookup(t)
	dependencies := []applicationmeta.Dependency{
		{
			ModulePath:    "example.com/platform-a",
			ModuleVersion: "v1.2.0",
			Manifest: configurationManifest(t, "plystra.yaml", `
config:
  example.com/acme/smtp.New:
    host: dependency.private.example
`),
		},
		{
			ModulePath: "example.com/platform-b",
			Manifest: configurationManifest(t, "plystra.yaml", `
config:
  example.com/acme/smtp.New:
    host: dependency.private.example
`),
		},
	}
	root := configurationManifest(t, "plystra.yaml", `
http: {address: ":8080"}
config:
  example.com/acme/smtp.New:
    host: current.private.example
    password: {env: CURRENT_PRIVATE_PASSWORD}
`)
	composition, err := applicationmeta.Compose(dependencies, root, lookup)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	rootDecisions := configurationDecisions(t, root, lookup)
	input := configurationEvidenceInput(t, generation.ConfigurationModeDefault, "", "plystra.yaml", composition, []resolutionevidence.ConfigurationLayerInput{{
		Owner:     resolutionevidence.ConfigurationOwnerRoot,
		Decisions: rootDecisions,
	}}, []resolutionevidence.ModuleInput{
		{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
		{Path: "example.com/platform-a", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.2.0", SourceModulePath: "example.com/platform-a"},
		{Path: "example.com/platform-b", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/platform-b"},
	})
	first, err := resolutionevidence.Build(input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !first.Valid() {
		t.Fatal("Build returned invalid evidence")
	}
	selection, exists := first.ConfigurationSelection()
	if !exists || selection.Mode() != generation.ConfigurationModeDefault || selection.Environment() != "" || selection.RootPath() != "plystra.yaml" || selection.SelectedPath() != "plystra.yaml" || selection.SelectedDigest() != selection.RootDigest() {
		t.Fatalf("configuration selection = %#v, %t", selection, exists)
	}

	host := configurationField(t, first, `config["example.com/acme/smtp.New"]["host"]`)
	if !host.Effective() || host.Owner() != resolutionevidence.ConfigurationOwnerRoot || host.Summary() != "string" || host.Removed() {
		t.Fatalf("current-project host = %#v", host)
	}
	contributions := host.Contributors()
	if len(contributions) != 1 || !contributions[0].Effective() || contributions[0].Owner() != resolutionevidence.ConfigurationOwnerRoot || len(contributions[0].Sources()) != 1 || contributions[0].Sources()[0].Module() != "example.com/app" || contributions[0].Sources()[0].Path() != "plystra.yaml" {
		t.Fatalf("current-project configuration contributions = %#v", contributions)
	}
	password := configurationField(t, first, `config["example.com/acme/smtp.New"]["password"]`)
	if password.Summary() != "secret-reference" || password.Owner() != resolutionevidence.ConfigurationOwnerRoot {
		t.Fatalf("secret reference evidence = %#v", password)
	}
	address := configurationField(t, first, "http.address")
	if address.Owner() != resolutionevidence.ConfigurationOwnerRoot || address.Summary() != "string" || len(address.Contributors()) != 1 {
		t.Fatalf("process configuration evidence = %#v", address)
	}

	for _, forbidden := range []string{"dependency.private.example", "current.private.example", "CURRENT_PRIVATE_PASSWORD", `C:\\`, "/tmp/"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("configuration evidence exposed %q: %s", forbidden, first.CanonicalJSON())
		}
	}

	fields := first.ConfigurationFields()
	fields[0] = resolutionevidence.ConfigurationField{}
	contributions = host.Contributors()
	contributions[0] = resolutionevidence.ConfigurationContribution{}
	sources := host.Contributors()[0].Sources()
	sources[0] = resolutionevidence.Source{}
	if configurationField(t, first, host.Path()).Owner() != resolutionevidence.ConfigurationOwnerRoot || host.Contributors()[0].Owner() != resolutionevidence.ConfigurationOwnerRoot || host.Contributors()[0].Sources()[0].Module() != "example.com/app" || !first.Valid() {
		t.Fatal("configuration evidence accessors are not defensive")
	}

	reversedDependencies := append([]applicationmeta.Dependency(nil), dependencies...)
	slices.Reverse(reversedDependencies)
	reversedComposition, err := applicationmeta.Compose(reversedDependencies, root, lookup)
	if err != nil {
		t.Fatalf("reversed Compose: %v", err)
	}
	reversedDecisions := append([]applicationmeta.ConfigurationDecision(nil), rootDecisions...)
	slices.Reverse(reversedDecisions)
	reversedInput := configurationEvidenceInput(t, generation.ConfigurationModeDefault, "", "plystra.yaml", reversedComposition, []resolutionevidence.ConfigurationLayerInput{{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: reversedDecisions}}, append([]resolutionevidence.ModuleInput(nil), input.Modules...))
	slices.Reverse(reversedInput.Modules)
	second, err := resolutionevidence.Build(reversedInput)
	if err != nil || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("input permutation changed evidence:\nfirst: %s\nsecond: %s\nerror: %v", first.CanonicalJSON(), second.CanonicalJSON(), err)
	}
}

func TestBuildRecordsEnvironmentRemovalAndSuppressedDescendants(t *testing.T) {
	t.Parallel()

	lookup := configurationSchemaLookup(t)
	dependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.0.0",
		Manifest: configurationManifest(t, "plystra.yaml", `
config:
  example.com/acme/smtp.New:
    host: dependency.private.example
    settings:
      nested:
        dependency: private
`),
	}}
	root := configurationManifest(t, "plystra.yaml", `
config:
  example.com/acme/smtp.New:
    host: root.private.example
    settings:
      nested:
        root: private
`)
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`
config:
  example.com/acme/smtp.New:
    host: production.private.example
    settings: {$remove: true}
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource: %v", err)
	}
	current, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	composition, err := applicationmeta.Compose(dependencies, current, lookup)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	input := configurationEvidenceInput(t, generation.ConfigurationModeEnvironment, "production", "plystra.production.yaml", composition, []resolutionevidence.ConfigurationLayerInput{
		{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)},
		{Owner: resolutionevidence.ConfigurationOwnerEnvironment, Decisions: configurationDecisions(t, overlay, lookup)},
	}, []resolutionevidence.ModuleInput{
		{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
		{Path: "example.com/platform", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/platform"},
	})
	evidence, err := resolutionevidence.Build(input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	selection, exists := evidence.ConfigurationSelection()
	if !exists || selection.Mode() != generation.ConfigurationModeEnvironment || selection.Environment() != "production" || selection.SelectedPath() != "plystra.production.yaml" {
		t.Fatalf("environment selection = %#v, %t", selection, exists)
	}
	host := configurationField(t, evidence, `config["example.com/acme/smtp.New"]["host"]`)
	if host.Owner() != resolutionevidence.ConfigurationOwnerEnvironment || host.Summary() != "string" || len(host.Contributors()) != 2 {
		t.Fatalf("environment replacement = %#v", host)
	}
	settings := configurationField(t, evidence, `config["example.com/acme/smtp.New"]["settings"]`)
	if !settings.Effective() || !settings.Removed() || settings.Owner() != resolutionevidence.ConfigurationOwnerEnvironment || settings.Summary() != "removal" {
		t.Fatalf("environment removal = %#v", settings)
	}
	for _, path := range []string{
		`config["example.com/acme/smtp.New"]["settings"]["nested"]`,
		`config["example.com/acme/smtp.New"]["settings"]["nested"]["root"]`,
	} {
		field := configurationField(t, evidence, path)
		if field.Effective() || field.Owner() != "" || field.Digest() != "" || field.Summary() != "" {
			t.Fatalf("suppressed descendant %s = %#v", path, field)
		}
		for _, contribution := range field.Contributors() {
			if contribution.Effective() {
				t.Fatalf("suppressed descendant %s has effective contribution %#v", path, contribution)
			}
		}
	}
	for _, forbidden := range []string{"dependency.private.example", "root.private.example", "production.private.example"} {
		if bytes.Contains(evidence.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("environment evidence exposed %q: %s", forbidden, evidence.CanonicalJSON())
		}
	}
}

func TestBuildFullReplacementExcludesRootConfigurationLayer(t *testing.T) {
	t.Parallel()

	lookup := configurationSchemaLookup(t)
	dependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.0.0",
		Manifest:      configurationManifest(t, "plystra.yaml", "config: {example.com/acme/smtp.New: {host: dependency.private.example}}\n"),
	}}
	selected := configurationManifest(t, "deploy/customer config.yaml", "config: {example.com/acme/smtp.New: {host: customer.private.example, password: {env: CUSTOMER_PRIVATE_PASSWORD}}}\n")
	composition, err := applicationmeta.Compose(dependencies, selected, lookup)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	input := configurationEvidenceInput(t, generation.ConfigurationModeExplicit, "", "deploy/customer config.yaml", composition, []resolutionevidence.ConfigurationLayerInput{{
		Owner:     resolutionevidence.ConfigurationOwnerExplicit,
		Decisions: configurationDecisions(t, selected, lookup),
	}}, []resolutionevidence.ModuleInput{
		{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
		{Path: "example.com/platform", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/platform"},
	})
	evidence, err := resolutionevidence.Build(input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	selection, exists := evidence.ConfigurationSelection()
	if !exists || selection.Mode() != generation.ConfigurationModeExplicit || selection.SelectedPath() != "deploy/customer config.yaml" || selection.RootPath() != "plystra.yaml" {
		t.Fatalf("explicit selection = %#v, %t", selection, exists)
	}
	for _, field := range evidence.ConfigurationFields() {
		for _, contribution := range field.Contributors() {
			if contribution.Owner() == resolutionevidence.ConfigurationOwnerRoot || contribution.Owner() == resolutionevidence.ConfigurationOwnerEnvironment {
				t.Fatalf("full replacement retained excluded root or environment contribution: %#v", contribution)
			}
		}
	}
	host := configurationField(t, evidence, `config["example.com/acme/smtp.New"]["host"]`)
	if host.Owner() != resolutionevidence.ConfigurationOwnerExplicit || host.Contributors()[len(host.Contributors())-1].Sources()[0].Path() != "deploy/customer config.yaml" {
		t.Fatalf("full-replacement host = %#v", host)
	}
	for _, forbidden := range []string{"dependency.private.example", "customer.private.example", "CUSTOMER_PRIVATE_PASSWORD"} {
		if bytes.Contains(evidence.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("full-replacement evidence exposed %q: %s", forbidden, evidence.CanonicalJSON())
		}
	}
}

func TestBuildRejectsInvalidConfigurationEvidenceInputs(t *testing.T) {
	t.Parallel()

	lookup := configurationSchemaLookup(t)
	root := configurationManifest(t, "plystra.yaml", "http: {address: ':8080'}\n")
	composition, err := applicationmeta.Compose(nil, root, lookup)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	modules := []resolutionevidence.ModuleInput{{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"}}
	valid := configurationEvidenceInput(t, generation.ConfigurationModeDefault, "", "plystra.yaml", composition, []resolutionevidence.ConfigurationLayerInput{{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)}}, modules)

	t.Run("invalid layer owner", func(t *testing.T) {
		input := valid
		configuration := *valid.Configuration
		configuration.Layers = append([]resolutionevidence.ConfigurationLayerInput(nil), configuration.Layers...)
		configuration.Layers[0].Owner = resolutionevidence.ConfigurationOwner("invalid")
		input.Configuration = &configuration
		if evidence, err := resolutionevidence.Build(input); err == nil || !strings.Contains(err.Error(), "configuration layer") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("machine-specific current source", func(t *testing.T) {
		unsafe := configurationManifest(t, "C:/private/plystra.yaml", "http: {address: ':8080'}\n")
		input := valid
		configuration := *valid.Configuration
		configuration.Layers = []resolutionevidence.ConfigurationLayerInput{{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, unsafe, lookup)}}
		input.Configuration = &configuration
		if evidence, err := resolutionevidence.Build(input); err == nil || !strings.Contains(err.Error(), "unsafe") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("ordinary dependency configuration is ignored", func(t *testing.T) {
		dependency := configurationManifest(t, "plystra.yaml", "capabilities: {require: [email.send/v1]}\n")
		dependencyComposition, err := applicationmeta.Compose([]applicationmeta.Dependency{{ModulePath: "example.com/missing", ModuleVersion: "v1.0.0", Manifest: dependency}}, root, lookup)
		if err != nil {
			t.Fatalf("Compose dependency: %v", err)
		}
		input := configurationEvidenceInput(t, generation.ConfigurationModeDefault, "", "plystra.yaml", dependencyComposition, []resolutionevidence.ConfigurationLayerInput{{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)}}, modules)
		evidence, err := resolutionevidence.Build(input)
		if err != nil || !evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
		if len(evidence.Requirements()) != 0 {
			t.Fatalf("ordinary dependency configuration entered evidence: %#v", evidence.Requirements())
		}
	})
}

func configurationEvidenceInput(
	t testing.TB,
	mode generation.ConfigurationMode,
	environment string,
	selectedPath string,
	composition applicationmeta.Composition,
	layers []resolutionevidence.ConfigurationLayerInput,
	modules []resolutionevidence.ModuleInput,
) resolutionevidence.Input {
	t.Helper()
	rootDigest := configurationDigest("1")
	selectedDigest := rootDigest
	if mode != generation.ConfigurationModeDefault {
		selectedDigest = configurationDigest("2")
	}
	context, err := generation.NewContext(generation.Input{ConfigurationProvenance: &generation.ConfigurationProvenanceInput{
		Mode:           mode,
		Environment:    environment,
		RootPath:       "plystra.yaml",
		RootDigest:     rootDigest,
		SelectedPath:   selectedPath,
		SelectedDigest: selectedDigest,
	}})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	providerResult, err := providerresolution.Resolve(providerresolution.Input{})
	if err != nil {
		t.Fatalf("providerresolution.Resolve: %v", err)
	}
	return resolutionevidence.Input{
		Context:            context,
		ProviderResolution: providerResult,
		AliasResolution:    resolveApplicationAliases(t, context),
		Modules:            append([]resolutionevidence.ModuleInput(nil), modules...),
		Configuration: &resolutionevidence.ConfigurationInput{
			Layers:    append([]resolutionevidence.ConfigurationLayerInput(nil), layers...),
			Effective: configurationDecisions(t, composition.Manifest(), configurationSchemaLookup(t)),
		},
	}
}

func configurationSchemaLookup(t testing.TB) applicationmeta.SchemaLookup {
	t.Helper()
	compiled := types.NewPackage("example.com/acme/smtp", "smtp")
	secretPackage := types.NewPackage("github.com/plystra/kernel/configuration", "configuration")
	secretName := types.NewTypeName(token.NoPos, secretPackage, "Secret", nil)
	secret := types.NewNamed(secretName, types.NewStruct(nil, nil), nil)
	secretPackage.Scope().Insert(secretName)
	secretPackage.MarkComplete()

	nested := types.NewStruct([]*types.Var{
		types.NewVar(token.NoPos, compiled, "Dependency", types.Typ[types.String]),
		types.NewVar(token.NoPos, compiled, "Root", types.Typ[types.String]),
	}, []string{`yaml:"dependency"`, `yaml:"root"`})
	settings := types.NewStruct([]*types.Var{
		types.NewVar(token.NoPos, compiled, "Nested", nested),
	}, []string{`yaml:"nested"`})
	configName := types.NewTypeName(token.NoPos, compiled, "Config", nil)
	config := types.NewNamed(configName, types.NewStruct([]*types.Var{
		types.NewVar(token.NoPos, compiled, "Host", types.Typ[types.String]),
		types.NewVar(token.NoPos, compiled, "Password", secret),
		types.NewVar(token.NoPos, compiled, "Settings", settings),
	}, []string{`yaml:"host"`, `yaml:"password"`, `yaml:"settings"`}), nil)
	compiled.Scope().Insert(configName)
	parameters := types.NewTuple(types.NewVar(token.NoPos, compiled, "configuration", config))
	constructor := types.NewFunc(token.NoPos, compiled, "New", types.NewSignatureType(nil, nil, nil, parameters, nil, false))
	compiled.Scope().Insert(constructor)
	schema, exists, err := implementationinventory.CompileConfiguration(compiled, constructor)
	if err != nil || !exists {
		t.Fatalf("CompileConfiguration = %#v, %t, %v", schema, exists, err)
	}
	return func(namespace applicationmeta.ConfigurationNamespace, symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return schema, namespace == applicationmeta.ConfigurationNamespaceImplementation && symbol.String() == "example.com/acme/smtp.New"
	}
}

func configurationManifest(t testing.TB, source, data string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.ParseSource(source, []byte(data))
	if err != nil {
		t.Fatalf("ParseSource(%s): %v", source, err)
	}
	return manifest
}

func configurationDecisions(t testing.TB, manifest applicationmeta.Manifest, lookup applicationmeta.SchemaLookup) []applicationmeta.ConfigurationDecision {
	t.Helper()
	decisions, err := applicationmeta.ConfigurationDecisions(manifest, lookup)
	if err != nil {
		t.Fatalf("ConfigurationDecisions: %v", err)
	}
	return decisions
}

func configurationField(t testing.TB, evidence resolutionevidence.Evidence, path string) resolutionevidence.ConfigurationField {
	t.Helper()
	for _, field := range evidence.ConfigurationFields() {
		if field.Path() == path {
			return field
		}
	}
	t.Fatalf("configuration field %s is absent from %#v", path, evidence.ConfigurationFields())
	return resolutionevidence.ConfigurationField{}
}

func configurationDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
