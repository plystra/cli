package clientgen_test

import (
	"context"
	"testing"
)

// These benchmark-only types mirror the emitted canonical invocation, client,
// and Alias method call graph. Golden and generated-module runtime tests in
// this package verify the production output that this harness measures.
type benchmarkGeneratedRequest struct {
	value uint64
}

type benchmarkGeneratedResponse struct {
	value uint64
}

type benchmarkCanonicalTarget func(context.Context, benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error)

type benchmarkGeneratedInvocation struct {
	target benchmarkCanonicalTarget
}

func (h benchmarkGeneratedInvocation) Invoke(ctx context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	return h.target(ctx, request)
}

type benchmarkGeneratedCanonicalClient struct {
	handle benchmarkGeneratedInvocation
}

func (c benchmarkGeneratedCanonicalClient) Send(ctx context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	return c.handle.Invoke(ctx, request)
}

type benchmarkGeneratedAliasClient struct {
	target benchmarkGeneratedCanonicalClient
}

func (c benchmarkGeneratedAliasClient) Deliver(ctx context.Context, request benchmarkGeneratedRequest) (benchmarkGeneratedResponse, error) {
	return c.target.Send(ctx, request)
}

func BenchmarkGeneratedCanonicalInvocation(b *testing.B) {
	client := benchmarkGeneratedCanonicalClient{
		handle: benchmarkGeneratedInvocation{target: benchmarkNoopCanonicalTarget},
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
			handle: benchmarkGeneratedInvocation{target: benchmarkNoopCanonicalTarget},
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

var benchmarkForwardingResponse benchmarkGeneratedResponse
