package applicationmeta_test

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
)

func TestParseNormalizesConciseExpandedAndMultipleAliases(t *testing.T) {
	t.Parallel()

	data := []byte(`http:
  address: ":8080"
  expose: []
timeouts:
  startup: 2m
capabilities:
  require: []
  use: {}
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
  use: {}
  require: []
timeouts: {startup: 2m}
http: {expose: [], address: ":8080"}
`)
	second, err := applicationmeta.Parse(reordered)
	if err != nil || !slices.Equal(aliasStrings(second.Aliases()), aliasStrings(manifest.Aliases())) {
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
	if len(manifest.Aliases()) != 0 {
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
		[]byte("http: {expose: []}\n"),
		[]byte("capabilities: {}\n"),
		[]byte("capabilities:\n  aliases: {}\n"),
	} {
		manifest, err := applicationmeta.Parse(data)
		address, hasAddress := manifest.HTTPAddress()
		if err != nil || len(manifest.Aliases()) != 0 || hasAddress || address != "" || len(manifest.HTTPExposures()) != 0 {
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
		{name: "http exposure type", data: "http: {expose: {}}\n", want: "http.expose must be a sequence"},
		{name: "http exposure item type", data: "http: {expose: [true]}\n", want: "http.expose[0] must be"},
		{name: "invalid HTTP exposure", data: "http: {expose: [Order.Create/v1]}\n", want: "not a canonical Capability ID"},
		{name: "duplicate HTTP exposure", data: "http: {expose: [order.create/v1, order.create/v1]}\n", want: "duplicates Capability"},
		{name: "timeouts type", data: "timeouts: []\n", want: "timeouts must be a mapping"},
		{name: "config type", data: "config: []\n", want: "config must be a mapping"},
		{name: "capabilities type", data: "capabilities: []\n", want: "capabilities must be a mapping"},
		{name: "unknown capabilities key", data: "capabilities: {providers: {}}\n", want: `unknown key "providers"`},
		{name: "require type", data: "capabilities: {require: {}}\n", want: "require must be a sequence"},
		{name: "use type", data: "capabilities: {use: []}\n", want: "use must be a mapping"},
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
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, err := applicationmeta.Parse([]byte(test.data))
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want ErrInvalidManifest containing %q", err, test.want)
			}
			if len(manifest.Aliases()) != 0 {
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
		"capabilities: {aliases: {}}\n",
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
		if !slices.Equal(aliasStrings(first.Aliases()), aliasStrings(second.Aliases())) ||
			!slices.Equal(httpExposureStrings(first.HTTPExposures()), httpExposureStrings(second.HTTPExposures())) ||
			firstAddress != secondAddress || firstHasAddress != secondHasAddress {
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
