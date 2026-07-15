package providergen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/providergen"
)

const (
	testModulePath  = "example.com/acme/project"
	emailSendSchema = `id: email.send/v1
description: Sends an email message.
request:
  to: {type: array, items: string, required: true}
  subject: {type: string, required: true}
  text: {type: string}
  html: {type: string}
response:
  message_id: {type: string, required: true}
  status: {type: string, enum: [queued, sent], required: true}
errors: [invalid_recipient, authentication_failed, temporarily_unavailable]
`
)

func TestRenderGoldenProviderInterface(t *testing.T) {
	t.Parallel()

	file, err := providergen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want, err := os.ReadFile("testdata/email.send.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/providers/email/send/v1/provider_gen.go" || file.PackageName() != "emailsendv1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated file = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	contract, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	assertGeneratedModuleCompiles(t, contract, file)
	returned := file.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, file.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
	repeated, err := providergen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated Render = %#v, %v", repeated, err)
	}
}

func TestRenderDerivesOperationNameAndVersionedImport(t *testing.T) {
	t.Parallel()

	file, err := providergen.Render("example.com/acme/project/v3", []byte("id: gateway.send-http/v12\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		`contract "example.com/acme/project/v3/generated/go/contracts/gateway/send-http/v12"`,
		`SendHTTP(ctx context.Context, request contract.Request) (contract.Response, error)`,
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated provider does not contain %q:\n%s", required, file.Data())
		}
	}
}

func TestRenderIgnoresNonSemanticSourceDifferences(t *testing.T) {
	t.Parallel()

	first, err := providergen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render(first): %v", err)
	}
	second, err := providergen.Render(testModulePath, []byte("errors: [temporarily_unavailable, authentication_failed, invalid_recipient]\r\nresponse: {status: {required: true, enum: [sent, queued], type: string}, message_id: {required: true, type: string}}\r\nrequest: {html: {type: string}, text: {type: string}, subject: {required: true, type: string}, to: {required: true, items: string, type: array}}\r\ndescription: Different words.\r\nid: email.send/v1\r\n"))
	if err != nil || first.Path() != second.Path() || !bytes.Equal(first.Data(), second.Data()) {
		t.Fatalf("non-semantic rendering differs: %v\nfirst:\n%s\nsecond:\n%s", err, first.Data(), second.Data())
	}
}

func TestRenderRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modulePath string
		schema     string
		also       error
	}{
		{name: "empty module path", schema: emailSendSchema},
		{name: "invalid module path", modulePath: "example.com/acme/../project", schema: emailSendSchema},
		{name: "invalid schema", modulePath: testModulePath, schema: "id: example.call/v1\nrequest: []\n", also: capabilitymeta.ErrInvalidManifest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := providergen.Render(test.modulePath, []byte(test.schema))
			if !errors.Is(err, providergen.ErrRender) || test.also != nil && !errors.Is(err, test.also) || file.Path() != "" || file.PackageName() != "" || file.Data() != nil {
				t.Fatalf("Render = %#v, %v", file, err)
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	for _, seed := range []string{emailSendSchema, "id: kernel.health/v1\n", "[]\n", "id: &x example.call/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := providergen.Render(testModulePath, []byte(input))
		if err != nil {
			if !errors.Is(err, providergen.ErrRender) {
				t.Fatalf("Render returned unexpected error: %v", err)
			}
			return
		}
		second, err := providergen.Render(testModulePath, []byte(input))
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("Render is not deterministic: %#v then %#v, %v", first, second, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), first.Path(), first.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse generated Go: %v", err)
		}
	})
}

func assertGeneratedModuleCompiles(t testing.TB, contract contractgen.File, provider providergen.File) {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, provider.Path(), provider.Data())
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+testModulePath+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("compile generated module: %v\n%s", err, output)
	}
}

func writeGeneratedFile(t testing.TB, root, relative string, data []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
