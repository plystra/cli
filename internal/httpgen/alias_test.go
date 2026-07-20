package httpgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/httpgen"
)

var (
	_ httpgen.AliasView           = aliasresolution.Alias{}
	_ httpgen.CanonicalTargetView = generation.CapabilityView{}
)

func TestRenderAliasRoutesForwardToOneCanonicalHandler(t *testing.T) {
	t.Parallel()

	target := exposedTarget(t, emailSendSchema)
	targetID := target.ID()
	aliases := []testHTTPAlias{
		{id: httpCapabilityID(t, "compat.send/v1"), target: targetID, digest: target.ContractDigest(), exposure: generation.Exposure{HTTP: true}},
		{id: httpCapabilityID(t, "email.dispatch/v1"), target: targetID, digest: target.ContractDigest(), exposure: generation.Exposure{HTTP: true}},
		{id: httpCapabilityID(t, "mail.deliver/v1"), target: targetID, digest: target.ContractDigest(), exposure: generation.Exposure{HTTP: true}, deprecated: "Use email.send/v1 instead."},
	}
	files := make([]httpgen.File, len(aliases))
	for index, alias := range aliases {
		file, err := httpgen.RenderAlias(testModulePath, alias, target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
		if err != nil {
			t.Fatalf("RenderAlias(%s): %v", alias.ID(), err)
		}
		files[index] = file
		generated := strings.Join(strings.Fields(string(file.Data())), " ")
		for _, required := range []string{
			`canonicaladapter "example.com/acme/project/generated/go/adapters/http/email/send/v1"`,
			`target canonicaladapter.Handler`,
			`return canonicaladapter.Available(handler.target)`,
			`h.target.ServeRoute(writer, request, RoutePath)`,
		} {
			if !strings.Contains(generated, required) {
				t.Fatalf("generated Alias %s omits %q:\n%s", alias.ID(), required, file.Data())
			}
		}
		for _, forbidden := range []string{"kernelinvocation", "applicationinvocation", "generated/go/contracts", "generated/go/invocation"} {
			if bytes.Contains(file.Data(), []byte(forbidden)) {
				t.Fatalf("Alias %s owns canonical runtime concern %q:\n%s", alias.ID(), forbidden, file.Data())
			}
		}
	}
	if got := []string{files[0].Path(), files[1].Path(), files[2].Path()}; !slices.Equal(got, []string{
		"generated/go/adapters/http/compat/send/v1/handler_gen.go",
		"generated/go/adapters/http/email/dispatch/v1/handler_gen.go",
		"generated/go/adapters/http/mail/deliver/v1/handler_gen.go",
	}) {
		t.Fatalf("Alias paths = %v", got)
	}
	want, err := os.ReadFile("testdata/mail.deliver.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(Alias golden): %v\n%s", err, files[2].Data())
	}
	if files[2].PackageName() != "maildeliverv1" || !bytes.Equal(files[2].Data(), want) {
		t.Fatalf("generated deprecated Alias:\n%s\nwant:\n%s", files[2].Data(), want)
	}
	if count := bytes.Count(files[2].Data(), []byte("Deprecated: Use email.send/v1 instead.")); count != 2 {
		t.Fatalf("deprecation marker count = %d\n%s", count, files[2].Data())
	}

	repeated, err := httpgen.RenderAlias(testModulePath, aliases[2], target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil || repeated.Path() != files[2].Path() || !bytes.Equal(repeated.Data(), files[2].Data()) {
		t.Fatalf("repeated RenderAlias = %#v, %v", repeated, err)
	}
	returned := files[2].Data()
	returned[0] = 'x'
	if bytes.Equal(returned, files[2].Data()) {
		t.Fatal("Alias Data exposed mutable generated storage")
	}
}

func TestRenderAliasSupportsIntrinsicCanonicalTarget(t *testing.T) {
	t.Parallel()

	target := exposedTarget(t, "id: kernel.health/v1\nresponse:\n  healthy: {type: boolean, required: true}\n")
	alias := testHTTPAlias{
		id:       httpCapabilityID(t, "health.status/v1"),
		target:   target.ID(),
		digest:   target.ContractDigest(),
		exposure: generation.Exposure{HTTP: true},
	}
	file, err := httpgen.RenderAlias(testModulePath, alias, target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("RenderAlias: %v", err)
	}
	generated := strings.Join(strings.Fields(string(file.Data())), " ")
	if file.Path() != "generated/go/adapters/http/health/status/v1/handler_gen.go" || !strings.Contains(generated, `canonicaladapter "example.com/acme/project/generated/go/adapters/http/kernel/health/v1"`) {
		t.Fatalf("intrinsic Alias = path %q\n%s", file.Path(), file.Data())
	}
}

func TestRenderAliasRejectsInvalidOrBroadenedRoute(t *testing.T) {
	t.Parallel()

	targetView := exposedTarget(t, emailSendSchema)
	validTarget := testTarget{
		id:       targetView.ID(),
		contract: targetView.ContractJSON(),
		digest:   targetView.ContractDigest(),
		exposure: targetView.Exposure(),
	}
	validAlias := testHTTPAlias{
		id:       httpCapabilityID(t, "mail.deliver/v1"),
		target:   validTarget.id,
		digest:   validTarget.digest,
		exposure: generation.Exposure{HTTP: true},
	}
	tests := []struct {
		name       string
		modulePath string
		alias      httpgen.AliasView
		target     httpgen.CanonicalTargetView
		want       string
		also       error
	}{
		{name: "invalid module", modulePath: "../application", alias: validAlias, target: validTarget, want: "Go Module path"},
		{name: "absent Alias", modulePath: testModulePath, target: validTarget, want: "Alias view is absent"},
		{name: "absent target", modulePath: testModulePath, alias: validAlias, want: "target view is absent", also: httpgen.ErrTarget},
		{name: "invalid Alias ID", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.id = generation.CapabilityID{} }), target: validTarget, want: "Alias ID"},
		{name: "invalid target ID", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.target = generation.CapabilityID{} }), target: validTarget, want: "target ID"},
		{name: "different target", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.target = httpCapabilityID(t, "email.queue/v1") }), target: validTarget, want: "received canonical target"},
		{name: "self target", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.id = validTarget.id }), target: validTarget, want: "cannot target itself"},
		{name: "reserved Alias", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.id = httpCapabilityID(t, "kernel.deliver/v1") }), target: validTarget, want: "reserved kernel.*"},
		{name: "version mismatch", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.id = httpCapabilityID(t, "mail.deliver/v2") }), target: validTarget, want: "same version"},
		{name: "not exposed", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.exposure.HTTP = false }), target: validTarget, want: "not exposed over HTTP"},
		{name: "broadens JavaScript", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.exposure.JavaScript = true }), target: withTarget(validTarget, func(value *testTarget) { value.exposure.JavaScript = false }), want: "exposure broadens"},
		{name: "internal target", modulePath: testModulePath, alias: validAlias, target: withTarget(validTarget, func(value *testTarget) { value.exposure.HTTP = false }), want: "not explicitly exposed", also: httpgen.ErrTarget},
		{name: "digest mismatch", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.digest = "sha256:" + strings.Repeat("0", 64) }), target: validTarget, want: "target digest"},
		{name: "oversized deprecation", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.deprecated = strings.Repeat("x", 1025) }), target: validTarget, want: "deprecation"},
		{name: "NUL deprecation", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.deprecated = "unsafe\x00message" }), target: validTarget, want: "deprecation"},
		{name: "invalid UTF-8 deprecation", modulePath: testModulePath, alias: withHTTPAlias(validAlias, func(value *testHTTPAlias) { value.deprecated = string([]byte{0xff}) }), target: validTarget, want: "deprecation"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := httpgen.RenderAlias(test.modulePath, test.alias, test.target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
			if !errors.Is(err, httpgen.ErrRender) || test.also != nil && !errors.Is(err, test.also) || !strings.Contains(err.Error(), test.want) || file.Path() != "" || file.Data() != nil {
				t.Fatalf("RenderAlias = %#v, %v; want %q", file, err, test.want)
			}
		})
	}
}

func FuzzRenderAliasDeprecation(f *testing.F) {
	for _, seed := range []string{"", "Use email.send/v1 instead.", "First line.\nSecond line.", "carriage\rreturn", "invalid\x00message"} {
		f.Add(seed)
	}
	target := exposedTarget(f, emailSendSchema)
	aliasID := httpCapabilityID(f, "mail.deliver/v1")
	f.Fuzz(func(t *testing.T, deprecation string) {
		alias := testHTTPAlias{
			id:         aliasID,
			target:     target.ID(),
			digest:     target.ContractDigest(),
			exposure:   generation.Exposure{HTTP: true},
			deprecated: deprecation,
		}
		first, err := httpgen.RenderAlias(testModulePath, alias, target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
		if len(deprecation) > 1024 || !utf8.ValidString(deprecation) || strings.ContainsRune(deprecation, '\x00') {
			if !errors.Is(err, httpgen.ErrRender) {
				t.Fatalf("RenderAlias invalid deprecation error = %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("RenderAlias: %v", err)
		}
		second, err := httpgen.RenderAlias(testModulePath, alias, target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("RenderAlias is not deterministic: %#v then %#v, %v", first, second, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), first.Path(), first.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse generated Alias Go: %v\n%s", err, first.Data())
		}
	})
}

type testHTTPAlias struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	digest     string
	exposure   generation.Exposure
	deprecated string
}

func (a testHTTPAlias) ID() generation.CapabilityID     { return a.id }
func (a testHTTPAlias) Target() generation.CapabilityID { return a.target }
func (a testHTTPAlias) TargetContractDigest() string    { return a.digest }
func (a testHTTPAlias) Exposure() generation.Exposure   { return a.exposure }
func (a testHTTPAlias) Deprecated() string              { return a.deprecated }
func withHTTPAlias(value testHTTPAlias, mutate func(*testHTTPAlias)) testHTTPAlias {
	mutate(&value)
	return value
}
