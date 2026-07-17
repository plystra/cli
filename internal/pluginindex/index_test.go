package pluginindex_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/pluginmeta"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestScanBuildsDeterministicImmutableIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "profile", "acme.app.profile")
	accountManifest := []byte("id: acme.app.account\nprovides:\n  - profile.get/v2\n  - account.register/v1\nrequires: [email.send/v1, audit.write/v1]\nconfig: {token: {type: secret, required: true}}\ngeneration: {api: v1, package: ./generation, activations: [{namespace: profile, capability: profile.get/v2}]}\n")
	writeManifest(t, root, "account", string(accountManifest))
	writeGenerationPackage(t, root, "account", "generation")
	index, err := pluginindex.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	plugins := index.Plugins()
	if len(plugins) != 2 || plugins[0].Name() != "account" || plugins[0].Path() != "account" || plugins[0].ID() != "acme.app.account" || plugins[1].Name() != "profile" {
		t.Fatalf("Plugins() = %#v", plugins)
	}
	if got := identifierStrings(plugins[0].Provides()); !reflect.DeepEqual(got, []string{"account.register/v1", "profile.get/v2"}) {
		t.Fatalf("account Provides() = %v", got)
	}
	if got := identifierStrings(plugins[0].Requires()); !reflect.DeepEqual(got, []string{"audit.write/v1", "email.send/v1"}) {
		t.Fatalf("account Requires() = %v", got)
	}
	if got := plugins[0].ManifestData(); !bytes.Equal(got, accountManifest) {
		t.Fatalf("account ManifestData() = %q", got)
	}
	token, ok := plugins[0].Config().Lookup("token")
	if !ok || token.Type() != kernelmanifest.ConfigSecret || !token.Required() {
		t.Fatalf("account Config token = %#v, %t", token, ok)
	}
	generation, ok := plugins[0].Generation()
	if !ok || generation.API() != "v1" || generation.Package() != "./generation" || len(generation.Activations()) != 1 || generation.Activations()[0].Namespace() != "profile" {
		t.Fatalf("account Generation() = %#v, %t", generation, ok)
	}
	if packagePath, ok := plugins[0].GenerationPackagePath(); !ok || packagePath != "account/generation" {
		t.Fatalf("account GenerationPackagePath() = %q, %t", packagePath, ok)
	}
	activations := generation.Activations()
	activations[0] = pluginmeta.GenerationActivation{}
	indexedGeneration, _ := index.Plugins()[0].Generation()
	if indexedGeneration.Activations()[0].Namespace() != "profile" {
		t.Fatal("Generation exposed mutable index storage")
	}
	if _, ok := plugins[1].Generation(); ok {
		t.Fatal("profile unexpectedly has generation metadata")
	}
	if _, ok := plugins[1].GenerationPackagePath(); ok {
		t.Fatal("profile unexpectedly has generation package provenance")
	}
	if byName, ok := index.ByReference("account"); !ok || byName.ID() != "acme.app.account" {
		t.Fatalf("ByReference(directory) = %#v, %t", byName, ok)
	}
	if byID, ok := index.ByReference("acme.app.profile"); !ok || byID.Name() != "profile" {
		t.Fatalf("ByReference(ID) = %#v, %t", byID, ok)
	}
	if _, ok := index.ByReference("missing"); ok {
		t.Fatal("ByReference(missing) succeeded")
	}
	plugins[0] = pluginindex.Plugin{}
	if index.Plugins()[0].Name() != "account" {
		t.Fatal("Plugins exposed mutable index storage")
	}
	provided := index.Plugins()[0].Provides()
	provided[0] = capabilityid.Identifier{}
	if index.Plugins()[0].Provides()[0].String() != "account.register/v1" {
		t.Fatal("Provides exposed mutable index storage")
	}
	required := index.Plugins()[0].Requires()
	required[0] = capabilityid.Identifier{}
	if index.Plugins()[0].Requires()[0].String() != "audit.write/v1" {
		t.Fatal("Requires exposed mutable index storage")
	}
	manifest := index.Plugins()[0].ManifestData()
	manifest[0] = 'x'
	if index.Plugins()[0].ManifestData()[0] != 'i' {
		t.Fatal("ManifestData exposed mutable index storage")
	}
}

func TestScanRejectsDuplicateIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "account", "acme.app.shared")
	writePlugin(t, root, "profile", "acme.app.shared")
	index, err := pluginindex.Scan(root)
	if !errors.Is(err, pluginindex.ErrIndex) || !errors.Is(err, pluginindex.ErrDuplicateID) {
		t.Fatalf("Scan error = %v, want ErrIndex and ErrDuplicateID", err)
	}
	if len(index.Plugins()) != 0 {
		t.Fatalf("invalid Scan returned %#v", index.Plugins())
	}
}

func TestScanRejectsInvalidIndexedMetadata(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"identity":   "id: Acme.Bad\n",
		"capability": "id: acme.app.account\nprovides: [account.register]\n",
	}
	for name, declaration := range tests {
		name, declaration := name, declaration
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, "account", declaration)
			if _, err := pluginindex.Scan(root); !errors.Is(err, pluginindex.ErrIndex) {
				t.Fatalf("Scan error = %v, want ErrIndex", err)
			}
		})
	}
}

func TestScanRejectsMissingAndNonDirectoryGenerationPackages(t *testing.T) {
	t.Parallel()

	manifest := "id: acme.app.account\nprovides: [authn.session.verify/v1]\ngeneration: {api: v1, package: ./generation/nested, activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"
	tests := map[string]func(*testing.T, string){
		"missing": func(t *testing.T, root string) {
			if err := os.Mkdir(filepath.Join(root, "account", "generation"), 0o755); err != nil {
				t.Fatalf("Mkdir(generation): %v", err)
			}
		},
		"non directory": func(t *testing.T, root string) {
			generation := filepath.Join(root, "account", "generation")
			if err := os.Mkdir(generation, 0o755); err != nil {
				t.Fatalf("Mkdir(generation): %v", err)
			}
			if err := os.WriteFile(filepath.Join(generation, "nested"), []byte("not a directory"), 0o644); err != nil {
				t.Fatalf("WriteFile(nested): %v", err)
			}
		},
	}
	for name, prepare := range tests {
		name, prepare := name, prepare
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, "account", manifest)
			prepare(t, root)
			index, err := pluginindex.Scan(root)
			if !errors.Is(err, pluginindex.ErrIndex) || !errors.Is(err, pluginindex.ErrInvalidGenerationPackage) {
				t.Fatalf("Scan error = %v, want ErrIndex and ErrInvalidGenerationPackage", err)
			}
			for _, detail := range []string{"acme.app.account", "./generation/nested", "account/generation/nested"} {
				if !strings.Contains(err.Error(), detail) {
					t.Fatalf("Scan error %q does not contain %q", err, detail)
				}
			}
			if len(index.Plugins()) != 0 {
				t.Fatalf("invalid Scan returned %#v", index.Plugins())
			}
		})
	}
}

func writePlugin(t *testing.T, root, name, id string) {
	t.Helper()
	writeManifest(t, root, name, "id: "+id+"\n")
}

func writeManifest(t *testing.T, root, name, declaration string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir(%s): %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(declaration), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func writeGenerationPackage(t *testing.T, root, plugin string, parts ...string) {
	t.Helper()
	components := append([]string{root, plugin}, parts...)
	if err := os.MkdirAll(filepath.Join(components...), 0o755); err != nil {
		t.Fatalf("MkdirAll(generation package): %v", err)
	}
}

func identifierStrings(identifiers []capabilityid.Identifier) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
}
