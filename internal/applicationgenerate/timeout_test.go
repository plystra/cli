package applicationgenerate_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/version"
)

func TestGeneratedTimeoutPolicyAndAbsenceDefaults(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "example.com/budget", "require github.com/plystra/kernel "+version.KernelVersion+"\n")
	downloadModuleDependencies(t, root)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [budget.run/v1], policies: {dormant.run/v1: {timeout: 1s}}}\n")
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {policies: {budget.run/v1: {timeout: 2s}}}\n")
	writeFile(t, filepath.Join(root, "deploy", "customer.yaml"), "interfaces: {require: [budget.run/v1], policies: {budget.run/v1: {timeout: 3s}}}\n")
	writeAssemblyInterface(t, root, "budget/run/v1", "runv1", "budget.run/v1", "Run", `
type Request struct { Mode string `+"`plystra:\"1\"`"+` }
type Response struct { Bounded bool `+"`plystra:\"1\"`"+`; Remaining int64 `+"`plystra:\"2\"`"+` }
`)
	writeFile(t, filepath.Join(root, "work", "service.go"), `package work
import (
	"context"
	"sync/atomic"
	"time"
	runv1 "example.com/budget/interfaces/budget/run/v1"
)
var Constructions atomic.Int32
type service struct{}
//plystra:implements budget.run/v1
func New() (*service, error) { Constructions.Add(1); return &service{}, nil }
func (*service) Run(ctx context.Context, request runv1.Request) (runv1.Response, error) {
	deadline, bounded := ctx.Deadline()
	remaining := int64(0)
	if bounded { remaining = int64(time.Until(deadline)) }
	if request.Mode == "long" { time.Sleep(31*time.Second) }
	if request.Mode == "wait" { <-ctx.Done(); return runv1.Response{}, ctx.Err() }
	return runv1.Response{Bounded: bounded, Remaining: remaining}, nil
}
`)
	var defaultDigest string
	for _, test := range []struct {
		name     string
		selector []string
		path     string
		timeout  int64
	}{
		{name: "default", path: "plystra.yaml"},
		{name: "environment", selector: []string{"--env", "production"}, path: "plystra.production.yaml", timeout: 2_000_000_000},
		{name: "replacement", selector: []string{"--config", "deploy/customer.yaml"}, path: "deploy/customer.yaml", timeout: 3_000_000_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := command.RunIn(append([]string{"generate"}, test.selector...), &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
				t.Fatalf("generate = %d, %s, %s", code, stdout.Bytes(), stderr.Bytes())
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "default" {
				defaultDigest = manifest.ApplicationModelDigest()
			} else if manifest.ApplicationModelDigest() == defaultDigest {
				t.Fatal("authored timeout did not change frozen identity")
			}
			bindings := manifest.InterfaceProvenance().Bindings()
			if len(bindings) != 1 || int64(bindings[0].Policy().Compiled().Timeout) != test.timeout {
				t.Fatalf("compiled timeout: %#v", bindings)
			}
			arguments := "[]string{"
			for _, value := range test.selector {
				arguments += fmt.Sprintf("%q,", value)
			}
			arguments += "}"
			writeFile(t, filepath.Join(root, "generated", "go", "budget_test.go"), fmt.Sprintf(generatedBudgetTest, test.timeout, arguments, test.path))
			cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", "./...")
			cmd.Dir, cmd.Env = root, goEnvironment(map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly"})
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated budget runtime: %v\n%s", err, output)
			}
			if err := os.Remove(filepath.Join(root, "generated", "go", "budget_test.go")); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Dormant intent changes provenance, but not executable policy or identity.
	var stdout, stderr bytes.Buffer
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [budget.run/v1], policies: {dormant.run/v1: {timeout: 9s}}}\n")
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("dormant generate = %d, %s", code, stderr.Bytes())
	}
	manifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil || manifest.ApplicationModelDigest() != defaultDigest {
		t.Fatalf("dormant policy changed executable digest: %s, %v", manifest.ApplicationModelDigest(), err)
	}
}

const generatedBudgetTest = `package budget_test
import (
	"context"
	"errors"
	"os"
	"testing"
	"testing/synctest"
	"time"
	bootstrap "example.com/budget/generated/go/bootstrap"
	runv1 "example.com/budget/interfaces/budget/run/v1"
	"example.com/budget/work"
	"github.com/plystra/kernel/invocation"
)
const timeout = time.Duration(%d)
var options = bootstrap.RuntimeOptions{Arguments: append([]string{"--configuration-root", "."}, %s...)}
const selectedPath = %q
func TestCompiledBudget(t *testing.T) {
	t.Chdir("../..")
	synctest.Test(t, func(t *testing.T) {
		app, err := bootstrap.New(context.Background(), options)
		if err != nil { t.Fatal(err) }
		if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
		defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
		for _, binding := range app.Interfaces().Catalog().Bindings() {
			p := binding.Policy()
			if p.SchemaVersion != 1 || p.CompilerVersion != 1 || p.DefaultsVersion != 1 || p.ConcurrencyLimit != 64 || p.QueueLimit != 0 || p.Retry.MaxAttempts != 1 || p.Circuit != (invocation.CircuitPolicy{}) { t.Fatal("incomplete compiled defaults") }
			if binding.InterfaceID().String() == "budget.run/v1" && p.Timeout != timeout { t.Fatal("wrong binding timeout") }
		}
		proxy := app.Interfaces().BudgetRunV1()
		mode := ""
		if timeout == 0 { mode = "long" }
		response, err := proxy.Run(context.Background(), runv1.Request{Mode: mode})
		if err != nil || response.Bounded != (timeout > 0) || response.Remaining != int64(timeout) { t.Fatal("incorrect default or authored timeout", response, err) }
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond); defer cancel()
		response, err = proxy.Run(ctx, runv1.Request{})
		if err != nil || !response.Bounded || response.Remaining != int64(500*time.Millisecond) { t.Fatal("earlier caller deadline was not retained", response, err) }
		start := time.Now()
		_, err = proxy.Run(ctx, runv1.Request{Mode: "wait"})
		var boundary *invocation.Error
		if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &boundary) || boundary.Attempts() != 1 || time.Since(start) != 500*time.Millisecond { t.Fatal("caller timeout did not execute through generated binding", err) }
		if timeout > 0 {
			start = time.Now()
			_, err = proxy.Run(context.Background(), runv1.Request{Mode: "wait"})
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != timeout { t.Fatal("authored timeout did not execute", err) }
		}
	})
}
func TestRuntimePolicyChangeRequiresRebuildBeforeConstruction(t *testing.T) {
	t.Chdir("../..")
	original, err := os.ReadFile(selectedPath)
	if err != nil { t.Fatal(err) }
	defer func() { if err := os.WriteFile(selectedPath, original, 0600); err != nil { t.Error(err) } }()
	if err := os.WriteFile(selectedPath, []byte("interfaces: {require: [budget.run/v1], policies: {budget.run/v1: {timeout: 11s}}}\n"), 0600); err != nil { t.Fatal(err) }
	before := work.Constructions.Load()
	if _, err := bootstrap.New(context.Background(), options); err == nil || work.Constructions.Load() != before { t.Fatal("changed policy reached construction", err) }
}
`
