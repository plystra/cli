package generationexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
)

func TestHelperGeneratesNormalizedOutputWithSanitizedEnvironment(t *testing.T) {
	t.Setenv("PLYSTRA_TEST_SECRET", "must-not-reach-extension")
	fixture := newExtensionFixture(t, validExtensionSource)
	helper, err := Build(t.Context(), fixture.spec, fixture.options(2*time.Second))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	helperRoot := helper.root
	t.Cleanup(func() {
		if err := helper.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := os.Stat(filepath.Join(helperRoot, "main.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transient helper source remains after Build: %v", err)
	}
	entries, err := os.ReadDir(helperRoot)
	if err != nil {
		t.Fatalf("ReadDir(helper root): %v", err)
	}
	if names := entryNames(entries); !slices.Equal(names, []string{helperExecutableName(), "work"}) {
		t.Fatalf("helper artifacts = %v", names)
	}

	context := extensionContext(t, "success")
	output, err := helper.Generate(t.Context(), context)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	requirements := output.Requirements()
	if len(requirements) != 1 || requirements[0].RuleID != "authn.require-audit" || requirements[0].Capability.String() != "audit.write/v1" {
		t.Fatalf("Requirements = %#v", requirements)
	}
	diagnostics := output.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Code != "authn.verified" || diagnostics[0].Message != "verified context reused" {
		t.Fatalf("Diagnostics = %#v", diagnostics)
	}
	contributions := output.Contributions()
	if len(contributions) != 1 || contributions[0].ID() != "authn.verify" || contributions[0].Point() != generation.GenerationPointInvocationPrepare || !slices.Equal(contributions[0].Provides(), []generation.ContributionToken{"verified-authn-context"}) {
		t.Fatalf("Contributions = %#v", contributions)
	}
	nodes := contributions[0].Nodes()
	if len(nodes) != 1 || nodes[0].ID() != "attach-verification" || nodes[0].Kind() != generation.GeneratedNodeKindMetadataAttachment {
		t.Fatalf("Generated nodes = %#v", nodes)
	}
	attachment, ok := nodes[0].MetadataAttachment()
	if !ok || attachment.Key != "authn.verification" || attachment.Value.Literal == nil || attachment.Value.Literal.String == nil || *attachment.Value.Literal.String != "reused" {
		t.Fatalf("Metadata attachment = %#v, %v", attachment, ok)
	}
	aliases := output.AliasContributions()
	if len(aliases) != 1 || aliases[0].ID() != "authn.order-shortcut" || aliases[0].Alias().String() != "orders.submit/v1" || aliases[0].Target().String() != "order.create/v1" {
		t.Fatalf("AliasContributions = %#v", aliases)
	}
	if output.Digest() == "" || len(output.CanonicalJSON()) == 0 {
		t.Fatalf("normalized output = %q, %s", output.Digest(), output.CanonicalJSON())
	}

	if err := helper.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(helperRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper artifacts remain after Close: %v", err)
	}
	if _, err := helper.Generate(t.Context(), context); !errors.Is(err, ErrClosed) {
		t.Fatalf("Generate after Close error = %v, want ErrClosed", err)
	}
}

func TestHelperClassifiesBoundedExecutionFailures(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	helper, err := Build(t.Context(), fixture.spec, fixture.options(2*time.Second))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		if err := helper.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	tests := []struct {
		mode string
		want error
	}{
		{mode: "error", want: ErrExtension},
		{mode: "panic", want: ErrCrash},
		{mode: "crash", want: ErrCrash},
		{mode: "timeout", want: ErrTimeout},
		{mode: "malformed", want: ErrMalformedOutput},
		{mode: "stderr", want: ErrMalformedOutput},
		{mode: "invalid-output", want: ErrInvalidOutput},
		{mode: "oversized", want: ErrOutputTooLarge},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			ctx := t.Context()
			if test.mode == "timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			_, err := helper.Generate(ctx, extensionContext(t, test.mode))
			if !errors.Is(err, ErrExecute) || !errors.Is(err, test.want) {
				t.Fatalf("Generate error = %v, want ErrExecute and %v", err, test.want)
			}
			message := err.Error()
			for _, detail := range []string{`plugin "example.extension"`, `api "v1"`, `package "./generation"`, `namespaces [authn]`} {
				if !strings.Contains(message, detail) {
					t.Fatalf("Generate error omits %q: %v", detail, err)
				}
			}
			if strings.Contains(message, "person:secret") || strings.Contains(message, helper.root) {
				t.Fatalf("Generate error leaked private diagnostic data: %v", err)
			}
			if test.mode == "error" && !strings.Contains(message, "<redacted-url>") {
				t.Fatalf("extension error did not redact URL: %v", err)
			}
			assertDirectoryEmpty(t, helper.workingDirectory)
		})
	}
}

func TestHelperUsesFreshWorkingDirectoryForEveryInvocation(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	helper, err := Build(t.Context(), fixture.spec, fixture.options(2*time.Second))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		if err := helper.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if _, err := helper.Generate(t.Context(), extensionContext(t, "write-work")); err != nil {
		t.Fatalf("Generate(write-work): %v", err)
	}
	assertDirectoryEmpty(t, helper.workingDirectory)
	if _, err := helper.Generate(t.Context(), extensionContext(t, "success")); err != nil {
		t.Fatalf("Generate(success after write): %v", err)
	}
	assertDirectoryEmpty(t, helper.workingDirectory)
}

func TestHelperSupportsConcurrentInvocations(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	helper, err := Build(t.Context(), fixture.spec, fixture.options(2*time.Second))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		if err := helper.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	context := extensionContext(t, "success")
	const count = 8
	errorsByInvocation := make([]error, count)
	var wait sync.WaitGroup
	for index := range errorsByInvocation {
		wait.Add(1)
		go func() {
			defer wait.Done()
			output, err := helper.Generate(t.Context(), context)
			if err == nil && len(output.Requirements()) != 1 {
				err = errors.New("unexpected requirement count")
			}
			errorsByInvocation[index] = err
		}()
	}
	wait.Wait()
	for index, err := range errorsByInvocation {
		if err != nil {
			t.Fatalf("Generate[%d]: %v", index, err)
		}
	}
}

func TestHelperRejectsMismatchedContextBeforeExecution(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	helper, err := Build(t.Context(), fixture.spec, fixture.options(2*time.Second))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		if err := helper.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	input := inputFromContext(extensionContext(t, "success"))
	input.Plugins = slices.DeleteFunc(input.Plugins, func(plugin generation.PluginInput) bool {
		return plugin.ID == "example.extension"
	})
	missing, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(missing extension): %v", err)
	}
	if _, err := helper.Generate(t.Context(), missing); !errors.Is(err, ErrExecute) || !errors.Is(err, ErrContext) {
		t.Fatalf("Generate missing-extension error = %v", err)
	}

	input = inputFromContext(extensionContext(t, "success"))
	for index := range input.Plugins {
		if input.Plugins[index].ID == "example.extension" {
			input.Plugins[index].ModulePath = "example.com/different"
		}
	}
	mismatched, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(mismatched extension): %v", err)
	}
	if _, err := helper.Generate(t.Context(), mismatched); !errors.Is(err, ErrExecute) || !errors.Is(err, ErrContext) {
		t.Fatalf("Generate mismatched-extension error = %v", err)
	}
}

func TestValidateResponseShapeRejectsContradictoryEnvelopes(t *testing.T) {
	t.Parallel()
	tests := []helperResponse{
		{Status: "success", Error: "unexpected"},
		{Status: "extension-error"},
		{Status: "panic", Error: "panic", Output: generation.Output{Diagnostics: []generation.Diagnostic{{Message: "hidden"}}}},
		{Status: "panic", Error: "panic", Output: generation.Output{Contributions: []generation.Contribution{{ID: "hidden"}}}},
		{Status: "unknown", Error: "error"},
	}
	for _, response := range tests {
		if err := validateResponseShape(response); err == nil {
			t.Fatalf("validateResponseShape(%#v) unexpectedly succeeded", response)
		}
	}
	if err := validateResponseShape(helperResponse{Status: "success"}); err != nil {
		t.Fatalf("validateResponseShape(success): %v", err)
	}
	if err := validateResponseShape(helperResponse{Status: "extension-error", Error: "failed"}); err != nil {
		t.Fatalf("validateResponseShape(extension-error): %v", err)
	}
}

func TestDecodeResponseRejectsDuplicateUnknownAndTrailingData(t *testing.T) {
	t.Parallel()
	tests := []string{
		`{"api":"v1","api":"v1","status":"success","output":{"requirements":[],"diagnostics":[]}}`,
		`{"api":"v1","status":"success","output":{"requirements":[],"diagnostics":[],"unknown":true}}`,
		`{"api":"v1","status":"success","output":{"requirements":[],"diagnostics":[],"contributions":[{"id":"authn.verify","namespace":"authn","source":"order.create/v1","point":"invocation.prepare","requires":[],"provides":[],"nodes":[{"id":"attach","metadata_attachment":{"key":"authn.value","value":{"literal":{"string":"value"}},"maximum_bytes":16,"unknown":true}}]}]}}`,
		`{"api":"v1","status":"success","output":{"requirements":[],"diagnostics":[],"contributions":[],"alias_contributions":[{"id":"authn.shortcut","namespace":"authn","source":"order.create/v1","alias":"orders.submit/v1","target":"order.create/v1","unknown":true}]}}`,
		`{"api":"v1","status":"success","output":{"requirements":[],"diagnostics":[]}} {}`,
		strings.Repeat("[", maximumProtocolJSONDepth+1) + strings.Repeat("]", maximumProtocolJSONDepth+1),
	}
	for _, payload := range tests {
		if _, err := decodeResponse([]byte(payload)); err == nil {
			t.Fatalf("decodeResponse(%q) unexpectedly succeeded", payload)
		}
	}
}

func FuzzDecodeResponse(f *testing.F) {
	for _, seed := range []string{
		`{"api":"v1","status":"success","output":{"requirements":[],"diagnostics":[]}}`,
		`{"api":"v1","status":"extension-error","output":{"requirements":[],"diagnostics":[]},"error":"failed"}`,
		`{"api":"v1","api":"v1"}`,
		`[]`,
		`{`,
		``,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > maximumResponseSize {
			return
		}
		first, firstErr := decodeResponse([]byte(payload))
		second, secondErr := decodeResponse([]byte(payload))
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("decodeResponse result changed: %v then %v", firstErr, secondErr)
		}
		if firstErr == nil && !reflect.DeepEqual(first, second) {
			t.Fatalf("decodeResponse output changed: %#v then %#v", first, second)
		}
	})
}

func TestBuildRejectsUnsupportedAPIAndCompileFailuresWithoutArtifacts(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	unsupported := fixture.spec
	unsupported.API = "v2"
	if _, err := Build(t.Context(), unsupported, fixture.options(time.Second)); !errors.Is(err, ErrBuild) || !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatalf("unsupported Build error = %v", err)
	}
	assertDirectoryEmpty(t, fixture.temporaryParent)

	writeTestFile(t, filepath.Join(fixture.root, "plugin", "generation", "generate.go"), invalidSignatureSource)
	_, err := Build(t.Context(), fixture.spec, fixture.options(time.Second))
	if !errors.Is(err, ErrBuild) || !errors.Is(err, ErrCompile) {
		t.Fatalf("compile Build error = %v, want ErrBuild and ErrCompile", err)
	}
	message := err.Error()
	for _, detail := range []string{`plugin "example.extension"`, `api "v1"`, `package "./generation"`, `namespaces [authn]`} {
		if !strings.Contains(message, detail) {
			t.Fatalf("compile error omits %q: %v", detail, err)
		}
	}
	if strings.Contains(message, fixture.root) || strings.Contains(message, fixture.temporaryParent) {
		t.Fatalf("compile error leaked private paths: %v", err)
	}
	assertDirectoryEmpty(t, fixture.temporaryParent)
}

func TestBuildRejectsMismatchedModuleProvenance(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	spec := fixture.spec
	spec.ModulePath = "example.com/different"
	_, err := Build(t.Context(), spec, fixture.options(time.Second))
	if !errors.Is(err, ErrBuild) || !strings.Contains(err.Error(), "differs from go.mod module path") {
		t.Fatalf("Build error = %v", err)
	}
	assertDirectoryEmpty(t, fixture.temporaryParent)
}

func TestBuildRejectsNonDirectoryGenerationPackage(t *testing.T) {
	fixture := newExtensionFixture(t, validExtensionSource)
	packagePath := filepath.Join(fixture.root, "plugin", "generation")
	if err := os.RemoveAll(packagePath); err != nil {
		t.Fatalf("RemoveAll(generation package): %v", err)
	}
	writeTestFile(t, packagePath, "not a directory")
	_, err := Build(t.Context(), fixture.spec, fixture.options(time.Second))
	if !errors.Is(err, ErrBuild) || !strings.Contains(err.Error(), "non-directory component") {
		t.Fatalf("Build error = %v", err)
	}
	assertDirectoryEmpty(t, fixture.temporaryParent)
}

func TestNormalizeSpecRejectsUnsafeOrAmbiguousDeclarations(t *testing.T) {
	valid := Spec{
		PluginID:   "example.extension",
		API:        generation.Version,
		ModulePath: "example.com/extensiontest",
		PluginPath: "plugin",
		Package:    "./generation",
		Namespaces: []string{"authz", "authn"},
	}
	normalized, err := normalizeSpec(valid)
	if err != nil {
		t.Fatalf("normalizeSpec: %v", err)
	}
	if !slices.Equal(normalized.namespaces, []string{"authn", "authz"}) || normalized.importPath != "example.com/extensiontest/plugin/generation" {
		t.Fatalf("normalized spec = %#v", normalized)
	}
	tests := map[string]Spec{
		"invalid plugin":      withSpec(valid, func(spec *Spec) { spec.PluginID = "Example" }),
		"unsupported API":     withSpec(valid, func(spec *Spec) { spec.API = "v2" }),
		"invalid module":      withSpec(valid, func(spec *Spec) { spec.ModulePath = "local" }),
		"escaping plugin":     withSpec(valid, func(spec *Spec) { spec.PluginPath = "../plugin" }),
		"escaping package":    withSpec(valid, func(spec *Spec) { spec.Package = "./../generation" }),
		"absolute package":    withSpec(valid, func(spec *Spec) { spec.Package = "/generation" }),
		"no namespaces":       withSpec(valid, func(spec *Spec) { spec.Namespaces = nil }),
		"invalid namespace":   withSpec(valid, func(spec *Spec) { spec.Namespaces = []string{"AuthN"} }),
		"duplicate namespace": withSpec(valid, func(spec *Spec) { spec.Namespaces = []string{"authn", "authn"} }),
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeSpec(spec); err == nil {
				t.Fatal("normalizeSpec unexpectedly succeeded")
			}
		})
	}
}

type extensionFixture struct {
	root            string
	temporaryParent string
	spec            Spec
}

func newExtensionFixture(t *testing.T, source string) extensionFixture {
	t.Helper()
	root := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := repositoryRoot(t)
	goMod := fmt.Sprintf("module example.com/extensiontest\n\ngo 1.26\n\nrequire github.com/plystra/cli v0.0.0\n\nrequire (\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n\tgolang.org/x/mod v0.38.0 // indirect\n)\n\nreplace github.com/plystra/cli => %s\n", strconv.Quote(filepath.ToSlash(cliRoot)))
	writeTestFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(cli go.sum): %v", err)
	}
	writeTestFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeTestFile(t, filepath.Join(root, "plugin", "generation", "generate.go"), source)
	return extensionFixture{
		root:            root,
		temporaryParent: temporaryParent,
		spec: Spec{
			PluginID:   "example.extension",
			API:        generation.Version,
			ModulePath: "example.com/extensiontest",
			PluginPath: "plugin",
			Package:    "./generation",
			Namespaces: []string{"authn"},
		},
	}
}

func (f extensionFixture) options(executionTimeout time.Duration) BuildOptions {
	return BuildOptions{
		ModuleRoot:       f.root,
		BuildEnvironment: replaceEnvironment(os.Environ(), "GOWORK", "off"),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: executionTimeout,
		TemporaryParent:  f.temporaryParent,
	}
}

func extensionContext(t *testing.T, mode string) generation.Context {
	t.Helper()
	input := generation.Input{
		Plugins: []generation.PluginInput{
			{ID: "example.audit", ModulePath: "example.com/extensiontest", Provides: []string{"audit.write/v1"}},
			{ID: "example.business", ModulePath: "example.com/extensiontest", Provides: []string{"order.create/v1"}},
			{ID: "example.extension", ModulePath: "example.com/extensiontest", BuildMetadataJSON: []byte(fmt.Sprintf(`{"mode":%q}`, mode))},
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: []byte(`{"id":"audit.write/v1","request":{},"response":{},"errors":[]}`)},
			{ContractJSON: []byte(`{"id":"order.create/v1","request":{},"response":{},"errors":[],"extensions":{"authn":{"authenticated":true}}}`)},
		},
		Requirements: []string{"order.create/v1"},
		Providers:    []generation.ProviderInput{{Capability: "order.create/v1", Plugin: "example.business"}},
	}
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", directory, err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary helper artifacts remain: %v", entryNames(entries))
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	slices.Sort(names)
	return names
}

func replaceEnvironment(environment []string, name, value string) []string {
	prefix := strings.ToUpper(name) + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if strings.HasPrefix(strings.ToUpper(item), prefix) {
			continue
		}
		result = append(result, item)
	}
	return append(result, name+"="+value)
}

func withSpec(spec Spec, mutate func(*Spec)) Spec {
	copy := spec
	copy.Namespaces = append([]string(nil), spec.Namespaces...)
	mutate(&copy)
	return copy
}

const validExtensionSource = `package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	generation "github.com/plystra/cli/generation/v1"
)

func Generate(context generation.GenerationContext) (generation.Output, error) {
	pluginID, _ := generation.ParsePluginID("example.extension")
	plugin, ok := context.Plugin(pluginID)
	if !ok {
		return generation.Output{}, errors.New("extension plugin is absent")
	}
	var metadata struct {
		Mode string ` + "`json:\"mode\"`" + `
	}
	if err := json.Unmarshal(plugin.BuildMetadataJSON(), &metadata); err != nil {
		return generation.Output{}, err
	}
	if os.Getenv("PLYSTRA_TEST_SECRET") != "" {
		return generation.Output{}, errors.New("inherited environment leaked to extension")
	}
	for _, item := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(item, "=", 2)[0])
		switch key {
		case "LANG", "LC_ALL", "SOURCE_DATE_EPOCH", "SYSTEMROOT", "TZ":
		default:
			return generation.Output{}, fmt.Errorf("unexpected environment key %q", key)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		return generation.Output{}, fmt.Errorf("helper working directory is not empty: entries=%d error=%v", len(entries), err)
	}
	order, _ := generation.ParseCapabilityID("order.create/v1")
	orderAlias, _ := generation.ParseCapabilityID("orders.submit/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	unknown, _ := generation.ParseCapabilityID("unknown.write/v1")
	switch metadata.Mode {
	case "success":
		return generation.Output{
			Requirements: []generation.Requirement{{RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit}},
			Diagnostics: []generation.Diagnostic{{Code: "authn.verified", Severity: generation.DiagnosticInfo, Message: "verified context reused", Namespace: "authn", Source: order, RuleID: "authn.require-audit"}},
			Contributions: []generation.Contribution{{
				ID: "authn.verify", Namespace: "authn", Source: order, Point: generation.GenerationPointInvocationPrepare,
				Provides: []generation.ContributionToken{"verified-authn-context"},
				Nodes: []generation.GeneratedNode{{
					ID: "attach-verification",
					MetadataAttachment: &generation.GeneratedMetadataAttachment{
						Key: "authn.verification", Value: generation.StringValue("reused"), MaximumBytes: 32,
					},
				}},
			}},
			AliasContributions: []generation.CapabilityAliasContribution{{
				ID: "authn.order-shortcut", Namespace: "authn", Source: order, Alias: orderAlias, Target: order,
			}},
		}, nil
	case "error":
		return generation.Output{}, errors.New("request failed at https://person:secret@example.com/private?token=secret")
	case "panic":
		panic("extension panic")
	case "crash":
		os.Exit(19)
	case "timeout":
		time.Sleep(5 * time.Second)
		return generation.Output{}, nil
	case "malformed":
		_, _ = fmt.Fprint(os.Stdout, "not-json")
		return generation.Output{}, nil
	case "stderr":
		_, _ = fmt.Fprintln(os.Stderr, "raw diagnostic")
		return generation.Output{}, nil
	case "invalid-output":
		return generation.Output{Requirements: []generation.Requirement{{RuleID: "authn.unknown", Namespace: "authn", Source: order, Capability: unknown}}}, nil
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 3<<20))
		return generation.Output{}, nil
	case "write-work":
		if err := os.WriteFile("leftover", []byte("state"), 0o600); err != nil {
			return generation.Output{}, err
		}
		return generation.Output{}, nil
	default:
		return generation.Output{}, fmt.Errorf("unsupported test mode %q", metadata.Mode)
	}
	return generation.Output{}, nil
}
`

const invalidSignatureSource = `package extension

func Generate() {}
`
