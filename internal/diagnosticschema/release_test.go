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

func TestReleaseV1BuildsExactCompleteResult(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	moduleSource := graphDiagnosticSource(evidence.Modules()[0].Source())
	policySource := diagnosticjson.Source{Module: "example.com/inspect", Path: "plystra.production.yaml", Kind: "release-policy", Line: 1, Column: 1}
	input := completeReleaseInput(evidence)
	input.PolicyEvaluations[1].Sources = []diagnosticjson.Source{policySource}
	input.EvidenceFiles[1].Sources = []diagnosticjson.Source{moduleSource}
	input.Artifacts = []ReleaseArtifact{
		{Kind: "go-module", Path: "dist/example-app-module.zip", Digest: inspectDigest("b")},
		{Kind: "binary", Path: "dist/example-app-windows-amd64.exe", Digest: inspectDigest("a"), Sources: []diagnosticjson.Source{moduleSource}},
	}
	input.Sources = []diagnosticjson.Source{policySource, policySource}
	slices.Reverse(input.PolicyEvaluations)
	slices.Reverse(input.EvidenceFiles)

	result, err := NewRelease(input)
	if err != nil {
		t.Fatalf("NewRelease: %v", err)
	}
	if !result.Valid() || result.Envelope().Schema() != ReleaseSchemaV1() || result.Envelope().SchemaVersion() != 1 || result.Envelope().ConfigurationMode() != generation.ConfigurationModeEnvironment || result.Envelope().ApplicationModelDigest() != evidence.BuildModelDigest() {
		t.Fatalf("release identity = valid %t schema %#v mode %q digest %q", result.Valid(), result.Envelope().Schema(), result.Envelope().ConfigurationMode(), result.Envelope().ApplicationModelDigest())
	}
	if result.Kind() != ReleaseKindPrereleaseCandidate || result.Status() != ReleaseStatusComplete || result.UnsatisfiedPolicyCount() != 0 || result.IncompleteEvidenceCount() != 0 || result.Policy() != input.Policy {
		t.Fatalf("release summary = kind %q status %q policy failures %d evidence failures %d policy %#v", result.Kind(), result.Status(), result.UnsatisfiedPolicyCount(), result.IncompleteEvidenceCount(), result.Policy())
	}
	if result.EvidenceDirectory() != "dist/release" || result.StatusPath() != "dist/release/release-status.json" {
		t.Fatalf("release paths = %q %q", result.EvidenceDirectory(), result.StatusPath())
	}
	if got := result.PolicyEvaluations(); len(got) != 4 || got[0].Field != ReleasePolicyAllowLocalReplace || got[3].Field != ReleasePolicyRequireCompatibilityBaseline {
		t.Fatalf("policy evaluation order = %#v", got)
	}
	if got := result.EvidenceFiles(); len(got) != 6 || got[0].Path != "dist/release/resolution.json" || got[5].Path != "dist/release/provenance.intoto.jsonl" {
		t.Fatalf("evidence file order = %#v", got)
	}
	if got := result.Artifacts(); len(got) != 2 || got[0].Kind != "binary" || got[1].Kind != "go-module" {
		t.Fatalf("artifact order = %#v", got)
	}
	if !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("release result did not retain complete resolution evidence")
	}

	wantResult := canonicalObject(t, `{
		"status":"complete",
		"release_kind":"prerelease_candidate",
		"evidence_directory":"dist/release",
		"status_path":"dist/release/release-status.json",
		"unsatisfied_policy_count":0,
		"incomplete_evidence_count":0,
		"policy":{"allow_local_replace":false,"require_provider_conformance":true,"require_clean_vulnerability_scan":true,"require_compatibility_baseline":true},
		"policy_evaluations":[
			{"field":"allow_local_replace","outcome":"passed","summary":"No local replacements participate in the release.","sources":[]},
			{"field":"require_provider_conformance","outcome":"passed","summary":"Every selected Provider passed required conformance.","sources":[{"module":"example.com/inspect","path":"plystra.production.yaml","kind":"release-policy","line":1,"column":1}]},
			{"field":"require_clean_vulnerability_scan","outcome":"passed","summary":"Reachable vulnerability analysis passed.","sources":[]},
			{"field":"require_compatibility_baseline","outcome":"passed","summary":"Compatibility evidence satisfies the selected baseline.","sources":[]}
		],
		"evidence_files":[
			{"kind":"resolution","path":"dist/release/resolution.json","status":"complete","digest":"`+inspectDigest("1")+`","sources":[]},
			{"kind":"compatibility","path":"dist/release/compatibility.json","status":"complete","digest":"`+inspectDigest("2")+`","sources":[{"module":"`+moduleSource.Module+`","path":"`+moduleSource.Path+`","kind":"`+moduleSource.Kind+`","line":1,"column":1}]},
			{"kind":"conformance","path":"dist/release/conformance.json","status":"complete","digest":"`+inspectDigest("3")+`","sources":[]},
			{"kind":"vulnerabilities","path":"dist/release/vulnerabilities.json","status":"complete","digest":"`+inspectDigest("4")+`","sources":[]},
			{"kind":"sbom","path":"dist/release/sbom.spdx.json","status":"complete","digest":"`+inspectDigest("5")+`","sources":[]},
			{"kind":"provenance","path":"dist/release/provenance.intoto.jsonl","status":"complete","digest":"`+inspectDigest("6")+`","sources":[]}
		],
		"artifacts":[
			{"kind":"binary","path":"dist/example-app-windows-amd64.exe","digest":"`+inspectDigest("a")+`","sources":[{"module":"`+moduleSource.Module+`","path":"`+moduleSource.Path+`","kind":"`+moduleSource.Kind+`","line":1,"column":1}]},
			{"kind":"go-module","path":"dist/example-app-module.zip","digest":"`+inspectDigest("b")+`","sources":[]}
		],
		"resolution_evidence":`+string(evidence.CanonicalJSON())+`
	}`)
	if got := result.Envelope().ResultJSON(); !bytes.Equal(got, wantResult) {
		t.Fatalf("release result JSON:\ngot:  %s\nwant: %s", got, wantResult)
	}
	if !slices.Contains(result.Envelope().Sources(), moduleSource) || !slices.Contains(result.Envelope().Sources(), policySource) {
		t.Fatalf("release envelope sources omit provenance: %#v", result.Envelope().Sources())
	}
	if bytes.Contains(result.Envelope().CanonicalJSON(), []byte("resolved-secret-marker")) || containsWindowsDrivePath(result.Envelope().CanonicalJSON()) {
		t.Fatal("release envelope contains unrestricted configuration or an absolute path")
	}
}

func TestReleaseV1SupportsKindsStatusesPoliciesAndConfigurationModes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		configuration string
		environment   string
		mode          generation.ConfigurationMode
		kind          ReleaseKind
	}{
		{name: "default prerelease", mode: generation.ConfigurationModeDefault, kind: ReleaseKindPrereleaseCandidate},
		{name: "environment stable", environment: "production", mode: generation.ConfigurationModeEnvironment, kind: ReleaseKindStable},
		{name: "explicit prerelease", configuration: "deploy/customer.yaml", mode: generation.ConfigurationModeExplicit, kind: ReleaseKindPrereleaseCandidate},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := resolvedInspectEvidenceFor(t, test.configuration, test.environment)
			input := completeReleaseInput(evidence)
			input.Kind = test.kind
			result, err := NewRelease(input)
			if err != nil || !result.Valid() || result.Status() != ReleaseStatusComplete || result.Kind() != test.kind || result.Envelope().ConfigurationMode() != test.mode {
				t.Fatalf("NewRelease = kind %q status %q mode %q, %v", result.Kind(), result.Status(), result.Envelope().ConfigurationMode(), err)
			}
		})
	}

	t.Run("disabled requirements", func(t *testing.T) {
		input := completeReleaseInput(resolvedInspectEvidence(t))
		input.Policy = ReleasePolicy{AllowLocalReplace: true}
		input.PolicyEvaluations = []ReleasePolicyEvaluation{
			{Field: ReleasePolicyAllowLocalReplace, Outcome: ReleasePolicyPassed, Summary: "Local replacements are permitted by the selected policy."},
			{Field: ReleasePolicyRequireProviderConformance, Outcome: ReleasePolicyNotRequired, Summary: "Provider conformance is not required by the selected policy."},
			{Field: ReleasePolicyRequireCleanVulnerabilityScan, Outcome: ReleasePolicyNotRequired, Summary: "A clean vulnerability scan is not required by the selected policy."},
			{Field: ReleasePolicyRequireCompatibilityBaseline, Outcome: ReleasePolicyNotRequired, Summary: "A compatibility baseline is not required by the selected policy."},
		}
		result, err := NewRelease(input)
		if err != nil || !result.Valid() || result.Status() != ReleaseStatusComplete || result.UnsatisfiedPolicyCount() != 0 {
			t.Fatalf("disabled policy result = %#v, %v", result, err)
		}
	})

	for _, test := range []struct {
		name              string
		mutate            func(*ReleaseInput)
		wantPolicyCount   int
		wantEvidenceCount int
	}{
		{name: "failed policy", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[0].Outcome = ReleasePolicyFailed }, wantPolicyCount: 1},
		{name: "indeterminate policy", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[2].Outcome = ReleasePolicyIndeterminate }, wantPolicyCount: 1},
		{name: "incomplete evidence", mutate: func(input *ReleaseInput) { input.EvidenceFiles[3].Status = ReleaseEvidenceIncomplete }, wantEvidenceCount: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseInput(resolvedInspectEvidence(t))
			test.mutate(&input)
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA-RELEASE-INCOMPLETE", Severity: diagnosticjson.SeverityError, Message: "Release evidence is incomplete."}}
			input.Artifacts = nil
			result, err := NewRelease(input)
			if err != nil || !result.Valid() || result.Status() != ReleaseStatusIncomplete || result.UnsatisfiedPolicyCount() != test.wantPolicyCount || result.IncompleteEvidenceCount() != test.wantEvidenceCount {
				t.Fatalf("incomplete result = status %q policy %d evidence %d, %v", result.Status(), result.UnsatisfiedPolicyCount(), result.IncompleteEvidenceCount(), err)
			}
		})
	}
}

func TestReleaseV1CanonicalizesInputPermutations(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	source := graphDiagnosticSource(evidence.Modules()[0].Source())
	input := completeReleaseInput(evidence)
	input.PolicyEvaluations[0].Sources = []diagnosticjson.Source{source, source}
	input.EvidenceFiles[0].Sources = []diagnosticjson.Source{source, source}
	input.Artifacts = append(input.Artifacts, ReleaseArtifact{Kind: "go-module", Path: "dist/module.zip", Digest: inspectDigest("b"), Sources: []diagnosticjson.Source{source, source}})
	input.Diagnostics = append(input.Diagnostics, diagnosticjson.Diagnostic{Code: "PLYSTRA-RELEASE-WARNING", Severity: diagnosticjson.SeverityWarning, Message: "Release evidence includes a warning."})
	input.Sources = []diagnosticjson.Source{source, source}
	first, err := NewRelease(input)
	if err != nil {
		t.Fatalf("first NewRelease: %v", err)
	}

	permuted := cloneReleaseInput(input)
	slices.Reverse(permuted.PolicyEvaluations)
	slices.Reverse(permuted.EvidenceFiles)
	slices.Reverse(permuted.Artifacts)
	slices.Reverse(permuted.Diagnostics)
	slices.Reverse(permuted.Sources)
	second, err := NewRelease(permuted)
	if err != nil {
		t.Fatalf("second NewRelease: %v", err)
	}
	if len(first.PolicyEvaluations()[0].Sources) != 1 || len(first.EvidenceFiles()[0].Sources) != 1 || len(first.Artifacts()[1].Sources) != 1 || !bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()) || first.Envelope().Digest() != second.Envelope().Digest() {
		t.Fatalf("permuted release = equal %t digest %q/%q", bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()), first.Envelope().Digest(), second.Envelope().Digest())
	}
}

func TestReleaseV1RejectsIncompleteAndUnsafeInput(t *testing.T) {
	t.Parallel()

	base := completeReleaseInput(resolvedInspectEvidence(t))
	tests := []struct {
		name   string
		mutate func(*ReleaseInput)
		want   string
	}{
		{name: "zero evidence", mutate: func(input *ReleaseInput) { input.Evidence = resolutionevidence.Evidence{} }, want: "resolution evidence"},
		{name: "missing configuration", mutate: func(input *ReleaseInput) { input.Evidence = syntheticInspectEvidence(t, false, true, true) }, want: "configuration"},
		{name: "missing assembly", mutate: func(input *ReleaseInput) { input.Evidence = syntheticInspectEvidence(t, true, false, true) }, want: "assembly"},
		{name: "missing transports", mutate: func(input *ReleaseInput) { input.Evidence = syntheticInspectEvidence(t, true, true, false) }, want: "transports"},
		{name: "release kind", mutate: func(input *ReleaseInput) { input.Kind = "candidate" }, want: "release kind"},
		{name: "policy evaluation count", mutate: func(input *ReleaseInput) { input.PolicyEvaluations = input.PolicyEvaluations[:3] }, want: "exactly 4"},
		{name: "policy field", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[0].Field = "remote_policy" }, want: "field"},
		{name: "duplicate policy field", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[1].Field = input.PolicyEvaluations[0].Field }, want: "duplicated"},
		{name: "policy outcome", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[0].Outcome = "skipped" }, want: "outcome"},
		{name: "required policy not required", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[1].Outcome = ReleasePolicyNotRequired }, want: "evaluated policy"},
		{name: "allowed replacement failed", mutate: func(input *ReleaseInput) {
			input.Policy.AllowLocalReplace = true
			input.PolicyEvaluations[0].Outcome = ReleasePolicyFailed
		}, want: "must be passed"},
		{name: "disabled requirement passed", mutate: func(input *ReleaseInput) { input.Policy.RequireProviderConformance = false }, want: "not-required"},
		{name: "policy summary", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[0].Summary = "" }, want: "summary"},
		{name: "unsafe policy summary", mutate: func(input *ReleaseInput) { input.PolicyEvaluations[0].Summary = "Open /home/person/policy.yaml." }, want: "absolute path"},
		{name: "policy source", mutate: func(input *ReleaseInput) {
			input.PolicyEvaluations[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../policy.yaml", Kind: "release-policy"}}
		}, want: "sources"},
		{name: "evidence file count", mutate: func(input *ReleaseInput) { input.EvidenceFiles = input.EvidenceFiles[:5] }, want: "exactly 6"},
		{name: "evidence kind", mutate: func(input *ReleaseInput) { input.EvidenceFiles[0].Kind = "signature" }, want: "kind"},
		{name: "duplicate evidence kind", mutate: func(input *ReleaseInput) { input.EvidenceFiles[1].Kind = input.EvidenceFiles[0].Kind }, want: "duplicated"},
		{name: "evidence status", mutate: func(input *ReleaseInput) { input.EvidenceFiles[0].Status = "indeterminate" }, want: "status"},
		{name: "evidence digest", mutate: func(input *ReleaseInput) { input.EvidenceFiles[0].Digest = "" }, want: "digest"},
		{name: "uppercase evidence digest", mutate: func(input *ReleaseInput) { input.EvidenceFiles[0].Digest = "sha256:" + strings.Repeat("A", 64) }, want: "digest"},
		{name: "evidence source", mutate: func(input *ReleaseInput) {
			input.EvidenceFiles[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../resolution.json", Kind: "release-evidence"}}
		}, want: "sources"},
		{name: "artifact count", mutate: func(input *ReleaseInput) { input.Artifacts = make([]ReleaseArtifact, maximumReleaseArtifactCount+1) }, want: "artifact count"},
		{name: "artifact kind", mutate: func(input *ReleaseInput) { input.Artifacts[0].Kind = "Bad Kind" }, want: "kind"},
		{name: "artifact path empty", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "" }, want: "path"},
		{name: "artifact path root", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "." }, want: "path"},
		{name: "artifact traversal", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "../artifact.zip" }, want: "path"},
		{name: "artifact absolute", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "/home/person/artifact.zip" }, want: "path"},
		{name: "artifact windows path", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "C:/Users/person/artifact.zip" }, want: "path"},
		{name: "artifact colon", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "dist/artifact:alternate.zip" }, want: "path"},
		{name: "artifact whitespace", mutate: func(input *ReleaseInput) { input.Artifacts[0].Path = "dist/release artifact.zip" }, want: "path"},
		{name: "artifact digest", mutate: func(input *ReleaseInput) { input.Artifacts[0].Digest = "sha256:no" }, want: "digest"},
		{name: "duplicate artifact", mutate: func(input *ReleaseInput) { input.Artifacts = append(input.Artifacts, input.Artifacts[0]) }, want: "duplicated"},
		{name: "duplicate artifact path", mutate: func(input *ReleaseInput) {
			input.Artifacts = append(input.Artifacts, ReleaseArtifact{Kind: "source-archive", Path: input.Artifacts[0].Path, Digest: inspectDigest("b")})
		}, want: "duplicated"},
		{name: "artifact source", mutate: func(input *ReleaseInput) {
			input.Artifacts[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../artifact", Kind: "release-artifact"}}
		}, want: "sources"},
		{name: "complete without artifact", mutate: func(input *ReleaseInput) { input.Artifacts = nil }, want: "at least one"},
		{name: "complete with error", mutate: func(input *ReleaseInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA-RELEASE-ERROR", Severity: diagnosticjson.SeverityError, Message: "Release failed."}}
		}, want: "complete release contains"},
		{name: "incomplete without error", mutate: func(input *ReleaseInput) { input.EvidenceFiles[0].Status = ReleaseEvidenceIncomplete }, want: "requires an error"},
		{name: "invalid diagnostic", mutate: func(input *ReleaseInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "invalid", Severity: diagnosticjson.SeverityInfo, Message: "Release evidence is complete."}}
		}, want: "shared envelope"},
		{name: "unsafe diagnostic", mutate: func(input *ReleaseInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA-UNSAFE", Severity: diagnosticjson.SeverityInfo, Message: "Open /home/person/release.json."}}
		}, want: "absolute path"},
		{name: "additional source", mutate: func(input *ReleaseInput) {
			input.Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../source", Kind: "release"}}
		}, want: "sources"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneReleaseInput(base)
			test.mutate(&input)
			result, err := NewRelease(input)
			if !errors.Is(err, ErrRelease) || !strings.Contains(err.Error(), test.want) || result.Valid() {
				t.Fatalf("NewRelease = %#v, %v; want ErrRelease containing %q", result, err, test.want)
			}
		})
	}
}

func TestReleaseV1StorageIsDefensiveAndSchemaIndependent(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	source := graphDiagnosticSource(evidence.Modules()[0].Source())
	input := completeReleaseInput(evidence)
	input.PolicyEvaluations[0].Sources = []diagnosticjson.Source{source}
	input.EvidenceFiles[0].Sources = []diagnosticjson.Source{source}
	input.Artifacts[0].Sources = []diagnosticjson.Source{source}
	result, err := NewRelease(input)
	if err != nil {
		t.Fatalf("NewRelease: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	input.PolicyEvaluations[0].Summary = "mutated"
	input.PolicyEvaluations[0].Sources[0].Path = "mutated"
	input.EvidenceFiles[0].Digest = inspectDigest("f")
	input.EvidenceFiles[0].Sources[0].Path = "mutated"
	input.Artifacts[0].Path = "mutated"
	input.Artifacts[0].Sources[0].Path = "mutated"
	evaluations := result.PolicyEvaluations()
	evaluations[0].Summary = "mutated"
	evaluations[0].Sources[0].Path = "mutated"
	evidenceFiles := result.EvidenceFiles()
	evidenceFiles[0].Digest = inspectDigest("f")
	evidenceFiles[0].Sources[0].Path = "mutated"
	artifacts := result.Artifacts()
	artifacts[0].Path = "mutated"
	artifacts[0].Sources[0].Path = "mutated"
	evidenceJSON := result.ResolutionEvidenceJSON()
	evidenceJSON[0] = '['
	canonical := result.Envelope().CanonicalJSON()
	canonical[0] = '['
	storedEvaluations := result.PolicyEvaluations()
	storedEvidence := result.EvidenceFiles()
	storedArtifacts := result.Artifacts()
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) || storedEvaluations[0].Summary == "mutated" || storedEvaluations[0].Sources[0].Path == "mutated" || storedEvidence[0].Digest == inspectDigest("f") || storedEvidence[0].Sources[0].Path == "mutated" || storedArtifacts[0].Path == "mutated" || storedArtifacts[0].Sources[0].Path == "mutated" || !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("release result storage aliases mutable input or returned data")
	}
	if (ReleaseResult{}).Valid() || ReleaseSchemaV1() == InspectSchemaV1() || ReleaseSchemaV1() == ExplainSchemaV1() || ReleaseSchemaV1() == GraphSchemaV1() || ReleaseSchemaV1() == CheckSchemaV1() || ReleaseSchemaV1() == TestSchemaV1() || ReleaseSchemaV1().Name() != "plystra.release" || ReleaseSchemaV1().Version() != 1 {
		t.Fatal("release and prior command schema identities are not independent")
	}
}

func completeReleaseInput(evidence resolutionevidence.Evidence) ReleaseInput {
	return ReleaseInput{
		Evidence: evidence,
		Kind:     ReleaseKindPrereleaseCandidate,
		Policy: ReleasePolicy{
			RequireProviderConformance:    true,
			RequireCleanVulnerabilityScan: true,
			RequireCompatibilityBaseline:  true,
		},
		PolicyEvaluations: []ReleasePolicyEvaluation{
			{Field: ReleasePolicyAllowLocalReplace, Outcome: ReleasePolicyPassed, Summary: "No local replacements participate in the release."},
			{Field: ReleasePolicyRequireProviderConformance, Outcome: ReleasePolicyPassed, Summary: "Every selected Provider passed required conformance."},
			{Field: ReleasePolicyRequireCleanVulnerabilityScan, Outcome: ReleasePolicyPassed, Summary: "Reachable vulnerability analysis passed."},
			{Field: ReleasePolicyRequireCompatibilityBaseline, Outcome: ReleasePolicyPassed, Summary: "Compatibility evidence satisfies the selected baseline."},
		},
		EvidenceFiles: []ReleaseEvidenceFileInput{
			{Kind: ReleaseEvidenceResolution, Status: ReleaseEvidenceComplete, Digest: inspectDigest("1")},
			{Kind: ReleaseEvidenceCompatibility, Status: ReleaseEvidenceComplete, Digest: inspectDigest("2")},
			{Kind: ReleaseEvidenceConformance, Status: ReleaseEvidenceComplete, Digest: inspectDigest("3")},
			{Kind: ReleaseEvidenceVulnerabilities, Status: ReleaseEvidenceComplete, Digest: inspectDigest("4")},
			{Kind: ReleaseEvidenceSBOM, Status: ReleaseEvidenceComplete, Digest: inspectDigest("5")},
			{Kind: ReleaseEvidenceProvenance, Status: ReleaseEvidenceComplete, Digest: inspectDigest("6")},
		},
		Artifacts:   []ReleaseArtifact{{Kind: "binary", Path: "dist/example-app.exe", Digest: inspectDigest("a")}},
		Diagnostics: []diagnosticjson.Diagnostic{{Code: "PLYSTRA-RELEASE-COMPLETE", Severity: diagnosticjson.SeverityInfo, Message: "Release evidence is complete."}},
	}
}

func cloneReleaseInput(input ReleaseInput) ReleaseInput {
	result := input
	result.PolicyEvaluations = cloneReleasePolicyEvaluations(input.PolicyEvaluations)
	result.EvidenceFiles = make([]ReleaseEvidenceFileInput, len(input.EvidenceFiles))
	for index, value := range input.EvidenceFiles {
		result.EvidenceFiles[index] = value
		result.EvidenceFiles[index].Sources = append([]diagnosticjson.Source(nil), value.Sources...)
	}
	result.Artifacts = cloneReleaseArtifacts(input.Artifacts)
	result.Diagnostics = append([]diagnosticjson.Diagnostic(nil), input.Diagnostics...)
	result.Sources = append([]diagnosticjson.Source(nil), input.Sources...)
	return result
}
