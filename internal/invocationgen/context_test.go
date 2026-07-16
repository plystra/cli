package invocationgen_test

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/plystra/cli/internal/invocationgen"
)

func TestRenderContextGoldenAndRuntime(t *testing.T) {
	t.Parallel()

	file, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	want, err := os.ReadFile("testdata/context.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/internal/invocationcontext/context_gen.go" || file.PackageName() != "invocationcontext" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated context = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	returned := file.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, file.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
	repeated, err := invocationgen.RenderContext()
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderContext = %#v, %v", repeated, err)
	}
	assertGeneratedContextRuns(t, file)
}

func assertGeneratedContextRuns(t testing.TB, contextFile invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFile(t, root, contextFile.Path(), contextFile.Data())
	writeGeneratedFile(t, root, "generated/go/invocation/contexttest/context_test.go", []byte(`package contexttest

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
)

type verifiedSession struct {
	CallerID string   `+"`json:\"caller_id\"`"+`
	Roles    []string `+"`json:\"roles\"`"+`
}

func TestTypedValuesAreBoundedAndDefensive(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	original := verifiedSession{CallerID: "caller-1", Roles: []string{"reader"}}
	ctx, err := invocationcontext.WithValue(parent, "authn.verified-session", original, 256)
	if err != nil {
		t.Fatalf("WithValue: %v", err)
	}
	original.Roles[0] = "changed"
	first, ok := invocationcontext.Value[verifiedSession](ctx, "authn.verified-session")
	if !ok || first.CallerID != "caller-1" || len(first.Roles) != 1 || first.Roles[0] != "reader" {
		t.Fatalf("Value = %#v, %t", first, ok)
	}
	first.Roles[0] = "mutated"
	second, ok := invocationcontext.Value[verifiedSession](ctx, "authn.verified-session")
	if !ok || second.Roles[0] != "reader" {
		t.Fatalf("second Value = %#v, %t", second, ok)
	}
	if _, ok := invocationcontext.Value[string](ctx, "authn.verified-session"); ok {
		t.Fatal("mismatched type was returned")
	}
	ctx, err = invocationcontext.WithValue(ctx, "authz.space-id", "space-1", 9)
	if err != nil {
		t.Fatalf("WithValue(second): %v", err)
	}
	if value, ok := invocationcontext.Value[verifiedSession](ctx, "authn.verified-session"); !ok || value.CallerID != "caller-1" {
		t.Fatalf("first value disappeared: %#v, %t", value, ok)
	}
	if value, ok := invocationcontext.Value[string](ctx, "authz.space-id"); !ok || value != "space-1" {
		t.Fatalf("second value = %q, %t", value, ok)
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context cancellation = %v", ctx.Err())
	}
}

func TestInvalidValuesFailClosed(t *testing.T) {
	valid := context.Background()
	for _, test := range []struct {
		name string
		ctx context.Context
		key string
		value any
		maximum uint32
	}{
		{name: "nil context", key: "authn.value", value: "x", maximum: 8},
		{name: "invalid key", ctx: valid, key: "AuthN.value", value: "x", maximum: 8},
		{name: "zero bound", ctx: valid, key: "authn.value", value: "x"},
		{name: "excessive bound", ctx: valid, key: "authn.value", value: "x", maximum: 65537},
		{name: "oversized", ctx: valid, key: "authn.value", value: strings.Repeat("x", 8), maximum: 8},
		{name: "unserializable", ctx: valid, key: "authn.value", value: math.Inf(1), maximum: 64},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, err := invocationcontext.WithValue(test.ctx, test.key, test.value, test.maximum)
			if !errors.Is(err, invocationcontext.ErrInvalidValue) || ctx != nil {
				t.Fatalf("WithValue = %#v, %v", ctx, err)
			}
		})
	}
	if _, ok := invocationcontext.Value[string](nil, "authn.value"); ok {
		t.Fatal("Value accepted nil context")
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module example.com/acme/project\n\ngo 1.26\n"))
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated context module: %v\n%s", err, output)
	}
	writeGeneratedFile(t, root, "plugin/import_test.go", []byte(`package plugin

import _ "example.com/acme/project/generated/go/internal/invocationcontext"
`))
	command = exec.CommandContext(t.Context(), "go", "test", "-count=1", "./plugin")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err = command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("use of internal package")) || !bytes.Contains(output, []byte("not allowed")) {
		t.Fatalf("user plugin imported generated invocation context: %v\n%s", err, output)
	}
}
