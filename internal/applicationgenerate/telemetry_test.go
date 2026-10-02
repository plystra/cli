package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/command"
)

func TestGeneratedInvocationLifetimeTelemetry(t *testing.T) {
	root := generateRetryProject(t)
	dependency := exec.CommandContext(t.Context(), "go", "get", "go.opentelemetry.io/otel/sdk/metric@v1.46.0")
	dependency.Dir, dependency.Env = root, goEnvironment(map[string]string{"GOWORK": "off"})
	if output, err := dependency.CombinedOutput(); err != nil {
		t.Fatalf("prepare telemetry collector: %v\n%s", err, output)
	}
	writeFile(t, filepath.Join(root, "telemetry_test.go"), generatedTelemetryTest)
	tidy := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	tidy.Dir, tidy.Env = root, goEnvironment(map[string]string{"GOWORK": "off"})
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("prepare telemetry Project: %v\n%s", err, output)
	}
	downloadModuleDependencies(t, root)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate telemetry Project = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	compiled := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", "./...")
	compiled.Dir, compiled.Env = root, goEnvironment(map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly"})
	if output, err := compiled.CombinedOutput(); err != nil {
		t.Fatalf("generated telemetry execution: %v\n%s", err, output)
	}
	stdout.Reset()
	stderr.Reset()
	if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("telemetry generated drift = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
}

const generatedTelemetryTest = `package retry_test
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
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
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)
const callerMetric = "plystra.invocation.caller.duration"
const targetMetric = "plystra.invocation.target.duration"
func request(mode string) runv1.Request {
	return runv1.Request{Mode: mode, Data: []byte{7}, Labels: map[string]string{"value": "original"}}
}
func metricApp(t *testing.T) (*bootstrap.Application, *sdkmetric.ManualReader) {
	t.Helper()
	work.Calls.Store(0); work.OuterCalls.Store(0); work.Stops.Store(0)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prior := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prior); if err := provider.Shutdown(context.Background()); err != nil { t.Error(err) } })
	app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { if err := app.Stop(context.Background()); err != nil { t.Error(err) } })
	if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
	if len(collect(t, reader)) != 0 { t.Fatal("construction emitted invocation telemetry") }
	return app, reader
}
func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string][]metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil { t.Fatal(err) }
	result := make(map[string][]metricdata.HistogramDataPoint[float64])
	for _, scope := range data.ScopeMetrics {
		if scope.Scope.Name != "github.com/plystra/kernel/invocation" { t.Fatal("unknown scope", scope.Scope.Name) }
		for _, metric := range scope.Metrics {
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok || metric.Unit != "s" || (metric.Name != callerMetric && metric.Name != targetMetric) { t.Fatal("unexpected metric", metric.Name) }
			if len(histogram.DataPoints) == 0 { continue }
			result[metric.Name] = histogram.DataPoints
			for _, point := range histogram.DataPoints {
				for _, attr := range point.Attributes.ToSlice() {
					switch string(attr.Key) {
					case "plystra.interface.id", "plystra.implementation.constructor", "plystra.invocation.outcome", "plystra.error.code", "plystra.invocation.completion", "plystra.invocation.target.late":
					default: t.Fatal("unbounded label", attr.Key)
					}
					if strings.Contains(attr.Value.String(), "private") || strings.Contains(attr.Value.String(), "original") { t.Fatal("private data escaped") }
				}
			}
		}
	}
	return result
}
func row(t *testing.T, points []metricdata.HistogramDataPoint[float64], id, outcome, code, completion string, late bool, count uint64) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, point := range points {
		identity, _ := point.Attributes.Value("plystra.interface.id")
		status, _ := point.Attributes.Value("plystra.invocation.outcome")
		category, _ := point.Attributes.Value("plystra.error.code")
		certainty, _ := point.Attributes.Value("plystra.invocation.completion")
		isLate, _ := point.Attributes.Value("plystra.invocation.target.late")
		constructor, _ := point.Attributes.Value("plystra.implementation.constructor")
		if identity.AsString() == id && status.AsString() == outcome && category.AsString() == code && certainty.AsString() == completion && isLate.AsBool() == late {
			wantConstructor := "example.com/retry/work.New"
			if id == "outer.run/v1" { wantConstructor += "Outer" }
			if id == "plain.run/v1" { wantConstructor += "Plain" }
			if point.Count != count || constructor.AsString() != wantConstructor || point.Sum < 0 { t.Fatal("incorrect count, constructor, or duration", point) }
			return point
		}
	}
	t.Fatalf("missing %s %s %s %s late=%v in %#v", id, outcome, code, completion, late, points)
	return metricdata.HistogramDataPoint[float64]{}
}
func TestProxyTelemetryOutcomesAndNonEntry(t *testing.T) {
	for _, test := range []struct { mode, status, code, completion string; attempts uint64 }{
		{"success", "success", "", "result_known", 3},
		{"unavailable", "runtime_error", "unavailable", "result_known", 3},
		{"semantic", "semantic_error", "rejected", "result_known", 1},
		{"unknown", "runtime_error", "unavailable", "result_unknown", 1},
		{"internal", "runtime_error", "internal", "result_known", 1},
		{"panic", "runtime_error", "internal", "result_known", 1},
		{"invalid-response", "runtime_error", "internal", "result_known", 1},
		{strings.Repeat("x", 65), "runtime_error", "invalid_argument", "not_started", 0},
	} {
		t.Run(test.mode, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			app, reader := metricApp(t)
			_, err := app.Interfaces().WorkRunV1().Run(context.Background(), request(test.mode))
			if invocation.CompletionOf(err).String() != test.completion || uint64(work.Calls.Load()) != test.attempts { t.Fatal("wrong invocation result", err, work.Calls.Load()) }
			metrics := collect(t, reader)
			caller := row(t, metrics[callerMetric], "work.run/v1", test.status, test.code, test.completion, false, 1)
			if test.attempts == 0 {
				if len(metrics[targetMetric]) != 0 { t.Fatal("invalid request emitted target fact") }
				return
			}
			if test.mode == "success" {
				row(t, metrics[targetMetric], "work.run/v1", "runtime_error", "unavailable", "result_known", false, 2)
				row(t, metrics[targetMetric], "work.run/v1", "success", "", "result_known", false, 1)
			} else { row(t, metrics[targetMetric], "work.run/v1", test.status, test.code, test.completion, false, test.attempts) }
			if caller.Sum != float64(test.attempts-1)*2 { t.Fatal("caller duration omitted backoff", caller.Sum) }
		}) })
	}
}
func TestNestedTelemetryCountsLogicalCallsAndAttempts(t *testing.T) {
	for _, outerOwns := range []bool{false, true} { synctest.Test(t, func(t *testing.T) {
		app, reader := metricApp(t)
		id, innerCalls := "plain.run/v1", uint64(1)
		var err error
		if outerOwns {
			id, innerCalls = "outer.run/v1", 3
			_, err = app.Interfaces().OuterRunV1().Run(context.Background(), outerv1.Request(request("nested-outer")))
		} else { _, err = app.Interfaces().PlainRunV1().Run(context.Background(), plainv1.Request(request("nested-inner"))) }
		if err == nil || work.Calls.Load() != 3 { t.Fatal("nested retry result", err) }
		metrics := collect(t, reader)
		row(t, metrics[callerMetric], id, "runtime_error", "unavailable", "result_known", false, 1)
		row(t, metrics[callerMetric], "work.run/v1", "runtime_error", "unavailable", "result_known", false, innerCalls)
		row(t, metrics[targetMetric], id, "runtime_error", "unavailable", "result_known", false, innerCalls)
		row(t, metrics[targetMetric], "work.run/v1", "runtime_error", "unavailable", "result_known", false, 3)
	}) }
}
func TestConnectAddsNoCallerOrRetryTelemetryLayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, reader := metricApp(t)
		handler, err := adapter.New(func(ctx context.Context, _ http.Header) (context.Context, error) { return ctx, nil }, app.Interfaces().WorkRunV1())
		if err != nil { t.Fatal(err) }
		input := httptest.NewRequest(http.MethodPost, adapter.Procedure, strings.NewReader("{\"mode\":\"success\",\"data\":\"Bw==\",\"labels\":{\"value\":\"original\"}}"))
		input.Header.Set("Content-Type", "application/json"); input.Header.Set("Connect-Protocol-Version", "1")
		output := httptest.NewRecorder(); handler.ServeHTTP(output, input)
		if output.Code != http.StatusOK || work.Calls.Load() != 3 { t.Fatal("Connect outcome", output.Code, output.Body.String()) }
		metrics := collect(t, reader)
		row(t, metrics[callerMetric], "work.run/v1", "success", "", "result_known", false, 1)
		row(t, metrics[targetMetric], "work.run/v1", "runtime_error", "unavailable", "result_known", false, 2)
		row(t, metrics[targetMetric], "work.run/v1", "success", "", "result_known", false, 1)
		if len(metrics[callerMetric]) != 1 || len(metrics[targetMetric]) != 2 { t.Fatal("transport added telemetry") }
	})
}
func TestLateTargetTelemetryRetainsAdmissionAndLifecycle(t *testing.T) {
	for _, reason := range []string{"cancel", "deadline", "policy", "shutdown"} {
		for _, late := range []string{"success", "error", "panic", "exit"} {
			t.Run(reason+"/"+late, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
				app, reader := metricApp(t)
				work.Entered, work.Release = make(chan struct{}, 64), make(chan struct{})
				work.Late = func() (runv1.Response, error) {
					switch late {
					case "error": return runv1.Response{}, errors.New("private late failure")
					case "panic": panic("private late panic")
					case "exit": runtime.Goexit()
					}
					return runv1.Response{Value: "done"}, nil
				}
				var once sync.Once
				finish := func() { once.Do(func() { close(work.Release) }) }
				defer func() { finish(); _ = app.Stop(context.Background()); work.Late = nil }()
				ctx, cancel := context.WithCancel(context.Background()); defer cancel()
				if reason == "deadline" { cancel(); ctx, cancel = context.WithTimeout(context.Background(), time.Second); defer cancel() }
				done := make(chan error, 64)
				for range 64 { go func() { _, err := app.Interfaces().WorkRunV1().Run(ctx, request("hold")); done <- err }() }
				synctest.Wait()
				if len(work.Entered) != 64 { t.Fatal("targets not entered", len(work.Entered)) }
				if reason == "cancel" { cancel() }
				if reason == "shutdown" {
					stopCtx, stop := context.WithTimeout(context.Background(), time.Millisecond)
					if err := app.Stop(stopCtx); err == nil { t.Fatal("shutdown stopped live targets") }; stop()
				}
				for range 64 { if err := <-done; invocation.CompletionOf(err) != invocation.CompletionResultUnknown { t.Fatal(err) } }
				code := "timeout"; if reason == "cancel" || reason == "shutdown" { code = "cancelled" }
				metrics := collect(t, reader)
				row(t, metrics[callerMetric], "work.run/v1", "runtime_error", code, "result_unknown", false, 64)
				if len(metrics[targetMetric]) != 0 || work.Stops.Load() != 0 { t.Fatal("caller completion released target ownership") }
				if reason != "shutdown" {
					_, err := app.Interfaces().WorkRunV1().Run(context.Background(), request("unavailable"))
					var exhausted *invocation.Error
					if !errors.As(err, &exhausted) || exhausted.Code() != invocation.ErrorResourceExhausted || exhausted.Attempts() != 3 { t.Fatal("capacity released early", err) }
					row(t, collect(t, reader)[callerMetric], "work.run/v1", "runtime_error", "resource_exhausted", "not_started", false, 1)
				}
				finish(); synctest.Wait()
				if err := app.Stop(context.Background()); err != nil || work.Stops.Load() != 1 || work.Calls.Load() != 64 { t.Fatal("late work replayed or blocked cleanup", err) }
				metrics = collect(t, reader)
				row(t, metrics[callerMetric], "work.run/v1", "runtime_error", code, "result_unknown", false, 64)
				targetCode, completion := "internal", "result_known"
				if late == "success" { targetCode, completion = code, "result_unknown" }
				row(t, metrics[targetMetric], "work.run/v1", "runtime_error", targetCode, completion, true, 64)
			}) })
		}
	}
}
`
