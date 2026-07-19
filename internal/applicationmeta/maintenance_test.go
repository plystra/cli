package applicationmeta_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestMaintainDependencyConfigurationMaterializesBaselineWithoutOverwritingLocalValues(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{
		"acme.smtp": composeSchema(t, "host: {type: string}\nsettings: {type: object}\ntoken: {type: secret}\n"),
	})
	dependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.0.0",
		Manifest: composeManifest(t, `
http:
  cors: {allowed_origins: ['*']}
  expose: [records.read/v1]
capabilities:
  require: [email.send/v1]
  use:
    email.send/v1: acme.smtp
  aliases:
    mail.send/v1: email.send/v1
config:
  acme.smtp:
    host: dependency.example
    settings:
      region: dependency
    token: {env: PRIVATE_DEPENDENCY_TOKEN}
`),
	}}
	current := []byte(`# Shared application configuration.
http:
  address: ":9090" # local process setting
  transports:
    connect: false # local Connect decision
    rest: true # local REST decision
  cors:
    allowed_origins: [https://app.example.com] # local CORS origin
    allow_credentials: true # local CORS credentials
  expose:
    - local.health/v1 # explicit local exposure
capabilities:
  require: []
  use:
    email.send/v1: local.smtp # explicit local Provider
  aliases: {}
config:
  acme.smtp:
    host: local.example # explicit local value
`)

	maintained, err := applicationmeta.MaintainDependencyConfiguration(current, applicationmeta.DependencyBaseline{}, dependencies, lookup)
	if err != nil {
		t.Fatalf("MaintainDependencyConfiguration: %v", err)
	}
	if !maintained.Changed() {
		t.Fatal("initial dependency baseline was not materialized")
	}
	data := maintained.Data()
	for _, expected := range [][]byte{
		[]byte("# Shared application configuration."),
		[]byte("# local process setting"),
		[]byte("# local Connect decision"),
		[]byte("# local REST decision"),
		[]byte("# local CORS origin"),
		[]byte("# local CORS credentials"),
		[]byte("# explicit local exposure"),
		[]byte("# explicit local Provider"),
		[]byte("# explicit local value"),
		[]byte("- records.read/v1"),
		[]byte("email.send/v1"),
		[]byte("mail.send/v1: email.send/v1"),
		[]byte("region: dependency"),
		[]byte("PRIVATE_DEPENDENCY_TOKEN"),
		[]byte("email.send/v1: local.smtp"),
		[]byte("host: local.example"),
	} {
		if !bytes.Contains(data, expected) {
			t.Fatalf("maintained YAML omits %q:\n%s", expected, data)
		}
	}
	manifest := composeManifest(t, string(data))
	if transports := manifest.HTTPTransports(); transports != (applicationmeta.HTTPTransports{REST: true}) {
		t.Fatalf("maintained HTTP transports = %#v", transports)
	}
	cors, exists := manifest.HTTPCORS()
	if !exists || len(cors.AllowedOrigins) != 1 || cors.AllowedOrigins[0] != "https://app.example.com" || !cors.AllowCredentials {
		t.Fatalf("maintained HTTPCORS = %#v, %t", cors, exists)
	}
	composition, err := applicationmeta.Compose(dependencies, manifest, lookup)
	if err != nil {
		t.Fatalf("Compose maintained configuration: %v", err)
	}
	repeated, err := applicationmeta.MaintainDependencyConfiguration(data, composition.DependencyBaseline(), dependencies, lookup)
	if err != nil || repeated.Changed() || !bytes.Equal(repeated.Data(), data) {
		t.Fatalf("repeated maintenance = changed %t, err %v\nfirst: %s\nagain: %s", repeated.Changed(), err, data, repeated.Data())
	}
}

func TestMaintainDependencyConfigurationFollowsChangedBaselineAndPreservesExplicitEdits(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{
		"acme.smtp": composeSchema(t, "host: {type: string}\nsettings: {type: object}\ntoken: {type: secret}\n"),
	})
	oldDependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.0.0",
		Manifest: composeManifest(t, `
http: {expose: [records.old/v1, records.stable/v1]}
capabilities:
  require: [email.send/v1]
  use: {email.send/v1: acme.smtp}
config:
  acme.smtp:
    host: old.example
    settings:
      inherited: old
      retained: old
    token: {env: PRIVATE_OLD_TOKEN}
`),
	}}
	initial, err := applicationmeta.MaintainDependencyConfiguration([]byte("http: {address: ':8080'}\n"), applicationmeta.DependencyBaseline{}, oldDependencies, lookup)
	if err != nil {
		t.Fatalf("materialize old baseline: %v", err)
	}
	oldManifest := composeManifest(t, string(initial.Data()))
	oldComposition, err := applicationmeta.Compose(oldDependencies, oldManifest, lookup)
	if err != nil {
		t.Fatalf("Compose old baseline: %v", err)
	}
	current := bytes.Replace(initial.Data(), []byte("retained: old"), []byte("retained: local # keep this edit"), 1)
	current = bytes.Replace(current, []byte("token:\n      env: PRIVATE_OLD_TOKEN"), []byte("token: null # explicit removal"), 1)
	newDependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v2.0.0",
		Manifest: composeManifest(t, `
http: {expose: [records.new/v1, records.stable/v1]}
capabilities:
  require: [email.send/v1, audit.write/v1]
  use: {email.send/v1: acme.smtp}
config:
  acme.smtp:
    host: new.example
    settings:
      inherited: new
      introduced: new
      retained: dependency-new
    token: {env: PRIVATE_NEW_TOKEN}
`),
	}}

	maintained, err := applicationmeta.MaintainDependencyConfiguration(current, oldComposition.DependencyBaseline(), newDependencies, lookup)
	if err != nil {
		t.Fatalf("maintain changed baseline: %v", err)
	}
	data := maintained.Data()
	for _, expected := range [][]byte{
		[]byte("records.new/v1"),
		[]byte("records.stable/v1"),
		[]byte("audit.write/v1"),
		[]byte("host: new.example"),
		[]byte("inherited: new"),
		[]byte("introduced: new"),
		[]byte("retained: local # keep this edit"),
		[]byte("token: null # explicit removal"),
	} {
		if !bytes.Contains(data, expected) {
			t.Fatalf("updated YAML omits %q:\n%s", expected, data)
		}
	}
	for _, absent := range [][]byte{
		[]byte("records.old/v1"),
		[]byte("old.example"),
		[]byte("PRIVATE_OLD_TOKEN"),
		[]byte("PRIVATE_NEW_TOKEN"),
		[]byte("retained: dependency-new"),
	} {
		if bytes.Contains(data, absent) {
			t.Fatalf("updated YAML retained %q:\n%s", absent, data)
		}
	}
}

func TestMaintainDependencyConfigurationRequiresExplicitRemovalAndConflictResolution(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{
		"acme.smtp": composeSchema(t, "host: {type: string}\n"),
	})
	oldDependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/old",
		ModuleVersion: "v1.0.0",
		Manifest:      composeManifest(t, "capabilities: {use: {email.send/v1: acme.smtp}}\nconfig: {acme.smtp: {host: old.example}}\n"),
	}}
	initial, err := applicationmeta.MaintainDependencyConfiguration([]byte("{}\n"), applicationmeta.DependencyBaseline{}, oldDependencies, lookup)
	if err != nil {
		t.Fatalf("materialize old baseline: %v", err)
	}
	oldComposition, err := applicationmeta.Compose(oldDependencies, composeManifest(t, string(initial.Data())), lookup)
	if err != nil {
		t.Fatalf("Compose old baseline: %v", err)
	}
	withoutHost := bytes.Replace(initial.Data(), []byte("host: old.example"), nil, 1)
	if bytes.Equal(withoutHost, initial.Data()) {
		t.Fatalf("test did not remove inherited host:\n%s", initial.Data())
	}
	_, err = applicationmeta.MaintainDependencyConfiguration(withoutHost, oldComposition.DependencyBaseline(), oldDependencies, lookup)
	if !errors.Is(err, applicationmeta.ErrAmbiguousConfigurationOwnership) || !strings.Contains(err.Error(), `config["acme.smtp"]["host"]`) {
		t.Fatalf("implicit deletion error = %v", err)
	}
	conflicting := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v2.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.smtp}}\nconfig: {acme.smtp: {host: new.example}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v2.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.other}}\n")},
	}
	_, err = applicationmeta.MaintainDependencyConfiguration(initial.Data(), oldComposition.DependencyBaseline(), conflicting, lookup)
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) || !strings.Contains(err.Error(), `capabilities.use["email.send/v1"]`) || strings.Contains(err.Error(), "new.example") {
		t.Fatalf("new inherited conflict = %v", err)
	}
	explicit := bytes.Replace(initial.Data(), []byte("email.send/v1: acme.smtp"), []byte("email.send/v1: acme.other"), 1)
	maintained, err := applicationmeta.MaintainDependencyConfiguration(explicit, oldComposition.DependencyBaseline(), conflicting, lookup)
	if err != nil || !bytes.Contains(maintained.Data(), []byte("acme.other")) {
		t.Fatalf("explicit conflict resolution = %v\n%s", err, maintained.Data())
	}
}

func TestMaintainDependencyConfigurationUsesOverlayOnlyAsConflictResolution(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(nil)
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.smtp}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.local}}\n")},
	}
	overlay := parseOverlayManifest(t, "plystra.production.yaml", "capabilities: {use: {email.send/v1: acme.production}}\n")
	root := []byte("# shared root\n{}\n")

	maintained, err := applicationmeta.MaintainDependencyConfigurationWithOverlay(root, overlay, applicationmeta.DependencyBaseline{}, dependencies, lookup)
	if err != nil {
		t.Fatalf("MaintainDependencyConfigurationWithOverlay: %v", err)
	}
	if maintained.Changed() || !bytes.Equal(maintained.Data(), root) || bytes.Contains(maintained.Data(), []byte("acme.production")) {
		t.Fatalf("overlay decision was materialized into root:\n%s", maintained.Data())
	}
	composition, err := applicationmeta.Compose(dependencies, overlay, lookup)
	if err != nil {
		t.Fatalf("Compose resolved overlay: %v", err)
	}
	repeated, err := applicationmeta.MaintainDependencyConfigurationWithOverlay(root, overlay, composition.DependencyBaseline(), dependencies, lookup)
	if err != nil || repeated.Changed() || !bytes.Equal(repeated.Data(), root) {
		t.Fatalf("repeat overlay maintenance = changed %t, error %v\n%s", repeated.Changed(), err, repeated.Data())
	}
	_, err = applicationmeta.MaintainDependencyConfiguration(root, composition.DependencyBaseline(), dependencies, lookup)
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) || !strings.Contains(err.Error(), `capabilities.use["email.send/v1"]`) {
		t.Fatalf("unselected overlay conflict error = %v", err)
	}
}

func TestRestoreDependencyBaselineRejectsTampering(t *testing.T) {
	t.Parallel()

	composition, err := applicationmeta.Compose(nil, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("Compose empty baseline: %v", err)
	}
	baseline := composition.DependencyBaseline()
	if !baseline.Valid() || baseline.Digest() == "" || baseline.Records() == nil {
		t.Fatalf("empty baseline = %#v", baseline)
	}
	restored, err := applicationmeta.RestoreDependencyBaseline(baseline.Digest(), baseline.Records())
	if err != nil || !restored.Valid() || restored.Digest() != baseline.Digest() {
		t.Fatalf("RestoreDependencyBaseline = %#v, %v", restored, err)
	}
	_, err = applicationmeta.RestoreDependencyBaseline(strings.Repeat("0", len(baseline.Digest())), baseline.Records())
	if !errors.Is(err, applicationmeta.ErrDependencyBaseline) {
		t.Fatalf("tampered baseline error = %v", err)
	}
}
