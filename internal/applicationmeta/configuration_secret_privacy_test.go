package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestSecretReferencePublicIdentityExcludesKindAndTarget(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Password configuration.Secret\nNested struct { Token configuration.Secret }\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	for _, wrapper := range []string{
		"config: {example.com/acme/smtp.New: %s}",
		"config: {example.com/acme/smtp.New: %s, example.com/unavailable/service.New: {unknown: private}}",
	} {
		var first string
		for _, reference := range []string{"{env: PRIVATE_FIRST}", "{env: PRIVATE_SECOND}", "{file: /PRIVATE_FILE}"} {
			values := "{password: " + reference + ", nested: {token: " + reference + "}}"
			manifest := composeManifest(t, fmt.Sprintf(wrapper, values))
			digest, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if first != "" && digest != first {
				t.Fatal("Secret reference kind or target changed public identity")
			}
			first = digest
		}
		for _, values := range []string{"{}", "{password: {$remove: true}}"} {
			digest, err := applicationmeta.ConfigurationLayerDigest(composeManifest(t, fmt.Sprintf(wrapper, values)), lookup)
			if err != nil || digest == first {
				t.Fatalf("absence or explicit removal lost its public identity: %v", err)
			}
		}
	}
}

func FuzzSecretReferencePublicIdentity(f *testing.F) {
	f.Add([]byte("one"), false)
	f.Add([]byte("two"), true)
	f.Add(bytes.Repeat([]byte("0"), 124), false)
	f.Add(bytes.Repeat([]byte("0"), 125), false)
	schema := composeSchema(f, "Password configuration.Secret\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	digest := func(t testing.TB, reference string) (string, error) {
		t.Helper()
		manifest := composeManifest(t, "config: {example.com/acme/smtp.New: {password: "+reference+"}}")
		_, validationErr := applicationmeta.ConfigurationDecisions(manifest, lookup)
		value, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		return value, validationErr
	}
	want, err := digest(f, "{env: PRIVATE_BASELINE}")
	if err != nil {
		f.Fatal(err)
	}
	wantInvalid, err := digest(f, "{env: "+strings.Repeat("A", 257)+"}")
	if !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) {
		f.Fatalf("overlong environment name = %v", err)
	}
	f.Fuzz(func(t *testing.T, target []byte, file bool) {
		if len(target) > 128 {
			t.Skip()
		}
		reference := fmt.Sprintf("{env: PRIVATE_%x}", target)
		if file {
			reference = fmt.Sprintf("{file: /PRIVATE_%x}", target)
		}
		got, err := digest(t, reference)
		if !file && len(target) > 124 {
			if !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) || got != wantInvalid {
				t.Fatal("invalid reference bypassed validation or entered opaque public identity")
			}
			return
		}
		if err != nil || got != want {
			t.Fatal("Secret reference changed public identity")
		}
	})
}
