package applicationgenerate_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/testmodulecache"
	"github.com/plystra/cli/internal/version"
)

func generateRetryProject(t *testing.T) string {
	t.Helper()
	testmodulecache.Ensure(t,
		connectgen.ConnectModulePath+"@"+connectgen.ConnectModuleVersion,
		connectgen.ProtobufModulePath+"@"+connectgen.ProtobufModuleVersion,
		"github.com/google/go-cmp@v0.7.0",
		"github.com/golang/protobuf@v1.5.0",
	)
	root := t.TempDir()
	writeModule(t, root, "example.com/retry", "require github.com/plystra/kernel "+version.KernelVersion+"\n")
	downloadModuleDependencies(t, root)
	writeFile(t, filepath.Join(root, "plystra.yaml"), retryConfiguration)
	for _, name := range []string{"work", "outer", "plain"} {
		writeAssemblyInterface(t, root, name+"/run/v1", "runv1", name+".run/v1", "Run", strings.ReplaceAll(`
type Request struct { Mode string ~plystra:"1" json:"mode"~; Data []byte ~plystra:"2" json:"data"~; Labels map[string]string ~plystra:"3" json:"labels"~ }
type Response struct { Value string ~plystra:"1" json:"value"~ }
`, "~", "`"))
	}
	writeFile(t, filepath.Join(root, "interfaces/work/run/v1/interface.yaml"), "errors: [{code: rejected}]\nconstraints: {request.mode: {max_length: 64}, response.value: {max_length: 8}}\n")
	writeFile(t, filepath.Join(root, "work/service.go"), retryImplementation)
	tidy := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	tidy.Dir, tidy.Env = root, goEnvironment(map[string]string{"GOWORK": "off"})
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("prepare authored retry Project: %v\n%s", err, output)
	}
	downloadModuleDependencies(t, root)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	return root
}

func TestGeneratedReplaySafeRetryExecution(t *testing.T) {
	root := generateRetryProject(t)
	writeFile(t, filepath.Join(root, "retry_test.go"), generatedRetryTest)
	cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", "./...")
	cmd.Dir, cmd.Env = root, goEnvironment(map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly"})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated retry execution: %v\n%s", err, output)
	}
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generated retry drift = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	if err := os.Remove(filepath.Join(root, "retry_test.go")); err != nil {
		t.Fatal(err)
	}
	dependency := t.TempDir()
	writeModule(t, dependency, "example.com/retry-policy", "")
	writeFile(t, filepath.Join(dependency, "plystra.yaml"), "composition: {exports: {defaults: {interfaces: {policies: {work.run/v1: {timeout: 5s, retry: {eligibility: replay_safe}}}}}}}\n")
	mod := string(readFile(t, root, "go.mod"))
	writeFile(t, filepath.Join(root, "go.mod"), mod+"\nrequire example.com/retry-policy v1.0.0\nreplace example.com/retry-policy => "+filepath.ToSlash(dependency)+"\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "composition: {adopt: [{module: example.com/retry-policy, export: defaults}]}\ninterfaces: {require: [work.run/v1]}\n")
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {policies: {work.run/v1: {timeout: 5s, retry: {eligibility: replay_safe, max_attempts: 3}}}}\n")
	writeFile(t, filepath.Join(root, "deploy/customer.yaml"), "interfaces: {require: [work.run/v1], policies: {work.run/v1: {timeout: 5s, retry: {eligibility: replay_safe, max_attempts: 4}}}}\n")
	for _, selected := range []struct {
		name      string
		arguments []string
		attempts  int
	}{
		{"adopted", nil, 2},
		{"environment", []string{"--env", "production"}, 3},
		{"replacement", []string{"--config", "deploy/customer.yaml"}, 4},
	} {
		t.Run(selected.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			if code := command.RunIn(append([]string{"generate"}, selected.arguments...), &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
				t.Fatalf("selected retry generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
			}
			writeFile(t, filepath.Join(root, "retry_test.go"), fmt.Sprintf(generatedSelectedRetryTest, selected.arguments, selected.attempts))
			cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", "./...")
			cmd.Dir, cmd.Env = root, goEnvironment(map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly"})
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("selected retry execution: %v\n%s", err, output)
			}
			if err := os.Remove(filepath.Join(root, "retry_test.go")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

const generatedSelectedRetryTest = `package retry_test
import (
	"context"
	"errors"
	"testing"
	bootstrap "example.com/retry/generated/go/bootstrap"
	runv1 "example.com/retry/interfaces/work/run/v1"
	"example.com/retry/work"
	"github.com/plystra/kernel/invocation"
)
func TestSelectedRetry(t *testing.T) {
	app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: %#v})
	if err != nil { t.Fatal(err) }
	defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
	_, err = app.Interfaces().WorkRunV1().Run(context.Background(), runv1.Request{Mode: "unavailable", Data: []byte{7}, Labels: map[string]string{"value": "original"}})
	var boundary *invocation.Error
	const attempts = %d
	if !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorUnavailable || boundary.Attempts() != attempts || work.Calls.Load() != attempts { t.Fatal("selected retry not enforced", err) }
}
`

const retryConfiguration = `interfaces:
  require: [work.run/v1, outer.run/v1, plain.run/v1]
  policies:
    work.run/v1:
      timeout: 5s
      retry: {eligibility: replay_safe, max_attempts: 3, backoff: 2s}
    outer.run/v1:
      timeout: 5s
      retry: {eligibility: replay_safe, max_attempts: 3, backoff: 2s}
http: {expose: {work.run/v1: {transport: connect}}}
`

const retryImplementation = `package work
import (
	"context"
	"errors"
	"sync/atomic"
	"time"
	runv1 "example.com/retry/interfaces/work/run/v1"
	outerv1 "example.com/retry/interfaces/outer/run/v1"
	plainv1 "example.com/retry/interfaces/plain/run/v1"
	"github.com/plystra/kernel/invocation"
)
var Calls, OuterCalls, Stops, Constructions atomic.Int32
var Entered, Release chan struct{}
var Late func() (runv1.Response, error)
type service struct{}
//plystra:implements work.run/v1
func New() (*service, error) { Constructions.Add(1); return &service{}, nil }
func (*service) Start(context.Context) error { return nil }
func (*service) Stop(context.Context) error { Stops.Add(1); return nil }
func failure(code invocation.ErrorCode) error {
	err, invalid := invocation.NewError(code, "test.outcome")
	if invalid != nil { panic(invalid) }; return err
}
func (*service) Run(ctx context.Context, request runv1.Request) (runv1.Response, error) {
	number := int(Calls.Add(1))
	current, ok := invocation.Current(ctx)
	if !ok || !current.RetryOwner().Valid() { return runv1.Response{}, errors.New("missing retry owner") }
	if request.Mode == "nested-outer" {
		if current.Attempt() != 1 || current.RetryOwner() != current.ParentInvocationID() { return runv1.Response{}, errors.New("nested retry was not suppressed") }
	} else if request.Mode != "hold" && (current.Attempt() != number || current.RetryOwner() != current.InvocationID()) {
		return runv1.Response{}, errors.New("wrong retry identity or attempt")
	}
	if len(request.Data) != 1 || request.Data[0] != 7 || request.Labels["value"] != "original" { return runv1.Response{}, errors.New("attempt reused mutated input") }
	request.Data[0] = 99
	request.Labels["value"] = "mutated"
	switch request.Mode {
	case "success":
		if number == 3 { return runv1.Response{Value: "done"}, nil }
	case "exhausted": return runv1.Response{}, failure(invocation.ErrorResourceExhausted)
	case "unknown": return runv1.Response{}, invocation.NewResultUnknown(failure(invocation.ErrorUnavailable))
	case "semantic": return runv1.Response{}, invocation.NewSemanticError("rejected", errors.New("private reason"))
	case "internal": return runv1.Response{}, errors.New("private failure")
	case "panic": panic("private panic")
	case "invalid-response": return runv1.Response{Value: "invalid response"}, nil
	case "cancelled": return runv1.Response{}, failure(invocation.ErrorCancelled)
	case "deadline": return runv1.Response{}, failure(invocation.ErrorTimeout)
	case "denied": return runv1.Response{}, failure(invocation.ErrorDenied)
	case "budget": time.Sleep(time.Second)
	case "hold": Entered <- struct{}{}; <-Release; if Late != nil { return Late() }
	}
	return runv1.Response{Value: "discard"}, failure(invocation.ErrorUnavailable)
}
type outer struct { target runv1.Interface }
//plystra:implements outer.run/v1
func NewOuter(target runv1.Interface) (*outer, error) { return &outer{target}, nil }
func (s *outer) Run(ctx context.Context, request outerv1.Request) (outerv1.Response, error) {
	OuterCalls.Add(1)
	response, err := s.target.Run(ctx, runv1.Request(request))
	return outerv1.Response(response), err
}
type plain struct { target runv1.Interface }
//plystra:implements plain.run/v1
func NewPlain(target runv1.Interface) (*plain, error) { return &plain{target}, nil }
func (s *plain) Run(ctx context.Context, request plainv1.Request) (plainv1.Response, error) {
	OuterCalls.Add(1)
	response, err := s.target.Run(ctx, runv1.Request(request))
	return plainv1.Response(response), err
}
`

const generatedRetryTest = `package retry_test
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	bootstrap "example.com/retry/generated/go/bootstrap"
	adapter "example.com/retry/generated/go/adapters/connect/work/run/v1"
	runv1 "example.com/retry/interfaces/work/run/v1"
	outerv1 "example.com/retry/interfaces/outer/run/v1"
	plainv1 "example.com/retry/interfaces/plain/run/v1"
	"example.com/retry/work"
	"github.com/plystra/kernel/invocation"
)
func request(mode string) runv1.Request {
	return runv1.Request{Mode: mode, Data: []byte{7}, Labels: map[string]string{"value": "original"}}
}
func outcome(t *testing.T, err error, code invocation.ErrorCode, completion invocation.Completion, attempts int) {
	t.Helper()
	var boundary *invocation.Error
	if !errors.As(err, &boundary) || boundary.Code() != code || invocation.CompletionOf(err) != completion || boundary.Attempts() != attempts { t.Fatalf("outcome = %v; want %s, %s, %d attempts", err, code, completion, attempts) }
	if strings.Contains(err.Error(), "private") { t.Fatal("private cause escaped") }
}
func TestEligibilityAndFreshAttempts(t *testing.T) {
	for _, test := range []struct { mode string; attempts int; code invocation.ErrorCode; completion invocation.Completion }{
		{"success", 3, "", invocation.CompletionResultKnown},
		{"unavailable", 3, invocation.ErrorUnavailable, invocation.CompletionResultKnown},
		{"exhausted", 3, invocation.ErrorResourceExhausted, invocation.CompletionResultKnown},
		{"unknown", 1, invocation.ErrorUnavailable, invocation.CompletionResultUnknown},
		{"semantic", 1, "", invocation.CompletionResultKnown},
		{"internal", 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
		{"panic", 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
		{"invalid-response", 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
		{"cancelled", 1, invocation.ErrorCancelled, invocation.CompletionResultKnown},
		{"deadline", 1, invocation.ErrorTimeout, invocation.CompletionResultKnown},
		{"denied", 1, invocation.ErrorDenied, invocation.CompletionResultKnown},
	} {
		t.Run(test.mode, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			work.Calls.Store(0)
			app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err != nil { t.Fatal(err) }
			defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
			input := request(test.mode)
			start := time.Now()
			response, err := app.Interfaces().WorkRunV1().Run(context.Background(), input)
			if work.Calls.Load() != int32(test.attempts) || time.Since(start) != time.Duration(test.attempts-1)*2*time.Second { t.Fatalf("attempts or backoff: calls %d, elapsed %v, error %v", work.Calls.Load(), time.Since(start), err) }
			if input.Data[0] != 7 || input.Labels["value"] != "original" { t.Fatal("caller input mutated") }
			switch test.mode {
			case "success": if err != nil || response.Value != "done" { t.Fatal("retry did not recover", response, err) }
			case "semantic":
				var semantic *invocation.SemanticError
				if !errors.As(err, &semantic) || semantic.Code() != "rejected" || semantic.Attempts() != 1 || invocation.CompletionOf(err) != invocation.CompletionResultKnown { t.Fatal("semantic outcome lost", err) }
			default: outcome(t, err, test.code, test.completion, test.attempts)
			}
			if err != nil && response != (runv1.Response{}) { t.Fatal("failed response escaped", response) }
		}) })
	}
}
func TestNestedOwnership(t *testing.T) {
	for _, outerOwns := range []bool{true, false} {
		synctest.Test(t, func(t *testing.T) {
			work.Calls.Store(0); work.OuterCalls.Store(0)
			app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err != nil { t.Fatal(err) }
			defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
			want := 1
			if outerOwns {
				want = 3
				_, err = app.Interfaces().OuterRunV1().Run(context.Background(), outerv1.Request(request("nested-outer")))
			} else {
				_, err = app.Interfaces().PlainRunV1().Run(context.Background(), plainv1.Request(request("nested-inner")))
			}
			outcome(t, err, invocation.ErrorUnavailable, invocation.CompletionResultKnown, want)
			if work.OuterCalls.Load() != int32(want) || work.Calls.Load() != 3 { t.Fatalf("nested attempts multiplied: outer %d, inner %d", work.OuterCalls.Load(), work.Calls.Load()) }
		})
	}
}
func TestConnectUsesOnlyBindingRetries(t *testing.T) {
	for _, test := range []struct { mode string; status, attempts int; body string }{
		{"success", http.StatusOK, 3, "done"},
		{"unavailable", http.StatusServiceUnavailable, 3, "unavailable"},
		{"semantic", http.StatusBadRequest, 1, "rejected"},
	} {
		t.Run(test.mode, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			work.Calls.Store(0)
			app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err != nil { t.Fatal(err) }
			defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
			handler, err := adapter.New(func(ctx context.Context, _ http.Header) (context.Context, error) { return ctx, nil }, app.Interfaces().WorkRunV1())
			if err != nil { t.Fatal(err) }
			input := httptest.NewRequest(http.MethodPost, adapter.Procedure, strings.NewReader("{\"mode\":\""+test.mode+"\",\"data\":\"Bw==\",\"labels\":{\"value\":\"original\"}}"))
			input.Header.Set("Content-Type", "application/json")
			input.Header.Set("Connect-Protocol-Version", "1")
			output := httptest.NewRecorder()
			handler.ServeHTTP(output, input)
			if output.Code != test.status || !strings.Contains(output.Body.String(), test.body) || work.Calls.Load() != int32(test.attempts) { t.Fatalf("transport outcome = %d %s; attempts %d", output.Code, output.Body.String(), work.Calls.Load()) }
			if strings.Contains(output.Body.String(), "private") { t.Fatal("transport exposed private cause") }
		}) })
	}
}
func TestRequestValidationPrecedesAttempts(t *testing.T) {
	work.Calls.Store(0)
	app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil { t.Fatal(err) }
	defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
	_, err = app.Interfaces().WorkRunV1().Run(context.Background(), request(strings.Repeat("x", 65)))
	if err == nil || invocation.CompletionOf(err) != invocation.CompletionNotStarted || work.Calls.Load() != 0 { t.Fatal("invalid request reached retry target", err) }
}
func TestRuntimeRetryCompatibilityBeforeConstruction(t *testing.T) {
	original, err := os.ReadFile("plystra.yaml")
	if err != nil { t.Fatal(err) }
	defer func() { if err := os.WriteFile("plystra.yaml", original, 0600); err != nil { t.Error(err) } }()
	for _, retry := range []string{
		"{eligibility: replay_safe}",
		"{eligibility: replay_safe, max_attempts: 2, backoff: 2s}",
		"{eligibility: replay_safe, max_attempts: 3, backoff: 1s}",
		"null", "[]", "{}", "{max_attempts: 3}", "{eligibility: idempotent}",
		"{eligibility: replay_safe, max_attempts: 1}", "{eligibility: replay_safe, max_attempts: 17}",
		"{eligibility: replay_safe, max_attempts: '3'}", "{eligibility: replay_safe, max_attempts: 3.0}",
		"{eligibility: replay_safe, max_attempts: 0x3}", "{eligibility: replay_safe, max_attempts: 1_0}",
		"{eligibility: replay_safe, max_attempts: -3}", "{eligibility: replay_safe, max_attempts: 999999999999999999999}",
		"{eligibility: replay_safe, backoff: -1ns}", "{eligibility: replay_safe, backoff: 0}",
		"{eligibility: replay_safe, backoff: ' 0s'}", "{eligibility: replay_safe, backoff: 2562047h47m16.854775808s}",
		"{eligibility: replay_safe, backoff: '"+strings.Repeat("0", 65)+"s'}",
		"{eligibility: replay_safe, unknown: true}",
	} {
		t.Run(retry, func(t *testing.T) {
			changed := strings.Replace(string(original), "{eligibility: replay_safe, max_attempts: 3, backoff: 2s}", retry, 1)
			if err := os.WriteFile("plystra.yaml", []byte(changed), 0600); err != nil { t.Fatal(err) }
			before := work.Constructions.Load()
			app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err == nil { _ = app.Stop(context.Background()); t.Fatal("runtime retry change accepted") }
			if work.Constructions.Load() != before { t.Fatal("invalid runtime policy reached construction") }
		})
	}
	for _, changed := range []string{
		strings.Replace(string(original), "timeout: 5s", "", 1),
		strings.Replace(string(original), "      retry: {eligibility: replay_safe, max_attempts: 3, backoff: 2s}\n", "", 1),
	} {
		if err := os.WriteFile("plystra.yaml", []byte(changed), 0600); err != nil { t.Fatal(err) }
		before := work.Constructions.Load()
		app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
		if err == nil { _ = app.Stop(context.Background()); t.Fatal("removed policy field accepted") }
		if work.Constructions.Load() != before { t.Fatal("removed field reached construction") }
	}
	for _, changed := range []string{
		strings.ReplaceAll(strings.ReplaceAll(string(original), "timeout: 5s", "timeout: 5000ms"), "backoff: 2s", "backoff: 2000ms"),
		strings.Replace(string(original), "  policies:\n", "  policies:\n    dormant.run/v1: {timeout: 1s, retry: {eligibility: replay_safe}}\n", 1),
	} {
		if err := os.WriteFile("plystra.yaml", []byte(changed), 0600); err != nil { t.Fatal(err) }
		app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
		if err != nil { t.Fatal("equivalent or dormant retry changed executable identity", err) }
		if err := app.Stop(context.Background()); err != nil { t.Fatal(err) }
	}
}
func TestTotalBudgetAndInterruptibleBackoff(t *testing.T) {
	for _, mode := range []string{"budget", "caller-deadline", "cancel", "shutdown"} {
		t.Run(mode, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			work.Calls.Store(0)
			app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err != nil { t.Fatal(err) }
			defer func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
			ctx, cancel := context.WithCancel(context.Background()); defer cancel()
			if mode == "caller-deadline" { cancel(); ctx, cancel = context.WithTimeout(context.Background(), time.Second); defer cancel() }
			start := time.Now()
			done := make(chan error, 1)
			go func() { _, err := app.Interfaces().WorkRunV1().Run(ctx, request(mode)); done <- err }()
			synctest.Wait()
			if work.Calls.Load() != 1 { t.Fatal("initial attempt missing") }
			code, attempts, elapsed := invocation.ErrorTimeout, 1, time.Second
			switch mode {
			case "budget": attempts, elapsed = 2, 5*time.Second
			case "cancel": code, elapsed = invocation.ErrorCancelled, 0; cancel()
			case "shutdown":
				code, elapsed = invocation.ErrorUnavailable, 0
				if err := app.Stop(context.Background()); err != nil { t.Fatal(err) }
			}
			err = <-done
			outcome(t, err, code, invocation.CompletionResultKnown, attempts)
			if work.Calls.Load() != int32(attempts) || time.Since(start) != elapsed { t.Fatalf("logical budget reset: calls %d elapsed %v", work.Calls.Load(), time.Since(start)) }
		}) })
	}
}
func TestLateTargetsNeverReplayAndKeepDependenciesAlive(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "policy-timeout"} {
		t.Run(mode, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			work.Calls.Store(0); work.Stops.Store(0)
			work.Entered, work.Release = make(chan struct{}, 64), make(chan struct{})
			app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
			if err != nil { t.Fatal(err) }
			if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
			var release sync.Once
			finish := func() { release.Do(func() { close(work.Release) }) }
			defer func() { finish(); if err := app.Stop(context.Background()); err != nil { t.Error(err) } }()
			ctx, cancel := context.WithCancel(context.Background()); defer cancel()
			if mode == "deadline" { cancel(); ctx, cancel = context.WithTimeout(context.Background(), time.Second); defer cancel() }
			done := make(chan error, 64)
			for range 64 { go func() { _, err := app.Interfaces().WorkRunV1().Run(ctx, request("hold")); done <- err }() }
			synctest.Wait()
			if len(work.Entered) != 64 { t.Fatalf("target entries = %d", len(work.Entered)) }
			code := invocation.ErrorTimeout
			if mode == "cancel" { code = invocation.ErrorCancelled; cancel() }
			for range 64 { outcome(t, <-done, code, invocation.CompletionResultUnknown, 1) }
			_, err = app.Interfaces().WorkRunV1().Run(context.Background(), request("unavailable"))
			outcome(t, err, invocation.ErrorResourceExhausted, invocation.CompletionNotStarted, 3)
			if work.Calls.Load() != 64 { t.Fatal("retry overlapped live target") }
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Millisecond)
			err = app.Stop(stopCtx); stopCancel()
			if err == nil || work.Stops.Load() != 0 { t.Fatal("drain released dependencies before target termination", err) }
			finish(); synctest.Wait()
			if err := app.Stop(context.Background()); err != nil || work.Stops.Load() != 1 || work.Calls.Load() != 64 { t.Fatal("late completion replayed or prevented cleanup", err) }
		}) })
	}
}
`
