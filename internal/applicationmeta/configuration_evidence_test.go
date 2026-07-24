package applicationmeta_test

import (
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
