package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestParseSourceRetainsSelectedDocumentProvenance(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.ParseSource("plystra.production.yaml", []byte(`
http: {expose: [email.send/v1]}
capabilities:
  require: [email.send/v1]
  use: {email.send/v1: acme.smtp}
  aliases: {mail.send/v1: email.send/v1}
config: {acme.smtp: {host: production.example}}
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
	configured, exists := manifest.Configuration("acme.smtp")
	if !exists || configured.Source() != `plystra.production.yaml config["acme.smtp"]` {
		t.Fatalf("configuration source = %q, %t", configured.Source(), exists)
	}
	if _, err := applicationmeta.ParseSource("bad\nsource", []byte("{}\n")); !errors.Is(err, applicationmeta.ErrInvalidManifest) {
		t.Fatalf("ParseSource(control) error = %v", err)
	}
}

func TestApplyOverlayUsesTypedFieldPrecedenceAndPreservesRemovals(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
host: {type: string}
hosts: {type: array, items: string}
settings: {type: object}
`)
	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{"acme.smtp": schema})
	base := parseOverlayManifest(t, "plystra.yaml", `
http:
  address: ":8080"
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
  acme.smtp:
    host: shared.example
    hosts: [shared-a.example]
    settings:
      keep: retained
      remove: shared-private-value
`)
	overlay := parseOverlayManifest(t, "plystra.production.yaml", `
http:
  address: ":9090"
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
  acme.smtp:
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
http: {expose: [audit.write/v1]}
capabilities:
  require: [audit.write/v1]
  use:
    audit.write/v1: acme.audit
    email.send/v1: acme.smtp.a
  aliases: {old.audit/v1: audit.write/v1}
config:
  acme.smtp:
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
	if got := overlayRequirementIDs(effective); !reflect.DeepEqual(got, []string{"email.send/v1", "reports.read/v1"}) {
		t.Fatalf("effective requirements = %v", got)
	}
	if got := overlayExposureIDs(effective); !reflect.DeepEqual(got, []string{"email.send/v1", "reports.read/v1"}) {
		t.Fatalf("effective exposures = %v", got)
	}
	if got := overlayProviderStrings(effective); !reflect.DeepEqual(got, []string{"email.send/v1=acme.smtp.production"}) {
		t.Fatalf("effective Providers = %v", got)
	}
	configured, exists := effective.Configuration("acme.smtp")
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

	schema := composeSchema(t, "host: {type: string}\nsettings: {type: object}\n")
	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{"acme.smtp": schema})
	base := parseOverlayManifest(t, "plystra.yaml", `
http: {address: ":8080", expose: [email.send/v1]}
timeouts: {startup: 45s}
capabilities: {require: [email.send/v1]}
config: {acme.smtp: {host: shared.example, settings: {mode: shared}}}
`)
	inherited, err := applicationmeta.ApplyOverlay(base, parseOverlayManifest(t, "plystra.test.yaml", "{}\n"), lookup)
	if err != nil {
		t.Fatalf("ApplyOverlay(empty): %v", err)
	}
	if address, exists := inherited.HTTPAddress(); !exists || address != ":8080" || inherited.StartupTimeout() != 45*time.Second {
		t.Fatalf("inherited process settings = %q/%t %s", address, exists, inherited.StartupTimeout())
	}
	if got := overlayRequirementIDs(inherited); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("inherited requirements = %v", got)
	}

	unknown := parseOverlayManifest(t, "plystra.test.yaml", "config: {acme.smtp: {unknown: private-value}}\n")
	if _, err := applicationmeta.ApplyOverlay(base, unknown, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("unknown field error = %v", err)
	}
	typeMismatch := parseOverlayManifest(t, "plystra.test.yaml", "config: {acme.smtp: {settings: {mode: {name: private-value}}}}\n")
	if _, err := applicationmeta.ApplyOverlay(base, typeMismatch, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !errors.Is(err, applicationmeta.ErrInheritedConflict) || !strings.Contains(err.Error(), "types") || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("type mismatch error = %v", err)
	}
	aliasChain := parseOverlayManifest(t, "plystra.test.yaml", "capabilities: {aliases: {email.send/v1: reports.read/v1}}\n")
	baseAlias := parseOverlayManifest(t, "plystra.yaml", "capabilities: {aliases: {mail.send/v1: email.send/v1}}\n")
	if _, err := applicationmeta.ApplyOverlay(baseAlias, aliasChain, lookup); !errors.Is(err, applicationmeta.ErrApplyOverlay) || !strings.Contains(err.Error(), "Alias chain") {
		t.Fatalf("Alias chain error = %v", err)
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
