package bootstrapgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/plystra/cli/internal/bootstrapgen"
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
	options := bootstrapgen.Options{
		ModulePath:            "example.com/acme/application",
		DefaultStartupTimeout: 2 * time.Minute,
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
		"func New(ctx context.Context, options RuntimeOptions)",
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
		"mergeRuntimeCapabilitySet",
		"mergeRuntimeOpenObject",
		"validateRuntimeOpenObjectType",
		"YAML anchors and aliases are not allowed",
		"defer clear(document)",
		"kernelconfiguration.ExtractStringMap(document, \"timeouts\")",
		"kernelconfiguration.NewResolver",
		"applicationassembly.NewRuntime",
		"applicationassembly.NewProviderLifecycle",
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

func TestRenderRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	for _, options := range []bootstrapgen.Options{
		{ModulePath: "not a module", DefaultStartupTimeout: 2 * time.Minute},
		{ModulePath: "example.com/acme/application"},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: -time.Second},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second, ConfigurationSchemas: []bootstrapgen.ConfigurationSchema{{PluginID: "not a Plugin"}}},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: time.Second, ConfigurationSchemas: []bootstrapgen.ConfigurationSchema{{PluginID: "acme.mail"}, {PluginID: "acme.mail"}}},
	} {
		generated, err := bootstrapgen.Render(options)
		if generated != nil || !errors.Is(err, bootstrapgen.ErrRender) || !errors.Is(err, bootstrapgen.ErrInvalidOptions) {
			t.Fatalf("Render(%#v) = %q, %v", options, generated, err)
		}
	}
}
