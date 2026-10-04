package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"go.yaml.in/yaml/v3"
)

func TestTemplateOrderOwnsExactKeyPrecedenceAndHistory(t *testing.T) {
	oldest := applicationmeta.Dependency{ModulePath: "example.com/z-oldest", ModuleVersion: "v1.2.3", Manifest: composeManifest(t, `
interfaces:
  require: [audit.write/v1, email.send/v1]
  use: {email.send/v1: example.com/old/mail.New}
  policies: {email.send/v1: {timeout: 5s, retry: {eligibility: replay_safe}}}
http:
  address: ":9999"
  expose: {email.send/v1: {transport: connect}}
  cors: {allowed_origins: [https://old.example], allow_credentials: true}
timeouts: {startup: 9s}
`)}
	nearest := applicationmeta.Dependency{ModulePath: "example.com/a-nearest", ModuleVersion: "v2.3.4", Manifest: composeManifest(t, `
template: example.com/z-oldest
interfaces:
  require: {remove: [audit.write/v1], add: [job.queue/v1]}
  use: {email.send/v1: example.com/near/mail.New}
  policies: {email.send/v1: {timeout: 2s}}
http:
  expose: {email.send/v1: {$remove: true}, job.queue/v1: {transport: connect}}
  cors: {allowed_origins: [https://near.example]}
`)}
	current := composeManifest(t, `template: example.com/a-nearest
interfaces:
  require: {remove: [job.queue/v1]}
http: {address: ":8080", cors: {allow_credentials: {$remove: true}}}
timeouts: {startup: 3s}
`)
	current, err := applicationmeta.WithProjectModule(current, "local-app")
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{oldest, nearest}, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	result := composition.Manifest()
	if !reflect.DeepEqual(interfaceRequirementIDs(result.InterfaceRequirements()), []string{"email.send/v1"}) || result.ImplementationChoices()[0].Constructor().String() != "example.com/near/mail.New" || result.InterfacePolicies()[0].RetryMaxAttempts() != 1 || result.InterfacePolicies()[0].Timeout().String() != "2s" {
		t.Fatal("nearest exact-key replacement did not win")
	}
	if result.Template() != current.Template() || result.TemplateSource() != current.TemplateSource() || result.StartupTimeout().String() != "3s" {
		t.Fatal("lost current metadata/process ownership")
	}
	if address, ok := result.HTTPAddress(); !ok || address != ":8080" {
		t.Fatal("template supplied listener address")
	}
	exposures := result.HTTPExposures()
	if len(exposures) != 1 || exposures[0].ID().String() != "job.queue/v1" || exposures[0].DeclarationSource().ModulePath() != nearest.ModulePath || !strings.HasPrefix(exposures[0].Source(), nearest.ModulePath+"@v2.3.4/plystra.yaml ") {
		t.Fatalf("inherited exposure source = %#v", exposures)
	}
	cors, exists := result.HTTPCORS()
	if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://near.example"}) || cors.AllowCredentials {
		t.Fatal("CORS did not compose by declared fields")
	}
	layers := composition.TemplateLayers()
	if len(layers) != 2 || layers[0].ModulePath != oldest.ModulePath || layers[1].ModulePath != nearest.ModulePath || layers[0].Source != "plystra.yaml" {
		t.Fatal("history lost linear order")
	}
	removed := false
	for _, decision := range layers[1].Decisions {
		removed = removed || decision.Path() == `http.expose["email.send/v1"]` && decision.Removed()
		if decision.Path() == "http.address" || decision.Path() == "timeouts.startup" {
			t.Fatal("excluded process declaration entered template history")
		}
	}
	if !removed {
		t.Fatal("suppressed exposure history disappeared")
	}
	layers[0].Decisions[0] = applicationmeta.ConfigurationDecision{}
	layers[0].ModulePath = "changed"
	if composition.TemplateLayers()[0].ModulePath != oldest.ModulePath || composition.TemplateLayers()[0].Decisions[0].Path() == "" {
		t.Fatal("mutable template history")
	}
	for _, record := range composition.ResolutionSources() {
		if strings.Contains(record.Path(), "job.queue") && strings.HasPrefix(record.Path(), "interfaces.require") || strings.Contains(record.Path(), "email.send") && strings.HasPrefix(record.Path(), "http.expose") {
			t.Fatal("removed declaration remained an effective source")
		}
	}
	reversed, err := applicationmeta.Compose([]applicationmeta.Dependency{nearest, oldest}, current, composeSchemaLookup(nil))
	if err != nil || reversed.DependencyDigest() == composition.DependencyDigest() || reversed.Manifest().ImplementationChoices()[0].Constructor().String() != "example.com/old/mail.New" {
		t.Fatalf("ancestry order was sorted away: %v", err)
	}
	restored, err := applicationmeta.RestoreDependencyBaseline(composition.DependencyDigest(), composition.DependencyBaseline().Records())
	if err != nil || !restored.Valid() {
		t.Fatalf("ordered baseline did not round trip: %v", err)
	}
}

func TestTemplateCompositionRetainsNestedRemovalBarriersThroughOverlay(t *testing.T) {
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Settings struct { First string; Second string }; Pointer *struct { First string; Second string }; Mapping map[string]string; List []string; Array [1]string"),
	})
	config := func(fields string) applicationmeta.Manifest {
		return composeManifest(t, "config: {"+constructorConfigurationSymbol+": "+fields+"}")
	}
	oldest := applicationmeta.Dependency{ModulePath: "example.com/oldest", Manifest: config("{settings: {first: inherited, second: inherited}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited]}")}
	for _, tc := range []struct{ name, middle, root, overlay, want string }{
		{"struct", "{}", "{}", "{settings: {first: current}}", "{settings: {first: current, second: inherited}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited]}"},
		{"atomic", "{}", "{}", "{pointer: {first: current}, mapping: {}, list: [], array: [current]}", "{settings: {first: inherited, second: inherited}, pointer: {first: current}, mapping: {}, list: [], array: [current]}"},
		{"field barrier", "{}", "{settings: {first: {$remove: true}}}", "{settings: {second: current}}", "{settings: {second: current}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited]}"},
		{"object barrier", "{}", "{settings: {$remove: true}}", "{settings: {second: current}}", "{settings: {second: current}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited]}"},
		{"ancestor object barrier", "{settings: {$remove: true}}", "{}", "{settings: {second: current}}", "{settings: {second: current}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited]}"},
		{"constructor barrier", "{}", "{$remove: true}", "{settings: {second: current}}", "{settings: {second: current}}"},
		{"ancestor constructor barrier", "{$remove: true}", "{}", "{settings: {second: current}}", "{settings: {second: current}}"},
		{"nullable", "{}", "{}", "{pointer: null, mapping: null, list: null}", "{settings: {first: inherited, second: inherited}, pointer: null, mapping: null, list: null, array: [inherited]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, upper := config(tc.root), config(tc.overlay)
			selected, err := applicationmeta.ApplyOverlay(root, upper, lookup)
			if err != nil {
				t.Fatal(err)
			}
			middle := applicationmeta.Dependency{ModulePath: "example.com/near", Manifest: config(tc.middle)}
			composition, err := applicationmeta.Compose([]applicationmeta.Dependency{oldest, middle}, selected, lookup)
			if err != nil {
				t.Fatal(err)
			}
			value, exists := composition.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
			if !exists {
				t.Fatal("configuration disappeared")
			}
			var got, want any
			if err := yaml.Unmarshal(value.YAML(), &got); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ordered typed values = %v, want %v", got, want)
			}
			if bytes.Contains(value.YAML(), []byte("$remove")) {
				t.Fatal("tombstone delivered as value")
			}
		})
	}
}

func TestTemplateCompositionRejectsDuplicateIdentitiesWithoutMutation(t *testing.T) {
	dependency := applicationmeta.Dependency{ModulePath: "example.com/base", Manifest: composeManifest(t, "{}")}
	current, err := applicationmeta.WithProjectModule(composeManifest(t, "{}"), "example.com/current")
	if err != nil {
		t.Fatal(err)
	}
	for _, deps := range [][]applicationmeta.Dependency{
		{dependency, dependency},
		{{ModulePath: "example.com/current", Manifest: dependency.Manifest}},
		{{ModulePath: "my-project/local", Manifest: dependency.Manifest}},
		{{ModulePath: "example.com/base", Manifest: current}},
	} {
		if _, err := applicationmeta.Compose(deps, current, composeSchemaLookup(nil)); !errors.Is(err, applicationmeta.ErrCompose) {
			t.Fatalf("invalid identity accepted: %v", err)
		}
	}
}

func TestShortWorkspaceProjectTemplateIdentity(t *testing.T) {
	data, err := applicationmeta.SetTemplate([]byte("{}\n"), "base-app")
	if err != nil {
		t.Fatal(err)
	}
	for _, parse := range []func(string, []byte) (applicationmeta.Manifest, error){
		applicationmeta.ParseSource, applicationmeta.ParseTemplateSource, applicationmeta.ParseRootMetadataSource,
	} {
		manifest, err := parse("plystra.yaml", data)
		if err != nil || manifest.Template() != "base-app" {
			t.Fatalf("short workspace relationship: %v", err)
		}
	}
	current, err := applicationmeta.WithProjectModule(composeManifest(t, string(data)), "local-app")
	if err != nil {
		t.Fatal(err)
	}
	base, err := applicationmeta.ParseTemplateSource("plystra.yaml", []byte("interfaces: {require: [local.health/v1]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	dependency := applicationmeta.Dependency{ModulePath: "base-app", Manifest: base}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{dependency}, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if composition.Manifest().Template() != "base-app" || composition.TemplateLayers()[0].ModulePath != "base-app" || !reflect.DeepEqual(interfaceRequirementIDs(composition.Manifest().InterfaceRequirements()), []string{"local.health/v1"}) {
		t.Fatal("short workspace template lost identity or baseline")
	}
	sources := composition.ResolutionSources()
	if len(sources) != 2 {
		t.Fatalf("short workspace provenance = %#v", sources)
	}
	for _, record := range sources {
		if len(record.Sources()) != 1 || !strings.HasPrefix(record.Sources()[0], "base-app@workspace/plystra.yaml interfaces.require") {
			t.Fatalf("short workspace provenance = %#v", record)
		}
	}
	for _, deps := range [][]applicationmeta.Dependency{
		{dependency, dependency},
		{{ModulePath: "local-app", Manifest: base}},
	} {
		if _, err := applicationmeta.Compose(deps, current, composeSchemaLookup(nil)); !errors.Is(err, applicationmeta.ErrCompose) {
			t.Fatalf("short workspace cycle accepted: %v", err)
		}
	}
}

func TestTemplateCORSExclusionsAndDeferredRequiredness(t *testing.T) {
	lookup := composeSchemaLookup(nil)
	base := applicationmeta.Dependency{ModulePath: "example.com/base", Manifest: composeManifest(t, "http: {cors: {allowed_origins: [https://base.example], allow_credentials: true}}")}
	for _, tc := range []struct {
		current                     string
		valid, present, credentials bool
	}{
		{"http: {cors: {allow_credentials: true}}", true, true, true},
		{"http: {cors: {allow_credentials: false}}", true, true, false},
		{"http: {cors: {$remove: true}}", true, false, false},
		{"http: {cors: {allow_credentials: {$remove: true}}}", true, true, false},
		{"http: {cors: {allowed_origins: {$remove: true}}}", false, false, false},
	} {
		c, err := applicationmeta.Compose([]applicationmeta.Dependency{base}, composeManifest(t, tc.current), lookup)
		if (err == nil) != tc.valid {
			t.Fatalf("CORS %s: %v", tc.current, err)
		}
		if err == nil {
			cors, present := c.Manifest().HTTPCORS()
			if present != tc.present || cors.AllowCredentials != tc.credentials {
				t.Fatal("wrong CORS effective value")
			}
		}
	}
	for _, invalid := range []string{"http: {cors: null}", "http: {cors: {$remove: false}}", "http: {cors: {$remove: true, allow_credentials: false}}", "http: {cors: {allow_credentials: null}}"} {
		if _, err := applicationmeta.Parse([]byte(invalid)); err == nil {
			t.Fatal("ambiguous CORS removal accepted")
		}
	}
	partial := composeManifest(t, "http: {cors: {allow_credentials: false}}")
	if _, err := applicationmeta.Compose(nil, partial, lookup); err == nil {
		t.Fatal("final CORS requiredness was not checked")
	}
}

func TestTemplateCORSEffectiveInvariantAfterEveryLayer(t *testing.T) {
	lookup := composeSchemaLookup(nil)
	for _, tc := range []struct {
		name, oldest, nearest, root, overlay string
		valid                                bool
	}{
		{"origins inherited", "http: {cors: {allowed_origins: [https://old.example]}}", "http: {cors: {allowed_origins: [https://near.example]}}", "http: {cors: {allow_credentials: true}}", "{}", true},
		{"template repaired by origins", "http: {cors: {allowed_origins: ['*'], allow_credentials: true}}", "http: {cors: {allowed_origins: [https://near.example]}}", "{}", "{}", true},
		{"root repaired by credentials", "{}", "{}", "http: {cors: {allowed_origins: ['*'], allow_credentials: true}}", "http: {cors: {allow_credentials: false}}", true},
		{"root repaired by origins", "{}", "{}", "http: {cors: {allowed_origins: ['*'], allow_credentials: true}}", "http: {cors: {allowed_origins: [https://app.example]}}", true},
		{"template removed", "http: {cors: {allowed_origins: ['*'], allow_credentials: true}}", "{}", "http: {cors: {$remove: true}}", "{}", true},
		{"final wildcard invalid", "http: {cors: {allowed_origins: ['*']}}", "{}", "http: {cors: {allow_credentials: true}}", "{}", false},
		{"final origins absent", "http: {cors: {allow_credentials: true}}", "{}", "{}", "{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldest, err := applicationmeta.ParseTemplateSource("plystra.yaml", []byte(tc.oldest))
			if err != nil {
				t.Fatal(err)
			}
			nearest, err := applicationmeta.ParseTemplateSource("plystra.yaml", []byte(tc.nearest))
			if err != nil {
				t.Fatal(err)
			}
			overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(tc.overlay))
			if err != nil {
				t.Fatal(err)
			}
			selected, err := applicationmeta.ApplyOverlay(composeManifest(t, tc.root), overlay, lookup)
			if err != nil {
				t.Fatalf("partial current CORS rejected: %v", err)
			}
			_, err = applicationmeta.Compose([]applicationmeta.Dependency{{ModulePath: "oldest", Manifest: oldest}, {ModulePath: "nearest", Manifest: nearest}}, selected, lookup)
			if (err == nil) != tc.valid {
				t.Fatalf("final CORS validity: %v", err)
			}
		})
	}
	for _, input := range []string{"http: {cors: {allowed_origins: []}}", "http: {cors: {allowed_origins: [true]}}", "http: {cors: {allowed_origins: [https://app.example/path]}}", "http: {cors: {allow_credentials: 1}}"} {
		if _, err := applicationmeta.ParseTemplateSource("plystra.yaml", []byte(input)); err == nil {
			t.Fatal("invalid individual CORS value accepted")
		}
	}
}

func TestTemplateMaintenanceNeverMaterializesOrInfersOwnership(t *testing.T) {
	input := []byte("# retain formatting\ninterfaces: {use: {email.send/v1: example.com/local/mail.New}}\n")
	dependency := applicationmeta.Dependency{ModulePath: "example.com/base", Manifest: composeManifest(t, "interfaces: {require: [email.send/v1], use: {email.send/v1: example.com/local/mail.New}}")}
	prior, err := applicationmeta.Compose([]applicationmeta.Dependency{dependency}, composeManifest(t, "{}"), composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	dependency.Manifest = composeManifest(t, "interfaces: {require: [changed.read/v1], use: {email.send/v1: example.com/changed/mail.New}}")
	for _, data := range [][]byte{input, []byte("{}\n"), []byte("interfaces: {use: {email.send/v1: {$remove: true}}}\n")} {
		result, err := applicationmeta.MaintainDependencyConfiguration(data, prior.DependencyBaseline(), nil, []applicationmeta.Dependency{dependency}, composeSchemaLookup(nil))
		if err != nil || result.Changed() || !bytes.Equal(result.Data(), data) {
			t.Fatalf("template update rewrote delta: %v", err)
		}
	}
}

func TestTemplateHistoryNeverGroupsByPrivateEquality(t *testing.T) {
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{constructorConfigurationSymbol: composeSchema(t, "Password configuration.Secret; Values map[string]string")})
	var baseline applicationmeta.DependencyBaseline
	var history []applicationmeta.TemplateLayer
	for _, private := range []string{"{password: {env: PRIVATE_A}, values: {key: PRIVATE_A}}", "{password: {file: /PRIVATE_B}, values: {other: PRIVATE_B, more: PRIVATE_C}}", "{password: {env: PRIVATE_A}, values: null}"} {
		deps := []applicationmeta.Dependency{
			{ModulePath: "example.com/old", Manifest: composeManifest(t, fmt.Sprintf("config: {%s: {password: {env: PRIVATE_A}, values: {key: PRIVATE_A}}}", constructorConfigurationSymbol))},
			{ModulePath: "example.com/near", Manifest: composeManifest(t, fmt.Sprintf("config: {%s: %s}", constructorConfigurationSymbol, private))},
		}
		composition, err := applicationmeta.Compose(deps, composeManifest(t, "{}"), lookup)
		if err != nil {
			t.Fatal(err)
		}
		if baseline.Valid() && (!reflect.DeepEqual(baseline, composition.DependencyBaseline()) || !reflect.DeepEqual(history, composition.TemplateLayers())) {
			t.Fatal("private equality changed public history")
		}
		baseline, history = composition.DependencyBaseline(), composition.TemplateLayers()
		if strings.Contains(fmt.Sprint(history, provenanceStrings(composition.Provenance()), provenanceStrings(composition.ResolutionSources())), "PRIVATE_") {
			t.Fatal("private value leaked")
		}
	}
}
