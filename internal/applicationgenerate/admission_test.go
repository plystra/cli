package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/testmodulecache"
	"github.com/plystra/cli/internal/version"
)

func TestGeneratedAdmissionRetainsCapacityUntilTargetTermination(t *testing.T) {
	testmodulecache.Ensure(t,
		connectgen.ConnectModulePath+"@"+connectgen.ConnectModuleVersion,
		connectgen.ProtobufModulePath+"@"+connectgen.ProtobufModuleVersion,
		"github.com/google/go-cmp@v0.7.0",
		"github.com/golang/protobuf@v1.5.0",
	)
	root := t.TempDir()
	writeModule(t, root, "example.com/admission", "require github.com/plystra/kernel "+version.KernelVersion+"\n")
	downloadModuleDependencies(t, root)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [work.run/v1, work.check/v1], policies: {work.run/v1: {timeout: 30s}}}\nhttp: {expose: {work.run/v1: {transport: connect}}}\n")
	writeAssemblyInterface(t, root, "work/run/v1", "runv1", "work.run/v1", "Run", `
type Request struct { Mode string `+"`plystra:\"1\"`"+` }
type Response struct { Value string `+"`plystra:\"1\"`"+` }
`)
	writeAssemblyInterface(t, root, "work/check/v1", "checkv1", "work.check/v1", "Check", `
type Request struct{}
type Response struct{}
`)
	writeFile(t, filepath.Join(root, "work", "service.go"), `package work

import (
	"context"
	"errors"
	"sync/atomic"
	runv1 "example.com/admission/interfaces/work/run/v1"
	checkv1 "example.com/admission/interfaces/work/check/v1"
)

var Entered chan struct{}
var Release chan struct{}
var Stops atomic.Int32
type service struct{}

//plystra:implements work.run/v1
//plystra:implements work.check/v1
func New() (*service, error) { return &service{}, nil }
func (*service) Start(context.Context) error { return nil }
func (*service) Stop(context.Context) error { Stops.Add(1); return nil }
func (*service) Check(context.Context, checkv1.Request) (checkv1.Response, error) { return checkv1.Response{}, nil }
func (*service) Run(_ context.Context, input runv1.Request) (runv1.Response, error) {
	if input.Mode != "" {
		Entered <- struct{}{}
		<-Release
		switch input.Mode {
		case "error": return runv1.Response{}, errors.New("private target failure")
		case "panic": panic("private target panic")
		}
	}
	return runv1.Response{Value: "finished"}, nil
}
`)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate exited %d:\n%s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	writeFile(t, filepath.Join(root, "generated", "go", "admission_test.go"), generatedAdmissionTest)
	command := exec.CommandContext(t.Context(), "go", "test", "-race", "./...", "-count=1")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{"GOFLAGS": "", "GOTOOLCHAIN": "local", "GOWORK": "off"})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated admission: %v\n%s", err, output)
	}
}

const generatedAdmissionTest = `package admission_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
	bootstrap "example.com/admission/generated/go/bootstrap"
	adapter "example.com/admission/generated/go/adapters/connect/work/run/v1"
	schema "example.com/admission/generated/go/internal/connectschema"
	runv1 "example.com/admission/interfaces/work/run/v1"
	checkv1 "example.com/admission/interfaces/work/check/v1"
	"example.com/admission/work"
	invocation "github.com/plystra/kernel/invocation"
)

func TestAdmission(t *testing.T) {
	t.Chdir("../..")
	for _, completion := range []string{"cancel", "deadline", "runtime-timeout"} {
		for _, late := range []string{"success", "error", "panic"} {
			for _, drain := range []bool{false, true} {
				name := completion+"/"+late
				if drain { name += "/drain" }
				t.Run(name, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
					work.Entered, work.Release = make(chan struct{}, 64), make(chan struct{})
					work.Stops.Store(0)
					app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", "."}})
					if err != nil { t.Fatal(err) }
					if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
					var release sync.Once
					finish := func() { release.Do(func() { close(work.Release) }) }
					defer func() { finish(); if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
					for _, binding := range app.Interfaces().Catalog().Bindings() {
						if binding.ConcurrencyLimit() != 64 { t.Fatalf("limit for %s = %d", binding.InterfaceID(), binding.ConcurrencyLimit()) }
					}
					ctx, cancel := context.WithCancel(context.Background())
					if completion == "deadline" { cancel(); ctx, cancel = context.WithTimeout(context.Background(), time.Second) }
					defer cancel()
					done := make(chan error, 64)
					for range 64 { go func() {
						response, err := app.Interfaces().WorkRunV1().Run(ctx, runv1.Request{Mode: late})
						if response != (runv1.Response{}) { done <- errors.New("late result escaped"); return }
						done <- err
					}() }
					synctest.Wait()
					if len(work.Entered) != 64 { t.Fatalf("entered = %d", len(work.Entered)) }
					assertSaturated(t, app.Interfaces().WorkRunV1())
					if _, err := app.Interfaces().WorkCheckV1().Check(context.Background(), checkv1.Request{}); err != nil { t.Fatalf("independent binding: %v", err) }
					other, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", "."}})
					if err != nil { t.Fatal(err) }
					if err := other.Start(context.Background()); err != nil { t.Fatal(err) }
					if _, err := other.Interfaces().WorkRunV1().Run(context.Background(), runv1.Request{}); err != nil { t.Fatalf("independent dispatcher: %v", err) }
					if err := other.Stop(context.Background()); err != nil { t.Fatal(err) }
					work.Stops.Store(0)
					if completion == "cancel" { cancel() } else if completion == "deadline" { time.Sleep(time.Second) } else { time.Sleep(30*time.Second) }
					synctest.Wait()
					for range 64 {
						err := <-done
						var failure *invocation.Error
						want := invocation.ErrorTimeout
						if completion == "cancel" { want = invocation.ErrorCancelled }
						if !errors.As(err, &failure) || failure.Code() != want || invocation.CompletionOf(err) != invocation.CompletionResultUnknown { t.Fatalf("caller: %v", err) }
					}
					assertSaturated(t, app.Interfaces().WorkRunV1())
					assertTransportSaturated(t, app.Interfaces().WorkRunV1())
					if len(work.Entered) != 64 { t.Fatal("rejected call entered target") }
					if drain {
						stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Millisecond)
						err := app.Stop(stopCtx)
						stopCancel()
						if err == nil || work.Stops.Load() != 0 { t.Fatalf("drain released live dependencies: %v", err) }
					}
					finish()
					synctest.Wait()
					if !drain {
						response, err := app.Interfaces().WorkRunV1().Run(context.Background(), runv1.Request{})
						if err != nil || response.Value != "finished" { t.Fatalf("capacity not released: %#v, %v", response, err) }
					}
					if err := app.Stop(context.Background()); err != nil || work.Stops.Load() != 1 { t.Fatalf("cleanup: %v, stops %d", err, work.Stops.Load()) }
				}) })
			}
		}
	}
}

func assertSaturated(t *testing.T, target runv1.Interface) {
	t.Helper()
	response, err := target.Run(context.Background(), runv1.Request{})
	var failure *invocation.Error
	if response != (runv1.Response{}) || !errors.As(err, &failure) || failure.Code() != invocation.ErrorResourceExhausted || invocation.CompletionOf(err) != invocation.CompletionNotStarted { t.Fatalf("saturation: %#v, %v", response, err) }
}

func assertTransportSaturated(t *testing.T, target runv1.Interface) {
	t.Helper()
	handler, err := adapter.New(func(ctx context.Context, _ http.Header) (context.Context, error) { return ctx, nil }, target)
	if err != nil { t.Fatal(err) }
	method, err := schema.Method("plystra.generated.work.run.v1.WorkRunV1Service.Invoke")
	if err != nil { t.Fatal(err) }
	response, err := handler.Invoke(context.Background(), connect.NewRequest(dynamicpb.NewMessage(method.Input())))
	var failure *connect.Error
	if response != nil || !errors.As(err, &failure) || failure.Code() != connect.CodeResourceExhausted || len(failure.Details()) != 1 { t.Fatalf("Connect saturation: %#v, %v", response, err) }
	descriptor, err := schema.Message("plystra.generated.transport.v1.PlystraErrorDetail")
	if err != nil { t.Fatal(err) }
	detail := dynamicpb.NewMessage(descriptor)
	if err := proto.Unmarshal(failure.Details()[0].Bytes(), detail); err != nil { t.Fatal(err) }
	if detail.Get(descriptor.Fields().ByNumber(4)).String() != "resource_exhausted" || detail.Get(descriptor.Fields().ByNumber(6)).String() != "not_started" { t.Fatalf("unsafe completion: %v", detail) }
	request := httptest.NewRequest(http.MethodPost, adapter.Procedure, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	if writer.Code != http.StatusTooManyRequests || !strings.Contains(writer.Body.String(), "resource_exhausted") || strings.Contains(writer.Body.String(), "private") { t.Fatalf("HTTP saturation: %d %s", writer.Code, writer.Body.String()) }
}
`
