package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicAnonymousConfigurationDefaultsStayPrivate(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "dormant"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandPointerConfigurationImplementation(t, root)
			path := filepath.Join(root, "smtp", "implementation.go")
			original := string(readCommandFile(t, root, "smtp/implementation.go"))
			write := func(literal string) {
				source := strings.Replace(original, "First string; Second string", "First string `plystra-default:\""+literal+"\"`; Second string", 1)
				writeCommandFile(t, path, source)
			}
			data := "interfaces:\n  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}\n"
			if active {
				data += "  require: [email.send/v1]\n"
			}
			data += "config: {example.com/acme/implementation-use/smtp.New: {settings: {second: supplied}}}\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), data)
			invoke := func(args ...string) string {
				t.Helper()
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") {
					t.Fatalf("%v failed or disclosed a default: code %d", args, code)
				}
				return stdout
			}
			write("PRIVATE_FIRST")
			invoke("generate")
			before := commandTree(t, filepath.Join(root, "generated"))
			inspection := invoke("inspect", "configuration", "--format", "json")
			write("PRIVATE_SECOND")
			unchanged := commandTree(t, root)
			invoke("generate", "--check")
			invoke("check")
			if invoke("inspect", "configuration", "--format", "json") != inspection {
				t.Fatal("private default entered public inspection identity")
			}
			if !reflect.DeepEqual(unchanged, commandTree(t, root)) {
				t.Fatal("read-only command changed the Project")
			}
			invoke("generate")
			if !reflect.DeepEqual(before, commandTree(t, filepath.Join(root, "generated"))) {
				t.Fatal("private default changed public generated output")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicInvalidAnonymousConfigurationTypeRedactsDefaults(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandPointerConfigurationImplementation(t, root)
	source := string(readCommandFile(t, root, "smtp/implementation.go"))
	source = strings.Replace(source, "*struct { First string; Second string }", "chan struct { First string `plystra-default:\"PRIVATE_INVALID\"`; Second string }", 1)
	writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), source)
	before := commandTree(t, root)
	for _, args := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}} {
		code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
		if code == 0 || strings.Contains(stdout+stderr, "PRIVATE_") || !strings.Contains(stderr, "not supported") {
			t.Fatalf("%v accepted an invalid type or disclosed its default", args)
		}
		if !reflect.DeepEqual(before, commandTree(t, root)) {
			t.Fatal("rejected schema changed the Project")
		}
	}
}

func writeCommandPointerConfigurationImplementation(t testing.TB, root string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), `package smtp

import (
	"context"
	contract "example.com/acme/implementation-use/interfaces/email/send/v1"
)

type Config struct { Settings *struct { First string; Second string } }
type Service struct{}

//plystra:implements email.send/v1
func New(Config) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
var _ contract.Interface = (*Service)(nil)
`)
}
