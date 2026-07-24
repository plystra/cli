package applicationgenerate_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGenerateRollsBackInterfaceConfigurationForEverySelectionMode(t *testing.T) {
	tests := []struct {
		name            string
		environmentName string
		configuration   string
		selectedPath    string
		maintenancePath string
	}{
		{
			name:            "default",
			selectedPath:    "plystra.yaml",
			maintenancePath: "plystra.yaml",
		},
		{
			name:            "environment",
			environmentName: "production",
			selectedPath:    "plystra.production.yaml",
			maintenancePath: "plystra.yaml",
		},
		{
			name:            "full replacement",
			configuration:   "deploy/customer.yaml",
			selectedPath:    "deploy/customer.yaml",
			maintenancePath: "deploy/customer.yaml",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			appRoot := filepath.Join(parent, "app")
			dependencyRoot := filepath.Join(parent, "platform")
			writeInterfaceRollbackDependency(t, dependencyRoot, "example.com/platform/smtp.New")
			writeApplicationModule(t, appRoot, "example.com/acme/rollback")

			goModPath := filepath.Join(appRoot, "go.mod")
			goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf(`
require example.com/platform v1.0.0

replace example.com/platform => %s
`, filepath.ToSlash(dependencyRoot))
			writeFile(t, goModPath, goMod)
			writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# Shared root configuration.\n{}\n")
			if test.environmentName != "" {
				writeFile(t, filepath.Join(appRoot, test.selectedPath), "# Sparse production configuration.\n{}\n")
			}
			if test.configuration != "" {
				writeFile(t, filepath.Join(appRoot, test.selectedPath), "# Complete customer configuration.\n{}\n")
			}

			environment := goEnvironment(map[string]string{
				"GOWORK":  "off",
				"GOPROXY": "off",
				"GOSUMDB": "off",
			})
			options := applicationgenerate.Options{
				Start:             appRoot,
				ConfigurationPath: test.configuration,
				EnvironmentName:   test.environmentName,
				Environment:       environment,
				Validate:          func(context.Context, string) error { return nil },
			}
			initial, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !initial.Report().Clean() || !initial.ConfigurationChanged() {
				t.Fatalf("initial Generate = changes %#v configuration changed %t, %v", initial.Report().Changes(), initial.ConfigurationChanged(), err)
			}
			if initial.ConfigurationPath() != test.selectedPath || initial.ConfigurationMaintenancePath() != test.maintenancePath {
				t.Fatalf("initial selection = selected %q maintenance %q", initial.ConfigurationPath(), initial.ConfigurationMaintenancePath())
			}

			before := snapshotTree(t, appRoot)
			writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), interfaceRollbackConfiguration("example.com/platform/memory.New"))
			validationFailure := errors.New("reject changed Interface selection")
			sawUpdatedTransaction := false
			options.Validate = func(_ context.Context, updatedRoot string) error {
				configuration := readFile(t, updatedRoot, test.maintenancePath)
				assembly := readFile(t, updatedRoot, "generated/go/assembly/interfaces_gen.go")
				sawUpdatedTransaction = bytes.Contains(configuration, []byte("example.com/platform/memory.New")) &&
					!bytes.Contains(configuration, []byte("example.com/platform/smtp.New")) &&
					bytes.Contains(assembly, []byte(`"example.com/platform/memory.New"`)) &&
					!bytes.Contains(assembly, []byte(`"example.com/platform/smtp.New"`))
				return validationFailure
			}

			_, err = applicationgenerate.Generate(t.Context(), options)
			if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, validationFailure) {
				t.Fatalf("Generate validation failure = %v", err)
			}
			if !sawUpdatedTransaction {
				t.Fatal("validation did not observe the recomposed configuration and generated Interface assembly")
			}
			if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed generation did not restore the complete Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoTransactions(t, appRoot)
		})
	}
}

func writeInterfaceRollbackDependency(t testing.TB, root, selectedConstructor string) {
	t.Helper()
	writeModule(t, root, "example.com/platform", "")
	writeGenerationGraphInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	for _, implementation := range []string{"smtp", "memory"} {
		writeFile(t, filepath.Join(root, implementation, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	sendv1 "example.com/platform/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`, implementation))
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), interfaceRollbackConfiguration(selectedConstructor))
}

func interfaceRollbackConfiguration(selectedConstructor string) string {
	return fmt.Sprintf(`interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: %s
`, selectedConstructor)
}
