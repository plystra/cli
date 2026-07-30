package applicationmeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestConfigurationDecisionsAreTypedDeterministicAndRedacted(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
	Enabled bool
	Host string
	Password configuration.Secret
	Settings struct { Nested string }
	Targets []string
`)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	data := []byte(`
http:
  address: ":9123"
  transports: {connect: false, rest: true}
  cors:
    allowed_origins: [https://private.example]
    allow_credentials: true
  expose: [email.send/v1]
timeouts: {startup: 9s}
capabilities:
  require: [audit.write/v1]
  use: {email.send/v1: acme.smtp}
  aliases: {mail.send/v1: email.send/v1}
config:
  example.com/acme/smtp.New:
    enabled: true
    host: private.smtp.example
    password: {env: PRIVATE_SMTP_PASSWORD}
    settings: {nested: private-setting}
    targets: [private-a.example, private-b.example]
`)
	manifest, err := applicationmeta.ParseSource("deploy/customer config.yaml", data)
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	first, err := applicationmeta.ConfigurationDecisions(manifest, lookup)
	if err != nil {
		t.Fatalf("ConfigurationDecisions: %v", err)
	}
	second, err := applicationmeta.ConfigurationDecisions(manifest, lookup)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated decisions differ: %#v, %#v, %v", first, second, err)
	}

	wantSummaries := map[string]applicationmeta.ConfigurationDecisionSummary{
		`capabilities.aliases["mail.send/v1"]`:            applicationmeta.ConfigurationSummaryAlias,
		`capabilities.require["audit.write/v1"]`:          applicationmeta.ConfigurationSummaryCapability,
		`capabilities.use["email.send/v1"]`:               applicationmeta.ConfigurationSummaryProvider,
		`config["example.com/acme/smtp.New"]`:             applicationmeta.ConfigurationSummaryObject,
		`config["example.com/acme/smtp.New"]["enabled"]`:  applicationmeta.ConfigurationSummaryBoolean,
		`config["example.com/acme/smtp.New"]["host"]`:     applicationmeta.ConfigurationSummaryString,
		`config["example.com/acme/smtp.New"]["password"]`: applicationmeta.ConfigurationSummarySecret,
		`config["example.com/acme/smtp.New"]["settings"]`: applicationmeta.ConfigurationSummaryObject,
		`config["example.com/acme/smtp.New"]["targets"]`:  applicationmeta.ConfigurationSummaryArray,
		`http.address`:                 applicationmeta.ConfigurationSummaryString,
		`http.cors`:                    applicationmeta.ConfigurationSummaryObject,
		`http.cors.allow_credentials`:  applicationmeta.ConfigurationSummaryBoolean,
		`http.cors.allowed_origins`:    applicationmeta.ConfigurationSummaryArray,
		`http.expose["email.send/v1"]`: applicationmeta.ConfigurationSummaryInterface,
		`http.transports.connect`:      applicationmeta.ConfigurationSummaryBoolean,
		`http.transports.rest`:         applicationmeta.ConfigurationSummaryBoolean,
		`timeouts.startup`:             applicationmeta.ConfigurationSummaryDuration,
	}
	seen := make(map[string]struct{}, len(first))
	var bounded strings.Builder
	previous := ""
	for _, decision := range first {
		if decision.Path() <= previous {
			t.Fatalf("decisions are not path ordered: %q then %q", previous, decision.Path())
		}
		previous = decision.Path()
		want, exists := wantSummaries[decision.Path()]
		if !exists {
			continue
		}
		seen[decision.Path()] = struct{}{}
		if decision.Summary() != want || decision.Removed() || decision.Source() != "deploy/customer config.yaml" || !strings.HasPrefix(decision.Digest(), "sha256:") {
			t.Fatalf("decision %s = summary %q removed %t source %q digest %q", decision.Path(), decision.Summary(), decision.Removed(), decision.Source(), decision.Digest())
		}
		bounded.WriteString(decision.Path())
		bounded.WriteString(string(decision.Summary()))
		bounded.WriteString(decision.Source())
		bounded.WriteString(decision.Digest())
	}
	if len(seen) != len(wantSummaries) {
		t.Fatalf("typed decisions = %v; want paths %v", seen, wantSummaries)
	}
	for _, forbidden := range []string{
		":9123",
		"https://private.example",
		"private.smtp.example",
		"PRIVATE_SMTP_PASSWORD",
		"private-setting",
		"private-a.example",
	} {
		if strings.Contains(bounded.String(), forbidden) {
			t.Fatalf("configuration evidence exposed %q: %s", forbidden, bounded.String())
		}
	}
}

func TestConfigurationDecisionsRecordTypedRemovals(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, "\tHost string\n\tSettings struct { Mode string }\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	manifest, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`
http: {address: null, cors: null}
capabilities:
  require: {remove: [audit.write/v1]}
  use: {email.send/v1: null}
  aliases: {mail.send/v1: null}
config:
  example.com/acme/smtp.New:
    host: null
    settings: null
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource: %v", err)
	}
	decisions, err := applicationmeta.ConfigurationDecisions(manifest, lookup)
	if err != nil {
		t.Fatalf("ConfigurationDecisions: %v", err)
	}
	want := map[string]bool{
		`capabilities.aliases["mail.send/v1"]`:            true,
		`capabilities.require["audit.write/v1"]`:          true,
		`capabilities.use["email.send/v1"]`:               true,
		`config["example.com/acme/smtp.New"]["host"]`:     true,
		`config["example.com/acme/smtp.New"]["settings"]`: true,
		`http.address`: true,
		`http.cors`:    true,
	}
	for _, decision := range decisions {
		if !want[decision.Path()] {
			continue
		}
		if !decision.Removed() || decision.Summary() != applicationmeta.ConfigurationSummaryRemoval || decision.Source() != "plystra.production.yaml" {
			t.Fatalf("removal %s = %#v", decision.Path(), decision)
		}
		delete(want, decision.Path())
	}
	if len(want) != 0 {
		t.Fatalf("missing removal decisions: %v", want)
	}
}

func TestConfigurationDecisionsPreserveProcessOwnershipThroughComposition(t *testing.T) {
	t.Parallel()

	current, err := applicationmeta.ParseSource("deploy/customer.yaml", []byte("http: {address: ':9123'}\ntimeouts: {startup: 17s}\n"))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	composition, err := applicationmeta.Compose(nil, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	decisions, err := applicationmeta.ConfigurationDecisions(composition.Manifest(), composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("ConfigurationDecisions: %v", err)
	}
	want := map[string]applicationmeta.ConfigurationDecisionSummary{
		"http.address":     applicationmeta.ConfigurationSummaryString,
		"timeouts.startup": applicationmeta.ConfigurationSummaryDuration,
	}
	for _, decision := range decisions {
		summary, exists := want[decision.Path()]
		if !exists {
			continue
		}
		if decision.Summary() != summary || decision.Source() != "deploy/customer.yaml" {
			t.Fatalf("composed process decision %s = %#v", decision.Path(), decision)
		}
		delete(want, decision.Path())
	}
	if len(want) != 0 {
		t.Fatalf("composed process decisions are missing: %v", want)
	}
}

func TestConfigurationLayerDigestUsesTypedNormalizedDecisions(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
	Delay time.Duration
	Endpoint string
	Targets []string
`)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/service.New": schema})
	leftData := []byte(`# presentation and set order are not semantic
interfaces:
  require: [email.send/v1, audit.write/v1]
  policies:
    email.send/v1: {timeout: 5s}
http:
  address: ":8080"
  transports: {connect: true, rest: false}
  cors:
    allowed_origins: [https://B.example:443, https://a.example, https://a.example:443]
    allow_credentials: false
  expose: [email.send/v1, audit.write/v1]
timeouts: {startup: 5s}
config:
  example.com/acme/service.New:
    delay: 5s
    endpoint: service.example
    targets: [primary, secondary]
`)
	left, err := applicationmeta.ParseSource("plystra.yaml", leftData)
	if err != nil {
		t.Fatalf("ParseSource(left): %v", err)
	}
	right, err := applicationmeta.ParseSource("deploy/equivalent.yaml", []byte(`config:
  example.com/acme/service.New: {targets: [primary, secondary], endpoint: "service.example", delay: 5000ms}
timeouts:
  startup: 5000ms
http:
  expose:
    - audit.write/v1
    - email.send/v1
  cors: {allow_credentials: false, allowed_origins: [https://a.example:443, https://b.example]}
  transports:
    rest: false
    connect: true
  address: ':8080'
interfaces:
  policies:
    email.send/v1:
      timeout: 5000ms
  require: [audit.write/v1, email.send/v1]
`))
	if err != nil {
		t.Fatalf("ParseSource(right): %v", err)
	}
	leftDigest, err := applicationmeta.ConfigurationLayerDigest(left, lookup)
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest(left): %v", err)
	}
	rightDigest, err := applicationmeta.ConfigurationLayerDigest(right, lookup)
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest(right): %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalent typed layer digests differ: %q != %q", leftDigest, rightDigest)
	}

	changed, err := applicationmeta.ParseSource("plystra.yaml", []byte(strings.Replace(
		string(leftData),
		"delay: 5s",
		"delay: 5001ms",
		1,
	)))
	if err != nil {
		t.Fatalf("ParseSource(changed): %v", err)
	}
	changedDigest, err := applicationmeta.ConfigurationLayerDigest(changed, lookup)
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest(changed): %v", err)
	}
	if changedDigest == leftDigest {
		t.Fatal("changed typed configuration value did not alter the layer digest")
	}
}

func TestConfigurationLayerDigestStructurallyHashesExcludedUnresolvedConfiguration(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(nil)
	left, err := applicationmeta.ParseSource("plystra.yaml", []byte(`config:
  example.com/excluded/root.New:
    enabled: TRUE
    nested: {second: 2, first: 1}
    ordered: [primary, secondary]
`))
	if err != nil {
		t.Fatalf("ParseSource(left): %v", err)
	}
	right, err := applicationmeta.ParseSource("deploy/customer.yaml", []byte(`# presentation-only differences
config:
  example.com/excluded/root.New: {ordered: [primary, secondary], nested: {first: 1, second: 2}, enabled: true}
`))
	if err != nil {
		t.Fatalf("ParseSource(right): %v", err)
	}
	if _, err := applicationmeta.ConfigurationDecisions(left, lookup); !errors.Is(err, applicationmeta.ErrConfigurationSchema) {
		t.Fatalf("ConfigurationDecisions(left) error = %v, want unavailable schema", err)
	}
	leftDigest, err := applicationmeta.ConfigurationLayerDigest(left, lookup)
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest(left): %v", err)
	}
	rightDigest, err := applicationmeta.ConfigurationLayerDigest(right, lookup)
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest(right): %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalent excluded layer digests differ: %q != %q", leftDigest, rightDigest)
	}

	changed, err := applicationmeta.ParseSource("plystra.yaml", []byte(`config:
  example.com/excluded/root.New:
    enabled: true
    nested: {first: 1, second: 2}
    ordered: [secondary, primary]
`))
	if err != nil {
		t.Fatalf("ParseSource(changed): %v", err)
	}
	changedDigest, err := applicationmeta.ConfigurationLayerDigest(changed, lookup)
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest(changed): %v", err)
	}
	if changedDigest == leftDigest {
		t.Fatal("changed ordered value did not alter the excluded layer digest")
	}
}
