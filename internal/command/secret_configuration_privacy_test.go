package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
)

func TestPublicSecretReferenceChangesKeepGeneratedIdentity(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, ownership := range []string{"active", "dormant", "adopted"} {
			t.Run(mode+"/"+ownership, func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), `package smtp

import (
	"context"
	contract "example.com/acme/implementation-use/interfaces/email/send/v1"
	"github.com/plystra/kernel/configuration"
)

type Config struct {
	Password configuration.Secret
	Nested struct { Token configuration.Secret }
}
type Service struct{}
//plystra:implements email.send/v1
func New(Config) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: commandGoEnvironment()}, "mod", "tidy"); err != nil {
					t.Fatal(err)
				}
				selectedPath := "plystra.yaml"
				var selector []string
				switch mode {
				case "environment":
					selectedPath = "plystra.production.yaml"
					selector = []string{"--env", "production"}
				case "replacement":
					selectedPath = "deploy/customer.yaml"
					selector = []string{"--config", selectedPath}
				}
				document := func(reference string) string {
					return "config: {example.com/acme/implementation-use/smtp.New: {password: " + reference + ", nested: {token: " + reference + "}}}\n"
				}
				write := func(reference string) {
					selected := "interfaces:\n  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}\n"
					if ownership != "dormant" {
						selected += "  require: [email.send/v1]\n"
					}
					rootDocument := "{}\n"
					if ownership == "adopted" {
						rootDocument = "composition:\n  exports:\n    shared:\n      " + document(reference)
						selected += "composition:\n  adopt: [{module: example.com/acme/implementation-use, export: shared}]\n"
					} else {
						selected += document(reference)
					}
					if selectedPath == "plystra.yaml" {
						if ownership == "adopted" {
							selected = strings.Replace(selected, "composition:\n", rootDocument, 1)
						}
					} else {
						writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootDocument)
					}
					writeCommandFile(t, filepath.Join(root, filepath.FromSlash(selectedPath)), selected)
				}
				invoke := func(arguments ...string) string {
					t.Helper()
					arguments = append(arguments, selector...)
					code, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
					if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") {
						t.Fatalf("%v = %d, %q, %q", arguments, code, stdout, stderr)
					}
					return stdout
				}
				write("{env: PRIVATE_FIRST}")
				invoke("generate")
				before := commandTree(t, filepath.Join(root, "generated"))
				inspection := invoke("inspect", "configuration", "--format", "json")
				for _, reference := range []string{"{env: PRIVATE_SECOND}", "{file: /PRIVATE_FILE}"} {
					write(reference)
					unchanged := commandTree(t, root)
					invoke("generate", "--check")
					invoke("check")
					if got := invoke("inspect", "configuration", "--format", "json"); got != inspection {
						t.Fatal("Secret reference changed public inspection")
					}
					if !reflect.DeepEqual(commandTree(t, root), unchanged) {
						t.Fatal("read-only checks mutated the Project")
					}
					invoke("generate")
					if !reflect.DeepEqual(commandTree(t, filepath.Join(root, "generated")), before) {
						t.Fatal("Secret reference changed public generated artifacts")
					}
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}
