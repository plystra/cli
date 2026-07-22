package diagnosticschema

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestTestV1BuildsExactPluginSliceResult(t *testing.T) {
	t.Parallel()

	evidence := resolvedTestEvidence(t, false)
	moduleSource := graphDiagnosticSource(evidence.Modules()[0].Source())
	input := TestInput{
		Evidence: evidence,
		Slice:    targetedTestSliceInput(),
		Outcomes: []TestOutcome{
			{Order: 2, ID: "orders-package", Kind: "go-package", Subject: "example.com/app/orders", Status: TestStatusFailed, Summary: "The orders package test failed.", Failures: []TestFailure{{Kind: "assertion", Subject: "TestSubmitOrder", Summary: "The canonical invocation returned an unexpected semantic error.", Sources: []diagnosticjson.Source{moduleSource}}}},
			{Order: 1, ID: "slice-startup", Kind: "lifecycle", Subject: "example.orders", Status: TestStatusPassed, Summary: "The selected Plugin slice started and stopped cleanly.", Sources: []diagnosticjson.Source{moduleSource}},
			{Order: 3, ID: "transport-tests", Kind: "transport", Subject: "connect", Status: TestStatusSkipped, Summary: "No exposed transport belongs to the selected Plugin slice."},
		},
		Diagnostics: []diagnosticjson.Diagnostic{{Code: "PLYSTRA-TEST-FAILED", Severity: diagnosticjson.SeverityError, Message: "Inspect the structured failure and rerun the selected Plugin test."}},
	}
	result, err := NewTest(input)
	if err != nil {
		t.Fatalf("NewTest: %v", err)
	}
	if !result.Valid() || result.Envelope().Schema() != TestSchemaV1() || result.Envelope().SchemaVersion() != 1 || result.Envelope().ConfigurationMode() != generation.ConfigurationModeEnvironment || result.Envelope().ApplicationModelDigest() != evidence.BuildModelDigest() {
		t.Fatalf("test identity = valid %t schema %#v mode %q digest %q", result.Valid(), result.Envelope().Schema(), result.Envelope().ConfigurationMode(), result.Envelope().ApplicationModelDigest())
	}
	configuration := result.Configuration()
	slice := result.Slice()
	if configuration.Environment != "test" || configuration.SelectedPath != "plystra.test.yaml" || slice.Scope != TestScopePlugin || slice.TargetPlugin != "example.orders" || !strings.HasPrefix(slice.Digest, "sha256:") || len(slice.Digest) != 71 {
		t.Fatalf("test selection = configuration %#v slice %#v", configuration, slice)
	}
	if result.Status() != TestStatusFailed || result.FailedOutcomeCount() != 1 || len(result.Outcomes()) != 3 {
		t.Fatalf("test result = status %q failures %d outcomes %#v", result.Status(), result.FailedOutcomeCount(), result.Outcomes())
	}
	if len(slice.Plugins) != 3 || slice.Plugins[0].ID != "example.authn" || slice.Plugins[1].ID != "example.catalog.fake" || slice.Plugins[2].ID != "example.orders" {
		t.Fatalf("slice Plugins = %#v", slice.Plugins)
	}
	if len(slice.Capabilities) != 3 || slice.Capabilities[0].ID != "authn.activate/v1" || slice.Capabilities[1].ID != "catalog.lookup/v1" || slice.Capabilities[2].ID != "kernel.health/v1" {
		t.Fatalf("slice Capabilities = %#v", slice.Capabilities)
	}
	if len(slice.Providers) != 2 || slice.Providers[0].Reason != TestProviderApplicationSelection || slice.Providers[1].Reason != TestProviderInvocationReplacement || len(slice.Intrinsics) != 1 || len(slice.GeneratedContributions) != 2 || len(slice.Replacements) != 1 || slice.Replacements[0].PluginID != "example.catalog.fake" {
		t.Fatalf("slice resolution = providers %#v intrinsics %#v contributions %#v replacements %#v", slice.Providers, slice.Intrinsics, slice.GeneratedContributions, slice.Replacements)
	}
	if !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("test result did not retain complete resolution evidence")
	}

	wantResult := canonicalObject(t, `{
		"status":"failed",
		"failed_outcome_count":1,
		"selected_model_digest":"`+evidence.SelectedModelDigest()+`",
		"configuration":{"mode":"environment","environment":"test","root_path":"plystra.yaml","root_digest":"`+inspectDigest("a")+`","selected_path":"plystra.test.yaml","selected_digest":"`+inspectDigest("c")+`","dependency_composition_digest":"`+inspectDigest("b")+`"},
		"slice":{
			"scope":"plugin","target_plugin":"example.orders","digest":"`+slice.Digest+`",
			"plugins":[
				{"order":1,"id":"example.authn","project_module":"example.com/authn","module_version":"v1.0.0","sources":[{"module":"example.com/authn","path":"authn/plugin.yaml","kind":"plugin-declaration","line":1,"column":1}]},
				{"order":2,"id":"example.catalog.fake","project_module":"example.com/fake","module_version":"v1.0.0","sources":[{"module":"example.com/fake","path":"fake/plugin.yaml","kind":"plugin-declaration","line":1,"column":1}]},
				{"order":3,"id":"example.orders","project_module":"example.com/app","sources":[{"module":"example.com/app","path":"orders/plugin.yaml","kind":"plugin-declaration","line":1,"column":1}]}
			],
			"capabilities":[
				{"id":"authn.activate/v1","contract_digest":"`+testCapabilityDigest(evidence, "authn.activate/v1")+`","sources":[{"module":"example.com/app","path":"plystra.yaml","kind":"activation","line":5,"column":3},{"module":"example.com/authn","path":"authn/capabilities/authn.activate/v1/capability.yaml","kind":"provider-declaration","line":1,"column":1}]},
				{"id":"catalog.lookup/v1","contract_digest":"`+testCapabilityDigest(evidence, "catalog.lookup/v1")+`","sources":[{"module":"example.com/app","path":"orders/plugin.yaml","kind":"plugin","line":1,"column":1},{"module":"example.com/authn","path":"authn/plugin.yaml","kind":"generation-rule","line":1,"column":1},{"module":"example.com/fake","path":"fake/capabilities/catalog.lookup/v1/capability.yaml","kind":"provider-declaration","line":1,"column":1}]},
				{"id":"kernel.health/v1","contract_digest":"`+testCapabilityDigest(evidence, "kernel.health/v1")+`","sources":[{"module":"example.com/app","path":"plystra.yaml","kind":"declaration","line":3,"column":3},{"module":"github.com/plystra/kernel","path":"capability/catalog/definitions/kernel.health/v1/capability.yaml","kind":"intrinsic-provider","line":1,"column":1}]}
			],
			"providers":[
				{"capability":"authn.activate/v1","plugin_id":"example.authn","contract_digest":"`+testCapabilityDigest(evidence, "authn.activate/v1")+`","reason":"application-selection","sources":[{"module":"example.com/authn","path":"authn/capabilities/authn.activate/v1/capability.yaml","kind":"provider-declaration","line":1,"column":1}]},
				{"capability":"catalog.lookup/v1","plugin_id":"example.catalog.fake","contract_digest":"`+testCapabilityDigest(evidence, "catalog.lookup/v1")+`","reason":"invocation-replacement","sources":[{"module":"example.com/fake","path":"fake/capabilities/catalog.lookup/v1/capability.yaml","kind":"provider-declaration","line":1,"column":1}]}
			],
			"intrinsics":[{"capability":"kernel.health/v1","contract_digest":"`+testCapabilityDigest(evidence, "kernel.health/v1")+`","sources":[{"module":"github.com/plystra/kernel","path":"capability/catalog/definitions/kernel.health/v1/capability.yaml","kind":"intrinsic-provider","line":1,"column":1}]}],
			"generated_contributions":[
				{"kind":"activation","namespace":"authn","source_capability":"kernel.health/v1","activation_capability":"authn.activate/v1","plugin_id":"example.authn","project_module":"example.com/authn","sources":[{"module":"example.com/app","path":"plystra.yaml","kind":"activation","line":5,"column":3}]},
				{"kind":"requirement","namespace":"authn","capability":"catalog.lookup/v1","source_capability":"kernel.health/v1","activation_capability":"authn.activate/v1","plugin_id":"example.authn","project_module":"example.com/authn","rule_id":"authn.require-catalog","sources":[{"module":"example.com/authn","path":"authn/plugin.yaml","kind":"generation-rule","line":1,"column":1}]}
			],
			"replacements":[{"capability":"catalog.lookup/v1","plugin_id":"example.catalog.fake","contract_digest":"`+testCapabilityDigest(evidence, "catalog.lookup/v1")+`","sources":[{"module":"example.com/fake","path":"fake/capabilities/catalog.lookup/v1/capability.yaml","kind":"provider-declaration","line":1,"column":1}]}]
		},
		"outcomes":[
			{"order":1,"id":"slice-startup","kind":"lifecycle","subject":"example.orders","status":"passed","summary":"The selected Plugin slice started and stopped cleanly.","failures":[],"sources":[{"module":"example.com/app","path":"plystra.yaml","kind":"project-marker","line":1,"column":1}]},
			{"order":2,"id":"orders-package","kind":"go-package","subject":"example.com/app/orders","status":"failed","summary":"The orders package test failed.","failures":[{"kind":"assertion","subject":"TestSubmitOrder","summary":"The canonical invocation returned an unexpected semantic error.","sources":[{"module":"example.com/app","path":"plystra.yaml","kind":"project-marker","line":1,"column":1}]}],"sources":[]},
			{"order":3,"id":"transport-tests","kind":"transport","subject":"connect","status":"skipped","summary":"No exposed transport belongs to the selected Plugin slice.","failures":[],"sources":[]}
		],
		"resolution_evidence":`+string(evidence.CanonicalJSON())+`
	}`)
	if got := result.Envelope().ResultJSON(); !bytes.Equal(got, wantResult) {
		t.Fatalf("test result JSON:\ngot:  %s\nwant: %s", got, wantResult)
	}
	if bytes.Contains(result.Envelope().CanonicalJSON(), []byte("resolved-secret-marker")) || containsWindowsDrivePath(result.Envelope().CanonicalJSON()) {
		t.Fatal("test envelope contains diagnostic-only Provider data or an absolute path")
	}
}

func TestTestV1SupportsProjectScopeAndEveryConfigurationMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		configuration string
		environment   string
		mode          generation.ConfigurationMode
	}{
		{name: "default", mode: generation.ConfigurationModeDefault},
		{name: "environment", environment: "production", mode: generation.ConfigurationModeEnvironment},
		{name: "explicit", configuration: "deploy/customer.yaml", mode: generation.ConfigurationModeExplicit},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := resolvedInspectEvidenceFor(t, test.configuration, test.environment)
			result, err := NewTest(TestInput{Evidence: evidence, Slice: TestSliceInput{Scope: TestScopeProject}, Outcomes: []TestOutcome{{Order: 1, ID: "go-packages", Kind: "go-package", Subject: "example.com/inspect", Status: TestStatusPassed, Summary: "All Go package tests passed."}}})
			if err != nil || !result.Valid() || result.Envelope().ConfigurationMode() != test.mode || result.Slice().Scope != TestScopeProject || result.Slice().TargetPlugin != "" || len(result.Slice().Providers) != 0 || len(result.Slice().Intrinsics) != 2 {
				t.Fatalf("NewTest = configuration %#v slice %#v, %v", result.Configuration(), result.Slice(), err)
			}
		})
	}
}

func TestTestV1CanonicalizesInputPermutations(t *testing.T) {
	t.Parallel()

	evidence := resolvedTestEvidence(t, false)
	sliceInput := targetedTestSliceInput()
	outcomes := []TestOutcome{
		{Order: 2, ID: "packages", Kind: "go-package", Subject: "example.com/app/orders", Status: TestStatusFailed, Summary: "A package failed.", Failures: []TestFailure{{Kind: "zeta", Subject: "TestZeta", Summary: "The zeta assertion failed."}, {Kind: "alpha", Subject: "TestAlpha", Summary: "The alpha assertion failed."}}},
		{Order: 1, ID: "startup", Kind: "lifecycle", Subject: "example.orders", Status: TestStatusPassed, Summary: "Startup passed."},
	}
	diagnostics := []diagnosticjson.Diagnostic{{Code: "PLYSTRA-ZETA", Severity: diagnosticjson.SeverityError, Message: "Resolve the test failure."}, {Code: "PLYSTRA-ALPHA", Severity: diagnosticjson.SeverityInfo, Message: "The slice resolved."}}
	build := func() TestResult {
		result, err := NewTest(TestInput{Evidence: evidence, Slice: sliceInput, Outcomes: outcomes, Diagnostics: diagnostics})
		if err != nil {
			t.Fatalf("NewTest: %v", err)
		}
		return result
	}
	first := build()
	slices.Reverse(sliceInput.Plugins)
	slices.Reverse(sliceInput.Providers)
	slices.Reverse(sliceInput.GeneratedContributions)
	slices.Reverse(outcomes)
	slices.Reverse(outcomes[0].Failures)
	slices.Reverse(diagnostics)
	second := build()
	if !bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()) || first.Envelope().Digest() != second.Envelope().Digest() || first.Slice().Digest != second.Slice().Digest {
		t.Fatalf("permuted test result changed identity: %q/%q slice %q/%q", first.Envelope().Digest(), second.Envelope().Digest(), first.Slice().Digest, second.Slice().Digest)
	}
	reversedEvidence := resolvedTestEvidence(t, true)
	third, err := NewTest(TestInput{Evidence: reversedEvidence, Slice: sliceInput, Outcomes: outcomes, Diagnostics: diagnostics})
	if err != nil || !bytes.Equal(first.Envelope().CanonicalJSON(), third.Envelope().CanonicalJSON()) || first.Envelope().Digest() != third.Envelope().Digest() {
		t.Fatalf("permuted resolution evidence changed test identity: %q/%q, %v", first.Envelope().Digest(), third.Envelope().Digest(), err)
	}

	applicationSlice := targetedTestSliceInput()
	applicationSlice.Plugins[2].PluginID = "example.catalog.smtp"
	applicationSlice.Providers[0].PluginID = "example.catalog.smtp"
	applicationSlice.Providers[0].Replacement = false
	applicationResult, err := NewTest(TestInput{Evidence: evidence, Slice: applicationSlice, Outcomes: []TestOutcome{{Order: 1, ID: "startup", Kind: "lifecycle", Subject: "example.orders", Status: TestStatusPassed, Summary: "Startup passed."}}})
	if err != nil || applicationResult.Slice().Digest == first.Slice().Digest {
		t.Fatalf("invocation replacement did not change slice identity: %q/%q, %v", first.Slice().Digest, applicationResult.Slice().Digest, err)
	}
}

func TestTestV1RejectsIncompleteAndUnsafeInput(t *testing.T) {
	t.Parallel()

	valid := resolvedTestEvidence(t, false)
	base := TestInput{
		Evidence: valid,
		Slice:    targetedTestSliceInput(),
		Outcomes: []TestOutcome{
			{Order: 1, ID: "startup", Kind: "lifecycle", Subject: "example.orders", Status: TestStatusPassed, Summary: "Startup passed."},
			{Order: 2, ID: "packages", Kind: "go-package", Subject: "example.com/app/orders", Status: TestStatusFailed, Summary: "A package failed.", Failures: []TestFailure{{Kind: "assertion", Subject: "TestSubmit", Summary: "The assertion failed."}}},
		},
	}
	tests := []struct {
		name   string
		mutate func(*TestInput)
		want   string
	}{
		{name: "zero evidence", mutate: func(input *TestInput) { input.Evidence = resolutionevidence.Evidence{} }, want: "resolution evidence"},
		{name: "missing configuration", mutate: func(input *TestInput) { input.Evidence = syntheticInspectEvidence(t, false, true, true) }, want: "configuration"},
		{name: "missing assembly", mutate: func(input *TestInput) { input.Evidence = syntheticInspectEvidence(t, true, false, true) }, want: "assembly"},
		{name: "missing transports", mutate: func(input *TestInput) { input.Evidence = syntheticInspectEvidence(t, true, true, false) }, want: "transports"},
		{name: "invalid scope", mutate: func(input *TestInput) { input.Slice.Scope = "workspace" }, want: "scope"},
		{name: "project explicit members", mutate: func(input *TestInput) { input.Slice.Scope = TestScopeProject }, want: "project scope"},
		{name: "missing target", mutate: func(input *TestInput) { input.Slice.TargetPlugin = "" }, want: "target Plugin"},
		{name: "dependency target", mutate: func(input *TestInput) { input.Slice.TargetPlugin = "example.authn" }, want: "current-Project"},
		{name: "no Plugins", mutate: func(input *TestInput) { input.Slice.Plugins = nil }, want: "at least one selected Plugin"},
		{name: "Plugin count", mutate: func(input *TestInput) { input.Slice.Plugins = make([]TestSlicePluginInput, maximumTestPluginCount+1) }, want: "plugin count"},
		{name: "zero Plugin order", mutate: func(input *TestInput) { input.Slice.Plugins[0].Order = 0 }, want: "order"},
		{name: "duplicate Plugin order", mutate: func(input *TestInput) { input.Slice.Plugins[1].Order = input.Slice.Plugins[0].Order }, want: "duplicated"},
		{name: "Plugin order gap", mutate: func(input *TestInput) { input.Slice.Plugins[2].Order = 4 }, want: "contiguous"},
		{name: "unknown Plugin", mutate: func(input *TestInput) { input.Slice.Plugins[0].PluginID = "example.unknown" }, want: "resolution evidence"},
		{name: "duplicate Plugin", mutate: func(input *TestInput) { input.Slice.Plugins[1].PluginID = input.Slice.Plugins[0].PluginID }, want: "duplicated"},
		{name: "target absent", mutate: func(input *TestInput) { input.Slice.Plugins[0].PluginID = "example.catalog.smtp" }, want: "target Plugin"},
		{name: "unrelated Plugin", mutate: func(input *TestInput) {
			input.Slice.Plugins = append(input.Slice.Plugins, TestSlicePluginInput{Order: 4, PluginID: "example.catalog.smtp"})
		}, want: "unrelated"},
		{name: "Provider count", mutate: func(input *TestInput) { input.Slice.Providers = make([]TestProviderInput, maximumTestProviderCount+1) }, want: "provider count"},
		{name: "empty Capability", mutate: func(input *TestInput) { input.Slice.Providers[0].Capability = "" }, want: "capability"},
		{name: "duplicate Capability", mutate: func(input *TestInput) { input.Slice.Providers[1].Capability = input.Slice.Providers[0].Capability }, want: "duplicated"},
		{name: "Provider Plugin absent", mutate: func(input *TestInput) { input.Slice.Providers[0].PluginID = "example.catalog.smtp" }, want: "selected slice Plugins"},
		{name: "unknown Provider", mutate: func(input *TestInput) { input.Slice.Providers[0].PluginID = "example.orders" }, want: "visible structurally conforming"},
		{name: "redundant replacement", mutate: func(input *TestInput) {
			input.Slice.Providers[0].PluginID = "example.catalog.smtp"
			input.Slice.Plugins[1].PluginID = "example.catalog.smtp"
		}, want: "repeats"},
		{name: "selection differs without replacement", mutate: func(input *TestInput) { input.Slice.Providers[0].Replacement = false }, want: "without Replacement"},
		{name: "intrinsic replacement", mutate: func(input *TestInput) { input.Slice.Providers[2].Replacement = true }, want: "intrinsic"},
		{name: "unknown intrinsic", mutate: func(input *TestInput) { input.Slice.Providers[2].Capability = "kernel.unknown/v1" }, want: "not a Kernel intrinsic"},
		{name: "missing required client", mutate: func(input *TestInput) {
			input.Slice.Providers = input.Slice.Providers[1:]
			input.Slice.GeneratedContributions = nil
			input.Slice.Plugins = []TestSlicePluginInput{{Order: 2, PluginID: "example.orders"}, {Order: 1, PluginID: "example.authn"}}
		}, want: "requires missing"},
		{name: "contribution count", mutate: func(input *TestInput) {
			input.Slice.GeneratedContributions = make([]TestContributionInput, maximumTestContributionCount+1)
		}, want: "contribution count"},
		{name: "duplicate contribution", mutate: func(input *TestInput) { input.Slice.GeneratedContributions[1] = input.Slice.GeneratedContributions[0] }, want: "duplicated"},
		{name: "unknown contribution", mutate: func(input *TestInput) { input.Slice.GeneratedContributions[0].Namespace = "authz" }, want: "resolution evidence"},
		{name: "contribution Plugin absent", mutate: func(input *TestInput) {
			input.Slice.Plugins = input.Slice.Plugins[1:]
			for index := range input.Slice.Plugins {
				input.Slice.Plugins[index].Order = uint32(index + 1)
			}
		}, want: "Plugin"},
		{name: "contribution source absent", mutate: func(input *TestInput) { input.Slice.Providers = input.Slice.Providers[:2] }, want: "source Capability"},
		{name: "missing generated requirement", mutate: func(input *TestInput) { input.Slice.GeneratedContributions = input.Slice.GeneratedContributions[1:] }, want: "generated requirement"},
		{name: "missing selected activation", mutate: func(input *TestInput) { input.Slice.GeneratedContributions = input.Slice.GeneratedContributions[:1] }, want: "activation"},
		{name: "no outcomes", mutate: func(input *TestInput) { input.Outcomes = nil }, want: "at least one"},
		{name: "outcome count", mutate: func(input *TestInput) { input.Outcomes = make([]TestOutcome, maximumTestOutcomeCount+1) }, want: "outcome count"},
		{name: "zero outcome order", mutate: func(input *TestInput) { input.Outcomes[0].Order = 0 }, want: "order"},
		{name: "duplicate outcome order", mutate: func(input *TestInput) { input.Outcomes[1].Order = 1 }, want: "duplicated"},
		{name: "outcome order gap", mutate: func(input *TestInput) { input.Outcomes[1].Order = 3 }, want: "contiguous"},
		{name: "invalid outcome id", mutate: func(input *TestInput) { input.Outcomes[0].ID = "Bad ID" }, want: "id"},
		{name: "duplicate outcome id", mutate: func(input *TestInput) { input.Outcomes[1].ID = input.Outcomes[0].ID }, want: "duplicated"},
		{name: "invalid outcome kind", mutate: func(input *TestInput) { input.Outcomes[0].Kind = "Go Package" }, want: "kind"},
		{name: "unsafe outcome subject", mutate: func(input *TestInput) { input.Outcomes[0].Subject = "C:/Users/person/private" }, want: "subject"},
		{name: "invalid outcome status", mutate: func(input *TestInput) { input.Outcomes[0].Status = "pending" }, want: "status"},
		{name: "unsafe outcome summary", mutate: func(input *TestInput) { input.Outcomes[0].Summary = "Open /home/person/private." }, want: "absolute path"},
		{name: "passed failures", mutate: func(input *TestInput) { input.Outcomes[0].Failures = input.Outcomes[1].Failures }, want: "passed"},
		{name: "failed without failures", mutate: func(input *TestInput) { input.Outcomes[1].Failures = nil }, want: "without"},
		{name: "skipped failures", mutate: func(input *TestInput) { input.Outcomes[1].Status = TestStatusSkipped }, want: "skipped"},
		{name: "all skipped", mutate: func(input *TestInput) {
			input.Outcomes[0].Status = TestStatusSkipped
			input.Outcomes[1].Status = TestStatusSkipped
			input.Outcomes[1].Failures = nil
		}, want: "all test outcomes"},
		{name: "failure count", mutate: func(input *TestInput) { input.Outcomes[1].Failures = make([]TestFailure, maximumTestFailureCount+1) }, want: "failure count"},
		{name: "invalid failure kind", mutate: func(input *TestInput) { input.Outcomes[1].Failures[0].Kind = "Bad Kind" }, want: "kind"},
		{name: "unsafe failure subject", mutate: func(input *TestInput) { input.Outcomes[1].Failures[0].Subject = "/home/person/private" }, want: "subject"},
		{name: "unsafe failure summary", mutate: func(input *TestInput) { input.Outcomes[1].Failures[0].Summary = "Open /home/person/private." }, want: "absolute path"},
		{name: "duplicate failure", mutate: func(input *TestInput) {
			input.Outcomes[1].Failures = append(input.Outcomes[1].Failures, input.Outcomes[1].Failures[0])
		}, want: "duplicates"},
		{name: "outcome source", mutate: func(input *TestInput) {
			input.Outcomes[0].Sources = []diagnosticjson.Source{{Module: "example.com/app", Path: "../private", Kind: "test"}}
		}, want: "sources"},
		{name: "failure source", mutate: func(input *TestInput) {
			input.Outcomes[1].Failures[0].Sources = []diagnosticjson.Source{{Module: "example.com/app", Path: "../private", Kind: "failure"}}
		}, want: "sources"},
		{name: "additional source", mutate: func(input *TestInput) {
			input.Sources = []diagnosticjson.Source{{Module: "example.com/app", Path: "../private", Kind: "test"}}
		}, want: "sources"},
		{name: "invalid diagnostic", mutate: func(input *TestInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "invalid", Severity: diagnosticjson.SeverityError, Message: "Resolve the error."}}
		}, want: "shared envelope"},
		{name: "unsafe diagnostic", mutate: func(input *TestInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA-UNSAFE", Severity: diagnosticjson.SeverityError, Message: "Open /home/person/private."}}
		}, want: "absolute path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneTestInput(base)
			test.mutate(&input)
			result, err := NewTest(input)
			if !errors.Is(err, ErrTest) || !strings.Contains(err.Error(), test.want) || result.Valid() {
				t.Fatalf("NewTest = %#v, %v; want ErrTest containing %q", result, err, test.want)
			}
		})
	}
}

func TestTestV1StorageIsDefensiveAndSchemaIndependent(t *testing.T) {
	t.Parallel()

	evidence := resolvedTestEvidence(t, false)
	sliceInput := targetedTestSliceInput()
	source := graphDiagnosticSource(evidence.Modules()[0].Source())
	outcomes := []TestOutcome{{Order: 1, ID: "packages", Kind: "go-package", Subject: "example.com/app/orders", Status: TestStatusFailed, Summary: "A package failed.", Failures: []TestFailure{{Kind: "assertion", Subject: "TestSubmit", Summary: "The assertion failed.", Sources: []diagnosticjson.Source{source}}}, Sources: []diagnosticjson.Source{source}}}
	result, err := NewTest(TestInput{Evidence: evidence, Slice: sliceInput, Outcomes: outcomes})
	if err != nil {
		t.Fatalf("NewTest: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	sliceInput.Plugins[0].PluginID = "mutated"
	sliceInput.Providers[0].PluginID = "mutated"
	sliceInput.GeneratedContributions[0].Namespace = "mutated"
	outcomes[0].ID = "mutated"
	outcomes[0].Sources[0].Path = "mutated"
	outcomes[0].Failures[0].Subject = "mutated"
	outcomes[0].Failures[0].Sources[0].Path = "mutated"
	returnedSlice := result.Slice()
	returnedSlice.Plugins[0].ID = "mutated"
	returnedSlice.Plugins[0].Sources[0].Path = "mutated"
	returnedSlice.Replacements[0].PluginID = "mutated"
	returnedSlice.Replacements[0].Sources[0].Path = "mutated"
	returnedOutcomes := result.Outcomes()
	returnedOutcomes[0].ID = "mutated"
	returnedOutcomes[0].Failures[0].Subject = "mutated"
	evidenceJSON := result.ResolutionEvidenceJSON()
	evidenceJSON[0] = '['
	canonical := result.Envelope().CanonicalJSON()
	canonical[0] = '['
	storedSlice := result.Slice()
	storedOutcomes := result.Outcomes()
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) || storedSlice.Plugins[0].ID == "mutated" || storedSlice.Plugins[0].Sources[0].Path == "mutated" || storedSlice.Replacements[0].PluginID == "mutated" || storedOutcomes[0].ID == "mutated" || storedOutcomes[0].Failures[0].Subject == "mutated" || !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("test result storage aliases mutable input or returned data")
	}
	if (TestResult{}).Valid() || TestSchemaV1() == CheckSchemaV1() || TestSchemaV1() == GraphSchemaV1() || TestSchemaV1() == ExplainSchemaV1() || TestSchemaV1() == InspectSchemaV1() || TestSchemaV1().Name() != "plystra.test" || TestSchemaV1().Version() != 1 {
		t.Fatal("test schema identity is not independent")
	}
}

func targetedTestSliceInput() TestSliceInput {
	return TestSliceInput{
		Scope:        TestScopePlugin,
		TargetPlugin: "example.orders",
		Plugins: []TestSlicePluginInput{
			{Order: 3, PluginID: "example.orders"},
			{Order: 1, PluginID: "example.authn"},
			{Order: 2, PluginID: "example.catalog.fake"},
		},
		Providers: []TestProviderInput{
			{Capability: "catalog.lookup/v1", PluginID: "example.catalog.fake", Replacement: true},
			{Capability: "authn.activate/v1", PluginID: "example.authn"},
			{Capability: "kernel.health/v1"},
		},
		GeneratedContributions: []TestContributionInput{
			{Kind: TestContributionRequirement, Namespace: "authn", Capability: "catalog.lookup/v1", SourceCapability: "kernel.health/v1", ActivationCapability: "authn.activate/v1", PluginID: "example.authn", RuleID: "authn.require-catalog"},
			{Kind: TestContributionActivation, Namespace: "authn", SourceCapability: "kernel.health/v1", ActivationCapability: "authn.activate/v1", PluginID: "example.authn"},
		},
	}
}

func resolvedTestEvidence(t testing.TB, reverse bool) resolutionevidence.Evidence {
	t.Helper()

	contracts := map[string][]byte{
		"authn.activate/v1": testQueryContract(t, "authn.activate/v1"),
		"catalog.lookup/v1": testQueryContract(t, "catalog.lookup/v1"),
		"kernel.health/v1":  testIntrinsicContract(t, "kernel.health/v1"),
	}
	plugins := []generation.PluginInput{
		{ID: "example.authn", ModulePath: "example.com/authn", ModuleVersion: "v1.0.0", Provides: []string{"authn.activate/v1"}, BuildMetadataJSON: []byte("{}")},
		{ID: "example.catalog.smtp", ModulePath: "example.com/smtp", ModuleVersion: "v1.0.0", Provides: []string{"catalog.lookup/v1"}, BuildMetadataJSON: []byte("{}")},
		{ID: "example.orders", ModulePath: "example.com/app", Requires: []string{"catalog.lookup/v1"}, BuildMetadataJSON: []byte("{}")},
	}
	capabilities := []generation.CapabilityInput{
		{ContractJSON: contracts["authn.activate/v1"]},
		{ContractJSON: contracts["catalog.lookup/v1"]},
		{ContractJSON: contracts["kernel.health/v1"], Intrinsic: true},
	}
	requirementIDs := []string{"authn.activate/v1", "catalog.lookup/v1", "kernel.health/v1"}
	providers := []generation.ProviderInput{{Capability: "authn.activate/v1", Plugin: "example.authn"}, {Capability: "catalog.lookup/v1", Plugin: "example.catalog.smtp"}}
	if reverse {
		slices.Reverse(plugins)
		slices.Reverse(capabilities)
		slices.Reverse(requirementIDs)
		slices.Reverse(providers)
	}
	context, err := generation.NewContext(generation.Input{
		ConfigurationProvenance: &generation.ConfigurationProvenanceInput{Mode: generation.ConfigurationModeEnvironment, Environment: "test", RootPath: "plystra.yaml", RootDigest: inspectDigest("a"), SelectedPath: "plystra.test.yaml", SelectedDigest: inspectDigest("c"), DependencyCompositionDigest: inspectDigest("b")},
		Plugins:                 plugins, Capabilities: capabilities, Requirements: requirementIDs, Providers: providers,
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	requirements := []providerresolution.Requirement{
		{Contract: contracts["kernel.health/v1"], Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementDeclaration, Reference: "private-health-diagnostic", ModulePath: "example.com/app", Path: "plystra.yaml", Line: 3, Column: 3}},
		{Contract: contracts["authn.activate/v1"], Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementActivation, Reference: "private-activation-diagnostic", ModulePath: "example.com/app", Path: "plystra.yaml", Line: 5, Column: 3, Namespace: "authn", SourceCapability: "kernel.health/v1"}},
		{Contract: contracts["catalog.lookup/v1"], Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementPlugin, Reference: "private-orders-diagnostic", ModulePath: "example.com/app", Path: "orders/plugin.yaml", Line: 1, Column: 1, PluginID: "example.orders"}},
		{Contract: contracts["catalog.lookup/v1"], Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementGenerationRule, Reference: "private-generation-diagnostic", ModulePath: "example.com/authn", Path: "authn/plugin.yaml", Line: 1, Column: 1, PluginID: "example.authn", Namespace: "authn", SourceCapability: "kernel.health/v1", RuleID: "authn.require-catalog"}},
	}
	candidateProviders := []providerresolution.Candidate{
		{PluginID: "example.authn", Contract: contracts["authn.activate/v1"], Source: "resolved-secret-marker-authn"},
		{PluginID: "example.catalog.fake", Contract: contracts["catalog.lookup/v1"], Source: "resolved-secret-marker-fake"},
		{PluginID: "example.catalog.smtp", Contract: contracts["catalog.lookup/v1"], Source: "resolved-secret-marker-smtp"},
	}
	choices := []providerresolution.Choice{{Capability: "catalog.lookup/v1", PluginID: "example.catalog.smtp", Sources: []providerresolution.ChoiceSource{{Kind: providerresolution.ChoiceSourceCurrentProject, Reference: "private-selection-diagnostic", ModulePath: "example.com/app", Path: "plystra.test.yaml", Line: 9, Column: 5}}}}
	modules := []resolutionevidence.ModuleInput{
		{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
		{Path: "example.com/authn", Role: resolutionevidence.ModuleRoleDependency, RequiredVersion: "v1.0.0", SelectedVersion: "v1.0.0", Direct: true, SourceModulePath: "example.com/authn"},
		{Path: "example.com/fake", Role: resolutionevidence.ModuleRoleDependency, RequiredVersion: "v1.0.0", SelectedVersion: "v1.0.0", Direct: true, SourceModulePath: "example.com/fake"},
		{Path: "example.com/smtp", Role: resolutionevidence.ModuleRoleDependency, RequiredVersion: "v1.0.0", SelectedVersion: "v1.0.0", Direct: true, SourceModulePath: "example.com/smtp"},
	}
	candidates := []resolutionevidence.PluginCandidateInput{
		{ID: "example.authn", ModulePath: "example.com/authn", Path: "authn"},
		{ID: "example.catalog.fake", ModulePath: "example.com/fake", Path: "fake"},
		{ID: "example.catalog.smtp", ModulePath: "example.com/smtp", Path: "smtp"},
		{ID: "example.orders", ModulePath: "example.com/app", Path: "orders"},
	}
	assemblyPlugins := []resolutionevidence.AssemblyPluginInput{
		{PluginID: "example.authn", ModulePath: "example.com/authn", ModuleVersion: "v1.0.0", ImportPath: "example.com/authn/authn"},
		{PluginID: "example.catalog.smtp", ModulePath: "example.com/smtp", ModuleVersion: "v1.0.0", ImportPath: "example.com/smtp/smtp"},
		{PluginID: "example.orders", ModulePath: "example.com/app", ImportPath: "example.com/app/orders"},
	}
	if reverse {
		slices.Reverse(requirements)
		slices.Reverse(candidateProviders)
		slices.Reverse(modules)
		slices.Reverse(candidates)
		slices.Reverse(assemblyPlugins)
	}
	providerResult, err := providerresolution.Resolve(providerresolution.Input{Requirements: requirements, Candidates: candidateProviders, Choices: choices})
	if err != nil {
		t.Fatalf("providerresolution.Resolve: %v", err)
	}
	aliases, err := aliasresolution.Resolve[inspectAliasOutput](context, nil)
	if err != nil {
		t.Fatalf("aliasresolution.Resolve: %v", err)
	}
	transports := applicationmeta.HTTPTransports{Connect: true}
	evidence, err := resolutionevidence.Build(resolutionevidence.Input{Context: context, ProviderResolution: providerResult, AliasResolution: aliases, Modules: modules, PluginCandidates: candidates, StaticAssembly: &resolutionevidence.StaticAssemblyInput{Plugins: assemblyPlugins}, HTTPTransports: &transports})
	if err != nil {
		t.Fatalf("resolutionevidence.Build: %v", err)
	}
	return evidence
}

func testQueryContract(t testing.TB, id string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte("id: " + id + `
request: {}
response: {}
semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`))
	if err != nil {
		t.Fatalf("capabilitymeta.NormalizeSchema(%s): %v", id, err)
	}
	return canonical
}

func testIntrinsicContract(t testing.TB, id string) []byte {
	t.Helper()
	for _, definition := range intrinsiccatalog.Definitions() {
		if definition.ID().String() == id {
			return definition.ContractJSON()
		}
	}
	t.Fatalf("intrinsic Capability %s is absent", id)
	return nil
}

func testCapabilityDigest(evidence resolutionevidence.Evidence, capability string) string {
	for _, requirement := range evidence.Requirements() {
		if requirement.Capability() == capability {
			return requirement.ContractDigest()
		}
	}
	for _, candidate := range evidence.ProviderCandidates() {
		if candidate.Capability() == capability {
			return candidate.ContractDigest()
		}
	}
	return ""
}

func cloneTestInput(input TestInput) TestInput {
	result := input
	result.Slice = cloneTestSliceInput(input.Slice)
	result.Outcomes = cloneTestOutcomes(input.Outcomes)
	result.Diagnostics = append([]diagnosticjson.Diagnostic(nil), input.Diagnostics...)
	result.Sources = append([]diagnosticjson.Source(nil), input.Sources...)
	return result
}
