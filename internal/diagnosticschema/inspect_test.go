package diagnosticschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestInspectV1BuildsExactFilesystemResult(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	input := InspectInput{
		Evidence: evidence,
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA_INSPECT_READY",
			Severity: diagnosticjson.SeverityInfo,
			Message:  "The selected application model is ready.",
		}},
		Sources: []diagnosticjson.Source{{
			Module: "example.com/inspect",
			Path:   "plystra.production.yaml",
			Kind:   "inspect-selection",
			Line:   1,
			Column: 1,
		}},
		NextAction: "Run `plystra dev --env production` to start the application.",
	}
	result, err := NewInspect(input)
	if err != nil {
		t.Fatalf("NewInspect: %v", err)
	}
	if !result.Valid() || result.Envelope().Schema() != InspectSchemaV1() || result.Envelope().SchemaVersion() != 1 || result.Envelope().ConfigurationMode() != generation.ConfigurationModeEnvironment || result.Envelope().ApplicationModelDigest() != evidence.BuildModelDigest() {
		t.Fatalf("inspect identity = valid %t schema %#v mode %q digest %q", result.Valid(), result.Envelope().Schema(), result.Envelope().ConfigurationMode(), result.Envelope().ApplicationModelDigest())
	}
	if result.ProjectModule() != "example.com/inspect" || result.ConfigurationMode() != generation.ConfigurationModeEnvironment || result.ConfigurationEnvironment() != "production" || result.ConfigurationPath() != "plystra.production.yaml" {
		t.Fatalf("inspect project/configuration = %q %q %q %q", result.ProjectModule(), result.ConfigurationMode(), result.ConfigurationEnvironment(), result.ConfigurationPath())
	}
	if result.SelectedPluginCount() != 0 || result.AvailableCapabilityCount() != 2 || result.RequiredCapabilityCount() != 0 || result.ExposedCapabilityCount() != 0 || result.CapabilityAliasCount() != 0 || result.AuthNActive() || result.AuthZActive() {
		t.Fatalf("inspect summary = plugins %d available %d required %d exposed %d aliases %d authn %t authz %t", result.SelectedPluginCount(), result.AvailableCapabilityCount(), result.RequiredCapabilityCount(), result.ExposedCapabilityCount(), result.CapabilityAliasCount(), result.AuthNActive(), result.AuthZActive())
	}
	if !slices.Equal(result.Transports(), []Transport{TransportREST}) || result.Readiness() != ReadinessReady || result.ProblemCount() != 0 || result.NextAction() != input.NextAction {
		t.Fatalf("inspect runtime summary = transports %#v readiness %q problems %d next %q", result.Transports(), result.Readiness(), result.ProblemCount(), result.NextAction())
	}
	if !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("inspect result did not retain the complete canonical resolution evidence")
	}

	wantResult := canonicalObject(t, `{
		"project":{"module":"example.com/inspect"},
		"configuration":{"mode":"environment","environment":"production","path":"plystra.production.yaml"},
		"summary":{"selected_plugin_count":0,"available_capability_count":2,"required_capability_count":0,"exposed_capability_count":0,"capability_alias_count":0,"authn_active":false,"authz_active":false,"transports":["rest"]},
		"readiness":{"state":"ready","problem_count":0,"next_action":`+strconv.Quote(input.NextAction)+`},
		"resolution_evidence":`+string(evidence.CanonicalJSON())+`
	}`)
	if got := result.Envelope().ResultJSON(); !bytes.Equal(got, wantResult) {
		t.Fatalf("inspect result JSON:\ngot:  %s\nwant: %s", got, wantResult)
	}

	var envelopeDocument map[string]json.RawMessage
	if err := json.Unmarshal(result.Envelope().CanonicalJSON(), &envelopeDocument); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	for _, field := range []string{"schema", "schema_version", "configuration_mode", "application_model_digest", "diagnostics", "sources", "result"} {
		if _, exists := envelopeDocument[field]; !exists {
			t.Fatalf("envelope omits %q: %s", field, result.Envelope().CanonicalJSON())
		}
	}
	if len(envelopeDocument) != 7 {
		t.Fatalf("envelope fields = %v", slices.Sorted(func(yield func(string) bool) {
			for field := range envelopeDocument {
				if !yield(field) {
					return
				}
			}
		}))
	}
	assertInspectSources(t, result.Envelope().Sources(), evidence)
	if bytes.Contains(result.Envelope().CanonicalJSON(), []byte("resolved-secret-marker")) || containsWindowsDrivePath(result.Envelope().CanonicalJSON()) {
		t.Fatal("inspect envelope contains unrestricted configuration or a machine-specific absolute path")
	}
}

func TestInspectV1UsesEverySelectedConfigurationMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configuration string
		environment   string
		mode          generation.ConfigurationMode
		path          string
		transports    []Transport
	}{
		{name: "default", mode: generation.ConfigurationModeDefault, path: "plystra.yaml"},
		{name: "environment", environment: "production", mode: generation.ConfigurationModeEnvironment, path: "plystra.production.yaml", transports: []Transport{TransportREST}},
		{name: "explicit", configuration: "deploy/customer.yaml", mode: generation.ConfigurationModeExplicit, path: "deploy/customer.yaml", transports: []Transport{TransportConnect, TransportREST}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := resolvedInspectEvidenceFor(t, test.configuration, test.environment)
			result, err := NewInspect(InspectInput{Evidence: evidence, NextAction: "Run plystra check to validate the selected model."})
			if err != nil {
				t.Fatalf("NewInspect: %v", err)
			}
			if result.ConfigurationMode() != test.mode || result.ConfigurationEnvironment() != test.environment || result.ConfigurationPath() != test.path || !slices.Equal(result.Transports(), test.transports) || result.Envelope().ConfigurationMode() != test.mode {
				t.Fatalf("configuration result = mode %q environment %q path %q transports %#v", result.ConfigurationMode(), result.ConfigurationEnvironment(), result.ConfigurationPath(), result.Transports())
			}
		})
	}
}

func TestInspectV1CanonicalizesPermutationsAndReadiness(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	diagnostics := []diagnosticjson.Diagnostic{
		{Code: "PLYSTRA_ZETA", Severity: diagnosticjson.SeverityError, Message: "Resolve the selected Provider conflict."},
		{Code: "PLYSTRA_ALPHA", Severity: diagnosticjson.SeverityWarning, Message: "Review the selected configuration."},
		{Code: "PLYSTRA_INFO", Severity: diagnosticjson.SeverityInfo, Message: "Resolution evidence is available."},
	}
	sources := []diagnosticjson.Source{
		{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "inspect-selection", Line: 1, Column: 1},
		{Module: "example.com/inspect", Path: "plystra.yaml", Kind: "project-marker", Line: 1, Column: 1},
	}
	first, err := NewInspect(InspectInput{Evidence: evidence, Diagnostics: diagnostics, Sources: sources, NextAction: "Run `plystra explain capability email.send/v1` to inspect the conflict."})
	if err != nil {
		t.Fatalf("NewInspect(first): %v", err)
	}
	slices.Reverse(diagnostics)
	slices.Reverse(sources)
	second, err := NewInspect(InspectInput{Evidence: evidence, Diagnostics: diagnostics, Sources: sources, NextAction: "Run `plystra explain capability email.send/v1` to inspect the conflict."})
	if err != nil {
		t.Fatalf("NewInspect(second): %v", err)
	}
	if first.Readiness() != ReadinessBlocked || first.ProblemCount() != 2 || !bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()) || first.Envelope().Digest() != second.Envelope().Digest() {
		t.Fatalf("permuted inspect results = readiness %q problems %d equal %t digest %q/%q", first.Readiness(), first.ProblemCount(), bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()), first.Envelope().Digest(), second.Envelope().Digest())
	}

	warning, err := NewInspect(InspectInput{
		Evidence: evidence,
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA_REVIEW",
			Severity: diagnosticjson.SeverityWarning,
			Message:  "Review the selected configuration.",
		}},
		NextAction: "Run `plystra check --env production` after reviewing the warning.",
	})
	if err != nil || warning.Readiness() != ReadinessNeedsAttention || warning.ProblemCount() != 1 {
		t.Fatalf("warning readiness = %#v, %v", warning, err)
	}
	info, err := NewInspect(InspectInput{
		Evidence: evidence,
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA_INFORMATION",
			Severity: diagnosticjson.SeverityInfo,
			Message:  "Resolution evidence is available.",
		}},
		NextAction: "Run `plystra dev --env production` to start the application.",
	})
	if err != nil || info.Readiness() != ReadinessReady || info.ProblemCount() != 0 {
		t.Fatalf("informational readiness = %#v, %v", info, err)
	}
}

func TestInspectV1RejectsIncompleteAndUnsafeInput(t *testing.T) {
	t.Parallel()

	valid := resolvedInspectEvidence(t)
	tests := []struct {
		name  string
		input InspectInput
		want  string
	}{
		{name: "zero evidence", input: InspectInput{NextAction: "Run plystra check."}, want: "resolution evidence"},
		{name: "missing configuration selection", input: InspectInput{Evidence: syntheticInspectEvidence(t, false, true, true), NextAction: "Run plystra check."}, want: "configuration"},
		{name: "missing static assembly", input: InspectInput{Evidence: syntheticInspectEvidence(t, true, false, true), NextAction: "Run plystra check."}, want: "assembly"},
		{name: "missing transports", input: InspectInput{Evidence: syntheticInspectEvidence(t, true, true, false), NextAction: "Run plystra check."}, want: "transports"},
		{name: "missing next action", input: InspectInput{Evidence: valid}, want: "next action"},
		{name: "long next action", input: InspectInput{Evidence: valid, NextAction: strings.Repeat("a", maximumNextActionLength+1)}, want: "next action"},
		{name: "multiline next action", input: InspectInput{Evidence: valid, NextAction: "Run plystra check.\nThen retry."}, want: "one line"},
		{name: "windows absolute path", input: InspectInput{Evidence: valid, NextAction: `Open C:\Users\person\secret.yaml.`}, want: "absolute path"},
		{name: "unix absolute path", input: InspectInput{Evidence: valid, NextAction: "Open /home/person/secret.yaml."}, want: "absolute path"},
		{name: "unsafe diagnostic path", input: InspectInput{Evidence: valid, Diagnostics: []diagnosticjson.Diagnostic{{Code: "PLYSTRA_UNSAFE", Severity: diagnosticjson.SeverityError, Message: "Open /home/person/secret.yaml."}}, NextAction: "Run plystra check."}, want: "absolute path"},
		{name: "invalid diagnostic", input: InspectInput{Evidence: valid, Diagnostics: []diagnosticjson.Diagnostic{{Code: "invalid", Severity: diagnosticjson.SeverityError, Message: "Resolve the error."}}, NextAction: "Run plystra check."}, want: "shared envelope"},
		{name: "invalid source", input: InspectInput{Evidence: valid, Sources: []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "inspect-selection"}}, NextAction: "Run plystra check."}, want: "shared envelope"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewInspect(test.input)
			if !errors.Is(err, ErrInspect) || !strings.Contains(err.Error(), test.want) || result.Valid() {
				t.Fatalf("NewInspect = %#v, %v; want ErrInspect containing %q", result, err, test.want)
			}
		})
	}
}

func TestInspectV1StorageIsDefensive(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	diagnostics := []diagnosticjson.Diagnostic{{Code: "PLYSTRA_READY", Severity: diagnosticjson.SeverityInfo, Message: "The application is ready."}}
	sources := []diagnosticjson.Source{{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "inspect-selection", Line: 1, Column: 1}}
	result, err := NewInspect(InspectInput{Evidence: evidence, Diagnostics: diagnostics, Sources: sources, NextAction: "Run `plystra dev --env production` to start the application."})
	if err != nil {
		t.Fatalf("NewInspect: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	diagnostics[0].Message = "mutated"
	sources[0].Path = "mutated"
	transports := result.Transports()
	transports[0] = TransportConnect
	evidenceJSON := result.ResolutionEvidenceJSON()
	evidenceJSON[0] = '['
	resultJSON := result.Envelope().ResultJSON()
	resultJSON[0] = '['
	canonical := result.Envelope().CanonicalJSON()
	canonical[0] = '['
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) || !slices.Equal(result.Transports(), []Transport{TransportREST}) || !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("inspect result storage aliases mutable input or returned data")
	}
	if (InspectResult{}).Valid() {
		t.Fatal("zero InspectResult is valid")
	}
}

func TestInspectClosedActivationAndTransportVocabularies(t *testing.T) {
	t.Parallel()

	authn, authz := activationStates([]string{"other", "authz", "authn", "authz"})
	if !authn || !authz {
		t.Fatalf("activation states = authn %t authz %t", authn, authz)
	}
	if authn, authz := activationStates(nil); authn || authz {
		t.Fatalf("empty activation states = authn %t authz %t", authn, authz)
	}
	for _, transports := range [][]Transport{nil, {TransportConnect}, {TransportREST}, {TransportConnect, TransportREST}} {
		if !validTransports(transports) {
			t.Fatalf("validTransports(%#v) = false", transports)
		}
	}
	for _, transports := range [][]Transport{{"grpc"}, {TransportREST, TransportConnect}, {TransportConnect, TransportConnect}, {TransportConnect, TransportREST, TransportREST}} {
		if validTransports(transports) {
			t.Fatalf("validTransports(%#v) = true", transports)
		}
	}
}

type inspectAliasOutput struct {
	pluginID string
	output   generation.NormalizedOutput
}

func (o inspectAliasOutput) PluginID() string                    { return o.pluginID }
func (o inspectAliasOutput) Output() generation.NormalizedOutput { return o.output }

func syntheticInspectEvidence(t testing.TB, selection, assembly, transports bool) resolutionevidence.Evidence {
	t.Helper()

	capabilities := make([]generation.CapabilityInput, 0, len(intrinsiccatalog.Definitions()))
	for _, definition := range intrinsiccatalog.Definitions() {
		capabilities = append(capabilities, generation.CapabilityInput{
			ContractJSON: definition.ContractJSON(),
			Sources:      []string{definition.Source()},
			Intrinsic:    true,
			Exposure:     generation.Exposure{Go: true},
		})
	}
	contextInput := generation.Input{Capabilities: capabilities}
	if selection {
		contextInput.ConfigurationProvenance = &generation.ConfigurationProvenanceInput{
			Mode:                        generation.ConfigurationModeDefault,
			RootPath:                    "plystra.yaml",
			RootDigest:                  inspectDigest("a"),
			SelectedPath:                "plystra.yaml",
			SelectedDigest:              inspectDigest("a"),
			DependencyCompositionDigest: inspectDigest("b"),
		}
	}
	context, err := generation.NewContext(contextInput)
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	providerResult, err := providerresolution.Resolve(providerresolution.Input{})
	if err != nil {
		t.Fatalf("providerresolution.Resolve: %v", err)
	}
	aliasResult, err := aliasresolution.Resolve[inspectAliasOutput](context, nil)
	if err != nil {
		t.Fatalf("aliasresolution.Resolve: %v", err)
	}
	input := resolutionevidence.Input{
		Context:            context,
		ProviderResolution: providerResult,
		AliasResolution:    aliasResult,
		Modules: []resolutionevidence.ModuleInput{{
			Path:             "example.com/inspect",
			Role:             resolutionevidence.ModuleRoleCurrent,
			SourceModulePath: "example.com/inspect",
		}},
	}
	if assembly {
		input.StaticAssembly = &resolutionevidence.StaticAssemblyInput{}
	}
	if transports {
		selected := applicationmeta.HTTPTransports{Connect: true}
		input.HTTPTransports = &selected
	}
	evidence, err := resolutionevidence.Build(input)
	if err != nil {
		t.Fatalf("resolutionevidence.Build: %v", err)
	}
	return evidence
}

func resolvedInspectEvidence(t testing.TB) resolutionevidence.Evidence {
	t.Helper()
	return resolvedInspectEvidenceFor(t, "", "production")
}

func resolvedInspectEvidenceFor(t testing.TB, configuration, environment string) resolutionevidence.Evidence {
	t.Helper()

	root := t.TempDir()
	writeInspectFile(t, filepath.Join(root, "go.mod"), "module example.com/inspect\n\ngo 1.26\n")
	writeInspectFile(t, filepath.Join(root, "plystra.yaml"), "http: {address: resolved-secret-marker, transports: {connect: false}}\n")
	writeInspectFile(t, filepath.Join(root, "plystra.production.yaml"), "http: {transports: {rest: true}}\n")
	writeInspectFile(t, filepath.Join(root, "deploy", "customer.yaml"), "http: {transports: {connect: true, rest: true}}\n")
	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:             root,
		ConfigurationPath: configuration,
		EnvironmentName:   environment,
		Environment:       inspectEnvironment(),
	})
	if err != nil {
		t.Fatalf("applicationresolve.Resolve: %v", err)
	}
	evidence := result.ResolutionEvidence()
	if !evidence.Valid() {
		t.Fatal("filesystem resolution returned invalid evidence")
	}
	return evidence
}

func assertInspectSources(t testing.TB, sources []diagnosticjson.Source, evidence resolutionevidence.Evidence) {
	t.Helper()

	if len(sources) < 5 {
		t.Fatalf("inspect sources are incomplete: %#v", sources)
	}
	if !slices.IsSortedFunc(sources, func(left, right diagnosticjson.Source) int {
		leftKey := left.Module + "\x00" + left.Path + "\x00" + left.Kind
		rightKey := right.Module + "\x00" + right.Path + "\x00" + right.Kind
		return strings.Compare(leftKey, rightKey)
	}) {
		t.Fatalf("inspect sources are not canonical: %#v", sources)
	}
	currentSource := evidence.Modules()[0].Source()
	wants := []diagnosticjson.Source{
		{Module: currentSource.Module(), Path: currentSource.Path(), Kind: currentSource.Kind(), Line: currentSource.Line(), Column: currentSource.Column()},
		{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "configuration-selection"},
		{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "inspect-selection", Line: 1, Column: 1},
		{Module: "github.com/plystra/kernel", Path: "capability/catalog/definitions/kernel.health/v1/capability.yaml", Kind: "intrinsic-provider", Line: 1, Column: 1},
		{Module: "github.com/plystra/kernel", Path: "capability/catalog/definitions/kernel.info/v1/capability.yaml", Kind: "intrinsic-provider", Line: 1, Column: 1},
	}
	for _, want := range wants {
		if !slices.Contains(sources, want) {
			t.Fatalf("inspect sources omit %#v: %#v", want, sources)
		}
	}
	seen := make(map[diagnosticjson.Source]struct{}, len(sources))
	for _, source := range sources {
		if _, duplicate := seen[source]; duplicate {
			t.Fatalf("inspect source is duplicated: %#v", source)
		}
		seen[source] = struct{}{}
		if filepath.IsAbs(source.Path) || strings.Contains(source.Path, "\\") {
			t.Fatalf("inspect source is not module relative: %#v", source)
		}
	}
}

func canonicalObject(t testing.TB, source string) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(source), &value); err != nil {
		t.Fatalf("decode expected JSON: %v\n%s", err, source)
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode expected JSON: %v", err)
	}
	return result
}

func inspectEnvironment() []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.EqualFold(key, "GOWORK") || strings.EqualFold(key, "GOPROXY") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "GOWORK=off", "GOPROXY=off")
}

func writeInspectFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func inspectDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func containsWindowsDrivePath(value []byte) bool {
	for drive := 'A'; drive <= 'Z'; drive++ {
		for _, prefix := range []string{string(drive) + ":/", string(drive) + `:\\`} {
			if bytes.Contains(value, []byte(prefix)) {
				return true
			}
		}
	}
	return false
}
