package implementationselect_test

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
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationselect"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestSelectedConfigurationWriteTargetsOnlyTheSelectedCurrentProjectLayer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeImplementationFile(t, filepath.Join(root, "plystra.yaml"), "# Root choices.\ninterfaces: {use: {email.send/v1: example.com/email/root.New}}\n")
	writeImplementationFile(t, filepath.Join(root, "plystra.production.yaml"), "# Production choices.\n{}\n")
	writeImplementationFile(t, filepath.Join(root, "deploy", "customer.yaml"), "# Customer choices.\ninterfaces: {require: [email.send/v1]}\n")
	id := mustInterfaceID(t, "email.send/v1")
	constructor := mustConstructor(t, "example.com/email/selected.New")

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
			write, changed, selection, err := implementationselect.SelectedConfigurationWrite(root, id, constructor, test.config, test.environment, test.ambient)
			if err != nil || !changed || write.Path != test.wantPath || selection.Path() != test.wantPath || !strings.Contains(string(write.Data), test.wantComment) {
				t.Fatalf("SelectedConfigurationWrite = path %q/%q, changed %t, data %q, %v", write.Path, selection.Path(), changed, write.Data, err)
			}
			parser := applicationmeta.Parse
			if test.environment != "" {
				parser = func(data []byte) (applicationmeta.Manifest, error) {
					return applicationmeta.ParseOverlaySource(test.wantPath, data)
				}
			}
			manifest, err := parser(write.Data)
			if err != nil || !hasImplementationChoice(manifest, id, constructor) {
				t.Fatalf("updated selected configuration = %#v, %v", manifest.ImplementationChoices(), err)
			}
		})
	}

	if _, _, _, err := implementationselect.SelectedConfigurationWrite(root, id, constructor, "deploy/customer.yaml", "production", nil); !errors.Is(err, implementationselect.ErrConfigurationWrite) || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("selector conflict = %v", err)
	}
	if _, _, _, err := implementationselect.SelectedConfigurationWrite(root, id, constructor, "", "missing", nil); !errors.Is(err, implementationselect.ErrConfigurationWrite) || !strings.Contains(err.Error(), "plystra.missing.yaml") {
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
	tests := []implementationselect.Options{
		{Start: root, InterfaceID: "email.send", Constructor: "example.com/email/smtp.New"},
		{Start: root, InterfaceID: "email.send/v1", Constructor: "example.com/email/smtp.new"},
	}
	for _, options := range tests {
		if _, err := implementationselect.Select(t.Context(), options); !errors.Is(err, implementationselect.ErrSelect) {
			t.Fatalf("Select(%#v) = %v", options, err)
		}
	}
	after, err := os.ReadDir(root)
	if err != nil || len(after) != len(before) {
		t.Fatalf("invalid selection mutated root: before %v, after %v, %v", before, after, err)
	}
}

func TestSelectRestoresConfigurationModuleMetadataAndGeneratedOutputAfterValidationFailure(t *testing.T) {
	root := writeTransactionalImplementationProject(t)
	environment := implementationTestEnvironment()
	if _, err := implementationselect.Select(t.Context(), implementationselect.Options{
		Start:       root,
		InterfaceID: "email.send/v1",
		Constructor: "example.com/acme/implementation-rollback/smtp.New",
		Environment: environment,
	}); err != nil {
		t.Fatalf("establish initial Implementation selection: %v", err)
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
	before := implementationProjectTree(t, root)

	validationFailure := errors.New("injected Implementation validation failure")
	var sawSelectedConfiguration bool
	var sawGeneratedImplementation bool
	var sawNormalizedModuleMetadata bool
	_, err = implementationselect.Select(t.Context(), implementationselect.Options{
		Start:       filepath.Join(root, "local"),
		InterfaceID: "email.send/v1",
		Constructor: "example.com/acme/implementation-rollback/local.New",
		Environment: environment,
		Validate: func(_ context.Context, updatedRoot string) error {
			manifestData, readErr := os.ReadFile(filepath.Join(updatedRoot, "plystra.yaml"))
			if readErr == nil {
				manifest, parseErr := applicationmeta.Parse(manifestData)
				sawSelectedConfiguration = parseErr == nil && hasImplementationChoice(
					manifest,
					mustInterfaceID(t, "email.send/v1"),
					mustConstructor(t, "example.com/acme/implementation-rollback/local.New"),
				)
			}
			assembly, assemblyErr := os.ReadFile(filepath.Join(updatedRoot, "generated", "go", "assembly", "interfaces_gen.go"))
			sawGeneratedImplementation = assemblyErr == nil &&
				bytes.Contains(assembly, []byte(`"example.com/acme/implementation-rollback/local.New"`)) &&
				!bytes.Contains(assembly, []byte(`"example.com/acme/implementation-rollback/smtp.New"`))
			currentMod, modErr := os.ReadFile(filepath.Join(updatedRoot, "go.mod"))
			currentSum, sumErr := os.ReadFile(filepath.Join(updatedRoot, "go.sum"))
			sawNormalizedModuleMetadata = modErr == nil && sumErr == nil && (!bytes.Equal(currentMod, goMod) || !bytes.Equal(currentSum, goSum))
			return validationFailure
		},
	})
	if !errors.Is(err, validationFailure) {
		t.Fatalf("Select validation error = %v, want injected failure", err)
	}
	if !sawSelectedConfiguration || !sawGeneratedImplementation || !sawNormalizedModuleMetadata {
		t.Fatalf("validator observations = configuration %t, generated Implementation %t, normalized module metadata %t", sawSelectedConfiguration, sawGeneratedImplementation, sawNormalizedModuleMetadata)
	}
	if after := implementationProjectTree(t, root); !equalImplementationTrees(after, before) {
		t.Fatalf("failed Implementation selection did not restore the complete Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	if transactions, globErr := filepath.Glob(filepath.Join(root, ".plystra-files-*")); globErr != nil || len(transactions) != 0 {
		t.Fatalf("transaction files = %v, %v", transactions, globErr)
	}
}

func writeTransactionalImplementationProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeImplementationFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/implementation-rollback

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(CLI go.sum): %v", err)
	}
	writeImplementationFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeImplementationFile(t, filepath.Join(root, "plystra.yaml"), "# Preserve this Implementation configuration.\ninterfaces:\n  require: [email.send/v1]\n")
	writeImplementationFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.go"), `package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`)
	for _, implementation := range []string{"smtp", "local"} {
		writeImplementationFile(t, filepath.Join(root, implementation, "implementation.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "example.com/acme/implementation-rollback/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, implementation))
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize transactional Implementation Project: %v", err)
	}
	return canonical
}

func writeImplementationFile(t testing.TB, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mustInterfaceID(t testing.TB, value string) interfaceid.Identifier {
	t.Helper()
	id, err := interfaceid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%s): %v", value, err)
	}
	return id
}

func mustConstructor(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	constructor, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%s): %v", value, err)
	}
	return constructor
}

func hasImplementationChoice(manifest applicationmeta.Manifest, id interfaceid.Identifier, constructor constructorsymbol.Symbol) bool {
	for _, choice := range manifest.ImplementationChoices() {
		if choice.InterfaceID() == id && choice.Constructor() == constructor {
			return true
		}
	}
	return false
}

func implementationTestEnvironment() []string {
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

func implementationProjectTree(t testing.TB, root string) map[string][]byte {
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

func equalImplementationTrees(left, right map[string][]byte) bool {
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
