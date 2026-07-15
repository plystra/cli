package generation_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
)

func TestNormalizeOutputBuildsDeterministicImmutableProtocolData(t *testing.T) {
	t.Parallel()

	context := outputContext(t)
	order := mustCapabilityID(t, "order.create/v1")
	audit := mustCapabilityID(t, "audit.write/v1")
	health := mustCapabilityID(t, "kernel.health/v1")
	output := generation.Output{
		Requirements: []generation.Requirement{
			{RuleID: "authz.require-health", Namespace: "authz", Source: order, Capability: health},
			{RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit},
		},
		Diagnostics: []generation.Diagnostic{
			{Code: "authz.optional-space", Severity: generation.DiagnosticWarning, Message: "Space binding is optional.", Namespace: "authz", Source: order, RuleID: "authz.require-health"},
			{Code: "authn.verified", Severity: generation.DiagnosticInfo, Message: "Verified state will be reused.", Namespace: "authn", Source: order, RuleID: "authn.require-audit"},
		},
	}
	normalized, err := generation.NormalizeOutput(context, output)
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	if got := requirementStrings(normalized.Requirements()); !slices.Equal(got, []string{
		"authn|order.create/v1|authn.require-audit|audit.write/v1",
		"authz|order.create/v1|authz.require-health|kernel.health/v1",
	}) {
		t.Fatalf("Requirements = %v", got)
	}
	if got := diagnosticStrings(normalized.Diagnostics()); !slices.Equal(got, []string{
		"authn.verified|info|authn|order.create/v1|authn.require-audit|Verified state will be reused.",
		"authz.optional-space|warning|authz|order.create/v1|authz.require-health|Space binding is optional.",
	}) {
		t.Fatalf("Diagnostics = %v", got)
	}
	if !json.Valid(normalized.CanonicalJSON()) || !digestPattern.MatchString(normalized.Digest()) {
		t.Fatalf("CanonicalJSON = %s, Digest = %q", normalized.CanonicalJSON(), normalized.Digest())
	}

	requirements := normalized.Requirements()
	requirements[0].RuleID = "changed"
	diagnostics := normalized.Diagnostics()
	diagnostics[0].Message = "changed"
	canonical := normalized.CanonicalJSON()
	canonical[0] = '['
	if normalized.Requirements()[0].RuleID != "authn.require-audit" || normalized.Diagnostics()[0].Message != "Verified state will be reused." || normalized.CanonicalJSON()[0] != '{' {
		t.Fatal("NormalizedOutput exposed mutable storage")
	}

	output.Requirements[0].RuleID = "changed"
	output.Diagnostics[0].Message = "changed"
	if normalized.Requirements()[1].RuleID != "authz.require-health" || normalized.Diagnostics()[1].Message != "Space binding is optional." {
		t.Fatal("NormalizeOutput retained mutable input storage")
	}

	equivalent := generation.Output{
		Requirements: []generation.Requirement{
			{RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit},
			{RuleID: "authz.require-health", Namespace: "authz", Source: order, Capability: health},
		},
		Diagnostics: []generation.Diagnostic{
			{Code: "authn.verified", Severity: generation.DiagnosticInfo, Message: "Verified state will be reused.", Namespace: "authn", Source: order, RuleID: "authn.require-audit"},
			{Code: "authz.optional-space", Severity: generation.DiagnosticWarning, Message: "Space binding is optional.", Namespace: "authz", Source: order, RuleID: "authz.require-health"},
		},
	}
	second, err := generation.NormalizeOutput(context, equivalent)
	if err != nil || !bytes.Equal(normalized.CanonicalJSON(), second.CanonicalJSON()) || normalized.Digest() != second.Digest() {
		t.Fatalf("equivalent NormalizeOutput = %s, %q, %v", second.CanonicalJSON(), second.Digest(), err)
	}

	payload, err := json.Marshal(equivalent)
	if err != nil {
		t.Fatalf("Marshal(Output): %v", err)
	}
	var decoded generation.Output
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal(Output): %v", err)
	}
	roundTrip, err := generation.NormalizeOutput(context, decoded)
	if err != nil || !bytes.Equal(roundTrip.CanonicalJSON(), normalized.CanonicalJSON()) {
		t.Fatalf("round-trip NormalizeOutput = %s, %v", roundTrip.CanonicalJSON(), err)
	}

	var generate generation.GenerateFunc = func(received generation.GenerationContext) (generation.Output, error) {
		if received.Digest() != context.Digest() {
			t.Fatal("GenerateFunc received a different Context")
		}
		return equivalent, nil
	}
	generated, err := generate(context)
	if err != nil || len(generated.Requirements) != 2 {
		t.Fatalf("GenerateFunc = %#v, %v", generated, err)
	}
}

func TestNormalizeOutputSupportsNoContributions(t *testing.T) {
	t.Parallel()

	normalized, err := generation.NormalizeOutput(outputContext(t), generation.Output{})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	want := `{"requirements":[],"diagnostics":[]}`
	if string(normalized.CanonicalJSON()) != want || len(normalized.Requirements()) != 0 || len(normalized.Diagnostics()) != 0 || !digestPattern.MatchString(normalized.Digest()) {
		t.Fatalf("empty NormalizedOutput = %s, %q", normalized.CanonicalJSON(), normalized.Digest())
	}
}

func TestNormalizeOutputRejectsMalformedOrInconsistentData(t *testing.T) {
	t.Parallel()

	order := mustCapabilityID(t, "order.create/v1")
	audit := mustCapabilityID(t, "audit.write/v1")
	profile := mustCapabilityID(t, "profile.get/v1")
	alias := mustCapabilityID(t, "orders.submit/v1")
	unknown := mustCapabilityID(t, "missing.operation/v1")
	validRequirement := generation.Requirement{RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit}
	validDiagnostic := generation.Diagnostic{Code: "authn.verified", Severity: generation.DiagnosticInfo, Message: "Verified state will be reused.", Namespace: "authn", Source: order, RuleID: "authn.require-audit"}
	tests := map[string]generation.Output{
		"invalid rule ID":             {Requirements: []generation.Requirement{{RuleID: "AuthN.Rule", Namespace: "authn", Source: order, Capability: audit}}},
		"invalid namespace":           {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "AuthN", Source: order, Capability: audit}}},
		"zero source":                 {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Capability: audit}}},
		"unknown source":              {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Source: unknown, Capability: audit}}},
		"source not required":         {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Source: profile, Capability: audit}}},
		"source missing metadata":     {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Source: audit, Capability: audit}}},
		"zero requirement":            {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Source: order}}},
		"unknown requirement":         {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Source: order, Capability: unknown}}},
		"alias requirement":           {Requirements: []generation.Requirement{{RuleID: "authn.rule", Namespace: "authn", Source: order, Capability: alias}}},
		"duplicate requirement":       {Requirements: []generation.Requirement{validRequirement, validRequirement}},
		"invalid diagnostic code":     {Diagnostics: []generation.Diagnostic{{Code: "AUTHN", Severity: generation.DiagnosticInfo, Message: "message", Namespace: "authn", Source: order, RuleID: "authn.rule"}}},
		"invalid diagnostic rule":     {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: generation.DiagnosticInfo, Message: "message", Namespace: "authn", Source: order, RuleID: "AuthN.Rule"}}},
		"invalid diagnostic severity": {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: "fatal", Message: "message", Namespace: "authn", Source: order, RuleID: "authn.rule"}}},
		"empty diagnostic message":    {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: generation.DiagnosticInfo, Namespace: "authn", Source: order, RuleID: "authn.rule"}}},
		"long diagnostic message":     {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: generation.DiagnosticInfo, Message: strings.Repeat("x", 4097), Namespace: "authn", Source: order, RuleID: "authn.rule"}}},
		"nul diagnostic message":      {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: generation.DiagnosticInfo, Message: "unsafe\x00message", Namespace: "authn", Source: order, RuleID: "authn.rule"}}},
		"invalid diagnostic UTF-8":    {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: generation.DiagnosticInfo, Message: string([]byte{0xff}), Namespace: "authn", Source: order, RuleID: "authn.rule"}}},
		"diagnostic missing metadata": {Diagnostics: []generation.Diagnostic{{Code: "authn.code", Severity: generation.DiagnosticInfo, Message: "message", Namespace: "authn", Source: audit, RuleID: "authn.rule"}}},
		"duplicate diagnostic":        {Diagnostics: []generation.Diagnostic{validDiagnostic, validDiagnostic}},
	}
	for name, output := range tests {
		name, output := name, output
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			normalized, err := generation.NormalizeOutput(outputContext(t), output)
			if !errors.Is(err, generation.ErrInvalidOutput) {
				t.Fatalf("NormalizeOutput error = %v, want ErrInvalidOutput", err)
			}
			if len(normalized.CanonicalJSON()) != 0 || normalized.Digest() != "" {
				t.Fatalf("invalid NormalizeOutput returned %s, %q", normalized.CanonicalJSON(), normalized.Digest())
			}
		})
	}

	diagnostics := make([]generation.Diagnostic, 300)
	for index := range diagnostics {
		diagnostics[index] = generation.Diagnostic{
			Code:      fmt.Sprintf("authn.code-%d", index),
			Severity:  generation.DiagnosticInfo,
			Message:   strings.Repeat("x", 4096),
			Namespace: "authn",
			Source:    order,
			RuleID:    "authn.rule",
		}
	}
	if _, err := generation.NormalizeOutput(outputContext(t), generation.Output{Diagnostics: diagnostics}); !errors.Is(err, generation.ErrInvalidOutput) {
		t.Fatalf("oversized NormalizeOutput error = %v, want ErrInvalidOutput", err)
	}
}

func FuzzNormalizeOutputJSON(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"requirements":[],"diagnostics":[]}`,
		`{"requirements":[{"rule_id":"authn.require-audit","namespace":"authn","source":"order.create/v1","capability":"audit.write/v1"}],"diagnostics":[]}`,
		`{"requirements":[],"diagnostics":[{"code":"authn.verified","severity":"info","message":"Verified state will be reused.","namespace":"authn","source":"order.create/v1","rule_id":"authn.require-audit"}]}`,
		`{"requirements":[{}]}`,
		`[]`,
		`{`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > 1<<20 {
			return
		}
		var output generation.Output
		if err := json.Unmarshal([]byte(payload), &output); err != nil {
			return
		}
		context := outputContext(t)
		first, firstErr := generation.NormalizeOutput(context, output)
		second, secondErr := generation.NormalizeOutput(context, output)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("NormalizeOutput result changed: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, generation.ErrInvalidOutput) || !errors.Is(secondErr, generation.ErrInvalidOutput) {
				t.Fatalf("NormalizeOutput errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
			t.Fatal("successful NormalizeOutput output is nondeterministic")
		}
	})
}

func outputContext(t *testing.T) generation.Context {
	t.Helper()
	input := validInput()
	input.Capabilities = append(input.Capabilities, generation.CapabilityInput{
		ContractJSON: canonicalContract("profile.get/v1", nil),
		Exposure:     generation.Exposure{Go: true},
	})
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context
}

func requirementStrings(values []generation.Requirement) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Namespace + "|" + value.Source.String() + "|" + value.RuleID + "|" + value.Capability.String()
	}
	return result
}

func diagnosticStrings(values []generation.Diagnostic) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Code + "|" + string(value.Severity) + "|" + value.Namespace + "|" + value.Source.String() + "|" + value.RuleID + "|" + value.Message
	}
	return result
}
