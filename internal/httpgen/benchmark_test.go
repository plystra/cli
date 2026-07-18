package httpgen_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	kernelinvocation "github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/plugin"
)

const benchmarkHTTPRoute = "/api/v1/capabilities/benchmark.invoke/v1/invoke"

type benchmarkHTTPRequest struct {
	Value int64 `json:"value"`
}

type benchmarkHTTPResponse struct {
	Value int64 `json:"value"`
}

type benchmarkHTTPInvocation struct {
	target kernelinvocation.Handle[benchmarkHTTPRequest, benchmarkHTTPResponse]
}

func (h benchmarkHTTPInvocation) Invoke(ctx context.Context, request benchmarkHTTPRequest) (benchmarkHTTPResponse, error) {
	return h.target.Invoke(ctx, request)
}

// benchmarkGeneratedHTTPHandler mirrors the emitted happy-path transport for
// one required integer field around a real published Kernel handle. Generator
// integration tests separately compile and execute the exact rendered source.
type benchmarkGeneratedHTTPHandler struct {
	target benchmarkHTTPInvocation
}

func (h benchmarkGeneratedHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != benchmarkHTTPRoute || request.URL.RawPath != "" || request.URL.RawQuery != "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	contentTypes := request.Header.Values("Content-Type")
	if len(contentTypes) != 1 || !benchmarkJSONContentType(contentTypes[0]) {
		writer.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil || !benchmarkValidRequestObject(body) {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	var decoded benchmarkHTTPRequest
	if json.Unmarshal(body, &decoded) != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	response, err := h.target.Invoke(request.Context(), decoded)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	payload, err := json.Marshal(response)
	if err != nil || len(payload) > 1<<20 {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}

func benchmarkJSONContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || mediaType != "application/json" || len(parameters) > 1 {
		return false
	}
	charset, exists := parameters["charset"]
	return !exists || charset == "utf-8"
}

func benchmarkValidRequestObject(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	hasValue := false
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || name != "value" || hasValue {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		hasValue = true
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !hasValue {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func BenchmarkGeneratedHTTPInvocation(b *testing.B) {
	handler := benchmarkGeneratedHTTPHandler{target: benchmarkHTTPInvocation{target: benchmarkHTTPHandle(b)}}
	payload := []byte(`{"value":42}`)
	request := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: benchmarkHTTPRoute},
		Header: http.Header{"Content-Type": []string{"application/json"}},
	}
	writer := newBenchmarkResponseWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		request.Body = io.NopCloser(bytes.NewReader(payload))
		writer.reset()
		handler.ServeHTTP(writer, request)
		if writer.status != http.StatusOK || writer.bytesWritten != len(`{"value":42}`) {
			b.Fatalf("HTTP response = status %d, bytes %d", writer.status, writer.bytesWritten)
		}
	}
	benchmarkHTTPBytes = writer.bytesWritten
}

func benchmarkHTTPHandle(b testing.TB) kernelinvocation.Handle[benchmarkHTTPRequest, benchmarkHTTPResponse] {
	b.Helper()
	contract := capability.MustParseContract[benchmarkHTTPRequest, benchmarkHTTPResponse]("benchmark.invoke/v1")
	endpoint, err := kernelinvocation.NewEndpoint(contract, func(_ context.Context, request benchmarkHTTPRequest) (benchmarkHTTPResponse, error) {
		return benchmarkHTTPResponse(request), nil
	})
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
		SchemaDigest:    sha256.Sum256([]byte("benchmark.invoke/v1")),
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

type benchmarkResponseWriter struct {
	header       http.Header
	status       int
	bytesWritten int
}

func newBenchmarkResponseWriter() *benchmarkResponseWriter {
	return &benchmarkResponseWriter{header: make(http.Header, 3)}
}

func (w *benchmarkResponseWriter) Header() http.Header { return w.header }

func (w *benchmarkResponseWriter) WriteHeader(status int) { w.status = status }

func (w *benchmarkResponseWriter) Write(value []byte) (int, error) {
	w.bytesWritten += len(value)
	return len(value), nil
}

func (w *benchmarkResponseWriter) reset() {
	w.status = 0
	w.bytesWritten = 0
}

var benchmarkHTTPBytes int
