package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestParseSourceRetainsSelectedDocumentProvenance(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.ParseSource("plystra.production.yaml", []byte(`
http: {expose: [email.send/v1]}
capabilities:
  require: [email.send/v1]
  use: {email.send/v1: acme.smtp}
  aliases: {mail.send/v1: email.send/v1}
config: {example.com/acme/smtp.New: {host: production.example}}
`))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	for _, source := range []string{
		manifest.HTTPExposures()[0].Source(),
		manifest.Requirements()[0].Source(),
		manifest.ProviderChoices()[0].Source(),
		manifest.Aliases()[0].Source(),
	} {
		if !strings.HasPrefix(source, "plystra.production.yaml ") {
			t.Fatalf("declaration source = %q", source)
		}
	}
	configured, exists := manifest.Configuration(mustConstructorSymbol(t, "example.com/acme/smtp.New"))
	if !exists || configured.Source() != `plystra.production.yaml config["example.com/acme/smtp.New"]` {
		t.Fatalf("configuration source = %q, %t", configured.Source(), exists)
	}
	if _, err := applicationmeta.ParseSource("bad\nsource", []byte("{}\n")); !errors.Is(err, applicationmeta.ErrInvalidManifest) {
		t.Fatalf("ParseSource(control) error = %v", err)
	}
}

func TestApplyOverlayUsesTypedFieldPrecedenceAndPreservesRemovals(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
	Host string
	Hosts []string
	Settings struct {
		Keep string
		Remove string
		Add string
		Zone string
	}
`)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	base := parseOverlayManifest(t, "plystra.yaml", `
http:
  address: ":8080"
  transports: {connect: false, rest: true}
  cors:
    allowed_origins: [https://shared.example]
    allow_credentials: true
  expose: [audit.write/v1, email.send/v1]
timeouts: {startup: 1m}
capabilities:
  require: [audit.write/v1, email.send/v1]
  use:
    email.send/v1: acme.smtp.shared
  aliases:
    mail.send/v1: email.send/v1
    old.audit/v1: audit.write/v1
config:
  example.com/acme/smtp.New:
    host: shared.example
    hosts: [shared-a.example]
    settings:
      keep: retained
      remove: shared-private-value
`)
	overlay := parseOverlayManifest(t, "plystra.production.yaml", `
http:
  address: ":9090"
  transports: {connect: true, rest: null}
  cors:
    allowed_origins: [https://production.example, https://PRODUCTION.example:443]
    allow_credentials: null
  expose:
    add: [reports.read/v1]
    remove: [audit.write/v1]
timeouts: {startup: null}
capabilities:
  require:
    add: [reports.read/v1]
    remove: [audit.write/v1]
  use:
    audit.write/v1: null
    email.send/v1: acme.smtp.production
  aliases:
    mail.send/v1: reports.read/v1
    old.audit/v1: null
config:
  example.com/acme/smtp.New:
    host: production.example
    hosts: [production-a.example]
    settings:
      add: production
      remove: null
`)

	current, err := applicationmeta.ApplyOverlay(base, overlay, lookup)
	if err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if address, exists := current.HTTPAddress(); !exists || address != ":9090" {
		t.Fatalf("HTTPAddress = %q, %t", address, exists)
	}
	if transports := current.HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("HTTPTransports = %#v", transports)
	}
	cors, exists := current.HTTPCORS()
	if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://production.example"}) || cors.AllowCredentials {
		t.Fatalf("HTTPCORS = %#v, %t", cors, exists)
	}
	if current.StartupTimeout() != applicationmeta.DefaultStartupTimeout {
		t.Fatalf("StartupTimeout = %s", current.StartupTimeout())
	}
	if got := overlayRequirementIDs(current); !reflect.DeepEqual(got, []string{"email.send/v1", "reports.read/v1"}) {
		t.Fatalf("current requirements = %v", got)
	}
	if got := overlayExposureIDs(current); !reflect.DeepEqual(got, []string{"email.send/v1", "reports.read/v1"}) {
		t.Fatalf("current exposures = %v", got)
	}
	if got := overlayProviderStrings(current); !reflect.DeepEqual(got, []string{"email.send/v1=acme.smtp.production"}) {
		t.Fatalf("current Providers = %v", got)
	}
	if got := aliasStrings(current.Aliases()); !reflect.DeepEqual(got, []string{"mail.send/v1->reports.read/v1"}) {
		t.Fatalf("current Aliases = %v", got)
	}

	dependencies := []applicationmeta.Dependency{
		{
			ModulePath:    "example.com/a",
			ModuleVersion: "v1.0.0",
			Manifest: parseOverlayManifest(t, "plystra.yaml", `
http: {transports: {connect: false, rest: true}, cors: {allowed_origins: ['*']}, expose: [audit.write/v1]}
capabilities:
  require: [audit.write/v1]
  use:
    audit.write/v1: acme.audit
    email.send/v1: acme.smtp.a
  aliases: {old.audit/v1: audit.write/v1}
config:
  example.com/acme/smtp.New:
    settings:
      remove: dependency-private-value
      zone: inherited
`),
		},
		{
			ModulePath:    "example.com/b",
			ModuleVersion: "v1.0.0",
			Manifest:      parseOverlayManifest(t, "plystra.yaml", "capabilities: {use: {email.send/v1: acme.smtp.b}}\n"),
		},
	}
	composed, err := applicationmeta.Compose(dependencies, current, lookup)
	if err != nil {
		t.Fatalf("Compose overlaid current Project: %v", err)
	}
	effective := composed.Manifest()
	if transports := effective.HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("effective HTTP transports = %#v", transports)
	}
	effectiveCORS, exists := effective.HTTPCORS()
	if !exists || !reflect.DeepEqual(effectiveCORS.AllowedOrigins, []string{"https://production.example"}) || effectiveCORS.AllowCredentials {
		t.Fatalf("effective HTTPCORS = %#v, %t", effectiveCORS, exists)
	}
	if got := overlayRequirementIDs(effective); !reflect.DeepEqual(got, []string{"email.send/v1", "reports.read/v1"}) {
		t.Fatalf("effective requirements = %v", got)
	}
	if got := overlayExposureIDs(effective); !reflect.DeepEqual(got, []string{"email.send/v1", "reports.read/v1"}) {
		t.Fatalf("effective exposures = %v", got)
	}
	if got := overlayProviderStrings(effective); !reflect.DeepEqual(got, []string{"email.send/v1=acme.smtp.production"}) {
		t.Fatalf("effective Providers = %v", got)
	}
	configured, exists := effective.Configuration(mustConstructorSymbol(t, "example.com/acme/smtp.New"))
	if !exists {
		t.Fatal("effective acme.smtp configuration is absent")
	}
	for _, expected := range [][]byte{
		[]byte("host: production.example"),
		[]byte("production-a.example"),
		[]byte("add: production"),
		[]byte("keep: retained"),
		[]byte("zone: inherited"),
	} {
		if !bytes.Contains(configured.YAML(), expected) {
			t.Fatalf("effective configuration omits %q:\n%s", expected, configured.YAML())
		}
	}
	for _, forbidden := range [][]byte{[]byte("shared-a.example"), []byte("shared-private-value"), []byte("dependency-private-value")} {
		if bytes.Contains(configured.YAML(), forbidden) {
			t.Fatalf("effective configuration retained %q:\n%s", forbidden, configured.YAML())
		}
	}
}

func TestApplyOverlayInheritsOmittedFieldsAndRejectsInvalidChanges(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, "\tHost string\n\tSettings struct { Mode string }\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	base := parseOverlayManifest(t, "plystra.yaml", `
http:
  address: ":8080"
  transports: {connect: false, rest: true}
  cors: {allowed_origins: [https://shared.example], allow_credentials: true}
  expose: [email.send/v1]
timeouts: {startup: 45s}
capabilities: {require: [email.send/v1]}
config: {example.com/acme/smtp.New: {host: shared.example, settings: {mode: shared}}}
`)
	inherited, err := applicationmeta.ApplyOverlay(base, parseOverlayManifest(t, "plystra.test.yaml", "{}\n"), lookup)
	if err != nil {
		t.Fatalf("ApplyOverlay(empty): %v", err)
	}
	if address, exists := inherited.HTTPAddress(); !exists || address != ":8080" || inherited.StartupTimeout() != 45*time.Second {
		t.Fatalf("inherited process settings = %q/%t %s", address, exists, inherited.StartupTimeout())
	}
	if transports := inherited.HTTPTransports(); transports != (applicationmeta.HTTPTransports{REST: true}) {
		t.Fatalf("inherited HTTP transports = %#v", transports)
	}
	inheritedCORS, exists := inherited.HTTPCORS()
	if !exists || !reflect.DeepEqual(inheritedCORS.AllowedOrigins, []string{"https://shared.example"}) || !inheritedCORS.AllowCredentials {
		t.Fatalf("inherited HTTPCORS = %#v, %t", inheritedCORS, exists)
	}
	if got := overlayRequirementIDs(inherited); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("inherited requirements = %v", got)
	}

	unknown := parseOverlayManifest(t, "plystra.test.yaml", "config: {example.com/acme/smtp.New: {unknown: private-value}}\n")
	if _, err := applicationmeta.ApplyOverlay(base, unknown, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("unknown field error = %v", err)
	}
	typeMismatch := parseOverlayManifest(t, "plystra.test.yaml", "config: {example.com/acme/smtp.New: {settings: {mode: {name: private-value}}}}\n")
	if _, err := applicationmeta.ApplyOverlay(base, typeMismatch, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !errors.Is(err, applicationmeta.ErrConfigurationValues) || !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("type mismatch error = %v", err)
	}
	aliasChain := parseOverlayManifest(t, "plystra.test.yaml", "capabilities: {aliases: {email.send/v1: reports.read/v1}}\n")
	baseAlias := parseOverlayManifest(t, "plystra.yaml", "capabilities: {aliases: {mail.send/v1: email.send/v1}}\n")
	if _, err := applicationmeta.ApplyOverlay(baseAlias, aliasChain, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !strings.Contains(err.Error(), "Alias chain") {
		t.Fatalf("Alias chain error = %v", err)
	}
	wildcard := parseOverlayManifest(t, "plystra.test.yaml", "http: {cors: {allowed_origins: ['*']}}\n")
	if _, err := applicationmeta.ApplyOverlay(base, wildcard, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !strings.Contains(err.Error(), "wildcard origin") {
		t.Fatalf("credentialed wildcard overlay error = %v", err)
	}
	sparseCredentials, err := applicationmeta.ParseOverlaySource("plystra.test.yaml", []byte("http: {cors: {allow_credentials: false}}\n"))
	if err != nil {
		t.Fatalf("ParseOverlaySource(sparse CORS): %v", err)
	}
	sparseResult, err := applicationmeta.ApplyOverlay(base, sparseCredentials, lookup)
	sparseCORS, exists := sparseResult.HTTPCORS()
	if err != nil || !exists || !reflect.DeepEqual(sparseCORS.AllowedOrigins, []string{"https://shared.example"}) || sparseCORS.AllowCredentials {
		t.Fatalf("sparse credential overlay HTTPCORS = %#v, %t, error %v", sparseCORS, exists, err)
	}
	if _, err := applicationmeta.ApplyOverlay(parseOverlayManifest(t, "plystra.yaml", "{}\n"), sparseCredentials, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !strings.Contains(err.Error(), "allowed_origins is required") {
		t.Fatalf("sparse CORS without root error = %v", err)
	}
	removed, err := applicationmeta.ApplyOverlay(base, parseOverlayManifest(t, "plystra.test.yaml", "http: {cors: null}\n"), lookup)
	if _, exists := removed.HTTPCORS(); err != nil || exists {
		t.Fatalf("removed HTTPCORS exists = %t, error %v", exists, err)
	}
}

func parseOverlayManifest(t *testing.T, source, data string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.ParseSource(source, []byte(data))
	if err != nil {
		t.Fatalf("ParseSource(%s): %v", source, err)
	}
	return manifest
}

func overlayRequirementIDs(manifest applicationmeta.Manifest) []string {
	values := manifest.Requirements()
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func overlayExposureIDs(manifest applicationmeta.Manifest) []string {
	values := manifest.HTTPExposures()
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func overlayProviderStrings(manifest applicationmeta.Manifest) []string {
	values := manifest.ProviderChoices()
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Capability().String() + "=" + value.PluginID()
	}
	return result
}
