package applicationentrygen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationentrygen"
)

func TestRenderProducesDeterministicSignalAndSmokeLifecycle(t *testing.T) {
	t.Parallel()

	options := applicationentrygen.Options{
		ModulePath:      "example.com/acme/application",
		ShutdownTimeout: 30 * time.Second,
	}
	generated, err := applicationentrygen.Render(options)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), applicationentrygen.Path, generated, parser.AllErrors); err != nil {
		t.Fatalf("parse generated entrypoint: %v\n%s", err, generated)
	}
	for _, required := range []string{
		`applicationbootstrap "example.com/acme/application/generated/go/bootstrap"`,
		`signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`,
		`run(ctx, os.Args[1:], os.Environ())`,
		`processArguments(arguments)`,
		`applicationbootstrap.New(ctx, applicationbootstrap.RuntimeOptions{`,
		`Arguments:   selectorArguments`,
		`Environment: environment`,
		`application.Start(ctx)`,
		`application.Invocations().IntrinsicHealth(ctx)`,
		`health.Status != kernelhealthv1.StatusHealthy`,
		`application.Stop(stopContext)`,
		`<-ctx.Done()`,
		`argument != "--smoke"`,
		`--smoke may be specified once`,
	} {
		if !bytes.Contains(generated, []byte(required)) {
			t.Fatalf("generated entrypoint omits %q:\n%s", required, generated)
		}
	}
	for _, deferred := range []string{"plystra.yaml", "defaultRuntimeDocument", "--env", "--config", "PLYSTRA_ENV", "PLYSTRA_CONFIG"} {
		if bytes.Contains(generated, []byte(deferred)) {
			t.Fatalf("generated entrypoint prematurely contains selector %q:\n%s", deferred, generated)
		}
	}
	repeated, err := applicationentrygen.Render(options)
	if err != nil || !bytes.Equal(generated, repeated) {
		t.Fatalf("repeated Render differs: %v", err)
	}
}

func TestRenderRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	for _, options := range []applicationentrygen.Options{
		{ModulePath: "../application", ShutdownTimeout: time.Second},
		{ModulePath: "example.com/acme/application"},
	} {
		generated, err := applicationentrygen.Render(options)
		if generated != nil || !errors.Is(err, applicationentrygen.ErrRender) || !errors.Is(err, applicationentrygen.ErrInvalidOptions) {
			t.Fatalf("Render(%#v) = %q, %v", options, generated, err)
		}
	}
}
