package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
)

func TestParseNormalizesProviderInputsAndMultipleAliases(t *testing.T) {
	t.Parallel()

	data := []byte(`http:
  address: ":8080"
  expose: []
timeouts:
  startup: 2m
capabilities:
  require: [kernel.info/v1, email.send/v1]
  use:
    email.send/v1: acme.email.smtp
    authz.check/v1: plystra.authz.rbac.default
  aliases:
    authn.sign-in/v1:
      target: authn.login.password/v1
    account.sign-in/v1:
      target: authn.login.password/v1
      expose:
        go: true
        http: false
        javascript: false
      deprecated:
        message: Use authn.login/v1 instead.
    authn.login/v1: authn.login.password/v1
config: {}
`)
	manifest, err := applicationmeta.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if manifest.StartupTimeout() != 2*time.Minute {
		t.Fatalf("StartupTimeout = %s", manifest.StartupTimeout())
	}
	requirements := manifest.Requirements()
	if got := requirementStrings(requirements); !slices.Equal(got, []string{
		`email.send/v1@plystra.yaml capabilities.require["email.send/v1"]`,
		`kernel.info/v1@plystra.yaml capabilities.require["kernel.info/v1"]`,
	}) {
		t.Fatalf("Requirements = %v", got)
	}
	choices := manifest.ProviderChoices()
	if got := providerChoiceStrings(choices); !slices.Equal(got, []string{
		`authz.check/v1->plystra.authz.rbac.default@plystra.yaml capabilities.use["authz.check/v1"]`,
		`email.send/v1->acme.email.smtp@plystra.yaml capabilities.use["email.send/v1"]`,
	}) {
		t.Fatalf("ProviderChoices = %v", got)
	}
	requirements[0] = applicationmeta.CapabilityRequirement{}
	choices[0] = applicationmeta.ProviderChoice{}
	if manifest.Requirements()[0].ID().String() != "email.send/v1" || manifest.ProviderChoices()[0].Capability().String() != "authz.check/v1" {
		t.Fatal("Manifest exposed mutable provider-resolution input storage")
	}
	aliases := manifest.Aliases()
	if got := aliasStrings(aliases); !slices.Equal(got, []string{
		"account.sign-in/v1->authn.login.password/v1",
		"authn.login/v1->authn.login.password/v1",
		"authn.sign-in/v1->authn.login.password/v1",
	}) {
		t.Fatalf("Aliases = %v", got)
	}
	exposure, explicit := aliases[0].Exposure()
	if !explicit || !exposure.Go || exposure.HTTP || exposure.JavaScript || aliases[0].Deprecated() != "Use authn.login/v1 instead." {
		t.Fatalf("expanded Alias = %#v, exposure %#v, explicit %v", aliases[0], exposure, explicit)
	}
	if aliases[0].Source() != `plystra.yaml capabilities.aliases["account.sign-in/v1"]` {
		t.Fatalf("Source = %q", aliases[0].Source())
	}
	for _, alias := range aliases[1:] {
		if exposure, explicit := alias.Exposure(); explicit || exposure != (generation.Exposure{}) {
			t.Fatalf("inherited Alias exposure = %#v, %v", exposure, explicit)
		}
	}
	aliases[0] = applicationmeta.Alias{}
	if manifest.Aliases()[0].ID().String() != "account.sign-in/v1" {
		t.Fatal("Manifest exposed mutable Alias storage")
	}

	reordered := []byte(`config: {}
capabilities:
  aliases:
    authn.login/v1: authn.login.password/v1
    account.sign-in/v1:
      deprecated: {message: Use authn.login/v1 instead.}
      expose: {javascript: false, http: false, go: true}
      target: authn.login.password/v1
    authn.sign-in/v1: {target: authn.login.password/v1}
  use: {authz.check/v1: plystra.authz.rbac.default, email.send/v1: acme.email.smtp}
  require: [email.send/v1, kernel.info/v1]
timeouts: {startup: 2m}
http: {expose: [], address: ":8080"}
`)
	second, err := applicationmeta.Parse(reordered)
	if err != nil || second.StartupTimeout() != manifest.StartupTimeout() || !slices.Equal(aliasStrings(second.Aliases()), aliasStrings(manifest.Aliases())) || !slices.Equal(requirementStrings(second.Requirements()), requirementStrings(manifest.Requirements())) || !slices.Equal(providerChoiceStrings(second.ProviderChoices()), providerChoiceStrings(manifest.ProviderChoices())) {
		t.Fatalf("reordered Parse = %v, %v", aliasStrings(second.Aliases()), err)
	}
	for index, alias := range second.Aliases() {
		firstExposure, firstExplicit := manifest.Aliases()[index].Exposure()
		secondExposure, secondExplicit := alias.Exposure()
		if alias.Deprecated() != manifest.Aliases()[index].Deprecated() || firstExposure != secondExposure || firstExplicit != secondExplicit {
			t.Fatalf("reordered Alias %d differs: %#v, %#v", index, manifest.Aliases()[index], alias)
		}
	}
}

func TestParseNormalizesHTTPAddressAndCanonicalExposure(t *testing.T) {
	t.Parallel()

	data := []byte(`http:
  expose:
    - customer.profile.get/v1
    - kernel.health/v1
    - authn.login.password/v1
  address: ":8080"
`)
	manifest, err := applicationmeta.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	address, explicit := manifest.HTTPAddress()
	if !explicit || address != ":8080" {
		t.Fatalf("HTTPAddress = %q, %t", address, explicit)
	}
	exposures := manifest.HTTPExposures()
	if got := httpExposureStrings(exposures); !slices.Equal(got, []string{
		`authn.login.password/v1@plystra.yaml http.expose["authn.login.password/v1"]`,
		`customer.profile.get/v1@plystra.yaml http.expose["customer.profile.get/v1"]`,
		`kernel.health/v1@plystra.yaml http.expose["kernel.health/v1"]`,
	}) {
		t.Fatalf("HTTPExposures = %v", got)
	}
	exposures[0] = applicationmeta.HTTPExposure{}
	if manifest.HTTPExposures()[0].ID().String() != "authn.login.password/v1" {
		t.Fatal("Manifest exposed mutable HTTP exposure storage")
	}

	reordered, err := applicationmeta.Parse([]byte(`http: {address: ":8080", expose: [kernel.health/v1, authn.login.password/v1, customer.profile.get/v1]}
`))
	if err != nil || !slices.Equal(httpExposureStrings(reordered.HTTPExposures()), httpExposureStrings(manifest.HTTPExposures())) {
		t.Fatalf("reordered Parse = %v, %v", httpExposureStrings(reordered.HTTPExposures()), err)
	}
}

func TestParseNormalizesClosedHTTPTransportSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want applicationmeta.HTTPTransports
	}{
		{
			name: "omitted defaults",
			data: "{}\n",
			want: applicationmeta.HTTPTransports{Connect: true},
		},
		{
			name: "explicit values",
			data: "http: {transports: {rest: true, connect: false}}\n",
			want: applicationmeta.HTTPTransports{REST: true},
		},
		{
			name: "field removals restore defaults",
			data: "http: {transports: {connect: null, rest: null}}\n",
			want: applicationmeta.HTTPTransports{Connect: true},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, err := applicationmeta.Parse([]byte(test.data))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := manifest.HTTPTransports(); got != test.want {
				t.Fatalf("HTTPTransports = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseNormalizesClosedHTTPCORS(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.Parse([]byte(`
http:
  cors:
    allowed_origins:
      - https://EXAMPLE.com:443
      - http://localhost:80
      - https://example.com
      - http://[2001:0DB8::1]:80
    allow_credentials: true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cors, exists := manifest.HTTPCORS()
	if !exists {
		t.Fatal("HTTPCORS is absent")
	}
	if !slices.Equal(cors.AllowedOrigins, []string{"http://[2001:db8::1]", "http://localhost", "https://example.com"}) || !cors.AllowCredentials {
		t.Fatalf("HTTPCORS = %#v", cors)
	}
	cors.AllowedOrigins[0] = "https://mutated.example"
	repeated, _ := manifest.HTTPCORS()
	if repeated.AllowedOrigins[0] != "http://[2001:db8::1]" {
		t.Fatalf("HTTPCORS exposed mutable origin storage: %#v", repeated)
	}

	for _, data := range []string{
		"{}\n",
		"http: {cors: null}\n",
	} {
		withoutCORS, err := applicationmeta.Parse([]byte(data))
		if _, exists := withoutCORS.HTTPCORS(); err != nil || exists {
			t.Fatalf("Parse(%q) HTTPCORS exists = %t, error %v", data, exists, err)
		}
	}
	withDefaults, err := applicationmeta.Parse([]byte("http: {cors: {allowed_origins: ['*']}}\n"))
	if err != nil {
		t.Fatalf("Parse(default credentials): %v", err)
	}
	defaultCORS, exists := withDefaults.HTTPCORS()
	if !exists || !slices.Equal(defaultCORS.AllowedOrigins, []string{"*"}) || defaultCORS.AllowCredentials {
		t.Fatalf("default HTTPCORS = %#v, %t", defaultCORS, exists)
	}
}

func TestParseNormalizesAndRedactsConstructorConfiguration(t *testing.T) {
	t.Parallel()

	const (
		environment = "SMTP_PASSWORD_PRIVATE_TARGET"
		privateHost = "private.smtp.example.com"
	)
	manifest, err := applicationmeta.Parse([]byte(`config:
  example.com/acme/profile.New:
    enabled: true
  example.com/acme/email/smtp.New:
    host: private.smtp.example.com
    password: {env: SMTP_PASSWORD_PRIVATE_TARGET}
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	configurations := manifest.Configurations()
	if len(configurations) != 2 || configurations[0].Constructor().String() != "example.com/acme/email/smtp.New" || configurations[1].Constructor().String() != "example.com/acme/profile.New" {
		t.Fatalf("Configurations = %#v", configurations)
	}
	smtp, ok := manifest.Configuration(mustConstructorSymbol(t, "example.com/acme/email/smtp.New"))
	if !ok || smtp.Source() != `plystra.yaml config["example.com/acme/email/smtp.New"]` {
		t.Fatalf("Configuration(smtp) = %#v, %t", smtp, ok)
	}
	data := smtp.YAML()
	if !bytes.Contains(data, []byte(privateHost)) || !bytes.Contains(data, []byte(environment)) {
		t.Fatalf("Configuration YAML lost private values: %q", data)
	}
	data[0] = 'X'
	if bytes.Equal(data, smtp.YAML()) {
		t.Fatal("Configuration YAML exposed mutable storage")
	}
	configurations[0] = applicationmeta.ConstructorConfiguration{}
	if manifest.Configurations()[0].Constructor().String() != "example.com/acme/email/smtp.New" {
		t.Fatal("Configurations exposed mutable storage")
	}
	if _, exists := manifest.Configuration(mustConstructorSymbol(t, "example.com/acme/missing.New")); exists {
		t.Fatal("Configuration(missing) succeeded")
	}
	for _, value := range []any{smtp, manifest} {
		for _, formatted := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%q", value)} {
			if !strings.Contains(formatted, "redacted") || strings.Contains(formatted, environment) || strings.Contains(formatted, privateHost) {
				t.Fatalf("configuration formatting = %q", formatted)
			}
		}
	}
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewTextHandler(buffer, nil) },
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(buffer, nil) },
	} {
		var output bytes.Buffer
		slog.New(handler(&output)).Info("configuration", "value", manifest)
		if !strings.Contains(output.String(), "redacted") || strings.Contains(output.String(), environment) || strings.Contains(output.String(), privateHost) {
			t.Fatalf("structured configuration log = %s", output.String())
		}
	}
}

func TestParseAcceptsCurrentGeneratedApplicationManifest(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../newproject/testdata/project/plystra.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	manifest, err := applicationmeta.Parse(data)
	if err != nil {
		t.Fatalf("Parse generated plystra.yaml: %v\n%s", err, data)
	}
	if len(manifest.Aliases()) != 0 || len(manifest.Requirements()) != 0 || len(manifest.ProviderChoices()) != 0 || len(manifest.InterfacePolicies()) != 0 || len(manifest.Configurations()) != 0 || manifest.StartupTimeout() != 2*time.Minute {
		t.Fatalf("generated Aliases = %#v", manifest.Aliases())
	}
	if address, explicit := manifest.HTTPAddress(); !explicit || address != ":8080" || len(manifest.HTTPExposures()) != 0 {
		t.Fatalf("generated HTTP = address %q/%t, exposures %#v", address, explicit, manifest.HTTPExposures())
	}
}

func TestParseAllowsEmptyOptionalSections(t *testing.T) {
	t.Parallel()

	for _, data := range [][]byte{
		[]byte(`{}`),
		[]byte("http: {}\n"),
		[]byte("http: {transports: {}}\n"),
		[]byte("http: {transports: {connect: null, rest: null}}\n"),
		[]byte("http: {expose: []}\n"),
		[]byte("http: {address: null, expose: {add: [], remove: []}}\n"),
		[]byte("capabilities: {}\n"),
		[]byte("capabilities:\n  aliases: {}\n"),
		[]byte("capabilities: {require: {add: [], remove: []}, use: {email.send/v1: null}, aliases: {mail.send/v1: null}}\n"),
		[]byte("interfaces: {policies: {}}\n"),
		[]byte("config: {example.com/acme/plugin.New: null}\n"),
		[]byte("timeouts: {startup: null}\n"),
	} {
		manifest, err := applicationmeta.Parse(data)
		address, hasAddress := manifest.HTTPAddress()
		if err != nil || len(manifest.Aliases()) != 0 || len(manifest.Requirements()) != 0 || len(manifest.ProviderChoices()) != 0 || len(manifest.InterfacePolicies()) != 0 || len(manifest.Configurations()) != 0 || manifest.StartupTimeout() != applicationmeta.DefaultStartupTimeout || manifest.HTTPTransports() != (applicationmeta.HTTPTransports{Connect: true}) || hasAddress || address != "" || len(manifest.HTTPExposures()) != 0 {
			t.Fatalf("Parse(%q) = %#v, %v", data, manifest, err)
		}
	}
}

func TestParseRejectsUnsafeOrInvalidApplicationManifest(t *testing.T) {
	t.Parallel()

	overlong := strings.Repeat("x", 1025)
	overlongAddress := strings.Repeat("x", 4097)
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "empty", data: "", want: "document is empty"},
		{name: "multiple documents", data: "{}\n---\n{}\n", want: "multiple YAML documents"},
		{name: "root sequence", data: "[]\n", want: "document must be a mapping"},
		{name: "anchor", data: "http: &shared {}\nconfig: *shared\n", want: "anchors and aliases"},
		{name: "non-string root key", data: "? [one, two]\n: {}\n", want: "non-string key"},
		{name: "duplicate root key", data: "http: {}\nhttp: {}\n", want: "duplicate key"},
		{name: "deprecated instance ID", data: "instance_id: app\n", want: `unknown key "instance_id"`},
		{name: "legacy database", data: "database: {}\n", want: `unknown key "database"`},
		{name: "http type", data: "http: []\n", want: "http must be a mapping"},
		{name: "unknown http key", data: "http: {route: /api}\n", want: `http contains unknown key "route"`},
		{name: "http address type", data: "http: {address: 8080}\n", want: "http.address must be"},
		{name: "empty http address", data: `http: {address: ""}` + "\n", want: "http.address must be"},
		{name: "untrimmed http address", data: `http: {address: " :8080 "}` + "\n", want: "http.address must be"},
		{name: "oversized http address", data: "http:\n  address: " + overlongAddress + "\n", want: "at most 4096 bytes"},
		{name: "NUL http address", data: `http: {address: "bad\0address"}` + "\n", want: "no NUL"},
		{name: "http transports type", data: "http: {transports: []}\n", want: "http.transports must be a mapping"},
		{name: "unknown http transport", data: "http: {transports: {grpc: true}}\n", want: `http.transports contains unknown key "grpc"`},
		{name: "connect transport type", data: "http: {transports: {connect: enabled}}\n", want: "http.transports.connect must be true, false, or null"},
		{name: "REST transport type", data: "http: {transports: {rest: 1}}\n", want: "http.transports.rest must be true, false, or null"},
		{name: "CORS type", data: "http: {cors: []}\n", want: "http.cors must be a mapping"},
		{name: "unknown CORS field", data: "http: {cors: {allowed_origins: ['*'], allowed_headers: ['*']}}\n", want: `http.cors contains unknown key "allowed_headers"`},
		{name: "missing CORS origins", data: "http: {cors: {allow_credentials: false}}\n", want: "http.cors.allowed_origins is required"},
		{name: "CORS origins type", data: "http: {cors: {allowed_origins: '*'}}\n", want: "must be a nonempty sequence"},
		{name: "empty CORS origins", data: "http: {cors: {allowed_origins: []}}\n", want: "must be a nonempty sequence"},
		{name: "CORS origin item type", data: "http: {cors: {allowed_origins: [true]}}\n", want: "allowed_origins[0] must be an origin string"},
		{name: "CORS origin scheme", data: "http: {cors: {allowed_origins: ['ftp://example.com']}}\n", want: "must use the http or https scheme"},
		{name: "CORS origin path", data: "http: {cors: {allowed_origins: ['https://example.com/path']}}\n", want: "must contain only an http or https scheme, host, and optional port"},
		{name: "CORS origin userinfo", data: "http: {cors: {allowed_origins: ['https://user@example.com']}}\n", want: "must contain only an http or https scheme, host, and optional port"},
		{name: "CORS origin unicode host", data: "http: {cors: {allowed_origins: ['https://münich.example']}}\n", want: "must contain a valid ASCII host"},
		{name: "CORS origin port", data: "http: {cors: {allowed_origins: ['https://example.com:0']}}\n", want: "port from 1 through 65535"},
		{name: "CORS credentials type", data: "http: {cors: {allowed_origins: ['https://example.com'], allow_credentials: yes}}\n", want: "allow_credentials must be true, false, or null"},
		{name: "credentialed wildcard CORS", data: "http: {cors: {allowed_origins: ['*'], allow_credentials: true}}\n", want: "cannot combine wildcard origin"},
		{name: "http exposure sparse key", data: "http: {expose: {append: []}}\n", want: `unknown sparse-edit key "append"`},
		{name: "http exposure sparse add type", data: "http: {expose: {add: {}}}\n", want: "http.expose.add must be a sequence"},
		{name: "http exposure sparse remove type", data: "http: {expose: {remove: true}}\n", want: "http.expose.remove must be a sequence"},
		{name: "http exposure ambiguous edit", data: "http: {expose: {add: [order.create/v1], remove: [order.create/v1]}}\n", want: "cannot both add and remove"},
		{name: "http exposure item type", data: "http: {expose: [true]}\n", want: "http.expose[0] must be"},
		{name: "invalid HTTP exposure", data: "http: {expose: [Order.Create/v1]}\n", want: "not a canonical Interface ID"},
		{name: "duplicate HTTP exposure", data: "http: {expose: [order.create/v1, order.create/v1]}\n", want: "duplicates Interface"},
		{name: "timeouts type", data: "timeouts: []\n", want: "timeouts must be a mapping"},
		{name: "unknown timeout", data: "timeouts: {shutdown: 1m}\n", want: `timeouts contains unknown key "shutdown"`},
		{name: "startup timeout type", data: "timeouts: {startup: 120}\n", want: "timeouts.startup must be"},
		{name: "empty startup timeout", data: `timeouts: {startup: ""}` + "\n", want: "timeouts.startup must be"},
		{name: "untrimmed startup timeout", data: `timeouts: {startup: " 2m "}` + "\n", want: "timeouts.startup must be"},
		{name: "invalid startup timeout", data: "timeouts: {startup: eventually}\n", want: "positive Go duration"},
		{name: "zero startup timeout", data: "timeouts: {startup: 0s}\n", want: "positive Go duration"},
		{name: "negative startup timeout", data: "timeouts: {startup: -1s}\n", want: "positive Go duration"},
		{name: "oversized startup timeout", data: "timeouts: {startup: " + strings.Repeat("1", 65) + "s}\n", want: "at most 64 bytes"},
		{name: "NUL startup timeout", data: `timeouts: {startup: "2m\0"}` + "\n", want: "no NUL"},
		{name: "config type", data: "config: []\n", want: "config must be a mapping"},
		{name: "non-string config key", data: "config:\n  ? [one, two]\n  : {}\n", want: "non-string key"},
		{name: "invalid config constructor", data: "config: {example.com/acme/plugin.new: {}}\n", want: "not a fully qualified constructor symbol"},
		{name: "duplicate config constructor", data: "config:\n  example.com/acme/plugin.New: {}\n  example.com/acme/plugin.New: {}\n", want: "duplicate key"},
		{name: "constructor config type", data: "config: {example.com/acme/plugin.New: []}\n", want: `config["example.com/acme/plugin.New"] must be a mapping`},
		{name: "capabilities type", data: "capabilities: []\n", want: "capabilities must be a mapping"},
		{name: "unknown capabilities key", data: "capabilities: {providers: {}}\n", want: `unknown key "providers"`},
		{name: "require sparse key", data: "capabilities: {require: {append: []}}\n", want: `unknown sparse-edit key "append"`},
		{name: "require ambiguous edit", data: "capabilities: {require: {add: [order.create/v1], remove: [order.create/v1]}}\n", want: "cannot both add and remove"},
		{name: "require item type", data: "capabilities: {require: [true]}\n", want: "require[0] must be"},
		{name: "invalid requirement", data: "capabilities: {require: [Order.Create/v1]}\n", want: "not a canonical Capability ID"},
		{name: "duplicate requirement", data: "capabilities: {require: [order.create/v1, order.create/v1]}\n", want: "duplicates Capability"},
		{name: "use type", data: "capabilities: {use: []}\n", want: "use must be a mapping"},
		{name: "non-string use key", data: "capabilities:\n  use:\n    ? [one, two]\n    : acme.orders\n", want: "non-string key"},
		{name: "invalid use Capability", data: "capabilities: {use: {Order.Create/v1: acme.orders}}\n", want: "not a canonical Capability ID"},
		{name: "intrinsic use", data: "capabilities: {use: {kernel.health/v1: acme.kernel}}\n", want: "intrinsic kernel.*"},
		{name: "use value type", data: "capabilities: {use: {order.create/v1: true}}\n", want: "canonical Plugin ID string"},
		{name: "invalid use Plugin", data: "capabilities: {use: {order.create/v1: Acme.Orders}}\n", want: "canonical Plugin ID string"},
		{name: "duplicate use", data: "capabilities:\n  use:\n    order.create/v1: acme.one\n    order.create/v1: acme.two\n", want: "duplicate key"},
		{name: "aliases type", data: "capabilities: {aliases: []}\n", want: "aliases must be a mapping"},
		{name: "non-string Alias key", data: "capabilities:\n  aliases:\n    ? [one, two]\n    : order.create/v1\n", want: "non-string key"},
		{name: "duplicate Alias", data: aliasYAML("orders.start/v1: order.create/v1\n    orders.start/v1: order.create/v1"), want: "duplicate key"},
		{name: "invalid Alias ID", data: aliasYAML("Orders.Start/v1: order.create/v1"), want: "not a canonical Capability ID"},
		{name: "reserved Alias ID", data: aliasYAML("kernel.compat/v1: kernel.health/v1"), want: "reserved kernel.*"},
		{name: "concise non-string", data: aliasYAML("orders.start/v1: 1"), want: "canonical target string or expanded mapping"},
		{name: "invalid target", data: aliasYAML("orders.start/v1: invalid"), want: "target \"invalid\" is not a canonical"},
		{name: "version mismatch", data: aliasYAML("orders.start/v2: order.create/v1"), want: "same major version"},
		{name: "self target", data: aliasYAML("orders.start/v1: orders.start/v1"), want: "cannot target itself"},
		{name: "missing target", data: aliasYAML("orders.start/v1: {expose: {go: true, http: true, javascript: true}}"), want: ".target is required"},
		{name: "unknown expanded key", data: aliasYAML("orders.start/v1: {target: order.create/v1, provider: acme.orders}"), want: `unknown key "provider"`},
		{name: "duplicate expanded key", data: aliasYAML("orders.start/v1: {target: order.create/v1, target: order.create/v1}"), want: "duplicate key"},
		{name: "target type", data: aliasYAML("orders.start/v1: {target: true}"), want: "target must be a non-empty string"},
		{name: "expose type", data: aliasYAML("orders.start/v1: {target: order.create/v1, expose: []}"), want: "expose must be a mapping"},
		{name: "missing expose field", data: aliasYAML("orders.start/v1: {target: order.create/v1, expose: {go: true, http: false}}"), want: "expose.javascript is required"},
		{name: "unknown expose field", data: aliasYAML("orders.start/v1: {target: order.create/v1, expose: {go: true, http: false, javascript: false, python: true}}"), want: `unknown key "python"`},
		{name: "expose field type", data: aliasYAML("orders.start/v1: {target: order.create/v1, expose: {go: yes, http: false, javascript: false}}"), want: "expose.go must be true or false"},
		{name: "deprecated type", data: aliasYAML("orders.start/v1: {target: order.create/v1, deprecated: true}"), want: "deprecated must be a mapping"},
		{name: "missing deprecation message", data: aliasYAML("orders.start/v1: {target: order.create/v1, deprecated: {}}"), want: "deprecated.message is required"},
		{name: "unknown deprecation field", data: aliasYAML("orders.start/v1: {target: order.create/v1, deprecated: {message: old, since: v1}}"), want: `unknown key "since"`},
		{name: "empty deprecation message", data: aliasYAML(`orders.start/v1: {target: order.create/v1, deprecated: {message: ""}}`), want: "non-empty string"},
		{name: "oversized deprecation message", data: aliasYAML("orders.start/v1:\n      target: order.create/v1\n      deprecated:\n        message: " + overlong), want: "at most 1024 bytes"},
		{name: "NUL deprecation message", data: aliasYAML(`orders.start/v1: {target: order.create/v1, deprecated: {message: "bad\0message"}}`), want: "no NUL"},
		{name: "Alias chain", data: aliasYAML("compat.a/v1: compat.b/v1\n    compat.b/v1: order.create/v1"), want: "compat.a/v1 -> compat.b/v1 -> order.create/v1"},
		{name: "Alias cycle", data: aliasYAML("compat.a/v1: compat.b/v1\n    compat.b/v1: compat.a/v1"), want: "compat.a/v1 -> compat.b/v1 -> compat.a/v1"},
		{name: "Alias requirement", data: "capabilities:\n  require: [orders.start/v1]\n  aliases: {orders.start/v1: order.create/v1}\n", want: "requirements must name canonical Capabilities"},
		{name: "Alias provider choice", data: "capabilities:\n  use: {orders.start/v1: acme.orders}\n  aliases: {orders.start/v1: order.create/v1}\n", want: "provider choices must name canonical Capabilities"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, err := applicationmeta.Parse([]byte(test.data))
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want ErrInvalidManifest containing %q", err, test.want)
			}
			if len(manifest.Aliases()) != 0 || len(manifest.Requirements()) != 0 || len(manifest.ProviderChoices()) != 0 {
				t.Fatalf("invalid Parse returned %#v", manifest)
			}
		})
	}
}

func TestParseRejectsOversizedManifest(t *testing.T) {
	t.Parallel()

	data := make([]byte, applicationmeta.MaximumSize+1)
	for index := range data {
		data[index] = ' '
	}
	if _, err := applicationmeta.Parse(data); !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Parse oversized error = %v", err)
	}
}

func FuzzParseApplicationManifest(f *testing.F) {
	for _, seed := range []string{
		"{}\n",
		"http: {address: \":8080\", expose: [kernel.health/v1, order.create/v1]}\n",
		"http: {transports: {connect: false, rest: true}}\n",
		"http: {transports: {connect: null, rest: null}}\n",
		"http: {cors: {allowed_origins: [https://example.com, http://localhost:80], allow_credentials: true}}\n",
		"http: {cors: null}\n",
		"http: {address: null, expose: {add: [kernel.health/v1], remove: [order.create/v1]}}\n",
		"capabilities: {aliases: {}}\n",
		"capabilities: {require: {remove: [order.create/v1]}, use: {email.send/v1: null}, aliases: {mail.send/v1: null}}\n",
		"interfaces: {policies: {email.send/v1: {timeout: 5000ms}, audit.write/v1: null}}\n",
		"config: {example.com/acme/plugin.New: null}\n",
		"config: {example.com/acme/plugin.New: {settings: {legacy: null, nested: {enabled: true}}}}\n",
		"capabilities: {require: [kernel.health/v1, order.create/v1], use: {order.create/v1: acme.orders}, aliases: {}}\n",
		aliasYAML("authn.login/v1: authn.login.password/v1"),
		aliasYAML("account.sign-in/v1: {target: authn.login.password/v1, expose: {go: true, http: false, javascript: false}, deprecated: {message: old}}"),
		aliasYAML("compat.a/v1: compat.b/v1\n    compat.b/v1: compat.a/v1"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > applicationmeta.MaximumSize {
			return
		}
		first, firstErr := applicationmeta.Parse([]byte(input))
		second, secondErr := applicationmeta.Parse([]byte(input))
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("Parse changed result: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, applicationmeta.ErrInvalidManifest) || !errors.Is(secondErr, applicationmeta.ErrInvalidManifest) || firstErr.Error() != secondErr.Error() {
				t.Fatalf("Parse errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		firstAddress, firstHasAddress := first.HTTPAddress()
		secondAddress, secondHasAddress := second.HTTPAddress()
		firstCORS, firstHasCORS := first.HTTPCORS()
		secondCORS, secondHasCORS := second.HTTPCORS()
		if !slices.Equal(aliasStrings(first.Aliases()), aliasStrings(second.Aliases())) ||
			!slices.Equal(httpExposureStrings(first.HTTPExposures()), httpExposureStrings(second.HTTPExposures())) ||
			!slices.Equal(requirementStrings(first.Requirements()), requirementStrings(second.Requirements())) ||
			!slices.Equal(providerChoiceStrings(first.ProviderChoices()), providerChoiceStrings(second.ProviderChoices())) ||
			!slices.Equal(interfacePolicyStrings(first.InterfacePolicies()), interfacePolicyStrings(second.InterfacePolicies())) ||
			firstAddress != secondAddress || firstHasAddress != secondHasAddress ||
			first.HTTPTransports() != second.HTTPTransports() ||
			firstHasCORS != secondHasCORS ||
			firstCORS.AllowCredentials != secondCORS.AllowCredentials ||
			!slices.Equal(firstCORS.AllowedOrigins, secondCORS.AllowedOrigins) ||
			first.StartupTimeout() != second.StartupTimeout() {
			t.Fatalf("Parse is nondeterministic: %#v then %#v", first, second)
		}
	})
}

func aliasYAML(body string) string {
	return "capabilities:\n  aliases:\n    " + body + "\n"
}

func aliasStrings(values []applicationmeta.Alias) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = fmt.Sprintf("%s->%s", value.ID(), value.Target())
	}
	return result
}

func httpExposureStrings(values []applicationmeta.HTTPExposure) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = fmt.Sprintf("%s@%s", value.ID(), value.Source())
	}
	return result
}

func requirementStrings(values []applicationmeta.CapabilityRequirement) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = fmt.Sprintf("%s@%s", value.ID(), value.Source())
	}
	return result
}

func providerChoiceStrings(values []applicationmeta.ProviderChoice) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = fmt.Sprintf("%s->%s@%s", value.Capability(), value.PluginID(), value.Source())
	}
	return result
}
