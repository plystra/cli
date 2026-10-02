package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/command"
)

func TestGeneratedApplicationCoordinatesBothLifecycleRuntimes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeConnectApplicationModule(t, root, "example.com/lifecycle")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [static.run/v1]}\nhttp: {expose: {legacy.run/v1: {transport: connect}}}\n")
	writeAssemblyInterface(t, root, "static/run/v1", "runv1", "static.run/v1", "Run", "type Request struct{}\ntype Response struct{}\n")
	writePlugin(t, root, "legacy", "id: example.legacy\nprovides: [legacy.run/v1]\n")
	writeCapability(t, root, "legacy", "legacy.run/v1", "id: legacy.run/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "probe", "probe.go"), mixedLifecycleProbe)
	writeFile(t, filepath.Join(root, "static", "service.go"), `package static
import (
	"context"
	"example.com/lifecycle/probe"
	contract "example.com/lifecycle/interfaces/static/run/v1"
)
type service struct { *probe.Instance }
//plystra:implements static.run/v1
func New() (*service, error) { instance, err := probe.New("static"); return &service{instance}, err }
func (s *service) Run(ctx context.Context, _ contract.Request) (contract.Response, error) { return contract.Response{}, s.Call(ctx) }
`)
	writeFile(t, filepath.Join(root, "legacy", "plugin.go"), `package legacy
import (
	"context"
	"example.com/lifecycle/probe"
	configuration "example.com/lifecycle/generated/go/configuration"
	contract "example.com/lifecycle/generated/go/contracts/legacy/run/v1"
)
type Config = configuration.LegacyConfig
type Plugin struct { *probe.Instance }
func New(Config) *Plugin { instance, _ := probe.New("legacy"); return &Plugin{instance} }
func (p *Plugin) Run(ctx context.Context, _ contract.Request) (contract.Response, error) { return contract.Response{}, p.Call(ctx) }
`)
	var stdout, stderr bytes.Buffer
	if exit := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); exit != 0 {
		t.Fatalf("generate exited %d:\n%s\n%s", exit, stdout.Bytes(), stderr.Bytes())
	}
	writeFile(t, filepath.Join(root, "lifecycle_test.go"), mixedLifecycleTests)
	cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-mod=readonly", "-count=1", "./...")
	cmd.Dir, cmd.Env = root, goEnvironment(nil)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated mixed lifecycle: %v\n%s", err, output)
	}
}

const mixedLifecycleProbe = `package probe
import (
	"context"
	"errors"
	"sync"
)
type Options struct {
	ConstructError bool
	Start, Stop, Target func(context.Context) error
}
type Instance struct { name string; options Options }
var mu sync.Mutex
var options map[string]Options
var events []string
func Reset(values map[string]Options) { mu.Lock(); defer mu.Unlock(); options = values; events = nil }
func Events() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), events...) }
func record(event string) { mu.Lock(); defer mu.Unlock(); events = append(events, event) }
func New(name string) (*Instance, error) {
	mu.Lock(); value := options[name]; mu.Unlock()
	record(name+".construct")
	instance := &Instance{name: name, options: value}
	if value.ConstructError { return instance, errors.New("private-constructor-secret") }
	return instance, nil
}
func (i *Instance) Start(ctx context.Context) error {
	record(i.name+".start")
	if i.options.Start != nil { return i.options.Start(ctx) }
	return nil
}
func (i *Instance) Stop(ctx context.Context) error {
	record(i.name+".stop")
	if i.options.Stop != nil { return i.options.Stop(ctx) }
	return nil
}
func (i *Instance) Call(ctx context.Context) error {
	if i.options.Target != nil { return i.options.Target(ctx) }
	return nil
}
`

const mixedLifecycleTests = `package lifecycle_test
import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"example.com/lifecycle/probe"
	"example.com/lifecycle/generated/go/bootstrap"
	static "example.com/lifecycle/interfaces/static/run/v1"
	legacy "example.com/lifecycle/generated/go/contracts/legacy/run/v1"
	"github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/lifecycle"
)
func calls(app *bootstrap.Application) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"static": func(ctx context.Context) error { _, err := app.Interfaces().StaticRunV1().Run(ctx, static.Request{}); return err },
		"legacy": func(ctx context.Context) error { _, err := app.Invocations().LegacyRunV1().Invoke(ctx, legacy.Request{}); return err },
	}
}
func assertClosed(t *testing.T, app *bootstrap.Application) {
	t.Helper()
	for name, call := range calls(app) {
		err := call(context.Background())
		var boundary *invocation.Error
		if !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorUnavailable || invocation.CompletionOf(err) != invocation.CompletionNotStarted { t.Fatalf("%s admitted public work: %v", name, err) }
	}
}
func TestReadiness(t *testing.T) {
	var app *bootstrap.Application
	probe.Reset(map[string]probe.Options{
		"static": {Start: func(ctx context.Context) error { assertClosed(t, app); return nil }},
		"legacy": {Start: func(ctx context.Context) error {
			assertClosed(t, app)
			if app.Interfaces().State() != lifecycle.StateRunning { t.Fatal("static manager not started first") }
			return nil
		}},
	})
	var err error
	app, err = bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", "."}})
	if err != nil { t.Fatal(err) }
	assertClosed(t, app)
	if err := app.Interfaces().OpenAdmission(); !errors.Is(err, lifecycle.ErrState) { t.Fatalf("premature open = %v", err) }
	if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
	for name, call := range calls(app) { if err := call(context.Background()); err != nil { t.Fatalf("%s not ready: %v", name, err) } }
	if err := app.Start(context.Background()); !errors.Is(err, lifecycle.ErrState) { t.Fatalf("duplicate Start = %v", err) }
	for _, call := range calls(app) { if err := call(context.Background()); err != nil { t.Fatalf("duplicate Start closed admission: %v", err) } }
	if err := app.Stop(context.Background()); err != nil { t.Fatal(err) }
	assertClosed(t, app)
	if err := app.Invocations().OpenAdmission(); err == nil { t.Fatal("reopened legacy runtime") }
}
func TestStartupFailureCleansBothManagers(t *testing.T) {
	for _, phase := range []string{"static", "legacy", "cancel"} {
		for _, mode := range []string{"error", "panic"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				options := map[string]probe.Options{}
				if phase != "cancel" {
					options[phase] = probe.Options{Start: func(context.Context) error {
						if mode == "panic" { panic("private-start-secret") }
						return errors.New("private-start-secret")
					}}
				}
				probe.Reset(options)
				app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", "."}})
				if err != nil { t.Fatal(err) }
				ctx, cancel := context.WithCancel(context.Background()); defer cancel()
				if phase == "cancel" { cancel() }
				err = app.Start(ctx)
				if !errors.Is(err, bootstrap.ErrApplicationStart) || strings.Contains(err.Error(), "secret") { t.Fatalf("Start = %v", err) }
				assertClosed(t, app)
				before := probe.Events()
				for _, name := range []string{"legacy.stop", "static.stop"} {
					if strings.Count(strings.Join(before, ","), name) != 1 { t.Fatalf("missing cleanup: %v", before) }
				}
				if err := app.Stop(context.Background()); err != nil || !reflect.DeepEqual(before, probe.Events()) { t.Fatalf("repeated cleanup = %v, %v", err, probe.Events()) }
			})
		}
	}
}
func TestConstructionRetainsBothCleanupOwners(t *testing.T) {
	staticStops, legacyStops := 0, 0
	probe.Reset(map[string]probe.Options{
		"static": {ConstructError: true, Stop: func(context.Context) error { staticStops++; if staticStops == 1 { return errors.New("private-static-stop-secret") }; return nil }},
		"legacy": {Stop: func(context.Context) error { legacyStops++; if legacyStops == 1 { return errors.New("private-legacy-stop-secret") }; return nil }},
	})
	app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", "."}})
	var cleanup *bootstrap.ApplicationAssemblyError
	if app != nil || !errors.As(err, &cleanup) || strings.Contains(fmt.Sprintf("%#+v", err), "secret") { t.Fatalf("construction = %v, %v", app, err) }
	if staticStops != 1 || legacyStops != 1 { t.Fatalf("initial stops = %d, %d", staticStops, legacyStops) }
	if err := cleanup.RetryCleanup(nil); !errors.Is(err, bootstrap.ErrInvalidContext) { t.Fatalf("nil retry = %v", err) }
	if err := cleanup.RetryCleanup(context.Background()); err != nil { t.Fatal(err) }
	if err := cleanup.RetryCleanup(context.Background()); err != nil || staticStops != 2 || legacyStops != 2 { t.Fatalf("retry = %v, stops %d, %d", err, staticStops, legacyStops) }
}
func TestBothDispatchersDrainBeforeAnyCleanup(t *testing.T) {
	for _, surface := range []string{"static", "legacy", "both"} {
		for _, outcome := range []string{"success", "error", "panic"} {
			t.Run(surface+"/"+outcome, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					entered := make(chan struct{}, 2)
					release := make(chan struct{})
					defer func() { if release != nil { close(release); synctest.Wait() } }()
					options := map[string]probe.Options{}
					for _, name := range []string{"static", "legacy"} {
						if surface != "both" && surface != name { continue }
						options[name] = probe.Options{Target: func(ctx context.Context) error {
							entered <- struct{}{}; <-release
							if ctx.Err() == nil { return errors.New("target not cancelled") }
							switch outcome { case "error": return errors.New("private-target-secret"); case "panic": panic("private-target-secret") }
							return nil
						}}
					}
					probe.Reset(options)
					app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", "."}})
					if err != nil { t.Fatal(err) }
					if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
					done := make(chan error, 2)
					for name, call := range calls(app) {
						if surface == "both" || surface == name { go func() { done <- call(context.Background()) }(); <-entered }
					}
					before := probe.Events()
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond); defer cancel()
					stopped := make(chan error, 1)
					go func() { stopped <- app.Stop(ctx) }()
					synctest.Wait()
					assertClosed(t, app)
					if !reflect.DeepEqual(before, probe.Events()) { t.Fatalf("early cleanup: %v", probe.Events()) }
					if err := <-stopped; !errors.Is(err, invocation.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) { t.Fatalf("drain = %v", err) }
					assertClosed(t, app)
					for range len(options) {
						if err := <-done; !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown { t.Fatalf("caller = %v", err) }
					}
					if !reflect.DeepEqual(before, probe.Events()) { t.Fatalf("failed drain cleaned values: %v", probe.Events()) }
					close(release); release = nil; synctest.Wait()
					if err := app.Stop(context.Background()); err != nil { t.Fatal(err) }
					want := append(before, "legacy.stop", "static.stop")
					if !reflect.DeepEqual(want, probe.Events()) { t.Fatalf("cleanup order = %v", probe.Events()) }
					if err := app.Stop(context.Background()); err != nil || !reflect.DeepEqual(want, probe.Events()) { t.Fatalf("duplicate cleanup = %v", err) }
				})
			})
		}
	}
}
`
