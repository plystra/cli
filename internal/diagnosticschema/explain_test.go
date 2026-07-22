package diagnosticschema

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestExplainV1BuildsExactCausalResult(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	primary := intrinsicExplainSource(t, evidence, "kernel.health/v1")
	input := ExplainInput{
		Evidence:       evidence,
		SubjectKind:    ExplainSubjectCapability,
		Subject:        "kernel.health/v1",
		Outcome:        "available",
		Reason:         "intrinsic-kernel",
		PrimarySources: []diagnosticjson.Source{primary},
		Change: ExplainChange{
			Kind:   ExplainChangeFile,
			Module: "example.com/inspect",
			Path:   "plystra.production.yaml",
			Field:  `capabilities.require["kernel.health/v1"]`,
		},
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA-EXPLAIN-AVAILABLE",
			Severity: diagnosticjson.SeverityInfo,
			Message:  "The Capability is available from the Kernel intrinsic catalog.",
		}},
	}
	result, err := NewExplain(input)
	if err != nil {
		t.Fatalf("NewExplain: %v", err)
	}
	if !result.Valid() || result.Envelope().Schema() != ExplainSchemaV1() || result.Envelope().SchemaVersion() != 1 || result.Envelope().ConfigurationMode() != generation.ConfigurationModeEnvironment || result.Envelope().ApplicationModelDigest() != evidence.BuildModelDigest() {
		t.Fatalf("explain identity = valid %t schema %#v mode %q digest %q", result.Valid(), result.Envelope().Schema(), result.Envelope().ConfigurationMode(), result.Envelope().ApplicationModelDigest())
	}
	if result.SubjectKind() != ExplainSubjectCapability || result.Subject() != "kernel.health/v1" || result.Outcome() != "available" || result.Reason() != "intrinsic-kernel" || !slices.Equal(result.PrimarySources(), []diagnosticjson.Source{primary}) || result.Change() != input.Change {
		t.Fatalf("explain result = kind %q subject %q outcome %q reason %q sources %#v change %#v", result.SubjectKind(), result.Subject(), result.Outcome(), result.Reason(), result.PrimarySources(), result.Change())
	}
	if !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("explain result did not retain complete resolution evidence")
	}

	wantResult := canonicalObject(t, `{
		"subject":{"kind":"capability","id":"kernel.health/v1"},
		"decision":{"outcome":"available"},
		"reason":{"code":"intrinsic-kernel","sources":[{"module":"`+primary.Module+`","path":"`+primary.Path+`","kind":"`+primary.Kind+`","line":1,"column":1}]},
		"change":{"kind":"file","module":"example.com/inspect","path":"plystra.production.yaml","field":"capabilities.require[\"kernel.health/v1\"]","command":""},
		"resolution_evidence":`+string(evidence.CanonicalJSON())+`
	}`)
	if got := result.Envelope().ResultJSON(); !bytes.Equal(got, wantResult) {
		t.Fatalf("explain result JSON:\ngot:  %s\nwant: %s", got, wantResult)
	}
	changeSource := diagnosticjson.Source{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "change-target"}
	if !slices.Contains(result.Envelope().Sources(), primary) || !slices.Contains(result.Envelope().Sources(), changeSource) {
		t.Fatalf("explain envelope sources omit cause or change target: %#v", result.Envelope().Sources())
	}
	if bytes.Contains(result.Envelope().CanonicalJSON(), []byte("resolved-secret-marker")) || containsWindowsDrivePath(result.Envelope().CanonicalJSON()) {
		t.Fatal("explain envelope contains unrestricted configuration or an absolute path")
	}
}

func TestExplainV1SupportsEverySubjectAndChangeKind(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	primary := intrinsicExplainSource(t, evidence, "kernel.health/v1")
	tests := []struct {
		name    string
		kind    ExplainSubjectKind
		subject string
	}{
		{name: "capability", kind: ExplainSubjectCapability, subject: "kernel.health/v1"},
		{name: "plugin", kind: ExplainSubjectPlugin, subject: "example.health"},
		{name: "configuration", kind: ExplainSubjectConfiguration, subject: "http.address"},
		{name: "alias", kind: ExplainSubjectAlias, subject: "legacy.health/v1"},
		{name: "exposure", kind: ExplainSubjectExposure, subject: "kernel.health/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewExplain(ExplainInput{
				Evidence:       evidence,
				SubjectKind:    test.kind,
				Subject:        test.subject,
				Outcome:        "not-selected",
				Reason:         "current-configuration",
				PrimarySources: []diagnosticjson.Source{primary},
				Change: ExplainChange{
					Kind:    ExplainChangeCommand,
					Command: "plystra check --env production",
				},
			})
			if err != nil || !result.Valid() || result.SubjectKind() != test.kind || result.Subject() != test.subject || result.Change().Kind != ExplainChangeCommand {
				t.Fatalf("NewExplain = %#v, %v", result, err)
			}
		})
	}

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
		t.Run("mode-"+test.name, func(t *testing.T) {
			evidence := resolvedInspectEvidenceFor(t, test.configuration, test.environment)
			primary := intrinsicExplainSource(t, evidence, "kernel.health/v1")
			result, err := NewExplain(ExplainInput{
				Evidence:       evidence,
				SubjectKind:    ExplainSubjectCapability,
				Subject:        "kernel.health/v1",
				Outcome:        "available",
				Reason:         "intrinsic-kernel",
				PrimarySources: []diagnosticjson.Source{primary},
				Change:         ExplainChange{Kind: ExplainChangeCommand, Command: "plystra check"},
			})
			if err != nil || result.Envelope().ConfigurationMode() != test.mode {
				t.Fatalf("configuration mode = %q, %v", result.Envelope().ConfigurationMode(), err)
			}
		})
	}
}

func TestExplainV1CanonicalizesSourceAndDiagnosticPermutations(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	intrinsic := intrinsicExplainSource(t, evidence, "kernel.health/v1")
	selection := diagnosticjson.Source{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "configuration-selection"}
	diagnostics := []diagnosticjson.Diagnostic{
		{Code: "PLYSTRA-ZETA", Severity: diagnosticjson.SeverityWarning, Message: "Review the current decision."},
		{Code: "PLYSTRA-ALPHA", Severity: diagnosticjson.SeverityInfo, Message: "The direct source is available."},
	}
	primary := []diagnosticjson.Source{selection, intrinsic, selection}
	additional := []diagnosticjson.Source{selection, selection}
	build := func() ExplainResult {
		result, err := NewExplain(ExplainInput{
			Evidence:       evidence,
			SubjectKind:    ExplainSubjectConfiguration,
			Subject:        "http.transports.rest",
			Outcome:        "effective",
			Reason:         "environment-override",
			PrimarySources: primary,
			Change: ExplainChange{
				Kind:   ExplainChangeFile,
				Module: "example.com/inspect",
				Path:   "plystra.production.yaml",
				Field:  "http.transports.rest",
			},
			Diagnostics: diagnostics,
			Sources:     additional,
		})
		if err != nil {
			t.Fatalf("NewExplain: %v", err)
		}
		return result
	}
	first := build()
	slices.Reverse(diagnostics)
	slices.Reverse(primary)
	slices.Reverse(additional)
	second := build()
	if len(first.PrimarySources()) != 2 || !bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()) || first.Envelope().Digest() != second.Envelope().Digest() {
		t.Fatalf("permuted explanation = sources %#v equal %t digest %q/%q", first.PrimarySources(), bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()), first.Envelope().Digest(), second.Envelope().Digest())
	}
}

func TestExplainV1RejectsIncompleteAndUnsafeInput(t *testing.T) {
	t.Parallel()

	valid := resolvedInspectEvidence(t)
	primary := intrinsicExplainSource(t, valid, "kernel.health/v1")
	base := ExplainInput{
		Evidence:       valid,
		SubjectKind:    ExplainSubjectCapability,
		Subject:        "kernel.health/v1",
		Outcome:        "available",
		Reason:         "intrinsic-kernel",
		PrimarySources: []diagnosticjson.Source{primary},
		Change:         ExplainChange{Kind: ExplainChangeCommand, Command: "plystra check"},
	}
	tests := []struct {
		name   string
		mutate func(*ExplainInput)
		want   string
	}{
		{name: "zero evidence", mutate: func(input *ExplainInput) { input.Evidence = resolutionevidence.Evidence{} }, want: "resolution evidence"},
		{name: "missing configuration", mutate: func(input *ExplainInput) { input.Evidence = syntheticInspectEvidence(t, false, true, true) }, want: "configuration"},
		{name: "missing assembly", mutate: func(input *ExplainInput) { input.Evidence = syntheticInspectEvidence(t, true, false, true) }, want: "assembly"},
		{name: "missing transports", mutate: func(input *ExplainInput) { input.Evidence = syntheticInspectEvidence(t, true, true, false) }, want: "transports"},
		{name: "subject kind", mutate: func(input *ExplainInput) { input.SubjectKind = "module" }, want: "kind"},
		{name: "capability subject", mutate: func(input *ExplainInput) { input.Subject = "kernel.health" }, want: "Capability ID"},
		{name: "plugin subject", mutate: func(input *ExplainInput) { input.SubjectKind, input.Subject = ExplainSubjectPlugin, "Bad Plugin" }, want: "Plugin ID"},
		{name: "configuration subject", mutate: func(input *ExplainInput) {
			input.SubjectKind, input.Subject = ExplainSubjectConfiguration, "http address"
		}, want: "field path"},
		{name: "outcome", mutate: func(input *ExplainInput) { input.Outcome = "Not Selected" }, want: "outcome"},
		{name: "reason", mutate: func(input *ExplainInput) { input.Reason = "bad_reason" }, want: "reason"},
		{name: "primary source absent", mutate: func(input *ExplainInput) { input.PrimarySources = nil }, want: "primary source"},
		{name: "primary source unknown", mutate: func(input *ExplainInput) {
			input.PrimarySources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "unknown.yaml", Kind: "unknown-source"}}
		}, want: "absent"},
		{name: "invalid source", mutate: func(input *ExplainInput) {
			input.Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "unknown-source"}}
		}, want: "sources"},
		{name: "file module", mutate: func(input *ExplainInput) {
			input.Change = ExplainChange{Kind: ExplainChangeFile, Module: "example.com/other", Path: "plystra.yaml", Field: "http.address"}
		}, want: "current Project"},
		{name: "file path", mutate: func(input *ExplainInput) {
			input.Change = ExplainChange{Kind: ExplainChangeFile, Module: "example.com/inspect", Path: "../plystra.yaml", Field: "http.address"}
		}, want: "file target"},
		{name: "file field", mutate: func(input *ExplainInput) {
			input.Change = ExplainChange{Kind: ExplainChangeFile, Module: "example.com/inspect", Path: "plystra.yaml"}
		}, want: "file field"},
		{name: "file command", mutate: func(input *ExplainInput) {
			input.Change = ExplainChange{Kind: ExplainChangeFile, Module: "example.com/inspect", Path: "plystra.yaml", Field: "http.address", Command: "plystra check"}
		}, want: "must not contain"},
		{name: "command file", mutate: func(input *ExplainInput) {
			input.Change = ExplainChange{Kind: ExplainChangeCommand, Path: "plystra.yaml", Command: "plystra check"}
		}, want: "must not contain file"},
		{name: "command empty", mutate: func(input *ExplainInput) { input.Change = ExplainChange{Kind: ExplainChangeCommand} }, want: "non-empty"},
		{name: "command foreign", mutate: func(input *ExplainInput) { input.Change.Command = "go test ./..." }, want: "public plystra"},
		{name: "command compound", mutate: func(input *ExplainInput) { input.Change.Command = "plystra check; go test ./..." }, want: "exactly one"},
		{name: "command absolute", mutate: func(input *ExplainInput) { input.Change.Command = `plystra check --config C:\Users\person\secret.yaml` }, want: "absolute path"},
		{name: "change kind", mutate: func(input *ExplainInput) { input.Change.Kind = "editor" }, want: "kind"},
		{name: "invalid diagnostic", mutate: func(input *ExplainInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "invalid", Severity: diagnosticjson.SeverityError, Message: "Resolve the error."}}
		}, want: "shared envelope"},
		{name: "unsafe diagnostic", mutate: func(input *ExplainInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA-UNSAFE", Severity: diagnosticjson.SeverityError, Message: "Open /home/person/secret.yaml."}}
		}, want: "absolute path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.PrimarySources = append([]diagnosticjson.Source(nil), base.PrimarySources...)
			test.mutate(&input)
			result, err := NewExplain(input)
			if !errors.Is(err, ErrExplain) || !strings.Contains(err.Error(), test.want) || result.Valid() {
				t.Fatalf("NewExplain = %#v, %v; want ErrExplain containing %q", result, err, test.want)
			}
		})
	}
}

func TestExplainV1StorageIsDefensiveAndSchemaIndependent(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	primary := []diagnosticjson.Source{intrinsicExplainSource(t, evidence, "kernel.health/v1")}
	diagnostics := []diagnosticjson.Diagnostic{{Code: "PLYSTRA-EXPLAIN", Severity: diagnosticjson.SeverityInfo, Message: "The decision is available."}}
	result, err := NewExplain(ExplainInput{
		Evidence:       evidence,
		SubjectKind:    ExplainSubjectCapability,
		Subject:        "kernel.health/v1",
		Outcome:        "available",
		Reason:         "intrinsic-kernel",
		PrimarySources: primary,
		Change:         ExplainChange{Kind: ExplainChangeCommand, Command: "plystra check"},
		Diagnostics:    diagnostics,
	})
	if err != nil {
		t.Fatalf("NewExplain: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	primary[0].Path = "mutated"
	diagnostics[0].Message = "mutated"
	returnedSources := result.PrimarySources()
	returnedSources[0].Path = "mutated"
	evidenceJSON := result.ResolutionEvidenceJSON()
	evidenceJSON[0] = '['
	canonical := result.Envelope().CanonicalJSON()
	canonical[0] = '['
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) || result.PrimarySources()[0].Path == "mutated" || !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("explain result storage aliases mutable input or returned data")
	}
	if (ExplainResult{}).Valid() || ExplainSchemaV1() == InspectSchemaV1() || ExplainSchemaV1().Name() != "plystra.explain" || ExplainSchemaV1().Version() != 1 || InspectSchemaV1().Name() != "plystra.inspect" || InspectSchemaV1().Version() != 1 {
		t.Fatal("explain and inspect schema identities are not independent")
	}
}

func intrinsicExplainSource(t testing.TB, evidence resolutionevidence.Evidence, capability string) diagnosticjson.Source {
	t.Helper()
	assembly, exists := evidence.StaticAssembly()
	if !exists {
		t.Fatal("evidence omits static assembly")
	}
	for _, binding := range assembly.Bindings() {
		if binding.Capability() != capability {
			continue
		}
		source := binding.ProviderSource()
		return diagnosticjson.Source{Module: source.Module(), Path: source.Path(), Kind: source.Kind(), Line: source.Line(), Column: source.Column()}
	}
	t.Fatalf("static assembly omits %s", capability)
	return diagnosticjson.Source{}
}
