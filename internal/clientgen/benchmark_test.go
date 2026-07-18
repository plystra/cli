package clientgen_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	kernelinvocation "github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/plugin"
)

// These benchmark-only wrappers mirror the emitted application invocation,
// client, and Alias method call graph around a real published Kernel catalog.
type benchmarkGeneratedRequest struct {
	value uint64
}

type benchmarkGeneratedResponse struct {
	value uint64
}

type benchmarkGeneratedInvocation struct {
	target kernelinvocation.Handle[benchmarkGeneratedRequest, benchmarkGeneratedResponse]
}

func (h benchmarkGeneratedInvocation) Available() bool { return h.target.Available() }

func (h benchmarkGeneratedInvocation) Invoke(ctx context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	return h.target.Invoke(ctx, request)
}

type benchmarkGeneratedHandle interface {
	Available() bool
	Invoke(context.Context, benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error)
}

type benchmarkGeneratedCanonicalClient struct {
	handle benchmarkGeneratedHandle
}

func (c benchmarkGeneratedCanonicalClient) Send(ctx context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	if c.handle == nil || !c.handle.Available() {
		return benchmarkGeneratedResponse{}, errBenchmarkUnavailable
	}
	return c.handle.Invoke(ctx, request)
}

var errBenchmarkUnavailable = errors.New("generated Capability client is unavailable")

type benchmarkGeneratedAliasClient struct {
	target benchmarkGeneratedCanonicalClient
}

func (c benchmarkGeneratedAliasClient) Deliver(ctx context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	return c.target.Send(ctx, request)
}

func BenchmarkGeneratedCanonicalInvocation(b *testing.B) {
	client := benchmarkGeneratedCanonicalClient{
		handle: benchmarkGeneratedInvocation{target: benchmarkHandle(b)},
	}
	ctx := context.Background()
	request := benchmarkGeneratedRequest{value: 42}
	var response benchmarkGeneratedResponse
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response, err = client.Send(ctx, request)
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkForwardingResponse = response
}

func BenchmarkGeneratedAliasForwarding(b *testing.B) {
	client := benchmarkGeneratedAliasClient{
		target: benchmarkGeneratedCanonicalClient{
			handle: benchmarkGeneratedInvocation{target: benchmarkHandle(b)},
		},
	}
	ctx := context.Background()
	request := benchmarkGeneratedRequest{value: 42}
	var response benchmarkGeneratedResponse
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response, err = client.Deliver(ctx, request)
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkForwardingResponse = response
}

func benchmarkNoopCanonicalTarget(_ context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	return benchmarkGeneratedResponse(request), nil
}

func benchmarkHandle(b testing.TB) kernelinvocation.Handle[benchmarkGeneratedRequest, benchmarkGeneratedResponse] {
	b.Helper()
	contract := capability.MustParseContract[benchmarkGeneratedRequest, benchmarkGeneratedResponse]("benchmark.send/v1")
	endpoint, err := kernelinvocation.NewEndpoint(contract, benchmarkNoopCanonicalTarget)
	if err != nil {
		b.Fatalf("NewEndpoint: %v", err)
	}
	providerID, err := plugin.ParseID("benchmark.provider")
	if err != nil {
		b.Fatalf("ParseID: %v", err)
	}
	providerBuild, err := kernelinvocation.NewModuleBuild("example.com/benchmark", "v1.0.0", "")
	if err != nil {
		b.Fatalf("NewModuleBuild: %v", err)
	}
	binding, err := kernelinvocation.NewBinding(kernelinvocation.BindingOptions{
		ProviderKind:    kernelinvocation.ProviderKindPlugin,
		ProviderID:      providerID,
		ProviderPackage: "example.com/benchmark/provider",
		ProviderBuild:   providerBuild,
		SelectionReason: kernelinvocation.SelectionReasonSoleProvider,
		SchemaDigest:    sha256.Sum256([]byte("benchmark.send/v1")),
	}, endpoint)
	if err != nil {
		b.Fatalf("NewBinding: %v", err)
	}
	catalog, err := kernelinvocation.NewCatalog([]kernelinvocation.Binding{binding})
	if err != nil {
		b.Fatalf("NewCatalog: %v", err)
	}
	dispatcher, err := kernelinvocation.NewDispatcher(kernelinvocation.DispatcherOptions{DefaultTimeout: 30 * time.Second})
	if err != nil {
		b.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		b.Fatalf("Publish: %v", err)
	}
	handle, err := kernelinvocation.NewHandle(dispatcher, contract, true)
	if err != nil {
		b.Fatalf("NewHandle: %v", err)
	}
	return handle
}

var benchmarkForwardingResponse benchmarkGeneratedResponse
