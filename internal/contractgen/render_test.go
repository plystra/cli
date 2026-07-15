package contractgen_test

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/contractgen"
)

const emailSendSchema = `id: email.send/v1
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

func TestRenderGoldenContract(t *testing.T) {
	t.Parallel()

	file, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want, err := os.ReadFile("testdata/email.send.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/contracts/email/send/v1/contract_gen.go" || file.PackageName() != "emailsendv1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated file = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedCompiles(t, file)
	returned := file.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, file.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
	repeated, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated Render = %#v, %v", repeated, err)
	}
}

func TestRenderSupportsEveryWireTypeAndPresence(t *testing.T) {
	t.Parallel()

	file, err := contractgen.Render([]byte(`id: example.types/v2
request:
  active: {type: boolean}
  count: {type: integer, required: true}
  metadata: {type: object, required: true}
  mode: {type: integer, enum: [-1, 0, 2]}
  objects: {type: array, items: object, required: true}
  ratio: {type: number}
response: {}
`))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		"Active *bool `json:\"active,omitempty\"`",
		"Count int64 `json:\"count\"`",
		"Metadata map[string]any `json:\"metadata\"`",
		"Mode *RequestMode `json:\"mode,omitempty\"`",
		"Objects []map[string]any `json:\"objects\"`",
		"Ratio *float64 `json:\"ratio,omitempty\"`",
		"RequestModeNegative1 RequestMode = -1",
		"RequestMode0 RequestMode = 0",
		"RequestMode2 RequestMode = 2",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated contract does not contain %q:\n%s", required, file.Data())
		}
	}
	assertGeneratedCompiles(t, file)
}

func TestRenderDerivesHierarchicalCapabilityPath(t *testing.T) {
	t.Parallel()

	file, err := contractgen.Render([]byte("id: authn.login.oidc.complete/v1\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if file.Path() != "generated/go/contracts/authn/login/oidc/complete/v1/contract_gen.go" || file.PackageName() != "authnloginoidccompletev1" || !bytes.Contains(file.Data(), []byte(`const CapabilityID = "authn.login.oidc.complete/v1"`)) {
		t.Fatalf("hierarchical generated file = path %q, package %q\n%s", file.Path(), file.PackageName(), file.Data())
	}
	assertGeneratedCompiles(t, file)
}

func TestRenderDisambiguatesEnumValueNames(t *testing.T) {
	t.Parallel()

	file, err := contractgen.Render([]byte("id: example.state/v1\nrequest:\n  status: {type: string, enum: [foo-bar, foo_bar, value2]}\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		`RequestStatusFooBar RequestStatus = "foo-bar"`,
		`RequestStatusValue2 RequestStatus = "foo_bar"`,
		`RequestStatusValue3 RequestStatus = "value2"`,
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated enum does not contain %q:\n%s", required, file.Data())
		}
	}
	assertGeneratedCompiles(t, file)
}

func TestRenderIgnoresNonSemanticSourceDifferences(t *testing.T) {
	t.Parallel()

	first, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render(first): %v", err)
	}
	second, err := contractgen.Render([]byte("errors: [temporarily_unavailable, authentication_failed, invalid_recipient]\r\nresponse: {status: {required: true, enum: [sent, queued], type: string}, message_id: {required: true, type: string}}\r\nrequest: {html: {type: string}, text: {type: string}, subject: {required: true, type: string}, to: {required: true, items: string, type: array}}\r\ndescription: Different words.\r\nid: email.send/v1\r\n"))
	if err != nil || first.Path() != second.Path() || !bytes.Equal(first.Data(), second.Data()) {
		t.Fatalf("non-semantic rendering differs: %v\nfirst:\n%s\nsecond:\n%s", err, first.Data(), second.Data())
	}
}

func TestRenderRejectsInvalidSchemaAndGoNameCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		also  error
	}{
		{name: "invalid schema", input: "id: example.types/v1\nrequest: []\n", also: capabilitymeta.ErrInvalidManifest},
		{name: "field collision", input: "id: example.types/v1\nrequest:\n  i_d: {type: string}\n  id: {type: string}\n", also: contractgen.ErrNameCollision},
		{name: "error collision", input: "id: example.types/v1\nerrors: [i_d, id]\n", also: contractgen.ErrNameCollision},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := contractgen.Render([]byte(test.input))
			if !errors.Is(err, contractgen.ErrRender) || !errors.Is(err, test.also) || file.Path() != "" || file.PackageName() != "" || file.Data() != nil {
				t.Fatalf("Render = %#v, %v", file, err)
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	for _, seed := range []string{emailSendSchema, "id: kernel.health/v1\n", "id: workflow.retry--now-/v2\n", "id: order.cancel/v1\nextensions: {authn: {authenticated: true}}\n", "[]\n", "id: &x example.call/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := contractgen.Render([]byte(input))
		if err != nil {
			if !errors.Is(err, contractgen.ErrRender) {
				t.Fatalf("Render returned unexpected error: %v", err)
			}
			return
		}
		second, err := contractgen.Render([]byte(input))
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("Render is not deterministic: %#v then %#v, %v", first, second, err)
		}
		assertGeneratedCompiles(t, first)
	})
}

func assertGeneratedCompiles(t testing.TB, file contractgen.File) {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, file.Path(), file.Data(), parser.AllErrors)
	if err != nil {
		t.Fatalf("parse generated Go: %v", err)
	}
	if _, err := new(types.Config).Check("generated.test", fileSet, []*ast.File{parsed}, nil); err != nil {
		t.Fatalf("type-check generated Go: %v", err)
	}
}
