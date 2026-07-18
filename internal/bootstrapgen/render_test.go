package bootstrapgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/plystra/cli/internal/bootstrapgen"
)

func TestRenderProducesDeterministicRedactedRuntimeBoundary(t *testing.T) {
	t.Parallel()

	options := bootstrapgen.Options{
		ModulePath:            "example.com/acme/application",
		DefaultStartupTimeout: 2 * time.Minute,
	}
	generated, err := bootstrapgen.Render(options)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), bootstrapgen.Path, generated, parser.AllErrors); err != nil {
		t.Fatalf("parse generated bootstrap: %v\n%s", err, generated)
	}
	for _, required := range []string{
		`applicationassembly "example.com/acme/application/generated/go/assembly"`,
		"kernelconfiguration.LoadDocument(documentPath)",
		"defer clear(document)",
		"kernelconfiguration.ExtractStringMap(document, \"timeouts\")",
		"kernelconfiguration.NewResolver",
		"applicationassembly.NewRuntime",
		"applicationassembly.NewProviderLifecycle",
		"context.WithTimeout(ctx, a.startupTimeout)",
		"<redacted-generated-application>",
		"kernelconfiguration.ErrSecretExposure",
	} {
		if !bytes.Contains(generated, []byte(required)) {
			t.Fatalf("generated source omits %q:\n%s", required, generated)
		}
	}
	for _, forbidden := range []string{
		"plystra.yaml",
		"private-runtime-value",
		"PRIVATE_SECRET_TARGET",
	} {
		if bytes.Contains(generated, []byte(forbidden)) {
			t.Fatalf("generated source contains runtime input %q:\n%s", forbidden, generated)
		}
	}
	repeated, err := bootstrapgen.Render(options)
	if err != nil || !bytes.Equal(generated, repeated) {
		t.Fatalf("repeated Render differs: %v", err)
	}
}

func TestRenderRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	for _, options := range []bootstrapgen.Options{
		{ModulePath: "not a module", DefaultStartupTimeout: 2 * time.Minute},
		{ModulePath: "example.com/acme/application"},
		{ModulePath: "example.com/acme/application", DefaultStartupTimeout: -time.Second},
	} {
		generated, err := bootstrapgen.Render(options)
		if generated != nil || !errors.Is(err, bootstrapgen.ErrRender) || !errors.Is(err, bootstrapgen.ErrInvalidOptions) {
			t.Fatalf("Render(%#v) = %q, %v", options, generated, err)
		}
	}
}
