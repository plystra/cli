package bootstrapgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/transportprovenance"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestRenderProducesDeterministicRedactedRuntimeBoundary(t *testing.T) {
	t.Parallel()

	configurationSchema, err := kernelmanifest.ParseConfig([]byte(`
endpoint: {type: url}
headers: {type: object}
recipients: {type: array, items: string}
token: {type: secret}
`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	provenance := bootstrapConfigurationProvenance(t, generation.ConfigurationModeDefault)
	options := bootstrapgen.Options{
		ModulePath:                    "example.com/acme/application",
		DefaultStartupTimeout:         2 * time.Minute,
		ConfigurationProvenance:       provenance,
		ApplicationModelCompatibility: bootstrapModelCompatibility(t, provenance),
		ConfigurationSchemas: []bootstrapgen.ConfigurationSchema{
			{PluginID: "acme.records", Schema: configurationSchema},
			{PluginID: "acme.audit", Schema: kernelmanifest.Config{}},
		},
	}
	generated, err := bootstrapgen.Render(options)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), bootstrapgen.Path, generated, parser.AllErrors); err != nil {
		t.Fatalf("parse generated bootstrap: %v\n%s", err, generated)
	}
	for _, required := range []string{
		`applicationassembly "example.com/acme/application/generated/go/assembly"`,
		`defaultRuntimeDocument = "plystra.yaml"`,
		"compiledConfigurationSelectionProvenanceJSON",
		strconv.Quote(string(options.ConfigurationProvenance.CanonicalJSON())),
		`compiledConfigurationSelectionProvenanceDigest = "` + options.ConfigurationProvenance.Digest() + `"`,
		"compiledApplicationModelCompatibilityJSON",
		strconv.Quote(string(options.ApplicationModelCompatibility.CanonicalJSON())),
		`compiledApplicationModelCompatibilityDigest = "` + options.ApplicationModelCompatibility.Digest() + `"`,
		"compiledApplicationModelDigest",
		strconv.Quote(options.ApplicationModelCompatibility.ApplicationModelDigest()),
		"func New(ctx context.Context, options RuntimeOptions)",
		"validateRuntimeApplicationModel(document)",
		"runtimeApplicationModelCompatibilityDigest",
		"func loadRuntimeDocument(options RuntimeOptions)",
		`runtimeEnvironmentVariable   = "PLYSTRA_ENV"`,
		`runtimeConfigurationVariable = "PLYSTRA_CONFIG"`,
		`case "--env":`,
		`case "--config":`,
		`path: "plystra." + arguments[1] + ".yaml"`,
		"runtimeProjectRelativeConfigurationPath",
		"inspectRuntimeConfigurationPath",
		"sameRuntimeConfigurationPathStates",
		"normalizeRuntimeDocument",
		`"acme.audit": {`,
		`"acme.records": {`,
		`"headers":    runtimeConfigurationObject`,
		`"recipients": runtimeConfigurationArray`,
		`"token":      runtimeConfigurationSecret`,
		"mergeRuntimeInterfaceSet",
		"mergeRuntimeOpenObject",
		"validateRuntimeOpenObjectType",
		"YAML anchors and aliases are not allowed",
		"defer clear(document)",
		"kernelconfiguration.ExtractStringMap(document, \"timeouts\")",
		"kernelconfiguration.NewResolver",
		"applicationassembly.NewRuntime",
		"applicationassembly.NewProviderLifecycle",
		"applicationassembly.NewInterfaceRuntime(applicationassembly.ConstructorConfiguration{}, startupTimeout)",
		"func (a *Application) Interfaces() applicationassembly.InterfaceRuntime",
		"a.interfaces.Valid()",
		"a.interfaces.Start(startupContext)",
		"a.interfaces.Stop(cleanupContext)",
		"a.interfaces.Stop(ctx)",
		"context.WithoutCancel(ctx)",
		"staticState := a.interfaces.State()",
		"legacyState := a.lifecycle.State()",
		"staticState == kernellifecycle.StateFailed || legacyState == kernellifecycle.StateFailed",
		"context.WithTimeout(ctx, a.startupTimeout)",
		"<redacted-generated-application>",
		"kernelconfiguration.ErrSecretExposure",
	} {
		if !bytes.Contains(generated, []byte(required)) {
			t.Fatalf("generated source omits %q:\n%s", required, generated)
		}
	}
	for _, forbidden := range []string{
		"documentPath string",
		"private-runtime-value",
		"PRIVATE_SECRET_TARGET",
	} {
		if bytes.Contains(generated, []byte(forbidden)) {
			t.Fatalf("generated source contains runtime input %q:\n%s", forbidden, generated)
		}
	}
	repeatedOptions := options
	repeatedOptions.ConfigurationSchemas = []bootstrapgen.ConfigurationSchema{
		options.ConfigurationSchemas[1],
		options.ConfigurationSchemas[0],
	}
	repeated, err := bootstrapgen.Render(repeatedOptions)
	if err != nil || !bytes.Equal(generated, repeated) {
		t.Fatalf("repeated Render differs: %v", err)
	}
}

func TestRenderRecordsEveryConfigurationSelectionProvenance(t *testing.T) {
	t.Parallel()

	generatedByMode := make(map[generation.ConfigurationMode][]byte)
	for _, mode := range []generation.ConfigurationMode{
		generation.ConfigurationModeDefault,
		generation.ConfigurationModeEnvironment,
		generation.ConfigurationModeExplicit,
	} {
		provenance := bootstrapConfigurationProvenance(t, mode)
		options := bootstrapgen.Options{
			ModulePath:                    "example.com/acme/application",
			DefaultStartupTimeout:         time.Minute,
			ConfigurationProvenance:       provenance,
			ApplicationModelCompatibility: bootstrapModelCompatibility(t, provenance),
		}
		generated, err := bootstrapgen.Render(options)
		if err != nil {
			t.Fatalf("Render(%s): %v", mode, err)
		}
		if !bytes.Contains(generated, []byte(strconv.Quote(string(provenance.CanonicalJSON())))) || !bytes.Contains(generated, []byte(strconv.Quote(provenance.Digest()))) {
			t.Fatalf("generated %s bootstrap omits exact canonical provenance:\n%s", mode, generated)
		}
		for _, forbidden := range []string{
			`C:\\Users\\private\\project`,
			"PRIVATE_SECRET_TARGET",
			"resolved-secret-value",
			"runtime-environment-content",
			"password: super-secret",
		} {
			if bytes.Contains(generated, []byte(forbidden)) {
				t.Fatalf("generated %s bootstrap contains forbidden configuration detail %q", mode, forbidden)
			}
		}
		repeated, err := bootstrapgen.Render(options)
		if err != nil || !bytes.Equal(generated, repeated) {
			t.Fatalf("repeated Render(%s) differs: %v", mode, err)
		}
		generatedByMode[mode] = generated
	}
	if bytes.Equal(generatedByMode[generation.ConfigurationModeDefault], generatedByMode[generation.ConfigurationModeEnvironment]) ||
		bytes.Equal(generatedByMode[generation.ConfigurationModeDefault], generatedByMode[generation.ConfigurationModeExplicit]) ||
		bytes.Equal(generatedByMode[generation.ConfigurationModeEnvironment], generatedByMode[generation.ConfigurationModeExplicit]) {
		t.Fatal("distinct configuration selections produced identical bootstrap provenance")
	}
}

func TestRenderRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	validProvenance := bootstrapConfigurationProvenance(t, generation.ConfigurationModeDefault)
	validCompatibility := bootstrapModelCompatibility(t, validProvenance)
	mismatchedCompatibility, err := bootstrapgen.NewApplicationModelCompatibility(bootstrapDigest("4"), applicationmeta.Manifest{})
	if err != nil {
		t.Fatalf("bootstrapgen.NewApplicationModelCompatibility(mismatched): %v", err)
	}
	for _, options := range []bootstrapgen.Options{
		{ModulePath: "not a module", DefaultStartupTimeout: 2 * time.Minute, ConfigurationProvenance: validProvenance, ApplicationModelCompatibility: validCompatibility},
		{ModulePath: "example.com/acme/application", ConfigurationProvenance: validProvenance, ApplicationModelCompatibility: validCompatibility},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: -time.Second, ConfigurationProvenance: validProvenance, ApplicationModelCompatibility: validCompatibility},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second, ConfigurationProvenance: validProvenance},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second, ConfigurationProvenance: validProvenance, ApplicationModelCompatibility: mismatchedCompatibility},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second, ConfigurationProvenance: validProvenance, ApplicationModelCompatibility: validCompatibility, ConfigurationSchemas: []bootstrapgen.ConfigurationSchema{{PluginID: "not a Plugin"}}},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second, ConfigurationProvenance: validProvenance, ApplicationModelCompatibility: validCompatibility, ConfigurationSchemas: []bootstrapgen.ConfigurationSchema{{PluginID: "acme.mail"}, {PluginID: "acme.mail"}}},
	} {
		generated, err := bootstrapgen.Render(options)
		if generated != nil || !errors.Is(err, bootstrapgen.ErrRender) || !errors.Is(err, bootstrapgen.ErrInvalidOptions) {
			t.Fatalf("Render(%#v) = %q, %v", options, generated, err)
		}
	}
}

func bootstrapConfigurationProvenance(t testing.TB, mode generation.ConfigurationMode) transportprovenance.Provenance {
	t.Helper()
	input := transportprovenance.Input{
		Mode:                        mode,
		RootPath:                    "plystra.yaml",
		RootDigest:                  bootstrapDigest("1"),
		SelectedPath:                "plystra.yaml",
		SelectedDigest:              bootstrapDigest("1"),
		DependencyCompositionDigest: bootstrapDigest("2"),
		ApplicationModelDigest:      bootstrapDigest("3"),
	}
	switch mode {
	case generation.ConfigurationModeEnvironment:
		input.Environment = "production"
		input.SelectedPath = "plystra.production.yaml"
		input.SelectedDigest = bootstrapDigest("4")
	case generation.ConfigurationModeExplicit:
		input.SelectedPath = "deploy/customer-a.yaml"
		input.SelectedDigest = bootstrapDigest("5")
	}
	provenance, err := transportprovenance.New(input)
	if err != nil {
		t.Fatalf("transportprovenance.New(%s): %v", mode, err)
	}
	return provenance
}

func bootstrapModelCompatibility(t testing.TB, provenance transportprovenance.Provenance) bootstrapgen.ApplicationModelCompatibility {
	t.Helper()
	compatibility, err := bootstrapgen.NewApplicationModelCompatibility(provenance.ApplicationModelDigest(), applicationmeta.Manifest{})
	if err != nil {
		t.Fatalf("bootstrapgen.NewApplicationModelCompatibility: %v", err)
	}
	return compatibility
}

func bootstrapDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
