package configurationgen_test

import (
	"bytes"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/kernel/plugin/manifest"
)

func TestRenderProducesDeterministicSchemaOnlyTypedDecoder(t *testing.T) {
	t.Parallel()

	schema := parseConfig(t, completeSchema)
	input := configurationgen.Input{
		PluginName: "email-smtp",
		PluginID:   "acme.email.smtp",
		Schema:     schema,
	}
	generated, err := configurationgen.Render(input)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if generated.Path() != "generated/go/configuration/email-smtp_gen.go" || generated.TypeName() != "EmailSMTPConfig" || generated.DecodeName() != "DecodeEmailSMTP" {
		t.Fatalf("generated identity = path %q, type %q, decode %q", generated.Path(), generated.TypeName(), generated.DecodeName())
	}
	if _, err := parser.ParseFile(token.NewFileSet(), generated.Path(), generated.Data(), parser.AllErrors); err != nil {
		t.Fatalf("parse generated source: %v\n%s", err, generated.Data())
	}
	for _, required := range []string{
		"kernelconfiguration.Decode",
		"type EmailSMTPConfig struct",
		"OptionalString *string",
		"OptionalObject map[string]any",
		"OptionalIntegers []int64",
		"Mode string",
		"public-default",
	} {
		if !bytes.Contains(generated.Data(), []byte(required)) {
			t.Fatalf("generated source omits %q:\n%s", required, generated.Data())
		}
	}
	for _, forbidden := range []string{"runtime-private-host", "PLYSTRA_CONFIGURATIONGEN_PRIVATE_SECRET", "/run/private/secret"} {
		if bytes.Contains(generated.Data(), []byte(forbidden)) {
			t.Fatalf("generated source contains runtime value %q:\n%s", forbidden, generated.Data())
		}
	}
	copyData := generated.Data()
	copyData[0] = 'X'
	if bytes.Equal(copyData, generated.Data()) {
		t.Fatal("generated Data exposed mutable storage")
	}
	repeated, err := configurationgen.Render(input)
	if err != nil || repeated.Path() != generated.Path() || !bytes.Equal(repeated.Data(), generated.Data()) {
		t.Fatalf("repeated Render is not deterministic: %v", err)
	}
}

func TestGeneratedDecoderConvertsEverySupportedTypeAndRedactsValues(t *testing.T) {
	generated, err := configurationgen.Render(configurationgen.Input{
		PluginName: "email-smtp",
		PluginID:   "acme.email.smtp",
		Schema:     parseConfig(t, completeSchema),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/configurationfixture\n\ngo 1.26\n\nrequire (\n\tgithub.com/plystra/kernel v0.0.0\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n)\n\nreplace github.com/plystra/kernel => "+filepath.ToSlash(kernelRoot)+"\n")
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeBytes(t, filepath.Join(root, "go.sum"), goSum)
	writeBytes(t, filepath.Join(root, "configuration_gen.go"), generated.Data())
	writeFile(t, filepath.Join(root, "configuration_gen_test.go"), generatedRuntimeTest)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", ".")
	command.Dir = root
	command.Env = append(filteredEnvironment(os.Environ(), "GOENV", "GOFLAGS", "GOPROXY", "GOSUMDB", "GOWORK"),
		"GOENV=off",
		"GOFLAGS=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOWORK=off",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated runtime test: %v\n%s", err, output)
	}
}

func TestRenderRejectsInvalidIdentityAndGoFieldCollisions(t *testing.T) {
	t.Parallel()

	empty := parseConfig(t, "{}\n")
	for _, input := range []configurationgen.Input{
		{PluginName: "Email", PluginID: "acme.email", Schema: empty},
		{PluginName: "email", PluginID: "Acme.Email", Schema: empty},
	} {
		if _, err := configurationgen.Render(input); !errors.Is(err, configurationgen.ErrRender) || !errors.Is(err, configurationgen.ErrInvalidInput) {
			t.Fatalf("Render(%#v) error = %v", input, err)
		}
	}
	collision := parseConfig(t, `
user_id: {type: string}
user_i_d: {type: string}
`)
	if _, err := configurationgen.Render(configurationgen.Input{PluginName: "account", PluginID: "acme.account", Schema: collision}); !errors.Is(err, configurationgen.ErrRender) || !errors.Is(err, configurationgen.ErrIdentifierCollision) {
		t.Fatalf("field collision error = %v", err)
	}
	reserved := parseConfig(t, "marshal_json: {type: string}\n")
	if _, err := configurationgen.Render(configurationgen.Input{PluginName: "account", PluginID: "acme.account", Schema: reserved}); !errors.Is(err, configurationgen.ErrRender) || !errors.Is(err, configurationgen.ErrIdentifierCollision) {
		t.Fatalf("reserved member collision error = %v", err)
	}
}

func TestDeriveGoNamesMatchesRenderedIdentity(t *testing.T) {
	t.Parallel()

	names, err := configurationgen.DeriveGoNames("email-smtp")
	if err != nil || names.TypeName() != "EmailSMTPConfig" || names.DecodeName() != "DecodeEmailSMTP" {
		t.Fatalf("DeriveGoNames = type %q, decode %q, %v", names.TypeName(), names.DecodeName(), err)
	}
	for _, invalid := range []string{"", "Email", "email--smtp", "email_legacy"} {
		if names, err := configurationgen.DeriveGoNames(invalid); names.TypeName() != "" || names.DecodeName() != "" || !errors.Is(err, configurationgen.ErrInvalidInput) {
			t.Fatalf("DeriveGoNames(%q) = %#v, %v", invalid, names, err)
		}
	}
}

func parseConfig(t testing.TB, source string) manifest.Config {
	t.Helper()
	schema, err := manifest.ParseConfig([]byte(source))
	if err != nil {
		t.Fatalf("manifest.ParseConfig: %v\n%s", err, source)
	}
	return schema
}

func writeFile(t testing.TB, name, data string) {
	t.Helper()
	writeBytes(t, name, []byte(data))
}

func writeBytes(t testing.TB, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func filteredEnvironment(environment []string, removed ...string) []string {
	keys := make(map[string]struct{}, len(removed))
	for _, key := range removed {
		keys[strings.ToUpper(key)] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, remove := keys[strings.ToUpper(key)]; !remove {
			result = append(result, entry)
		}
	}
	return result
}

const completeSchema = `
email: {type: string, required: true, format: email}
mode: {type: string, default: public-default, enum: [public-default, public-fast]}
required_string: {type: string, required: true}
optional_string: {type: string}
default_integer: {type: integer, default: 42}
optional_number: {type: number}
optional_boolean: {type: boolean}
duration: {type: duration, required: true}
optional_url: {type: url}
password: {type: secret, required: true}
metadata: {type: object, required: true}
optional_object: {type: object}
strings: {type: array, items: string, required: true}
optional_integers: {type: array, items: integer}
numbers: {type: array, items: number, required: true}
booleans: {type: array, items: boolean, required: true}
durations: {type: array, items: duration, required: true}
urls: {type: array, items: url, required: true}
objects: {type: array, items: object, required: true}
`

const generatedRuntimeTest = `package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	kernelconfiguration "github.com/plystra/kernel/configuration"
)

func TestDecodeGeneratedConfiguration(t *testing.T) {
	t.Setenv("PLYSTRA_CONFIGURATIONGEN_PRIVATE_SECRET", "runtime-secret-value")
	resolver, err := kernelconfiguration.NewResolver(kernelconfiguration.ResolverOptions{MaximumValueBytes: 1024})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	raw := []byte(` + "`" + `
email: operator@example.com
mode: public-fast
required_string: runtime-private-host
optional_boolean: false
duration: 2s
optional_url: https://private.example.com/path
password: {env: PLYSTRA_CONFIGURATIONGEN_PRIVATE_SECRET}
metadata: {nested: [one, 2, true]}
strings: []
numbers: [1.5, 2]
booleans: [true, false]
durations: [1s, 2m]
urls: [https://one.example.com, https://two.example.com/path]
objects: [{name: one}, {name: two}]
` + "`" + `)
	config, err := DecodeEmailSMTP(context.Background(), resolver, raw)
	if err != nil {
		t.Fatalf("DecodeEmailSMTP: %v", err)
	}
	if config.Email != "operator@example.com" || config.Mode != "public-fast" || config.RequiredString != "runtime-private-host" || config.DefaultInteger != 42 {
		t.Fatalf("scalar values = %#v", config)
	}
	if config.OptionalString != nil || config.OptionalNumber != nil || config.OptionalBoolean == nil || *config.OptionalBoolean || config.OptionalURL == nil || config.OptionalURL.Host != "private.example.com" {
		t.Fatalf("optional scalar values are not explicit: %#v", config)
	}
	if config.Duration != 2*time.Second || !config.Password.Valid() || string(config.Password.Bytes()) != "runtime-secret-value" {
		t.Fatalf("duration or Secret conversion failed: %#v", config)
	}
	if config.Metadata["nested"] == nil || config.OptionalObject != nil {
		t.Fatalf("object values = %#v, optional %#v", config.Metadata, config.OptionalObject)
	}
	if config.Strings == nil || len(config.Strings) != 0 || config.OptionalIntegers != nil {
		t.Fatalf("array absence/empty behavior = strings %#v, integers %#v", config.Strings, config.OptionalIntegers)
	}
	if len(config.Numbers) != 2 || len(config.Booleans) != 2 || len(config.Durations) != 2 || config.Durations[1] != 2*time.Minute || len(config.Urls) != 2 || config.Urls[1].Path != "/path" || len(config.Objects) != 2 || config.Objects[1]["name"] != "two" {
		t.Fatalf("array conversions = numbers %#v booleans %#v durations %#v urls %#v objects %#v", config.Numbers, config.Booleans, config.Durations, config.Urls, config.Objects)
	}
	for _, formatted := range []string{fmt.Sprintf("%v", config), fmt.Sprintf("%+v", config), fmt.Sprintf("%#v", config), fmt.Sprintf("%q", config)} {
		if !strings.Contains(formatted, "redacted") || strings.Contains(formatted, "runtime-private-host") || strings.Contains(formatted, "runtime-secret-value") {
			t.Fatalf("configuration formatting = %q", formatted)
		}
	}
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("configuration", "value", config)
	if !strings.Contains(logOutput.String(), "redacted") || strings.Contains(logOutput.String(), "runtime-private-host") || strings.Contains(logOutput.String(), "runtime-secret-value") {
		t.Fatalf("configuration log = %s", logOutput.String())
	}
	if _, err := json.Marshal(config); !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("json.Marshal error = %v", err)
	}

	invalid := bytes.Replace(raw, []byte("mode: public-fast"), []byte("mode: runtime-forbidden-enum"), 1)
	if _, err := DecodeEmailSMTP(context.Background(), resolver, invalid); err == nil || strings.Contains(err.Error(), "runtime-forbidden-enum") || strings.Contains(err.Error(), "PLYSTRA_CONFIGURATIONGEN_PRIVATE_SECRET") {
		t.Fatalf("invalid enum error = %v", err)
	}
}
`
