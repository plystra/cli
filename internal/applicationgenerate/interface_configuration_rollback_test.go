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
	}{
		{
			name:         "default",
			selectedPath: "plystra.yaml",
		},
		{
			name:            "environment",
			environmentName: "production",
			selectedPath:    "plystra.production.yaml",
		},
		{
			name:          "full replacement",
			configuration: "deploy/customer.yaml",
			selectedPath:  "deploy/customer.yaml",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			appRoot := filepath.Join(parent, "app")
			dependencyRoot := filepath.Join(parent, "platform")
			writeInterfaceRollbackDependency(t, dependencyRoot)
			writeApplicationModule(t, appRoot, "example.com/acme/rollback")

			goModPath := filepath.Join(appRoot, "go.mod")
			goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf(`
require example.com/platform v1.0.0

replace example.com/platform => %s
`, filepath.ToSlash(dependencyRoot))
			writeFile(t, goModPath, goMod)
			writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# Shared root configuration.\n{}\n")
			switch {
			case test.environmentName != "":
				writeFile(t, filepath.Join(appRoot, test.selectedPath), "# Production configuration.\n"+interfaceRollbackConfiguration("example.com/platform/smtp.New"))
			case test.configuration != "":
				writeFile(t, filepath.Join(appRoot, test.selectedPath), "# Complete customer configuration.\n"+interfaceRollbackConfiguration("example.com/platform/smtp.New"))
			default:
				writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# Shared root configuration.\n"+interfaceRollbackConfiguration("example.com/platform/smtp.New"))
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
			if err != nil || !initial.Report().Clean() {
				t.Fatalf("initial Generate = changes %#v, %v", initial.Report().Changes(), err)
			}
			if initial.ConfigurationPath() != test.selectedPath {
				t.Fatalf("initial selection = selected %q", initial.ConfigurationPath())
			}

			before := snapshotTree(t, appRoot)
			writeFile(t, filepath.Join(appRoot, test.selectedPath), interfaceRollbackConfiguration("example.com/platform/memory.New"))
			expectedAfterEdit := snapshotTree(t, appRoot)
			validationFailure := errors.New("reject changed Interface selection")
			sawUpdatedTransaction := false
			options.Validate = func(_ context.Context, updatedRoot string) error {
				assembly := readFile(t, updatedRoot, "generated/go/assembly/interfaces_gen.go")
				sawUpdatedTransaction = bytes.Contains(assembly, []byte(`"example.com/platform/memory.New"`)) &&
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
			if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, expectedAfterEdit) {
				t.Fatalf("failed generation did not restore the complete Project after the source edit:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoTransactions(t, appRoot)
		})
	}
}

func writeInterfaceRollbackDependency(t testing.TB, root string) {
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
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
}

func interfaceRollbackConfiguration(selectedConstructor string) string {
	return fmt.Sprintf(`interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: %s
`, selectedConstructor)
}
