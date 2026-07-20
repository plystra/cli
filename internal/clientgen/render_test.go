package clientgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
)

var (
	_ clientgen.AliasView           = aliasresolution.Alias{}
	_ clientgen.CanonicalTargetView = generation.CapabilityView{}
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

func TestRenderGoldenCapabilityClient(t *testing.T) {
	t.Parallel()

	file, err := clientgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want, err := os.ReadFile("testdata/email.send.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/clients/email/send/v1/client_gen.go" || file.PackageName() != "emailsendv1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated file = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	contract, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	assertGeneratedModuleCompiles(t, contract, invocation, file)
	returned := file.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, file.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
	repeated, err := clientgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated Render = %#v, %v", repeated, err)
	}
}

func TestRenderGoldenAliasClientsForwardToOneCanonicalTarget(t *testing.T) {
	t.Parallel()

	context, result := resolvedClientAliases(t)
	targetID := clientCapabilityID(t, "email.send/v1")
	target, ok := context.Capability(targetID)
	if !ok {
		t.Fatal("email.send/v1 target is absent")
	}
	aliases := result.Aliases()
	if len(aliases) != 3 {
		t.Fatalf("Aliases = %#v", aliases)
	}
	files := make([]clientgen.File, len(aliases))
	for index, alias := range aliases {
		file, err := clientgen.RenderAlias(testModulePath, alias, target)
		if err != nil {
			t.Fatalf("RenderAlias(%s): %v", alias.ID(), err)
		}
		files[index] = file
		got := strings.Join(strings.Fields(string(file.Data())), " ")
		for _, required := range []string{
			`canonicalclient "example.com/acme/project/generated/go/clients/email/send/v1"`,
			`contract "example.com/acme/project/generated/go/contracts/email/send/v1"`,
			`target canonicalclient.Client`,
			`return canonicalclient.Available(c.target)`,
			`return c.target.Send(ctx, request)`,
		} {
			if !strings.Contains(got, required) {
				t.Fatalf("generated Alias %s omits %q:\n%s", alias.ID(), required, file.Data())
			}
		}
		if bytes.Contains(file.Data(), []byte("kernelinvocation")) || bytes.Contains(file.Data(), []byte("generated/go/invocation/")) {
			t.Fatalf("Alias %s bypasses the canonical client:\n%s", alias.ID(), file.Data())
		}
	}
	if got := []string{files[0].Path(), files[1].Path(), files[2].Path()}; !slices.Equal(got, []string{
		"generated/go/clients/compat/send/v1/client_gen.go",
		"generated/go/clients/email/dispatch/v1/client_gen.go",
		"generated/go/clients/mail/deliver/v1/client_gen.go",
	}) {
		t.Fatalf("Alias paths = %v", got)
	}
	want, err := os.ReadFile("testdata/mail.deliver.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(Alias golden): %v\n%s", err, files[2].Data())
	}
	if !bytes.Equal(files[2].Data(), want) || files[2].PackageName() != "maildeliverv1" {
		t.Fatalf("generated Alias = package %q\n%s\nwant:\n%s", files[2].PackageName(), files[2].Data(), want)
	}

	contract, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	canonical, err := clientgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render canonical client: %v", err)
	}
	allClients := append([]clientgen.File{canonical}, files...)
	assertGeneratedModuleCompiles(t, contract, invocation, allClients...)
	assertGeneratedAliasRuns(t, contract, invocation, canonical, files[2])

	repeated, err := clientgen.RenderAlias(testModulePath, aliases[2], target)
	if err != nil || repeated.Path() != files[2].Path() || repeated.PackageName() != files[2].PackageName() || !bytes.Equal(repeated.Data(), files[2].Data()) {
		t.Fatalf("repeated RenderAlias = %#v, %v", repeated, err)
	}
}

func TestRenderAliasSupportsIntrinsicCanonicalTarget(t *testing.T) {
	t.Parallel()

	schema := []byte("id: kernel.health/v1\nresponse:\n  healthy: {type: boolean, required: true}\n")
	canonical, err := capabilitymeta.NormalizeSchema(schema)
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	context, err := generation.NewContext(generation.Input{
		Capabilities: []generation.CapabilityInput{{ContractJSON: canonical, Intrinsic: true, Exposure: generation.Exposure{Go: true}}},
		Requirements: []string{"kernel.health/v1"},
		CapabilityAliases: []generation.CapabilityAliasInput{{
			ID:       "health.status/v1",
			Target:   "kernel.health/v1",
			Exposure: generation.Exposure{Go: true},
			Sources:  []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	result, err := aliasresolution.Resolve(context, []aliasExtensionOutput{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	targetID := clientCapabilityID(t, "kernel.health/v1")
	target, _ := context.Capability(targetID)
	file, err := clientgen.RenderAlias(testModulePath, result.Aliases()[0], target)
	if err != nil {
		t.Fatalf("RenderAlias: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	if file.Path() != "generated/go/clients/health/status/v1/client_gen.go" || !strings.Contains(got, `canonicalclient "example.com/acme/project/generated/go/clients/kernel/health/v1"`) || !strings.Contains(got, `return c.target.Health(ctx, request)`) {
		t.Fatalf("intrinsic Alias = path %q\n%s", file.Path(), file.Data())
	}
	contract, err := contractgen.Render(schema)
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, schema)
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	targetClient, err := clientgen.Render(testModulePath, schema)
	if err != nil {
		t.Fatalf("Render target client: %v", err)
	}
	assertGeneratedModuleCompiles(t, contract, invocation, targetClient, file)
}

func TestRenderDerivesOperationNameAndVersionedImport(t *testing.T) {
	t.Parallel()

	file, err := clientgen.Render("example.com/acme/project/v3", []byte("id: gateway.send-http/v12\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		`contract "example.com/acme/project/v3/generated/go/contracts/gateway/send-http/v12"`,
		`type Handle interface`,
		`Available() bool`,
		`Invoke(context.Context, contract.Request) (contract.Response, error)`,
		`handle Handle`,
		`func New(handle Handle) Client`,
		`func (c Client) SendHTTP(ctx context.Context, request contract.Request) (contract.Response, error)`,
		`return contract.Response{}, ErrUnavailable`,
		`return c.handle.Invoke(ctx, request)`,
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated client does not contain %q:\n%s", required, file.Data())
		}
	}
}

func TestRenderDerivesHierarchicalCapabilityPath(t *testing.T) {
	t.Parallel()

	file, err := clientgen.Render(testModulePath, []byte("id: authn.login.oidc.complete/v1\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	if file.Path() != "generated/go/clients/authn/login/oidc/complete/v1/client_gen.go" || file.PackageName() != "authnloginoidccompletev1" || !strings.Contains(got, `func (c Client) Complete(ctx context.Context, request contract.Request) (contract.Response, error)`) {
		t.Fatalf("hierarchical generated file = path %q, package %q\n%s", file.Path(), file.PackageName(), file.Data())
	}
}

func TestRenderAvoidsAvailableOperationCollision(t *testing.T) {
	t.Parallel()

	schema := []byte("id: status.available/v1\n")
	file, err := clientgen.Render(testModulePath, schema)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		`func Available(c Client) bool`,
		`func (c Client) Available(ctx context.Context, request contract.Request) (contract.Response, error)`,
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated client does not contain %q:\n%s", required, file.Data())
		}
	}
	contract, err := contractgen.Render(schema)
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, schema)
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	assertGeneratedModuleCompiles(t, contract, invocation, file)
}

func TestRenderIgnoresNonSemanticSourceDifferences(t *testing.T) {
	t.Parallel()

	first, err := clientgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render(first): %v", err)
	}
	second, err := clientgen.Render(testModulePath, []byte("errors: [temporarily_unavailable, authentication_failed, invalid_recipient]\r\nresponse: {status: {required: true, enum: [sent, queued], type: string}, message_id: {required: true, type: string}}\r\nrequest: {html: {type: string}, text: {type: string}, subject: {required: true, type: string}, to: {required: true, items: string, type: array}}\r\ndescription: Different words.\r\nid: email.send/v1\r\n"))
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
			file, err := clientgen.Render(test.modulePath, []byte(test.schema))
			if !errors.Is(err, clientgen.ErrRender) || test.also != nil && !errors.Is(err, test.also) || file.Path() != "" || file.PackageName() != "" || file.Data() != nil {
				t.Fatalf("Render = %#v, %v", file, err)
			}
		})
	}
}

func TestRenderAliasRejectsInconsistentFinalMappings(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	aliasID := clientCapabilityID(t, "mail.deliver/v1")
	targetID := clientCapabilityID(t, "email.send/v1")
	validAlias := clientAliasView{
		id:         aliasID,
		target:     targetID,
		digest:     digest,
		exposure:   generation.Exposure{Go: true, HTTP: true},
		deprecated: "Use email.send/v1 instead.",
	}
	validTarget := clientTargetView{id: targetID, digest: digest, exposure: generation.Exposure{Go: true, HTTP: true}}
	tests := []struct {
		name       string
		modulePath string
		alias      clientgen.AliasView
		target     clientgen.CanonicalTargetView
		want       string
	}{
		{name: "module", modulePath: "../app", alias: validAlias, target: validTarget, want: "Go Module path"},
		{name: "absent Alias", modulePath: testModulePath, target: validTarget, want: "Alias view is absent"},
		{name: "absent target", modulePath: testModulePath, alias: validAlias, want: "target view is absent"},
		{name: "self target", modulePath: testModulePath, alias: withClientAlias(validAlias, func(value *clientAliasView) { value.id = targetID }), target: validTarget, want: "cannot target itself"},
		{name: "reserved Alias", modulePath: testModulePath, alias: withClientAlias(validAlias, func(value *clientAliasView) { value.id = clientCapabilityID(t, "kernel.compat/v1") }), target: validTarget, want: "reserved kernel.*"},
		{name: "version mismatch", modulePath: testModulePath, alias: withClientAlias(validAlias, func(value *clientAliasView) { value.id = clientCapabilityID(t, "mail.deliver/v2") }), target: validTarget, want: "same version"},
		{name: "wrong target", modulePath: testModulePath, alias: validAlias, target: withClientTarget(validTarget, func(value *clientTargetView) { value.id = clientCapabilityID(t, "audit.write/v1") }), want: "received canonical target"},
		{name: "exposure broadening", modulePath: testModulePath, alias: validAlias, target: withClientTarget(validTarget, func(value *clientTargetView) { value.exposure.HTTP = false }), want: "exposure broadens"},
		{name: "no Go exposure", modulePath: testModulePath, alias: withClientAlias(validAlias, func(value *clientAliasView) { value.exposure.Go = false }), target: validTarget, want: "not exposed to generated Go"},
		{name: "invalid target digest", modulePath: testModulePath, alias: validAlias, target: withClientTarget(validTarget, func(value *clientTargetView) { value.digest = "sha256:not-a-digest" }), want: "target digest"},
		{name: "digest mismatch", modulePath: testModulePath, alias: withClientAlias(validAlias, func(value *clientAliasView) { value.digest = "sha256:" + strings.Repeat("b", 64) }), target: validTarget, want: "target digest"},
		{name: "invalid deprecation", modulePath: testModulePath, alias: withClientAlias(validAlias, func(value *clientAliasView) { value.deprecated = "invalid\x00message" }), target: validTarget, want: "deprecation metadata"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := clientgen.RenderAlias(test.modulePath, test.alias, test.target)
			if !errors.Is(err, clientgen.ErrRender) || !strings.Contains(err.Error(), test.want) || file.Path() != "" || file.PackageName() != "" || file.Data() != nil {
				t.Fatalf("RenderAlias = %#v, %v; want %q", file, err, test.want)
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	for _, seed := range []string{emailSendSchema, "id: kernel.health/v1\n", "id: workflow.retry--now-/v2\n", "id: order.cancel/v1\nextensions: {authn: {authenticated: true}}\n", "[]\n", "id: &x example.call/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := clientgen.Render(testModulePath, []byte(input))
		if err != nil {
			if !errors.Is(err, clientgen.ErrRender) {
				t.Fatalf("Render returned unexpected error: %v", err)
			}
			return
		}
		second, err := clientgen.Render(testModulePath, []byte(input))
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("Render is not deterministic: %#v then %#v, %v", first, second, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), first.Path(), first.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse generated Go: %v", err)
		}
	})
}

func FuzzRenderAliasDeprecation(f *testing.F) {
	for _, seed := range []string{"", "Use email.send/v1 instead.", "First line.\nSecond line.", "carriage\rreturn", "invalid\x00message"} {
		f.Add(seed)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	aliasID, _ := generation.ParseCapabilityID("mail.deliver/v1")
	targetID, _ := generation.ParseCapabilityID("email.send/v1")
	target := clientTargetView{id: targetID, digest: digest, exposure: generation.Exposure{Go: true}}
	f.Fuzz(func(t *testing.T, deprecation string) {
		alias := clientAliasView{id: aliasID, target: targetID, digest: digest, exposure: generation.Exposure{Go: true}, deprecated: deprecation}
		first, err := clientgen.RenderAlias(testModulePath, alias, target)
		if len(deprecation) > 1024 || !utf8.ValidString(deprecation) || strings.ContainsRune(deprecation, '\x00') {
			if !errors.Is(err, clientgen.ErrRender) {
				t.Fatalf("RenderAlias invalid deprecation error = %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("RenderAlias: %v", err)
		}
		second, err := clientgen.RenderAlias(testModulePath, alias, target)
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("RenderAlias is not deterministic: %#v then %#v, %v", first, second, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), first.Path(), first.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse generated Alias Go: %v\n%s", err, first.Data())
		}
	})
}

type aliasExtensionOutput struct{}

func (aliasExtensionOutput) PluginID() string { return "" }
func (aliasExtensionOutput) Output() generation.NormalizedOutput {
	return generation.NormalizedOutput{}
}

type clientAliasView struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	digest     string
	exposure   generation.Exposure
	deprecated string
}

func (a clientAliasView) ID() generation.CapabilityID     { return a.id }
func (a clientAliasView) Target() generation.CapabilityID { return a.target }
func (a clientAliasView) TargetContractDigest() string    { return a.digest }
func (a clientAliasView) Exposure() generation.Exposure   { return a.exposure }
func (a clientAliasView) Deprecated() string              { return a.deprecated }

type clientTargetView struct {
	id       generation.CapabilityID
	digest   string
	exposure generation.Exposure
}

func (t clientTargetView) ID() generation.CapabilityID   { return t.id }
func (t clientTargetView) ContractDigest() string        { return t.digest }
func (t clientTargetView) Exposure() generation.Exposure { return t.exposure }

func withClientAlias(value clientAliasView, mutate func(*clientAliasView)) clientAliasView {
	mutate(&value)
	return value
}

func withClientTarget(value clientTargetView, mutate func(*clientTargetView)) clientTargetView {
	mutate(&value)
	return value
}

func clientCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", value, err)
	}
	return id
}

func resolvedClientAliases(t testing.TB) (generation.Context, aliasresolution.Result) {
	t.Helper()
	contract, err := capabilitymeta.NormalizeSchema([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	target := clientCapabilityID(t, "email.send/v1")
	exposure := generation.Exposure{Go: true, HTTP: true, JavaScript: true}
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "example.email",
			ModulePath:        "example.com/plugins/email",
			Provides:          []string{target.String()},
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: contract, Exposure: exposure}},
		Requirements: []string{target.String()},
		Providers:    []generation.ProviderInput{{Capability: target.String(), Plugin: "example.email"}},
		CapabilityAliases: []generation.CapabilityAliasInput{
			{ID: "mail.deliver/v1", Target: target.String(), Exposure: exposure, Deprecated: "Use email.send/v1 instead.\nScheduled removal in v2.", Sources: []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}}},
			{ID: "email.dispatch/v1", Target: target.String(), Exposure: exposure, Sources: []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}}},
			{ID: "compat.send/v1", Target: target.String(), Exposure: generation.Exposure{Go: true}, Sources: []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}}},
		},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	result, err := aliasresolution.Resolve(context, []aliasExtensionOutput{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return context, result
}

func assertGeneratedModuleCompiles(t testing.TB, contract contractgen.File, invocation invocationgen.File, clients ...clientgen.File) {
	t.Helper()
	root := prepareGeneratedClientModule(t, contract, invocation, clients...)
	runGeneratedClientModuleTests(t, root)
}

func assertGeneratedAliasRuns(
	t testing.TB,
	contract contractgen.File,
	invocation invocationgen.File,
	canonical clientgen.File,
	alias clientgen.File,
) {
	t.Helper()
	root := prepareGeneratedClientModule(t, contract, invocation, canonical, alias)
	writeGeneratedFile(t, root, "generated/go/clients/mail/deliver/v1/client_gen_test.go", []byte(`package maildeliverv1_test

import (
	"context"
	"testing"

	canonicalclient "example.com/acme/project/generated/go/clients/email/send/v1"
	aliasclient "example.com/acme/project/generated/go/clients/mail/deliver/v1"
	contract "example.com/acme/project/generated/go/contracts/email/send/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/email/send/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestAliasForwardsThroughCanonicalGeneratedClient(t *testing.T) {
	calls := 0
	target := kernelinvocation.NewTestHandle(true, func(_ context.Context, request contract.Request) (contract.Response, error) {
		calls++
		if len(request.To) != 1 || request.To[0] != "person@example.com" || request.Subject != "Welcome" {
			t.Fatalf("request = %#v", request)
		}
		return contract.Response{MessageID: "message-1", Status: contract.ResponseStatusSent}, nil
	})
	canonical := canonicalclient.New(applicationinvocation.New(target))
	alias := aliasclient.New(canonical)
	if !aliasclient.Available(alias) {
		t.Fatal("Alias did not preserve canonical availability")
	}
	response, err := alias.Deliver(context.Background(), contract.Request{To: []string{"person@example.com"}, Subject: "Welcome"})
	if err != nil || response.MessageID != "message-1" || calls != 1 {
		t.Fatalf("Deliver = %#v, %v, calls %d", response, err, calls)
	}
}
`))
	runGeneratedClientModuleTests(t, root)
}

func prepareGeneratedClientModule(t testing.TB, contract contractgen.File, invocation invocationgen.File, clients ...clientgen.File) string {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	for _, client := range clients {
		writeGeneratedFile(t, root, client.Path(), client.Data())
	}
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(`package invocation

import (
	"context"
	"strings"
)

type Handle[Request, Response any] struct {
	available bool
	invoke func(context.Context, Request) (Response, error)
}

func NewTestHandle[Request, Response any](available bool, invoke func(context.Context, Request) (Response, error)) Handle[Request, Response] {
	return Handle[Request, Response]{available: available, invoke: invoke}
}

func (h Handle[Request, Response]) Available() bool { return h.available }

func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	if h.invoke != nil {
		return h.invoke(ctx, request)
	}
	var response Response
	return response, nil
}

type ErrorCode string

const (
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorNotFound ErrorCode = "not_found"
	ErrorConflict ErrorCode = "conflict"
	ErrorDenied ErrorCode = "denied"
	ErrorUnauthenticated ErrorCode = "unauthenticated"
	ErrorUnavailable ErrorCode = "unavailable"
	ErrorTimeout ErrorCode = "timeout"
	ErrorCancelled ErrorCode = "cancelled"
	ErrorResultUnknown ErrorCode = "result_unknown"
	ErrorInternal ErrorCode = "internal"
	ErrorVersionIncompatible ErrorCode = "version_incompatible"
)

func (c ErrorCode) String() string { return string(c) }
func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorDenied, ErrorUnauthenticated, ErrorUnavailable, ErrorTimeout, ErrorCancelled, ErrorResultUnknown, ErrorInternal, ErrorVersionIncompatible:
		return true
	default:
		return false
	}
}

func ValidDetailCode(value string) bool {
	return value == "" || len(value) <= 128 && !strings.ContainsAny(value, " \r\n\x00")
}

type Error struct {
	code ErrorCode
	detail string
}

func (e *Error) Error() string { return "invocation error" }
func (e *Error) Code() ErrorCode { if e == nil { return "" }; return e.code }
func (e *Error) DetailCode() string { if e == nil { return "" }; return e.detail }
`))
	moduleFile := "module " + testModulePath + "\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"
	writeGeneratedFile(t, root, "go.mod", []byte(moduleFile))
	return root
}

func runGeneratedClientModuleTests(t testing.TB, root string) {
	t.Helper()
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
