package pluginmeta_test

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginmeta"
)

func TestAddProvidedPreservesManifestSourceAndAddsSortedCapability(t *testing.T) {
	t.Parallel()

	input := []byte("# Account plugin.\r\nid: acme.app.account # Stable identity.\r\nprovides:\r\n  - profile.get/v2 # Profile API.\r\n  - account.register/v1 # Account API.\r\nrequires: [audit.write/v1]\r\ngeneration:\r\n  api: v1\r\n  package: ./generation\r\n  activations:\r\n    - namespace: profile\r\n      capability: profile.get/v2\r\nconfig:\r\n  token: {type: secret} # Runtime secret.\r\n")
	original := append([]byte(nil), input...)
	target := mustCapability(t, "audit.export/v1")
	got, changed, err := pluginmeta.AddProvided(input, target)
	if err != nil || !changed {
		t.Fatalf("AddProvided = changed %t, %v", changed, err)
	}
	want, err := os.ReadFile("testdata/account.with-provide.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("updated manifest:\n got: %s\nwant: %s", got, want)
	}
	metadata, err := pluginmeta.Parse(got)
	if err != nil || metadata.ID() != "acme.app.account" || !reflect.DeepEqual(identifierStrings(metadata.Provides()), []string{"account.register/v1", "audit.export/v1", "profile.get/v2"}) {
		t.Fatalf("Parse(updated) = %#v, %v", metadata, err)
	}
	generation, ok := metadata.Generation()
	if !ok || generation.API() != pluginmeta.GenerationAPIV1 || generation.Package() != "./generation" || len(generation.Activations()) != 1 || generation.Activations()[0].Namespace() != "profile" {
		t.Fatalf("updated generation = %#v, %t", generation, ok)
	}
	for _, retained := range []string{"# Account plugin.", "# Stable identity.", "# Profile API.", "# Account API.", "audit.write/v1", "generation:", "namespace: profile", "capability: profile.get/v2", "token: {type: secret} # Runtime secret."} {
		if !strings.Contains(string(got), retained) {
			t.Fatalf("updated manifest does not contain %q:\n%s", retained, got)
		}
	}
	if !bytes.Equal(input, original) {
		t.Fatal("AddProvided mutated input bytes")
	}
	repeated, repeatedChanged, err := pluginmeta.AddProvided(input, target)
	if err != nil || !repeatedChanged || !bytes.Equal(repeated, got) {
		t.Fatalf("repeated AddProvided = changed %t, %q, %v", repeatedChanged, repeated, err)
	}
}

func TestAddProvidedCreatesBlockDeclarationAfterIdentity(t *testing.T) {
	t.Parallel()

	input := []byte("id: acme.app.account\nconfig: {}\n")
	got, changed, err := pluginmeta.AddProvided(input, mustCapability(t, "account.register/v1"))
	if err != nil || !changed {
		t.Fatalf("AddProvided = changed %t, %v", changed, err)
	}
	want := "id: acme.app.account\nprovides:\n  - account.register/v1\nconfig: {}\n"
	if string(got) != want {
		t.Fatalf("updated manifest = %q, want %q", got, want)
	}
}

func TestAddProvidedIsExactAndDefensiveWhenAlreadyDeclared(t *testing.T) {
	t.Parallel()

	input := []byte("id: acme.app.account\r\nprovides: [account.register/v1]\r\n")
	got, changed, err := pluginmeta.AddProvided(input, mustCapability(t, "account.register/v1"))
	if err != nil || changed || !bytes.Equal(got, input) {
		t.Fatalf("AddProvided(existing) = changed %t, %q, %v", changed, got, err)
	}
	got[0] = 'x'
	if input[0] != 'i' {
		t.Fatal("AddProvided returned mutable input storage")
	}
}

func TestAddProvidedRejectsInvalidCapabilityAndManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		capability capabilityid.Identifier
		also       error
	}{
		{name: "empty capability", input: "id: acme.app.account\n"},
		{name: "invalid manifest", input: "id: acme.app.account\nunknown: true\n", capability: mustCapability(t, "account.register/v1"), also: pluginmeta.ErrInvalidManifest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, changed, err := pluginmeta.AddProvided([]byte(test.input), test.capability)
			if !errors.Is(err, pluginmeta.ErrAddProvided) || (test.also != nil && !errors.Is(err, test.also)) || changed || got != nil {
				t.Fatalf("AddProvided = changed %t, %q, %v", changed, got, err)
			}
		})
	}
}

func FuzzAddProvided(f *testing.F) {
	for _, seed := range []string{
		"id: acme.app.account\n",
		"id: acme.app.account\nprovides: [account.register/v1]\nconfig: {}\n",
		"id: acme.authn\nprovides: [authn.session.verify/v1]\ngeneration: {api: v1, package: ./generation, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n",
		"[]\n",
		"id: &x acme.app.account\nconfig: *x\n",
	} {
		f.Add(seed)
	}
	target := mustFuzzCapability(f, "account.register/v1")
	f.Fuzz(func(t *testing.T, input string) {
		before, beforeErr := pluginmeta.Parse([]byte(input))
		got, changed, err := pluginmeta.AddProvided([]byte(input), target)
		if err != nil {
			if !errors.Is(err, pluginmeta.ErrAddProvided) {
				t.Fatalf("AddProvided returned unexpected error: %v", err)
			}
			return
		}
		if beforeErr != nil {
			t.Fatalf("AddProvided accepted a manifest Parse rejected: %v", beforeErr)
		}
		metadata, err := pluginmeta.Parse(got)
		want := identifierStrings(before.Provides())
		wasPresent := false
		for _, value := range want {
			wasPresent = wasPresent || value == target.String()
		}
		if !wasPresent {
			want = append(want, target.String())
			sort.Strings(want)
		}
		if err != nil || metadata.ID() != before.ID() || changed == wasPresent || !reflect.DeepEqual(identifierStrings(metadata.Provides()), want) || !sameGenerationMetadata(before, metadata) {
			t.Fatalf("Parse(updated) = %#v, %v", metadata, err)
		}
		repeated, changed, err := pluginmeta.AddProvided(got, target)
		if err != nil || changed || !bytes.Equal(repeated, got) {
			t.Fatalf("repeated AddProvided = changed %t, %q, %v", changed, repeated, err)
		}
	})
}

func sameGenerationMetadata(left, right pluginmeta.Manifest) bool {
	leftGeneration, leftExists := left.Generation()
	rightGeneration, rightExists := right.Generation()
	if leftExists != rightExists || !leftExists {
		return leftExists == rightExists
	}
	return leftGeneration.API() == rightGeneration.API() && leftGeneration.Package() == rightGeneration.Package() && reflect.DeepEqual(leftGeneration.Activations(), rightGeneration.Activations())
}

func mustCapability(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return identifier
}

func mustFuzzCapability(f *testing.F, value string) capabilityid.Identifier {
	f.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		f.Fatalf("Parse(%q): %v", value, err)
	}
	return identifier
}
