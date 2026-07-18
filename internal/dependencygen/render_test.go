package dependencygen_test

import (
	"bytes"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/dependencygen"
)

const dependencyModulePath = "example.com/acme/project"

func TestRenderProducesDeterministicImmutableClientSet(t *testing.T) {
	t.Parallel()

	generated, err := dependencygen.Render(
		dependencyModulePath,
		"mailer",
		"acme.mailer",
		[]string{"policy.check/v2", "email.send/v1"},
	)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if generated.Path() != "generated/go/dependencies/mailer/dependencies_gen.go" {
		t.Fatalf("generated path = %q", generated.Path())
	}
	if _, err := parser.ParseFile(token.NewFileSet(), generated.Path(), generated.Data(), parser.AllErrors); err != nil {
		t.Fatalf("parse generated dependencies: %v\n%s", err, generated.Data())
	}
	for _, required := range []string{
		`client0 "example.com/acme/project/generated/go/clients/email/send/v1"`,
		`client1 "example.com/acme/project/generated/go/clients/policy/check/v2"`,
		"clientEmailSendV1 client0.Client",
		"clientPolicyCheckV2 client1.Client",
		"func (d Dependencies) EmailSendV1() client0.Client",
		"func (d Dependencies) PolicyCheckV2() client1.Client",
		"<generated-plugin-dependencies>",
	} {
		if !bytes.Contains(generated.Data(), []byte(required)) {
			t.Fatalf("generated dependencies omit %q:\n%s", required, generated.Data())
		}
	}
	if bytes.Index(generated.Data(), []byte("clients/email/send")) > bytes.Index(generated.Data(), []byte("clients/policy/check")) {
		t.Fatalf("generated imports are not sorted:\n%s", generated.Data())
	}
	repeated, err := dependencygen.Render(
		dependencyModulePath,
		"mailer",
		"acme.mailer",
		[]string{"email.send/v1", "policy.check/v2"},
	)
	if err != nil || repeated.Path() != generated.Path() || !bytes.Equal(repeated.Data(), generated.Data()) {
		t.Fatalf("declaration order changed generated dependencies: %v\nfirst:\n%s\nsecond:\n%s", err, generated.Data(), repeated.Data())
	}
	copyData := generated.Data()
	copyData[0] = 'X'
	if bytes.Equal(copyData, generated.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
}

func TestGeneratedDependenciesValidateAndExposeClients(t *testing.T) {
	schema := []byte("id: email.send/v1\nrequest:\n  message: {type: string, required: true}\nresponse:\n  receipt: {type: string, required: true}\n")
	contract, err := contractgen.Render(schema)
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	client, err := clientgen.Render(dependencyModulePath, schema)
	if err != nil {
		t.Fatalf("Render client: %v", err)
	}
	dependencies, err := dependencygen.Render(dependencyModulePath, "mailer", "acme.mailer", []string{"email.send/v1"})
	if err != nil {
		t.Fatalf("Render dependencies: %v", err)
	}

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+dependencyModulePath+"\n\ngo 1.26\n")
	writeBytes(t, filepath.Join(root, filepath.FromSlash(contract.Path())), contract.Data())
	writeBytes(t, filepath.Join(root, filepath.FromSlash(client.Path())), client.Data())
	writeBytes(t, filepath.Join(root, filepath.FromSlash(dependencies.Path())), dependencies.Data())
	writeFile(t, filepath.Join(root, "generated", "go", "dependencies", "mailer", "dependencies_gen_test.go"), generatedRuntimeTest)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(filteredEnvironment(os.Environ(), "GOENV", "GOFLAGS", "GOPROXY", "GOSUMDB", "GOWORK"),
		"GOENV=off",
		"GOFLAGS=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOWORK=off",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated dependency runtime test: %v\n%s", err, output)
	}
}

func TestRenderRejectsInvalidAndCollidingRequirements(t *testing.T) {
	t.Parallel()

	invalid := []struct {
		name       string
		modulePath string
		pluginName string
		pluginID   string
		required   []string
		also       error
	}{
		{name: "module", pluginName: "mailer", pluginID: "acme.mailer", required: []string{"email.send/v1"}},
		{name: "plugin name", modulePath: dependencyModulePath, pluginName: "Mailer", pluginID: "acme.mailer", required: []string{"email.send/v1"}},
		{name: "plugin id", modulePath: dependencyModulePath, pluginName: "mailer", pluginID: "Acme.Mailer", required: []string{"email.send/v1"}},
		{name: "empty", modulePath: dependencyModulePath, pluginName: "mailer", pluginID: "acme.mailer"},
		{name: "capability", modulePath: dependencyModulePath, pluginName: "mailer", pluginID: "acme.mailer", required: []string{"not-a-capability"}},
		{name: "duplicate", modulePath: dependencyModulePath, pluginName: "mailer", pluginID: "acme.mailer", required: []string{"email.send/v1", "email.send/v1"}},
		{name: "identifier collision", modulePath: dependencyModulePath, pluginName: "mailer", pluginID: "acme.mailer", required: []string{"user.id/v1", "user.i-d/v1"}, also: dependencygen.ErrIdentifierCollision},
	}
	for _, test := range invalid {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := dependencygen.Render(test.modulePath, test.pluginName, test.pluginID, test.required)
			if generated.Path() != "" || generated.Data() != nil || !errors.Is(err, dependencygen.ErrRender) || !errors.Is(err, dependencygen.ErrInvalidInput) && test.also == nil || test.also != nil && !errors.Is(err, test.also) {
				t.Fatalf("Render = %#v, %v", generated, err)
			}
		})
	}
}

func writeFile(t testing.TB, name, data string) {
	t.Helper()
	writeBytes(t, name, []byte(data))
}

func writeBytes(t testing.TB, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func filteredEnvironment(environment []string, removed ...string) []string {
	keys := make(map[string]struct{}, len(removed))
	for _, key := range removed {
		keys[strings.ToUpper(key)] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, remove := keys[strings.ToUpper(key)]; !remove {
			result = append(result, entry)
		}
	}
	return result
}

const generatedRuntimeTest = `package dependencies

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	emailsendv1 "example.com/acme/project/generated/go/clients/email/send/v1"
	contract "example.com/acme/project/generated/go/contracts/email/send/v1"
)

type testHandle struct{ available bool }

func (h testHandle) Available() bool { return h.available }

func (testHandle) Invoke(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Receipt: "sent:" + request.Message}, nil
}

func TestGeneratedDependencies(t *testing.T) {
	client := emailsendv1.New(testHandle{available: true})
	dependencies, err := New(client)
	if err != nil || !dependencies.Valid() || !emailsendv1.Available(dependencies.EmailSendV1()) {
		t.Fatalf("New = %v, %v", dependencies, err)
	}
	response, err := dependencies.EmailSendV1().Send(context.Background(), contract.Request{Message: "hello"})
	if err != nil || response.Receipt != "sent:hello" {
		t.Fatalf("Send = %#v, %v", response, err)
	}
	if dependencies, err := New(emailsendv1.Client{}); dependencies.Valid() || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(unavailable) = %v, %v", dependencies, err)
	}
	var zero Dependencies
	if zero.Valid() || emailsendv1.Available(zero.EmailSendV1()) {
		t.Fatalf("zero dependencies are available")
	}
	if _, err := zero.EmailSendV1().Send(context.Background(), contract.Request{}); !errors.Is(err, emailsendv1.ErrUnavailable) {
		t.Fatalf("zero client error = %v", err)
	}
	for _, formatted := range []string{fmt.Sprintf("%v", dependencies), fmt.Sprintf("%#v", dependencies)} {
		if !strings.Contains(formatted, "generated-plugin-dependencies") {
			t.Fatalf("dependencies formatting = %q", formatted)
		}
	}
}
`
