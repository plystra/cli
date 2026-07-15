package pluginmeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginmeta"
)

func TestParseIndexesCanonicalMetadata(t *testing.T) {
	t.Parallel()

	input := `provides: [profile.get/v2, authz.check/v1, account.register/v1, authn.session.verify/v1]
id: acme.app.account
requires: [audit.write/v1]
generation:
  package: ./generation
  activations:
    - namespace: authz
      capability: authz.check/v1
    - capability: authn.session.verify/v1
      namespace: authn
  api: v1
config:
  token: {type: secret}
`
	metadata, err := pluginmeta.Parse([]byte(input))
	if err != nil || metadata.ID() != "acme.app.account" {
		t.Fatalf("Parse = %#v, %v", metadata, err)
	}
	if got := identifierStrings(metadata.Provides()); !reflect.DeepEqual(got, []string{"account.register/v1", "authn.session.verify/v1", "authz.check/v1", "profile.get/v2"}) {
		t.Fatalf("Provides = %v", got)
	}
	generation, ok := metadata.Generation()
	if !ok || generation.API() != pluginmeta.GenerationAPIV1 || generation.Package() != "./generation" {
		t.Fatalf("Generation = %#v, %t", generation, ok)
	}
	activations := generation.Activations()
	if len(activations) != 2 || activations[0].Namespace() != "authn" || activations[0].Capability().String() != "authn.session.verify/v1" || activations[1].Namespace() != "authz" || activations[1].Capability().String() != "authz.check/v1" {
		t.Fatalf("Activations = %#v", activations)
	}
	provided := metadata.Provides()
	provided[0] = capabilityid.Identifier{}
	if metadata.Provides()[0].String() != "account.register/v1" {
		t.Fatal("Provides exposed mutable metadata")
	}
	activations[0] = pluginmeta.GenerationActivation{}
	if metadataGenerationActivations(metadata)[0].Namespace() != "authn" {
		t.Fatal("Activations exposed mutable metadata")
	}
	quoted, err := pluginmeta.Parse([]byte("id: 'acme.app.account'\n"))
	_, hasQuotedGeneration := quoted.Generation()
	if err != nil || quoted.ID() != metadata.ID() || len(quoted.Provides()) != 0 || hasQuotedGeneration {
		t.Fatalf("Parse quoted = %#v, %v", quoted, err)
	}
}

func TestParseRejectsInvalidGenerationDeclarations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		declaration string
		also        error
	}{
		"not mapping":              {declaration: "generation: []\n"},
		"non string key":           {declaration: "generation: {1: value}\n"},
		"unknown key":              {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}], rules: []}\n"},
		"duplicate key":            {declaration: "generation:\n  api: v1\n  api: v1\n  package: ./generation\n  activations: [{namespace: authn, capability: authn.session.verify/v1}]\n"},
		"missing api":              {declaration: "generation: {package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"empty api":                {declaration: "generation: {api: '', package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"non string api":           {declaration: "generation: {api: 1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"unsupported api":          {declaration: "generation: {api: v2, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n", also: pluginmeta.ErrUnsupportedGenerationAPI},
		"missing package":          {declaration: "generation: {api: v1, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"empty package":            {declaration: "generation: {api: v1, package: '', activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"non string package":       {declaration: "generation: {api: v1, package: 1, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"unprefixed package":       {declaration: "generation: {api: v1, package: generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"parent package":           {declaration: "generation: {api: v1, package: ../generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"unclean package":          {declaration: "generation: {api: v1, package: ./generation/../outside, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"absolute package":         {declaration: "generation: {api: v1, package: /generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"backslash package":        {declaration: "generation: {api: v1, package: '.\\generation', activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"invalid import package":   {declaration: "generation: {api: v1, package: './generation@v1', activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"},
		"missing activations":      {declaration: "generation: {api: v1, package: ./generation}\n"},
		"empty activations":        {declaration: "generation: {api: v1, package: ./generation, activations: []}\n"},
		"activations not sequence": {declaration: "generation: {api: v1, package: ./generation, activations: {namespace: authn, capability: authn.session.verify/v1}}\n"},
		"activation not mapping":   {declaration: "generation: {api: v1, package: ./generation, activations: [authn]}\n"},
		"activation unknown key":   {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1, priority: 1}]}\n"},
		"activation duplicate key": {declaration: "generation:\n  api: v1\n  package: ./generation\n  activations:\n    - namespace: authn\n      namespace: authn\n      capability: authn.session.verify/v1\n"},
		"missing namespace":        {declaration: "generation: {api: v1, package: ./generation, activations: [{capability: authn.session.verify/v1}]}\n"},
		"non string namespace":     {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: 1, capability: authn.session.verify/v1}]}\n"},
		"invalid namespace":        {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: AuthN, capability: authn.session.verify/v1}]}\n"},
		"dotted namespace":         {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: acme.authn, capability: authn.session.verify/v1}]}\n"},
		"missing capability":       {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authn}]}\n"},
		"non string capability":    {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authn, capability: 1}]}\n"},
		"invalid capability":       {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify}]}\n"},
		"unprovided capability":    {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authz, capability: authz.check/v1}]}\n"},
		"duplicate namespace":      {declaration: "generation: {api: v1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}, {namespace: authn, capability: authn.session.verify/v1}]}\n"},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := "id: acme.app.account\nprovides: [authn.session.verify/v1]\n" + test.declaration
			metadata, err := pluginmeta.Parse([]byte(input))
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) || test.also != nil && !errors.Is(err, test.also) {
				t.Fatalf("Parse error = %v", err)
			}
			if metadata.ID() != "" || len(metadata.Provides()) != 0 {
				t.Fatalf("invalid Parse returned %#v", metadata)
			}
			if _, ok := metadata.Generation(); ok {
				t.Fatalf("invalid Parse returned generation metadata")
			}
		})
	}
}

func TestParseRejectsInvalidMetadataEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"[]\n",
		"id: acme.one\n---\nid: acme.two\n",
		"id: &identity acme.one\n",
		"id: &identity acme.one\nprovides: [*identity]\n",
		"1: value\n",
		"id: acme.one\nversion: 1.0.0\n",
		"id: acme.one\nid: acme.two\n",
		"provides: []\n",
		"id: 1\n",
		"id: Acme.One\n",
		"id: acme.one\nprovides: email.send/v1\n",
		"id: acme.one\nprovides: [1]\n",
		"id: acme.one\nprovides: [email.send]\n",
		"id: acme.one\nprovides: [email.send/v1, email.send/v1]\n",
		"id: acme.one\nrequires: [Audit.write/v1]\n",
		strings.Repeat("x", pluginmeta.MaximumSize+1),
	}
	for _, input := range tests {
		input := input
		t.Run(testName(input), func(t *testing.T) {
			t.Parallel()
			metadata, err := pluginmeta.Parse([]byte(input))
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) {
				t.Fatalf("Parse error = %v, want ErrInvalidManifest", err)
			}
			if metadata.ID() != "" || len(metadata.Provides()) != 0 {
				t.Fatalf("invalid Parse returned %#v", metadata)
			}
			if _, ok := metadata.Generation(); ok {
				t.Fatal("invalid Parse returned generation metadata")
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"id: acme.one\n", "id: acme.one\nprovides: [email.send/v1]\n", "id: acme.authn\nprovides: [authn.session.verify/v1]\ngeneration: {api: v1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n", "[]\n", "id: &x acme.one\nrequires: [*x]\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		metadata, err := pluginmeta.Parse([]byte(input))
		if err != nil {
			if !errors.Is(err, pluginmeta.ErrInvalidManifest) {
				t.Fatalf("Parse returned unexpected error: %v", err)
			}
			return
		}
		if metadata.ID() == "" {
			t.Fatal("Parse returned metadata without an ID")
		}
		provided := metadata.Provides()
		for index := 1; index < len(provided); index++ {
			if provided[index-1].String() >= provided[index].String() {
				t.Fatalf("Provides are not uniquely sorted: %q then %q", provided[index-1], provided[index])
			}
		}
		if generation, ok := metadata.Generation(); ok {
			if generation.API() != pluginmeta.GenerationAPIV1 || !strings.HasPrefix(generation.Package(), "./") || len(generation.Activations()) == 0 {
				t.Fatalf("Generation is not canonical: %#v", generation)
			}
			activations := generation.Activations()
			for index, activation := range activations {
				if index > 0 && activations[index-1].Namespace() >= activation.Namespace() {
					t.Fatalf("Activations are not uniquely sorted: %q then %q", activations[index-1].Namespace(), activation.Namespace())
				}
				if !containsIdentifier(provided, activation.Capability()) {
					t.Fatalf("Activation %q names unprovided %s", activation.Namespace(), activation.Capability())
				}
			}
		}
	})
}

func metadataGenerationActivations(metadata pluginmeta.Manifest) []pluginmeta.GenerationActivation {
	generation, _ := metadata.Generation()
	return generation.Activations()
}

func containsIdentifier(values []capabilityid.Identifier, target capabilityid.Identifier) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func identifierStrings(identifiers []capabilityid.Identifier) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
}

func testName(input string) string {
	if input == "" {
		return "empty"
	}
	if len(input) > 64 {
		return "large"
	}
	return strings.NewReplacer("\n", "_", " ", "_").Replace(input)
}
