package connectgen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/sdkmodel"
)

func TestGeneratedBrowserInvokesCanonicalCapabilityAndAlias(t *testing.T) {
	fixture := buildFixture(t, connectContract, "account.profile/v1")
	provenance := connectConfigurationProvenance(t, generation.ConfigurationModeDefault)
	handlers, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, nil, provenance)
	if err != nil {
		t.Fatalf("Render Connect handlers: %v", err)
	}
	contract, err := contractgen.Render([]byte(connectContract))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(connectContract))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	target := browserTargetView{targetView: fixture.target}
	alias := browserAliasView{aliasView: newAlias(t, "account.profile/v1", fixture.target)}
	model, err := sdkmodel.Build([]sdkmodel.CanonicalTargetView{target}, []sdkmodel.AliasView{alias})
	if err != nil {
		t.Fatalf("Build JavaScript SDK model: %v", err)
	}
	javaScript, err := javascriptgen.Render(javascriptgen.Options{
		PackageName:             "@acme/browser-acceptance",
		ConfigurationProvenance: provenance,
		Transport: javascriptgen.TransportOptions{
			Projection:    fixture.model,
			WireMap:       fixture.wireMap,
			DescriptorSet: fixture.descriptorSet,
		},
	}, model)
	if err != nil {
		t.Fatalf("Render JavaScript SDK: %v", err)
	}

	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	for _, file := range handlers {
		writeGeneratedFile(t, root, file.Path(), file.Data())
	}
	for _, file := range javaScript {
		writeGeneratedFile(t, root, file.Path(), file.Data())
	}
	writeGeneratedFile(t, root, "browser/index.html", []byte(browserCanonicalPage))
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(testKernelInvocationSource))
	writeGeneratedFile(t, root, "generated/go/adapters/connect/customer/profile/sync/v1/browser_test.go", []byte(generatedBrowserCanonicalRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire (\n\tconnectrpc.com/connect "+connectgen.ConnectModuleVersion+"\n\tgithub.com/plystra/kernel v0.0.0\n\tgoogle.golang.org/protobuf "+connectgen.ProtobufModuleVersion+"\n)\n\nreplace github.com/plystra/kernel => ./kernel\n"))

	download := exec.CommandContext(t.Context(), "go", "mod", "download", "all")
	download.Dir = root
	download.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if output, err := download.CombinedOutput(); err != nil {
		t.Fatalf("download generated browser module dependencies: %v\n%s", err, output)
	}
	validateGeneratedBrowserPackage(t, filepath.Join(root, "generated", "sdk", "javascript"))

	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "-run=^TestRealBrowserCanonicalAndAliasInvocation$", "./generated/go/adapters/connect/customer/profile/sync/v1")
	command.Dir = root
	command.Env = append(os.Environ(),
		"GOWORK=off",
		"GOFLAGS=-mod=readonly",
		"PLYSTRA_BROWSER_PROJECT_ROOT="+root,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated real-browser canonical acceptance: %v\n%s", err, output)
	}
}

type browserTargetView struct{ targetView }

func (browserTargetView) Exposure() generation.Exposure {
	return generation.Exposure{HTTP: true, JavaScript: true}
}

type browserAliasView struct{ aliasView }

func (browserAliasView) Exposure() generation.Exposure {
	return generation.Exposure{HTTP: true, JavaScript: true}
}

func validateGeneratedBrowserPackage(t testing.TB, root string) {
	t.Helper()
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = "npm.cmd"
	}
	npmPath, err := exec.LookPath(npm)
	if err != nil {
		t.Fatalf("real-browser acceptance requires npm on PATH: %v", err)
	}
	for _, arguments := range [][]string{
		{"install", "--ignore-scripts", "--no-audit", "--no-fund", "--package-lock=false"},
		{"run", "build"},
	} {
		command := exec.CommandContext(t.Context(), npmPath, arguments...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("validate generated browser package with npm %s: %v\n%s", strings.Join(arguments, " "), err, output)
		}
	}
}

const browserCanonicalPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Plystra browser canonical and Alias acceptance</title>
  <script type="importmap">
  {
    "imports": {
      "@bufbuild/protobuf": "/modules/@bufbuild/protobuf/dist/esm/index.js",
      "@bufbuild/protobuf/codegenv1": "/modules/@bufbuild/protobuf/dist/esm/codegenv1/index.js",
      "@bufbuild/protobuf/codegenv2": "/modules/@bufbuild/protobuf/dist/esm/codegenv2/index.js",
      "@bufbuild/protobuf/reflect": "/modules/@bufbuild/protobuf/dist/esm/reflect/index.js",
      "@bufbuild/protobuf/wire": "/modules/@bufbuild/protobuf/dist/esm/wire/index.js",
      "@bufbuild/protobuf/wkt": "/modules/@bufbuild/protobuf/dist/esm/wkt/index.js",
      "@connectrpc/connect": "/modules/@connectrpc/connect/dist/esm/index.js",
      "@connectrpc/connect/protocol": "/modules/@connectrpc/connect/dist/esm/protocol/index.js",
      "@connectrpc/connect/protocol-connect": "/modules/@connectrpc/connect/dist/esm/protocol-connect/index.js",
      "@connectrpc/connect/protocol-grpc": "/modules/@connectrpc/connect/dist/esm/protocol-grpc/index.js",
      "@connectrpc/connect/protocol-grpc-web": "/modules/@connectrpc/connect/dist/esm/protocol-grpc-web/index.js",
      "@connectrpc/connect-web": "/modules/@connectrpc/connect-web/dist/esm/index.js"
    }
  }
  </script>
</head>
<body data-result="pending">pending</body>
<script type="module">
  import { createPlystraClient } from "/sdk/index.js";

  try {
    const client = createPlystraClient({
      baseUrl: window.location.origin,
      getAccessToken: async () => "browser-token"
    });
    const canonical = await client.customer.profile.sync.v1({
      active: true,
      count: 42n,
      metadata: {source: "browser"},
      note: "canonical-browser",
      ratio: 1.5,
      records: [{id: "record-1"}],
      state: "ready",
      tags: ["one", "two"]
    });
    const alias = await client.account.profile.v1({
      active: true,
      count: 84n,
      metadata: {source: "alias"},
      note: "alias-browser",
      ratio: 1.5,
      records: [{id: "record-1"}],
      state: "ready",
      tags: ["one", "two"]
    });
    if (!canonical.accepted || canonical.count !== 42n || canonical.metadata.source !== "canonical" ||
        canonical.note !== "accepted" || canonical.ratio !== 1.5 || canonical.records[0].id !== "record-1" ||
        canonical.state !== "blocked" || canonical.tags.join(",") !== "one,two" ||
        !alias.accepted || alias.count !== 84n || alias.metadata.source !== "canonical" ||
        alias.note !== "accepted" || alias.ratio !== 1.5 || alias.records[0].id !== "record-1" ||
        alias.state !== "blocked" || alias.tags.join(",") !== "one,two") {
      throw new Error("unexpected canonical or Alias response");
    }
    document.body.dataset.result = "pass";
    document.body.textContent = "canonical:42:blocked;alias:84:blocked";
  } catch (error) {
    document.body.dataset.result = "fail";
    document.body.textContent = String(error && error.stack ? error.stack : error);
  }
</script>
</html>
`

const generatedBrowserCanonicalRuntimeTest = `package customerprofilesyncv1_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	canonicaladapter "example.com/acme/project/generated/go/adapters/connect/customer/profile/sync/v1"
	aliasadapter "example.com/acme/project/generated/go/adapters/connect/account/profile/v1"
	contract "example.com/acme/project/generated/go/contracts/customer/profile/sync/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/customer/profile/sync/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestRealBrowserCanonicalAndAliasInvocation(t *testing.T) {
	projectRoot := os.Getenv("PLYSTRA_BROWSER_PROJECT_ROOT")
	if projectRoot == "" {
		t.Fatal("PLYSTRA_BROWSER_PROJECT_ROOT is required")
	}
	sdkRoot := filepath.Join(projectRoot, "generated", "sdk", "javascript")
	var providerCalls atomic.Int32
	var rootCalls atomic.Int32
	target := kernelinvocation.NewTestHandle(func(_ context.Context, request contract.Request) (contract.Response, error) {
		providerCalls.Add(1)
		expectedCount := int64(42)
		expectedSource := "browser"
		if request.Note != nil && *request.Note == "alias-browser" {
			expectedCount = 84
			expectedSource = "alias"
		}
		if !request.Active || request.Count == nil || *request.Count != expectedCount || request.Metadata["source"] != expectedSource ||
			request.Note == nil || *request.Note != "canonical-browser" && *request.Note != "alias-browser" || request.Ratio != 1.5 || len(request.Records) != 1 ||
			request.Records[0]["id"] != "record-1" || request.State != contract.RequestStateReady ||
			len(request.Tags) != 2 || request.Tags[0] != "one" || request.Tags[1] != "two" {
			return contract.Response{}, fmt.Errorf("canonical browser request did not preserve the contract: %#v", request)
		}
		note := "accepted"
		return contract.Response{
			Accepted: true,
			Count: *request.Count,
			Metadata: map[string]any{"source": "canonical"},
			Note: &note,
			Ratio: request.Ratio,
			Records: []map[string]any{{"id": "record-1"}},
			State: contract.ResponseStateBlocked,
			Tags: append([]string(nil), request.Tags...),
		}, nil
	})
	canonical, err := canonicaladapter.New(func(parent context.Context, headers http.Header) (context.Context, error) {
		rootCalls.Add(1)
		if token := headers.Get("Authorization"); token != "Bearer browser-token" {
			return nil, fmt.Errorf("authorization header = %q", token)
		}
		return context.WithoutCancel(parent), nil
	}, applicationinvocation.New(target))
	if err != nil || !canonicaladapter.Available(canonical) {
		t.Fatalf("New canonical browser handler = %#v, %v", canonical, err)
	}
	alias, err := aliasadapter.New(canonical)
	if err != nil || !aliasadapter.Available(alias) {
		t.Fatalf("New Alias browser handler = %#v, %v", alias, err)
	}

	mux := http.NewServeMux()
	mux.Handle(canonicaladapter.Procedure, canonical)
	mux.Handle(aliasadapter.Procedure, alias)
	mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir(filepath.Join(sdkRoot, "dist")))))
	mux.Handle("/modules/", http.StripPrefix("/modules/", http.FileServer(http.Dir(filepath.Join(sdkRoot, "node_modules")))))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(projectRoot, "browser", "index.html"))
	})
	var canonicalRequests atomic.Int32
	var aliasRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case canonicaladapter.Procedure:
			canonicalRequests.Add(1)
		case aliasadapter.Procedure:
			aliasRequests.Add(1)
		}
		mux.ServeHTTP(writer, request)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	profile := t.TempDir()
	command := exec.CommandContext(ctx, browserExecutable(t),
		"--headless=new",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--disable-gpu",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-default-browser-check",
		"--no-first-run",
		"--no-sandbox",
		"--dump-dom",
		"--virtual-time-budget=10000",
		"--user-data-dir="+profile,
		server.URL,
	)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("real browser canonical invocation timed out: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("real browser canonical invocation failed: %v\n%s", err, output)
	}
	document := string(output)
	if !strings.Contains(document, "data-result=\"pass\"") || !strings.Contains(document, "canonical:42:blocked;alias:84:blocked") {
		t.Fatalf("real browser canonical result was not successful:\n%s", document)
	}
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("canonical Provider calls = %d, want 2", calls)
	}
	if calls := rootCalls.Load(); calls != 2 {
		t.Fatalf("trusted-root calls = %d, want 2", calls)
	}
	if calls := canonicalRequests.Load(); calls != 1 {
		t.Fatalf("canonical Connect requests = %d, want 1", calls)
	}
	if calls := aliasRequests.Load(); calls != 1 {
		t.Fatalf("Alias Connect requests = %d, want 1", calls)
	}
}

func browserExecutable(t *testing.T) string {
	t.Helper()
	if explicit := os.Getenv("PLYSTRA_BROWSER_EXECUTABLE"); explicit != "" {
		if resolved, err := exec.LookPath(explicit); err == nil {
			return resolved
		}
		if info, err := os.Stat(explicit); err == nil && info.Mode().IsRegular() {
			return explicit
		}
		t.Fatalf("PLYSTRA_BROWSER_EXECUTABLE %q is not an executable file", explicit)
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if resolved, err := exec.LookPath(name); err == nil {
			return resolved
		}
	}
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, candidate := range candidates {
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	t.Fatal("real-browser acceptance requires Chrome, Chromium, or Edge; install one or set PLYSTRA_BROWSER_EXECUTABLE")
	return ""
}
`
