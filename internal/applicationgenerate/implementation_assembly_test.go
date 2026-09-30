package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/command"
)

func TestGeneratedStaticAssemblyPreservesOrdinaryTypedBusinessCalls(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const modulePath = "example.com/acme/static-interface-runtime"
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.check/v1, app.run/v1]}\n")
	writeAssemblyInterface(t, root, "app/check/v1", "checkv1", "app.check/v1", "Check", `
type Request struct{}
type Response struct { Instance int64 `+"`plystra:\"1,required\"`"+` }
`)
	writeAssemblyInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run", `
type Request struct { Value string `+"`plystra:\"1,required\"`"+` }
type Response struct {
	Value string `+"`plystra:\"1,required\"`"+`
	Instance int64 `+"`plystra:\"2,required\"`"+`
	NotifyAvailable bool `+"`plystra:\"3,required\"`"+`
}
`)
	writeAssemblyInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write", `
type Request struct { Value string `+"`plystra:\"1,required\"`"+` }
type Response struct { Value string `+"`plystra:\"1,required\"`"+` }
`)
	writeAssemblyInterface(t, root, "notify/send/v1", "sendv1", "notify.send/v1", "Send", `
type Request struct{}
type Response struct{}
`)
	writeFile(t, filepath.Join(root, "probe", "probe.go"), `package probe

import "sync"

var (
	mu sync.Mutex
	events []string
	next int64
)

func Constructed(name string) int64 {
	mu.Lock()
	defer mu.Unlock()
	events = append(events, "construct:"+name)
	next++
	return next
}

func Lifecycle(event string) {
	mu.Lock()
	defer mu.Unlock()
	events = append(events, event)
}

func Events() []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), events...)
}

func Reset() {
	mu.Lock()
	defer mu.Unlock()
	events = nil
	next = 0
}
`)
	writeFile(t, filepath.Join(root, "audit", "service.go"), `package audit

import (
	"context"
	"errors"
	"sync/atomic"

	"example.com/acme/static-interface-runtime/probe"
	writev1 "example.com/acme/static-interface-runtime/interfaces/audit/write/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

type service struct{}

var failStart atomic.Bool
var failConstruction atomic.Bool
var entered, release chan struct{}

func SetStartFailure(value bool) { failStart.Store(value) }
func SetConstructorFailure(value bool) { failConstruction.Store(value) }
func BlockCalls(started, finish chan struct{}) { entered, release = started, finish }

//plystra:implements audit.write/v1
func New() (*service, error) {
	probe.Constructed("audit")
	if failConstruction.Load() { return nil, errors.New("private-first-constructor-secret") }
	return &service{}, nil
}

func (*service) Start(context.Context) error {
	probe.Lifecycle("start:audit")
	if failStart.Load() { return errors.New("private-audit-startup-secret") }
	return nil
}

func (*service) Stop(context.Context) error {
	probe.Lifecycle("stop:audit")
	return nil
}

func (*service) Write(ctx context.Context, request writev1.Request) (writev1.Response, error) {
	if _, governed := kernelinvocation.Current(ctx); !governed {
		panic("audit call bypassed Kernel governance")
	}
	if entered != nil {
		close(entered)
		<-release
		probe.Lifecycle("late:audit")
		switch request.Value {
		case "error": return writev1.Response{}, errors.New("private-late-secret")
		case "panic": panic("private-late-secret")
		}
	}
	return writev1.Response{Value: "audit:" + request.Value}, nil
}
`)
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"
	"errors"
	"sync/atomic"

	plystra "github.com/plystra/kernel"
	checkv1 "example.com/acme/static-interface-runtime/interfaces/app/check/v1"
	runv1 "example.com/acme/static-interface-runtime/interfaces/app/run/v1"
	writev1 "example.com/acme/static-interface-runtime/interfaces/audit/write/v1"
	sendv1 "example.com/acme/static-interface-runtime/interfaces/notify/send/v1"
	"example.com/acme/static-interface-runtime/probe"
)

type service struct {
	audit writev1.Interface
	notify plystra.Optional[sendv1.Interface]
	instance int64
	mode string
	stops int
	private string
}

var failStart atomic.Bool
var constructorMode string
var lastAudit writev1.Interface

func SetStartFailure(value bool) { failStart.Store(value) }
func SetConstructorMode(value string) { constructorMode = value; lastAudit = nil }
func CallCapturedDependency(ctx context.Context) error {
	_, err := lastAudit.Write(ctx, writev1.Request{})
	return err
}

//plystra:implements app.check/v1
//plystra:implements app.run/v1
func New(audit writev1.Interface, notify plystra.Optional[sendv1.Interface]) (*service, error) {
	lastAudit = audit
	value := &service{audit: audit, notify: notify, instance: probe.Constructed("app"), mode: constructorMode, private: "private-instance-secret"}
	switch constructorMode {
	case "nil-error":
		return nil, errors.New("private-constructor-secret")
	case "partial-error", "partial-retry", "partial-panic-stop", "partial-timeout", "partial-cancel-success", "partial-retry-bounds":
		return value, errors.New("private-constructor-secret")
	case "nil-nil":
		return nil, nil
	case "typed-nil":
		var absent *service
		return absent, nil
	case "panic":
		panic("private-constructor-secret")
	case "nil-panic":
		panic(nil)
	}
	return value, nil
}

func (*service) Start(context.Context) error {
	probe.Lifecycle("start:app")
	if failStart.Load() {
		return errors.New("private-startup-secret")
	}
	return nil
}

func (value *service) Stop(ctx context.Context) error {
	probe.Lifecycle("stop:app")
	if value.mode == "partial-retry" || value.mode == "partial-panic-stop" || value.mode == "partial-timeout" {
		if _, ok := ctx.Deadline(); !ok { panic("cleanup deadline missing") }
	}
	value.stops++
	if value.mode == "partial-retry-bounds" {
		if value.stops == 1 { return errors.New("private-stop-secret") }
		if value.stops < 4 { <-ctx.Done(); return ctx.Err() }
	}
	if value.stops == 1 {
		switch value.mode {
		case "partial-retry": return errors.New("private-stop-secret")
		case "partial-panic-stop": panic("private-stop-secret")
		case "partial-timeout": <-ctx.Done(); return ctx.Err()
		case "partial-cancel-success": <-ctx.Done(); return nil
		}
	}
	return nil
}

func (service *service) Check(context.Context, checkv1.Request) (checkv1.Response, error) {
	return checkv1.Response{Instance: service.instance}, nil
}

func (service *service) Run(ctx context.Context, request runv1.Request) (runv1.Response, error) {
	audit, err := service.audit.Write(ctx, writev1.Request{Value: request.Value})
	if err != nil {
		return runv1.Response{}, err
	}
	return runv1.Response{
		Value: audit.Value,
		Instance: service.instance,
		NotifyAvailable: service.notify.Available(),
	}, nil
}
`)

	var stdout, stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); exitCode != 0 {
		t.Fatalf("plystra generate exited %d:\n%s\n%s", exitCode, stdout.Bytes(), stderr.Bytes())
	}
	assemblyPath := "generated/go/assembly/interfaces_gen.go"
	assemblySource := readFile(t, root, assemblyPath)
	for _, required := range [][]byte{
		[]byte(`interface1 :=`),
		[]byte(`plystra.Optional[`),
		[]byte(`Constructor:     "example.com/acme/static-interface-runtime/app.New"`),
		[]byte(`kernelinvocation.NewCatalog(bindings)`),
		[]byte(`dispatcher.Publish(catalog)`),
	} {
		if !bytes.Contains(assemblySource, required) {
			t.Fatalf("generated static assembly omits %q:\n%s", required, assemblySource)
		}
	}
	writeFile(t, filepath.Join(root, "static_interface_runtime_test.go"), `package staticinterfaceruntime_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	appimplementation "example.com/acme/static-interface-runtime/app"
	auditimplementation "example.com/acme/static-interface-runtime/audit"
	assembly "example.com/acme/static-interface-runtime/generated/go/assembly"
	bootstrap "example.com/acme/static-interface-runtime/generated/go/bootstrap"
	checkv1 "example.com/acme/static-interface-runtime/interfaces/app/check/v1"
	runv1 "example.com/acme/static-interface-runtime/interfaces/app/run/v1"
	"example.com/acme/static-interface-runtime/probe"
	kernelinvocation "github.com/plystra/kernel/invocation"
	kernellifecycle "github.com/plystra/kernel/lifecycle"
)

func TestRuntime(t *testing.T) {
	probe.Reset()
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Application.Start: %v", err)
	}
	if application.State() != kernellifecycle.StateRunning {
		t.Fatalf("Application.State after Start = %s", application.State())
	}
	runtime := application.Interfaces()
	if !runtime.Valid() {
		t.Fatal("bootstrap returned an invalid static Interface runtime")
	}
	wantStarted := []string{"construct:audit", "construct:app", "start:audit", "start:app"}
	if events := probe.Events(); !reflect.DeepEqual(events, wantStarted) {
		t.Fatalf("constructor and startup order = %v, want %v", events, wantStarted)
	}
	checked, err := runtime.AppCheckV1().Check(context.Background(), checkv1.Request{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	run, err := runtime.AppRunV1().Run(context.Background(), runv1.Request{Value: "request"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Value != "audit:request" || run.Instance != checked.Instance || run.NotifyAvailable {
		t.Fatalf("ordinary typed call = %#v; check = %#v", run, checked)
	}
	seen := map[string]bool{}
	for _, binding := range runtime.Catalog().Bindings() {
		if binding.Kind() == kernelinvocation.BindingKindImplementation {
			seen[binding.InterfaceID().String()] = true
			if binding.Constructor() == "" || binding.SelectionReason() != kernelinvocation.SelectionReasonUniqueCompatible || binding.ContractDigest() == [32]byte{} {
				t.Fatalf("binding provenance = %#v", binding)
			}
		}
	}
	for _, id := range []string{"app.check/v1", "app.run/v1", "audit.write/v1"} {
		if !seen[id] {
			t.Fatalf("catalog omits %s: %v", id, seen)
		}
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Application.Stop: %v", err)
	}
	wantStopped := append(append([]string(nil), wantStarted...), "stop:app", "stop:audit")
	if events := probe.Events(); !reflect.DeepEqual(events, wantStopped) || application.State() != kernellifecycle.StateStopped {
		t.Fatalf("shutdown order = %v, State %s; want %v, stopped", events, application.State(), wantStopped)
	}

	probe.Reset()
	appimplementation.SetStartFailure(true)
	defer appimplementation.SetStartFailure(false)
	failed, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !failed.Valid() {
		t.Fatalf("bootstrap.New(failing) = %#v, %v", failed, err)
	}
	err = failed.Start(context.Background())
	if err == nil || strings.Contains(err.Error(), "private-startup-secret") || failed.State() != kernellifecycle.StateFailed {
		t.Fatalf("failing Application.Start = %v, State %s", err, failed.State())
	}
	wantRollback := []string{"construct:audit", "construct:app", "start:audit", "start:app", "stop:app", "stop:audit"}
	if events := probe.Events(); !reflect.DeepEqual(events, wantRollback) {
		t.Fatalf("partial-startup rollback = %v, want %v", events, wantRollback)
	}
	if err := failed.Stop(context.Background()); err != nil || failed.State() != kernellifecycle.StateStopped || !reflect.DeepEqual(probe.Events(), wantRollback) {
		t.Fatalf("post-rollback Stop = %v, State %s, events %v", err, failed.State(), probe.Events())
	}
}

func TestConstructorResults(t *testing.T) {
	for _, mode := range []string{"nil-error", "partial-error", "nil-nil", "typed-nil", "panic", "nil-panic", "partial-retry", "partial-panic-stop", "partial-timeout", "partial-cancel-success"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
			probe.Reset()
			appimplementation.SetConstructorMode(mode)
			defer appimplementation.SetConstructorMode("")
			timeout := time.Second
			if mode == "partial-timeout" || mode == "partial-cancel-success" { timeout = 20 * time.Millisecond }
			runtime, err := assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{}, timeout)
			if !errors.Is(err, assembly.ErrInterfaceAssembly) || runtime.Valid() || runtime.AppRunV1() != nil || len(runtime.Catalog().Bindings()) != 0 {
				t.Fatalf("NewInterfaceRuntime = %v, %v", runtime, err)
			}
			if strings.Contains(fmt.Sprintf("%v %#+v", err, err), "secret") {
				t.Fatalf("assembly error leaked: %v", err)
			}
			var logged bytes.Buffer
			slog.New(slog.NewJSONHandler(&logged, nil)).Error("assembly failed", "error", err)
			if strings.Contains(logged.String(), "secret") { t.Fatal("assembly error leaked through logging") }
			dependencyErr := appimplementation.CallCapturedDependency(context.Background())
			var boundary *kernelinvocation.Error
			if !errors.As(dependencyErr, &boundary) || boundary.Code() != kernelinvocation.ErrorUnavailable {
				t.Fatalf("failed assembly published captured dependency: %v", dependencyErr)
			}
			want := []string{"construct:audit", "construct:app"}
			if strings.HasPrefix(mode, "partial-") { want = append(want, "stop:app") }
			if mode != "partial-timeout" && mode != "partial-cancel-success" { want = append(want, "stop:audit") }
			if events := probe.Events(); !reflect.DeepEqual(events, want) {
				t.Fatalf("construction rollback = %v, want %v", events, want)
			}
			var cleanup *assembly.InterfaceAssemblyError
			needsRetry := mode == "partial-retry" || mode == "partial-panic-stop" || mode == "partial-timeout" || mode == "partial-cancel-success"
			if errors.As(err, &cleanup) != needsRetry || errors.Is(err, kernellifecycle.ErrStop) != needsRetry {
				t.Fatalf("cleanup failure identity = %v", err)
			}
			if needsRetry {
				if mode == "partial-timeout" && !errors.Is(err, context.DeadlineExceeded) { t.Fatal("cleanup deadline identity lost") }
				if strings.Contains(fmt.Sprintf("%#+v", cleanup), "secret") { t.Fatal("cleanup state leaked") }
				if err := cleanup.RetryCleanup(nil); !errors.Is(err, kernellifecycle.ErrInvalidContext) { t.Fatalf("nil retry context: %v", err) }
				cancelled, cancel := context.WithCancel(context.Background())
				cancel()
				if err := cleanup.RetryCleanup(cancelled); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(probe.Events(), want) { t.Fatalf("cancelled retry: %v, %v", err, probe.Events()) }
				if err := cleanup.RetryCleanup(context.Background()); err != nil { t.Fatalf("RetryCleanup: %v", err) }
				if mode != "partial-cancel-success" { want = append(want, "stop:app") }
				if mode == "partial-timeout" || mode == "partial-cancel-success" { want = append(want, "stop:audit") }
				if err := cleanup.RetryCleanup(context.Background()); err != nil || !reflect.DeepEqual(probe.Events(), want) {
					t.Fatalf("retry duplicated successful cleanup: %v, %v, want %v", err, probe.Events(), want)
				}
			}
			})
		})
	}
}

func TestFirstConstructorFailure(t *testing.T) {
	probe.Reset()
	auditimplementation.SetConstructorFailure(true)
	defer auditimplementation.SetConstructorFailure(false)
	runtime, err := assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{}, time.Second)
	want := []string{"construct:audit"}
	if !errors.Is(err, assembly.ErrInterfaceAssembly) || runtime.Valid() || !reflect.DeepEqual(probe.Events(), want) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("first-constructor failure = %v, %v, events %v", runtime, err, probe.Events())
	}
}

func TestCleanupRetryRetainsTimeoutBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		probe.Reset()
		appimplementation.SetConstructorMode("partial-retry-bounds")
		defer appimplementation.SetConstructorMode("")
		const timeout = 20 * time.Millisecond
		_, err := assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{}, timeout)
		var cleanup *assembly.InterfaceAssemblyError
		if !errors.As(err, &cleanup) { t.Fatalf("missing cleanup retry: %v", err) }
		for _, callerTimeout := range []time.Duration{5 * time.Millisecond, time.Second} {
			ctx, cancel := context.WithTimeout(context.Background(), callerTimeout)
			started := time.Now()
			err := cleanup.RetryCleanup(ctx)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != min(callerTimeout, timeout) {
				t.Fatalf("retry bound %s: elapsed %s, error %v", callerTimeout, time.Since(started), err)
			}
		}
		if err := cleanup.RetryCleanup(context.Background()); err != nil { t.Fatal(err) }
		if err := cleanup.RetryCleanup(context.Background()); err != nil { t.Fatal(err) }
		want := []string{"construct:audit", "construct:app", "stop:app", "stop:audit", "stop:app", "stop:app", "stop:app"}
		if !reflect.DeepEqual(probe.Events(), want) { t.Fatalf("bounded retries = %v, want %v", probe.Events(), want) }
	})
}

func TestBootstrapPreservesCleanupRetry(t *testing.T) {
	probe.Reset()
	appimplementation.SetConstructorMode("partial-retry")
	defer appimplementation.SetConstructorMode("")
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	var cleanup *assembly.InterfaceAssemblyError
	if application != nil || !errors.Is(err, bootstrap.ErrBootstrap) || !errors.As(err, &cleanup) {
		t.Fatalf("bootstrap failure = %v, %v", application, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cleanup.RetryCleanup(ctx); err != nil { t.Fatal(err) }
	want := []string{"construct:audit", "construct:app", "stop:app", "stop:audit", "stop:app"}
	if !reflect.DeepEqual(probe.Events(), want) { t.Fatalf("bootstrap cleanup = %v, want %v", probe.Events(), want) }
}

func TestNeverStartedCleanup(t *testing.T) {
	for _, mode := range []string{"stop before start", "cancel before start", "dependency failure"} {
		t.Run(mode, func(t *testing.T) {
			probe.Reset()
			application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err != nil { t.Fatal(err) }
			want := []string{"construct:audit", "construct:app"}
			if mode != "stop before start" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "cancel before start" { cancel() } else {
					auditimplementation.SetStartFailure(true)
					defer auditimplementation.SetStartFailure(false)
					want = append(want, "start:audit")
				}
				if err := application.Start(ctx); !errors.Is(err, kernellifecycle.ErrStart) { t.Fatalf("Start = %v", err) }
			}
			if err := application.Stop(context.Background()); err != nil { t.Fatal(err) }
			want = append(want, "stop:app", "stop:audit")
			if !reflect.DeepEqual(probe.Events(), want) { t.Fatalf("events = %v, want %v", probe.Events(), want) }
		})
	}
}

func TestShutdownRetainsDependenciesUntilLateTargetsTerminate(t *testing.T) {
	for _, surface := range []string{"runtime", "bootstrap"} {
		for _, outcome := range []string{"success", "error", "panic"} {
			for _, finish := range []string{"cancel", "deadline", "shutdown"} {
				t.Run(surface+"/"+outcome+"/"+finish, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						probe.Reset()
						var runtime assembly.InterfaceRuntime
						var stop func(context.Context) error
						if surface == "bootstrap" {
							application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
							if err != nil { t.Fatal(err) }
							if err := application.Start(context.Background()); err != nil { t.Fatal(err) }
							runtime, stop = application.Interfaces(), application.Stop
						} else {
							var err error
							runtime, err = assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{}, 20*time.Millisecond)
							if err != nil { t.Fatal(err) }
							if err := runtime.Start(context.Background()); err != nil { t.Fatal(err) }
							stop = runtime.Stop
						}
						if err := stop(nil); err == nil { t.Fatal("nil shutdown accepted") }
						entered, release := make(chan struct{}), make(chan struct{})
						auditimplementation.BlockCalls(entered, release)
						defer auditimplementation.BlockCalls(nil, nil)
						defer func() { if release != nil { close(release); synctest.Wait() } }()
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
						defer cancel()
						if finish != "deadline" { cancel(); ctx, cancel = context.WithCancel(context.Background()); defer cancel() }
						type result struct { response runv1.Response; err error }
						done := make(chan result, 1)
						go func() { response, err := runtime.AppRunV1().Run(ctx, runv1.Request{Value: outcome}); done <- result{response, err} }()
						<-entered
						if finish == "cancel" { cancel() }
						// Observe caller completion before shutdown can independently cancel it.
						var got result
						if finish != "shutdown" { got = <-done }
						before := probe.Events()
						shutdown := context.Background()
						if surface == "bootstrap" { var end context.CancelFunc; shutdown, end = context.WithTimeout(shutdown, 20*time.Millisecond); defer end() }
						start := time.Now()
						err := stop(shutdown)
						if !errors.Is(err, kernelinvocation.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 20*time.Millisecond {
							t.Fatalf("bounded drain = %v after %s", err, time.Since(start))
						}
						if !reflect.DeepEqual(probe.Events(), before) { t.Fatalf("cleanup raced target: %v", probe.Events()) }
						if finish == "shutdown" { got = <-done }
						want := context.Canceled
						if finish == "deadline" { want = context.DeadlineExceeded }
						if !errors.Is(got.err, want) || kernelinvocation.CompletionOf(got.err) != kernelinvocation.CompletionResultUnknown || got.response != (runv1.Response{}) {
							t.Fatalf("caller result = %#v, %v, %s", got.response, got.err, kernelinvocation.CompletionOf(got.err))
						}
						_, rejected := runtime.AppRunV1().Run(context.Background(), runv1.Request{})
						if kernelinvocation.CompletionOf(rejected) != kernelinvocation.CompletionNotStarted { t.Fatalf("closed admission = %v", rejected) }
						close(release); release = nil
						synctest.Wait()
						if err := stop(context.Background()); err != nil { t.Fatal(err) }
						wantEvents := append(before, "late:audit", "stop:app", "stop:audit")
						if !reflect.DeepEqual(probe.Events(), wantEvents) { t.Fatalf("retry cleanup = %v, want %v", probe.Events(), wantEvents) }
						if err := stop(context.Background()); err != nil || !reflect.DeepEqual(probe.Events(), wantEvents) { t.Fatalf("repeated stop = %v, %v", err, probe.Events()) }
					})
				})
			}
		}
	}
}

func TestInvalidCleanupTimeoutDoesNotConstruct(t *testing.T) {
	probe.Reset()
	for _, timeout := range []time.Duration{0, -time.Second} {
		runtime, err := assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{}, timeout)
		if !errors.Is(err, assembly.ErrInterfaceAssembly) || runtime.Valid() || len(probe.Events()) != 0 {
			t.Fatalf("invalid timeout constructed values: %v, %v", probe.Events(), err)
		}
	}
}
`)

	compiledTests := exec.CommandContext(t.Context(), "go", "test", "-race", "./...", "-count=1")
	compiledTests.Dir = root
	compiledTests.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := compiledTests.CombinedOutput(); err != nil {
		t.Fatalf("go test generated static runtime: %v\n%s", err, output)
	}

	check, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: goEnvironment(nil),
	})
	if err != nil || !check.Report().Clean() {
		t.Fatalf("Generate --check = %#v, %v", check.Report().Changes(), err)
	}
	stdout.Reset()
	stderr.Reset()
	if exitCode := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); exitCode != 0 {
		t.Fatalf("plystra generate --check exited %d:\n%s\n%s", exitCode, stdout.Bytes(), stderr.Bytes())
	}
}

func writeAssemblyInterface(t testing.TB, root, relative, packageName, identifier, method, messages string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(relative), "interface.go"), `package `+packageName+`

import "context"

//plystra:interface `+identifier+`
type Interface interface {
	`+method+`(context.Context, Request) (Response, error)
}
`+messages)
}
