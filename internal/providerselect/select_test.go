package providerselect_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/providerselect"
)

func TestSelectedManifestWriteTargetsOnlyTheSelectedCurrentProjectLayer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProviderFile(t, filepath.Join(root, "plystra.yaml"), "# Root choices.\ncapabilities: {use: {email.send/v1: acme.email.root}}\n")
	writeProviderFile(t, filepath.Join(root, "plystra.production.yaml"), "# Production choices.\n{}\n")
	writeProviderFile(t, filepath.Join(root, "deploy", "customer.yaml"), "# Customer choices.\ncapabilities: {require: [email.send/v1]}\n")
	id := mustProviderID(t, "email.send/v1")

	tests := []struct {
		name        string
		config      string
		environment string
		ambient     []string
		wantPath    string
		wantComment string
	}{
		{name: "root", wantPath: "plystra.yaml", wantComment: "# Root choices."},
		{name: "environment", environment: "production", ambient: []string{"PLYSTRA_CONFIG=ignored.yaml"}, wantPath: "plystra.production.yaml", wantComment: "# Production choices."},
		{name: "configuration", config: "deploy/customer.yaml", ambient: []string{"PLYSTRA_ENV=ignored"}, wantPath: "deploy/customer.yaml", wantComment: "# Customer choices."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			write, changed, selection, err := providerselect.SelectedManifestWrite(root, id, "acme.email.selected", test.config, test.environment, test.ambient)
			if err != nil || !changed || write.Path != test.wantPath || selection.Path() != test.wantPath || !strings.Contains(string(write.Data), test.wantComment) {
				t.Fatalf("SelectedManifestWrite = path %q/%q, changed %t, data %q, %v", write.Path, selection.Path(), changed, write.Data, err)
			}
			parser := applicationmeta.Parse
			if test.environment != "" {
				parser = func(data []byte) (applicationmeta.Manifest, error) {
					return applicationmeta.ParseOverlaySource(test.wantPath, data)
				}
			}
			manifest, err := parser(write.Data)
			if err != nil || !hasSelectedProvider(manifest, id, "acme.email.selected") {
				t.Fatalf("updated selected manifest = %#v, %v", manifest.ProviderChoices(), err)
			}
		})
	}

	if _, _, _, err := providerselect.SelectedManifestWrite(root, id, "acme.email", "deploy/customer.yaml", "production", nil); !errors.Is(err, providerselect.ErrManifestWrite) || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("selector conflict = %v", err)
	}
	if _, _, _, err := providerselect.SelectedManifestWrite(root, id, "acme.email", "", "missing", nil); !errors.Is(err, providerselect.ErrManifestWrite) || !strings.Contains(err.Error(), "plystra.missing.yaml") {
		t.Fatalf("missing environment = %v", err)
	}
}

func TestSelectRejectsInvalidIdentityBeforeProjectMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	tests := []providerselect.Options{
		{Start: root, Capability: "email.send", PluginID: "acme.email"},
		{Start: root, Capability: "email.send/v1", PluginID: "Acme.Email"},
	}
	for _, options := range tests {
		if _, err := providerselect.Select(t.Context(), options); !errors.Is(err, providerselect.ErrSelect) {
			t.Fatalf("Select(%#v) = %v", options, err)
		}
	}
	after, err := os.ReadDir(root)
	if err != nil || len(after) != len(before) {
		t.Fatalf("invalid selection mutated root: before %v, after %v, %v", before, after, err)
	}
}

func TestSelectRestoresConfigurationModuleMetadataAndGeneratedOutputAfterValidationFailure(t *testing.T) {
	root := writeTransactionalProviderProject(t)
	environment := providerTestEnvironment()
	if _, err := providerselect.Select(t.Context(), providerselect.Options{
		Start:       root,
		Capability:  "email.send/v1",
		PluginID:    "acme.email.smtp",
		Environment: environment,
	}); err != nil {
		t.Fatalf("establish initial Provider selection: %v", err)
	}

	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("ReadFile(go.mod): %v", err)
	}
	goMod = append(goMod, '\n', '\n')
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
	goSumPath := filepath.Join(root, "go.sum")
	goSum, err := os.ReadFile(goSumPath)
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(goSum), "\r\n", "\n"), "\n"), "\n")
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	goSum = []byte(strings.Join(lines, "\n") + "\n")
	if err := os.WriteFile(goSumPath, goSum, 0o644); err != nil {
		t.Fatalf("WriteFile(go.sum): %v", err)
	}
	before := providerProjectTree(t, root)

	validationFailure := errors.New("injected Provider validation failure")
	var sawSelectedConfiguration bool
	var sawGeneratedProvider bool
	var sawNormalizedModuleMetadata bool
	_, err = providerselect.Select(t.Context(), providerselect.Options{
		Start:       filepath.Join(root, "local"),
		Capability:  "email.send/v1",
		PluginID:    "acme.email.local",
		Environment: environment,
		Validate: func(_ context.Context, updatedRoot string) error {
			manifestData, readErr := os.ReadFile(filepath.Join(updatedRoot, "plystra.yaml"))
			if readErr == nil {
				manifest, parseErr := applicationmeta.Parse(manifestData)
				sawSelectedConfiguration = parseErr == nil && hasSelectedProvider(manifest, mustProviderID(t, "email.send/v1"), "acme.email.local")
			}
			assembly, assemblyErr := os.ReadFile(filepath.Join(updatedRoot, "generated", "go", "assembly", "invocations_gen.go"))
			sawGeneratedProvider = assemblyErr == nil && bytes.Contains(assembly, []byte(`"acme.email.local"`)) && !bytes.Contains(assembly, []byte(`"acme.email.smtp"`))
			currentMod, modErr := os.ReadFile(filepath.Join(updatedRoot, "go.mod"))
			currentSum, sumErr := os.ReadFile(filepath.Join(updatedRoot, "go.sum"))
			sawNormalizedModuleMetadata = modErr == nil && sumErr == nil && (!bytes.Equal(currentMod, goMod) || !bytes.Equal(currentSum, goSum))
			return validationFailure
		},
	})
	if !errors.Is(err, validationFailure) {
		t.Fatalf("Select validation error = %v, want injected failure", err)
	}
	if !sawSelectedConfiguration || !sawGeneratedProvider || !sawNormalizedModuleMetadata {
		t.Fatalf("validator observations = configuration %t, generated Provider %t, normalized module metadata %t", sawSelectedConfiguration, sawGeneratedProvider, sawNormalizedModuleMetadata)
	}
	if after := providerProjectTree(t, root); !reflectProviderTreesEqual(after, before) {
		t.Fatalf("failed Provider selection did not restore the complete Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	if transactions, globErr := filepath.Glob(filepath.Join(root, ".plystra-files-*")); globErr != nil || len(transactions) != 0 {
		t.Fatalf("transaction files = %v, %v", transactions, globErr)
	}
}

func writeProviderFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mustProviderID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%s): %v", value, err)
	}
	return id
}

func hasSelectedProvider(manifest applicationmeta.Manifest, capability capabilityid.Identifier, pluginID string) bool {
	for _, choice := range manifest.ProviderChoices() {
		if choice.Capability() == capability && choice.PluginID() == pluginID {
			return true
		}
	}
	return false
}

func writeTransactionalProviderProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeProviderFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/provider-rollback

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(CLI go.sum): %v", err)
	}
	writeProviderFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeProviderFile(t, filepath.Join(root, "plystra.yaml"), "# Preserve this Provider configuration.\ncapabilities:\n  require: [email.send/v1]\n")
	contract := "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n"
	for _, provider := range []struct {
		directory string
		packageID string
		pluginID  string
		config    string
	}{
		{directory: "smtp", packageID: "smtp", pluginID: "acme.email.smtp", config: "SMTPConfig"},
		{directory: "local", packageID: "local", pluginID: "acme.email.local", config: "LocalConfig"},
	} {
		writeProviderFile(t, filepath.Join(root, provider.directory, "plugin.yaml"), "id: "+provider.pluginID+"\nprovides: [email.send/v1]\n")
		writeProviderFile(t, filepath.Join(root, provider.directory, "capabilities", "email.send", "v1", "capability.yaml"), contract)
		writeProviderFile(t, filepath.Join(root, provider.directory, "plugin.go"), fmt.Sprintf(`package %s

import (
	"context"

	configuration "example.com/acme/provider-rollback/generated/go/configuration"
	contract "example.com/acme/provider-rollback/generated/go/contracts/email/send/v1"
)

type Config = configuration.%s
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, provider.packageID, provider.config))
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize transactional Provider Project: %v", err)
	}
	return canonical
}

func providerTestEnvironment() []string {
	overrides := map[string]string{
		"GOENV":          "off",
		"GOFLAGS":        "",
		"GOPROXY":        "off",
		"GOSUMDB":        "off",
		"GOTOOLCHAIN":    "local",
		"GOWORK":         "off",
		"PLYSTRA_CONFIG": "",
		"PLYSTRA_ENV":    "",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(name)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for name := range overrides {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if name == "PLYSTRA_CONFIG" || name == "PLYSTRA_ENV" {
			continue
		}
		environment = append(environment, name+"="+overrides[name])
	}
	return environment
}

func providerProjectTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		result[filepath.ToSlash(name)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", root, err)
	}
	return result
}

func reflectProviderTreesEqual(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, data := range left {
		if !bytes.Equal(data, right[name]) {
			return false
		}
	}
	return true
}
