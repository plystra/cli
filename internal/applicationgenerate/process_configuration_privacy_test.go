package applicationgenerate_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGenerateDetectsConcurrentPrivateProcessSettingChanges(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			const modulePath = "example.com/acme/private-process"
			root := t.TempDir()
			writeApplicationModule(t, root, modulePath)
			options := applicationgenerate.Options{Start: root, Environment: goEnvironment(nil)}
			path := "plystra.yaml"
			switch mode {
			case "environment":
				path, options.EnvironmentName = "plystra.production.yaml", "production"
			case "replacement":
				path, options.ConfigurationPath = "deploy/customer.yaml", "deploy/customer.yaml"
			}
			if mode != "default" {
				writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			}
			absolute := filepath.Join(root, filepath.FromSlash(path))
			writeFile(t, absolute, "http: {address: 'PRIVATE_FIRST:19080'}\ntimeouts: {startup: 1s}\n")
			if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			before := snapshotGenerated(t, root)
			const changed = "http: {address: 'PRIVATE_SECOND:29080'}\ntimeouts: {startup: 2m}\n"
			options.Validate = func(context.Context, string) error {
				writeFile(t, absolute, changed)
				return nil
			}
			if _, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
				t.Fatalf("concurrent private process change = %v", err)
			} else {
				assertConcurrentGenerationSource(t, err, modulePath, path, "configuration-declaration")
				if strings.Contains(err.Error(), "PRIVATE_") {
					t.Fatal("concurrent change disclosed a private process value")
				}
			}
			if string(readAbsoluteFile(t, absolute)) != changed || !reflect.DeepEqual(snapshotGenerated(t, root), before) {
				t.Fatal("concurrent edit was overwritten or generated output was not restored")
			}
			assertNoTransactions(t, root)
			options.Validate, options.Check = nil, true
			if checked, err := applicationgenerate.Generate(t.Context(), options); err != nil || !checked.Report().Clean() {
				t.Fatalf("private process edit left generated output stale: %v", err)
			}
		})
	}
}

func TestGeneratedRuntimeReloadsPrivateStartupTimeoutWithoutRegeneration(t *testing.T) {
	t.Parallel()
	const modulePath = "example.com/acme/process-runtime"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.check/v1]}\nhttp: {address: 'PRIVATE_INITIAL:19080'}\ntimeouts: {startup: 10s}\n")
	writeAssemblyInterface(t, root, "app/check/v1", "checkv1", "app.check/v1", "Check", "type Request struct{}\ntype Response struct{}\n")
	writeFile(t, filepath.Join(root, "service", "service.go"), `package service

import (
	"context"
	"sync"
	"time"
	contract "example.com/acme/process-runtime/interfaces/app/check/v1"
)

var mu sync.Mutex
var deadline time.Time
type Service struct{}
//plystra:implements app.check/v1
func New() (*Service, error) { return &Service{}, nil }
func (*Service) Check(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
func (*Service) Start(ctx context.Context) error {
	mu.Lock()
	defer mu.Unlock()
	deadline, _ = ctx.Deadline()
	return nil
}
func (*Service) Stop(context.Context) error { return nil }
func Deadline() time.Time { mu.Lock(); defer mu.Unlock(); return deadline }
`)
	options := applicationgenerate.Options{Start: root, Environment: goEnvironment(nil)}
	if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	before := snapshotGenerated(t, root)
	writeFile(t, filepath.Join(root, "process_runtime_test.go"), `package processruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	bootstrap "example.com/acme/process-runtime/generated/go/bootstrap"
	"example.com/acme/process-runtime/service"
)

func TestRuntimeTimeout(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			path := "plystra.yaml"
			var selector []string
			switch mode {
			case "environment": path, selector = "plystra.production.yaml", []string{"--env", "production"}
			case "replacement": path, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { t.Fatal(err) }
			for _, timeout := range []time.Duration{3 * time.Second, 5 * time.Second} {
				data := "interfaces: {require: [app.check/v1]}\nhttp: {address: 'PRIVATE_RUNTIME:29080'}\ntimeouts: {startup: " + timeout.String() + "}\n"
				if err := os.WriteFile(path, []byte(data), 0600); err != nil { t.Fatal(err) }
				application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: append([]string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}, selector...), Environment: []string{}})
				if err != nil { t.Fatal(err) }
				before := time.Now()
				if err := application.Start(context.Background()); err != nil { t.Fatal(err) }
				after := time.Now()
				deadline := service.Deadline()
				if deadline.Before(before.Add(timeout)) || deadline.After(after.Add(timeout)) {
					t.Fatalf("startup did not use the selected runtime timeout %s", timeout)
				}
				if err := application.Stop(context.Background()); err != nil { t.Fatal(err) }
			}
		})
	}
}
`)
	compiled := exec.CommandContext(t.Context(), "go", "test", "-race", "./...", "-count=1")
	compiled.Dir = root
	compiled.Env = mergedEnvironment(map[string]string{"GOFLAGS": "", "GOTOOLCHAIN": "local", "GOWORK": "off"})
	if output, err := compiled.CombinedOutput(); err != nil {
		t.Fatalf("generated process runtime: %v\n%s", err, output)
	}
	options.Check = true
	if checked, err := applicationgenerate.Generate(t.Context(), options); err != nil || !checked.Report().Clean() || !reflect.DeepEqual(snapshotGenerated(t, root), before) {
		t.Fatalf("runtime process edits changed generated artifacts: %v", err)
	}
}
