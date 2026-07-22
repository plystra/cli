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

func TestCheckV1BuildsExactOrderedResult(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	moduleSource := graphDiagnosticSource(evidence.Modules()[0].Source())
	selectionSource := diagnosticjson.Source{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "configuration-selection"}
	input := CheckInput{
		Evidence: evidence,
		Checks: []Check{
			{Order: 3, ID: "go-packages", Status: CheckStatusSkipped, Summary: "Go package tests were skipped because generated output is stale."},
			{Order: 1, ID: "configuration-composition", Status: CheckStatusPassed, Summary: "Dependency composition and selected configuration are current.", Sources: []diagnosticjson.Source{selectionSource}},
			{
				Order:   2,
				ID:      "generated-output",
				Status:  CheckStatusFailed,
				Summary: "Generated output is not current.",
				Findings: []CheckFinding{{
					Kind:    "changed",
					Subject: "generated/manifest.json",
					Summary: "The generated manifest differs from the selected application model.",
					Sources: []diagnosticjson.Source{moduleSource},
				}},
				Sources: []diagnosticjson.Source{moduleSource},
			},
		},
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA-GENERATED-DRIFT",
			Severity: diagnosticjson.SeverityError,
			Message:  "Run `plystra generate --env production` to refresh generated output.",
		}},
	}
	result, err := NewCheck(input)
	if err != nil {
		t.Fatalf("NewCheck: %v", err)
	}
	if !result.Valid() || result.Envelope().Schema() != CheckSchemaV1() || result.Envelope().SchemaVersion() != 1 || result.Envelope().ConfigurationMode() != generation.ConfigurationModeEnvironment || result.Envelope().ApplicationModelDigest() != evidence.BuildModelDigest() {
		t.Fatalf("check identity = valid %t schema %#v mode %q digest %q", result.Valid(), result.Envelope().Schema(), result.Envelope().ConfigurationMode(), result.Envelope().ApplicationModelDigest())
	}
	checks := result.Checks()
	if result.Status() != CheckStatusFailed || result.FailedCheckCount() != 1 || len(checks) != 3 || checks[0].ID != "configuration-composition" || checks[1].ID != "generated-output" || checks[2].ID != "go-packages" {
		t.Fatalf("check result = status %q failures %d checks %#v", result.Status(), result.FailedCheckCount(), checks)
	}
	if !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("check result did not retain complete resolution evidence")
	}

	wantResult := canonicalObject(t, `{
		"status":"failed",
		"failed_check_count":1,
		"checks":[
			{"order":1,"id":"configuration-composition","status":"passed","summary":"Dependency composition and selected configuration are current.","findings":[],"sources":[{"module":"example.com/inspect","path":"plystra.production.yaml","kind":"configuration-selection"}]},
			{"order":2,"id":"generated-output","status":"failed","summary":"Generated output is not current.","findings":[{"kind":"changed","subject":"generated/manifest.json","summary":"The generated manifest differs from the selected application model.","sources":[{"module":"`+moduleSource.Module+`","path":"`+moduleSource.Path+`","kind":"`+moduleSource.Kind+`","line":1,"column":1}]}],"sources":[{"module":"`+moduleSource.Module+`","path":"`+moduleSource.Path+`","kind":"`+moduleSource.Kind+`","line":1,"column":1}]},
			{"order":3,"id":"go-packages","status":"skipped","summary":"Go package tests were skipped because generated output is stale.","findings":[],"sources":[]}
		],
		"resolution_evidence":`+string(evidence.CanonicalJSON())+`
	}`)
	if got := result.Envelope().ResultJSON(); !bytes.Equal(got, wantResult) {
		t.Fatalf("check result JSON:\ngot:  %s\nwant: %s", got, wantResult)
	}
	if !slices.Contains(result.Envelope().Sources(), moduleSource) || !slices.Contains(result.Envelope().Sources(), selectionSource) {
		t.Fatalf("check envelope sources omit check provenance: %#v", result.Envelope().Sources())
	}
	if bytes.Contains(result.Envelope().CanonicalJSON(), []byte("resolved-secret-marker")) || containsWindowsDrivePath(result.Envelope().CanonicalJSON()) {
		t.Fatal("check envelope contains unrestricted configuration or an absolute path")
	}
}

func TestCheckV1SupportsEveryStatusAndConfigurationMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		checks      []Check
		wantStatus  CheckStatus
		wantFailure int
	}{
		{name: "passed", checks: []Check{{Order: 1, ID: "resolution", Status: CheckStatusPassed, Summary: "Resolution passed."}}, wantStatus: CheckStatusPassed},
		{name: "passed with skip", checks: []Check{{Order: 1, ID: "resolution", Status: CheckStatusPassed, Summary: "Resolution passed."}, {Order: 2, ID: "compatibility", Status: CheckStatusSkipped, Summary: "No compatibility baseline was selected."}}, wantStatus: CheckStatusPassed},
		{name: "failed", checks: []Check{{Order: 1, ID: "resolution", Status: CheckStatusFailed, Summary: "Resolution failed.", Findings: []CheckFinding{{Kind: "unresolved", Subject: "email.send/v1", Summary: "The required Capability has no Provider."}}}}, wantStatus: CheckStatusFailed, wantFailure: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewCheck(CheckInput{Evidence: resolvedInspectEvidence(t), Checks: test.checks})
			if err != nil || !result.Valid() || result.Status() != test.wantStatus || result.FailedCheckCount() != test.wantFailure {
				t.Fatalf("NewCheck = status %q failures %d, %v", result.Status(), result.FailedCheckCount(), err)
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
			result, err := NewCheck(CheckInput{Evidence: evidence, Checks: []Check{{Order: 1, ID: "resolution", Status: CheckStatusPassed, Summary: "Resolution passed."}}})
			if err != nil || result.Envelope().ConfigurationMode() != test.mode {
				t.Fatalf("configuration mode = %q, %v", result.Envelope().ConfigurationMode(), err)
			}
		})
	}
}

func TestCheckV1CanonicalizesPermutations(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	firstSource := graphDiagnosticSource(evidence.Modules()[0].Source())
	secondSource := intrinsicExplainSource(t, evidence, "kernel.health/v1")
	checks := []Check{
		{Order: 2, ID: "generated-output", Status: CheckStatusFailed, Summary: "Generated output is stale.", Findings: []CheckFinding{
			{Kind: "missing", Subject: "generated/go/application/main_gen.go", Summary: "The generated application entrypoint is missing.", Sources: []diagnosticjson.Source{secondSource}},
			{Kind: "changed", Subject: "generated/manifest.json", Summary: "The generated manifest changed.", Sources: []diagnosticjson.Source{firstSource, firstSource}},
		}},
		{Order: 1, ID: "configuration-composition", Status: CheckStatusPassed, Summary: "Configuration is current.", Sources: []diagnosticjson.Source{firstSource, firstSource}},
	}
	diagnostics := []diagnosticjson.Diagnostic{
		{Code: "PLYSTRA-ZETA", Severity: diagnosticjson.SeverityError, Message: "Regenerate the Project."},
		{Code: "PLYSTRA-ALPHA", Severity: diagnosticjson.SeverityInfo, Message: "Configuration is current."},
	}
	build := func() CheckResult {
		result, err := NewCheck(CheckInput{Evidence: evidence, Checks: checks, Diagnostics: diagnostics})
		if err != nil {
			t.Fatalf("NewCheck: %v", err)
		}
		return result
	}
	first := build()
	slices.Reverse(checks)
	slices.Reverse(checks[1].Findings)
	slices.Reverse(diagnostics)
	second := build()
	if len(first.Checks()[0].Sources) != 1 || !bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()) || first.Envelope().Digest() != second.Envelope().Digest() {
		t.Fatalf("permuted check = checks %#v equal %t digest %q/%q", first.Checks(), bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()), first.Envelope().Digest(), second.Envelope().Digest())
	}
}

func TestCheckV1RejectsIncompleteAndUnsafeInput(t *testing.T) {
	t.Parallel()

	valid := resolvedInspectEvidence(t)
	base := CheckInput{
		Evidence: valid,
		Checks: []Check{
			{Order: 1, ID: "configuration", Status: CheckStatusPassed, Summary: "Configuration passed."},
			{Order: 2, ID: "generated-output", Status: CheckStatusFailed, Summary: "Generated output failed.", Findings: []CheckFinding{{Kind: "changed", Subject: "generated/manifest.json", Summary: "The generated manifest changed."}}},
		},
	}
	tests := []struct {
		name   string
		mutate func(*CheckInput)
		want   string
	}{
		{name: "zero evidence", mutate: func(input *CheckInput) { input.Evidence = resolutionevidence.Evidence{} }, want: "resolution evidence"},
		{name: "missing configuration", mutate: func(input *CheckInput) { input.Evidence = syntheticInspectEvidence(t, false, true, true) }, want: "configuration"},
		{name: "missing assembly", mutate: func(input *CheckInput) { input.Evidence = syntheticInspectEvidence(t, true, false, true) }, want: "assembly"},
		{name: "missing transports", mutate: func(input *CheckInput) { input.Evidence = syntheticInspectEvidence(t, true, true, false) }, want: "transports"},
		{name: "no checks", mutate: func(input *CheckInput) { input.Checks = nil }, want: "at least one"},
		{name: "check count", mutate: func(input *CheckInput) { input.Checks = make([]Check, maximumCheckCount+1) }, want: "check count"},
		{name: "zero order", mutate: func(input *CheckInput) { input.Checks[0].Order = 0 }, want: "order"},
		{name: "duplicate order", mutate: func(input *CheckInput) { input.Checks[1].Order = 1 }, want: "duplicated"},
		{name: "order gap", mutate: func(input *CheckInput) { input.Checks[1].Order = 3 }, want: "contiguous"},
		{name: "invalid id", mutate: func(input *CheckInput) { input.Checks[0].ID = "Bad Check" }, want: "id"},
		{name: "duplicate id", mutate: func(input *CheckInput) { input.Checks[1].ID = input.Checks[0].ID }, want: "duplicated"},
		{name: "invalid status", mutate: func(input *CheckInput) { input.Checks[0].Status = "warning" }, want: "status"},
		{name: "empty summary", mutate: func(input *CheckInput) { input.Checks[0].Summary = "" }, want: "summary"},
		{name: "unsafe summary", mutate: func(input *CheckInput) { input.Checks[0].Summary = "Open /home/person/project." }, want: "absolute path"},
		{name: "passed findings", mutate: func(input *CheckInput) { input.Checks[0].Findings = input.Checks[1].Findings }, want: "passed"},
		{name: "failed no findings", mutate: func(input *CheckInput) { input.Checks[1].Findings = nil }, want: "without"},
		{name: "skipped findings", mutate: func(input *CheckInput) { input.Checks[1].Status = CheckStatusSkipped }, want: "skipped"},
		{name: "all skipped", mutate: func(input *CheckInput) {
			input.Checks[0].Status = CheckStatusSkipped
			input.Checks[1].Status = CheckStatusSkipped
			input.Checks[1].Findings = nil
		}, want: "all checks"},
		{name: "finding count", mutate: func(input *CheckInput) { input.Checks[1].Findings = make([]CheckFinding, maximumCheckFindingCount+1) }, want: "finding count"},
		{name: "finding kind", mutate: func(input *CheckInput) { input.Checks[1].Findings[0].Kind = "Bad Kind" }, want: "kind"},
		{name: "finding subject", mutate: func(input *CheckInput) { input.Checks[1].Findings[0].Subject = "generated output" }, want: "subject"},
		{name: "finding windows path", mutate: func(input *CheckInput) { input.Checks[1].Findings[0].Subject = "C:/Users/person/secret" }, want: "subject"},
		{name: "finding unix path", mutate: func(input *CheckInput) { input.Checks[1].Findings[0].Subject = "/home/person/secret" }, want: "subject"},
		{name: "finding summary", mutate: func(input *CheckInput) { input.Checks[1].Findings[0].Summary = "Open /home/person/secret." }, want: "absolute path"},
		{name: "duplicate finding", mutate: func(input *CheckInput) {
			input.Checks[1].Findings = append(input.Checks[1].Findings, input.Checks[1].Findings[0])
		}, want: "duplicates"},
		{name: "check source", mutate: func(input *CheckInput) {
			input.Checks[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "check"}}
		}, want: "sources"},
		{name: "finding source", mutate: func(input *CheckInput) {
			input.Checks[1].Findings[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "finding"}}
		}, want: "sources"},
		{name: "additional source", mutate: func(input *CheckInput) {
			input.Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "check"}}
		}, want: "sources"},
		{name: "invalid diagnostic", mutate: func(input *CheckInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "invalid", Severity: diagnosticjson.SeverityError, Message: "Resolve the error."}}
		}, want: "shared envelope"},
		{name: "unsafe diagnostic", mutate: func(input *CheckInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA-UNSAFE", Severity: diagnosticjson.SeverityError, Message: "Open /home/person/secret.yaml."}}
		}, want: "absolute path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneCheckInput(base)
			test.mutate(&input)
			result, err := NewCheck(input)
			if !errors.Is(err, ErrCheck) || !strings.Contains(err.Error(), test.want) || result.Valid() {
				t.Fatalf("NewCheck = %#v, %v; want ErrCheck containing %q", result, err, test.want)
			}
		})
	}
}

func TestCheckV1StorageIsDefensiveAndSchemaIndependent(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	source := graphDiagnosticSource(evidence.Modules()[0].Source())
	checks := []Check{
		{Order: 1, ID: "configuration", Status: CheckStatusPassed, Summary: "Configuration passed.", Sources: []diagnosticjson.Source{source}},
		{Order: 2, ID: "generated-output", Status: CheckStatusFailed, Summary: "Generated output failed.", Findings: []CheckFinding{{Kind: "changed", Subject: "generated/manifest.json", Summary: "The manifest changed.", Sources: []diagnosticjson.Source{source}}}},
	}
	result, err := NewCheck(CheckInput{Evidence: evidence, Checks: checks})
	if err != nil {
		t.Fatalf("NewCheck: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	checks[0].ID = "mutated"
	checks[0].Sources[0].Path = "mutated"
	checks[1].Findings[0].Subject = "mutated"
	checks[1].Findings[0].Sources[0].Path = "mutated"
	returned := result.Checks()
	returned[0].ID = "mutated"
	returned[0].Sources[0].Path = "mutated"
	returned[1].Findings[0].Subject = "mutated"
	returned[1].Findings[0].Sources[0].Path = "mutated"
	evidenceJSON := result.ResolutionEvidenceJSON()
	evidenceJSON[0] = '['
	canonical := result.Envelope().CanonicalJSON()
	canonical[0] = '['
	stored := result.Checks()
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) || stored[0].ID == "mutated" || stored[0].Sources[0].Path == "mutated" || stored[1].Findings[0].Subject == "mutated" || stored[1].Findings[0].Sources[0].Path == "mutated" || !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("check result storage aliases mutable input or returned data")
	}
	if (CheckResult{}).Valid() || CheckSchemaV1() == GraphSchemaV1() || CheckSchemaV1() == ExplainSchemaV1() || CheckSchemaV1() == InspectSchemaV1() || CheckSchemaV1().Name() != "plystra.check" || CheckSchemaV1().Version() != 1 {
		t.Fatal("check, graph, explain, and inspect schema identities are not independent")
	}
}

func cloneCheckInput(input CheckInput) CheckInput {
	result := input
	result.Checks = cloneChecks(input.Checks)
	result.Diagnostics = append([]diagnosticjson.Diagnostic(nil), input.Diagnostics...)
	result.Sources = append([]diagnosticjson.Source(nil), input.Sources...)
	return result
}
