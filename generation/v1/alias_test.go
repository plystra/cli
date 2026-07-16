package generation_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
)

func TestNormalizeOutputBuildsDeterministicAliasContributions(t *testing.T) {
	t.Parallel()

	input := validInput()
	input.Capabilities = append(input.Capabilities, generation.CapabilityInput{ContractJSON: canonicalContract("profile.get/v1", nil), Exposure: generation.Exposure{Go: true}})
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	order := mustCapabilityID(t, "order.create/v1")
	health := mustCapabilityID(t, "kernel.health/v1")
	narrow := generation.Exposure{Go: true}
	fullHealth := generation.Exposure{Go: true, HTTP: true}
	output := generation.Output{AliasContributions: []generation.CapabilityAliasContribution{
		{
			ID:        "authn.health-compatibility",
			Namespace: "authn",
			Source:    order,
			Alias:     mustCapabilityID(t, "health.status/v1"),
			Target:    health,
			Exposure:  &fullHealth,
		},
		{
			ID:         "authz.order-shortcut",
			Namespace:  "authz",
			Source:     order,
			Alias:      mustCapabilityID(t, "orders.start/v1"),
			Target:     order,
			Exposure:   &narrow,
			Deprecated: "Use order.create/v1 instead.",
		},
	}}
	normalized, err := generation.NormalizeOutput(context, output)
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	aliases := normalized.AliasContributions()
	if len(aliases) != 2 || aliases[0].ID() != "authn.health-compatibility" || aliases[1].ID() != "authz.order-shortcut" {
		t.Fatalf("AliasContributions = %#v", aliases)
	}
	if aliases[0].Alias().String() != "health.status/v1" || aliases[0].Target() != health || aliases[0].Namespace() != "authn" || aliases[0].Source() != order || aliases[0].Deprecated() != "" {
		t.Fatalf("inherited Alias contribution = %#v", aliases[0])
	}
	if exposure, explicit := aliases[0].Exposure(); explicit || exposure != (generation.Exposure{}) {
		t.Fatalf("equivalent full exposure did not normalize to inheritance: %#v, %v", exposure, explicit)
	}
	exposure, explicit := aliases[1].Exposure()
	if !explicit || exposure != narrow || aliases[1].Deprecated() != "Use order.create/v1 instead." {
		t.Fatalf("narrowed Alias contribution = %#v, %v", aliases[1], explicit)
	}
	canonical := normalized.CanonicalJSON()
	for _, want := range []string{`"alias_contributions":[`, `"alias":"health.status/v1"`, `"alias":"orders.start/v1"`, `"exposure":{"go":true,"http":false,"javascript":false}`, `"deprecated":"Use order.create/v1 instead."`} {
		if !bytes.Contains(canonical, []byte(want)) {
			t.Fatalf("canonical output %s omits %s", canonical, want)
		}
	}
	if bytes.Count(canonical, []byte(`"exposure"`)) != 1 {
		t.Fatalf("inherited exposure was not omitted: %s", canonical)
	}

	fullHealth.Go = false
	narrow.HTTP = true
	output.AliasContributions[0].Deprecated = "changed"
	aliases[0] = generation.NormalizedCapabilityAliasContribution{}
	fresh := normalized.AliasContributions()
	freshExposure, freshExplicit := fresh[1].Exposure()
	if fresh[0].Deprecated() != "" || !freshExplicit || freshExposure != (generation.Exposure{Go: true}) {
		t.Fatal("NormalizedOutput exposed mutable Alias contribution storage")
	}

	var roundTrip generation.Output
	if err := json.Unmarshal(canonical, &roundTrip); err != nil {
		t.Fatalf("Unmarshal canonical output: %v", err)
	}
	second, err := generation.NormalizeOutput(context, roundTrip)
	if err != nil || !bytes.Equal(second.CanonicalJSON(), canonical) || second.Digest() != normalized.Digest() {
		t.Fatalf("Alias contribution round trip = %s, %q, %v", second.CanonicalJSON(), second.Digest(), err)
	}

	equivalent := cloneAliasOutput(t, roundTrip)
	slices.Reverse(equivalent.AliasContributions)
	second, err = generation.NormalizeOutput(context, equivalent)
	if err != nil || !bytes.Equal(second.CanonicalJSON(), canonical) || second.Digest() != normalized.Digest() {
		t.Fatalf("reordered Alias contributions changed output = %s, %q, %v", second.CanonicalJSON(), second.Digest(), err)
	}
	changed := cloneAliasOutput(t, roundTrip)
	changed.AliasContributions[0].Deprecated = "Compatibility only."
	third, err := generation.NormalizeOutput(context, changed)
	if err != nil || bytes.Equal(third.CanonicalJSON(), canonical) || third.Digest() == normalized.Digest() {
		t.Fatalf("Alias metadata did not affect canonical output = %s, %q, %v", third.CanonicalJSON(), third.Digest(), err)
	}
}

func TestNormalizeOutputLeavesAliasConflictsForFinalCLIMerge(t *testing.T) {
	t.Parallel()

	context, err := generation.NewContext(validInput())
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	order := mustCapabilityID(t, "order.create/v1")
	alias := mustCapabilityID(t, "orders.submit/v1")
	output := generation.Output{AliasContributions: []generation.CapabilityAliasContribution{
		{ID: "authn.application-conflict", Namespace: "authn", Source: order, Alias: alias, Target: mustCapabilityID(t, "kernel.health/v1")},
		{ID: "authn.generated-choice", Namespace: "authn", Source: order, Alias: alias, Target: order},
	}}
	normalized, err := generation.NormalizeOutput(context, output)
	if err != nil {
		t.Fatalf("NormalizeOutput should preserve valid conflict candidates for final merge: %v", err)
	}
	if len(normalized.AliasContributions()) != 2 {
		t.Fatalf("AliasContributions = %#v", normalized.AliasContributions())
	}
}

func TestNormalizeOutputRejectsInvalidAliasContributions(t *testing.T) {
	t.Parallel()

	input := validInput()
	input.Capabilities = append(input.Capabilities, generation.CapabilityInput{ContractJSON: canonicalContract("profile.get/v1", nil), Exposure: generation.Exposure{Go: true}})
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	order := mustCapabilityID(t, "order.create/v1")
	valid := generation.Output{AliasContributions: []generation.CapabilityAliasContribution{{
		ID:        "authn.order-shortcut",
		Namespace: "authn",
		Source:    order,
		Alias:     mustCapabilityID(t, "orders.start/v1"),
		Target:    order,
	}}}

	tests := []struct {
		name string
		edit func(*generation.Output)
		want string
	}{
		{name: "invalid ID", edit: func(output *generation.Output) { output.AliasContributions[0].ID = "Invalid_ID" }, want: ".id"},
		{name: "duplicate ID", edit: func(output *generation.Output) {
			output.AliasContributions = append(output.AliasContributions, output.AliasContributions[0])
		}, want: "duplicates Alias contribution"},
		{name: "invalid namespace", edit: func(output *generation.Output) { output.AliasContributions[0].Namespace = "audit" }, want: "has no extensions.audit"},
		{name: "zero source", edit: func(output *generation.Output) { output.AliasContributions[0].Source = generation.CapabilityID{} }, want: ".source"},
		{name: "zero Alias", edit: func(output *generation.Output) { output.AliasContributions[0].Alias = generation.CapabilityID{} }, want: ".alias"},
		{name: "canonical collision", edit: func(output *generation.Output) { output.AliasContributions[0].Alias = order }, want: "collides with a canonical Capability"},
		{name: "reserved Alias", edit: func(output *generation.Output) {
			output.AliasContributions[0].Alias = mustCapabilityID(t, "kernel.compat/v1")
		}, want: "reserved kernel.*"},
		{name: "zero target", edit: func(output *generation.Output) { output.AliasContributions[0].Target = generation.CapabilityID{} }, want: ".target"},
		{name: "Alias target", edit: func(output *generation.Output) {
			output.AliasContributions[0].Target = mustCapabilityID(t, "orders.submit/v1")
		}, want: "not a visible canonical Capability"},
		{name: "missing target", edit: func(output *generation.Output) {
			output.AliasContributions[0].Target = mustCapabilityID(t, "missing.target/v1")
		}, want: "not a visible canonical Capability"},
		{name: "unrequired target", edit: func(output *generation.Output) {
			output.AliasContributions[0].Target = mustCapabilityID(t, "profile.get/v1")
		}, want: "not a current canonical requirement"},
		{name: "version mismatch", edit: func(output *generation.Output) {
			output.AliasContributions[0].Alias = mustCapabilityID(t, "orders.start/v2")
		}, want: "same major version"},
		{name: "exposure broadening", edit: func(output *generation.Output) {
			exposure := generation.Exposure{HTTP: true}
			output.AliasContributions[0].Target = mustCapabilityID(t, "audit.write/v1")
			output.AliasContributions[0].Exposure = &exposure
		}, want: "exposure broadens"},
		{name: "oversized deprecation", edit: func(output *generation.Output) { output.AliasContributions[0].Deprecated = strings.Repeat("x", 1025) }, want: ".deprecated"},
		{name: "NUL deprecation", edit: func(output *generation.Output) { output.AliasContributions[0].Deprecated = "bad\x00message" }, want: ".deprecated"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output := cloneAliasOutput(t, valid)
			test.edit(&output)
			normalized, err := generation.NormalizeOutput(context, output)
			if !errors.Is(err, generation.ErrInvalidOutput) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NormalizeOutput error = %v, want ErrInvalidOutput containing %q", err, test.want)
			}
			if len(normalized.CanonicalJSON()) != 0 || normalized.Digest() != "" || len(normalized.AliasContributions()) != 0 {
				t.Fatalf("invalid output returned %#v", normalized)
			}
		})
	}
}

func cloneAliasOutput(t *testing.T, input generation.Output) generation.Output {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal output: %v", err)
	}
	var result generation.Output
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("Unmarshal output: %v", err)
	}
	return result
}

func FuzzNormalizeAliasContributionsJSON(f *testing.F) {
	context, err := generation.NewContext(validInput())
	if err != nil {
		f.Fatalf("NewContext: %v", err)
	}
	order, _ := generation.ParseCapabilityID("order.create/v1")
	alias, _ := generation.ParseCapabilityID("orders.start/v1")
	valid, err := json.Marshal(generation.Output{AliasContributions: []generation.CapabilityAliasContribution{{
		ID: "authn.order-shortcut", Namespace: "authn", Source: order, Alias: alias, Target: order,
	}}})
	if err != nil {
		f.Fatalf("Marshal seed: %v", err)
	}
	for _, seed := range []string{
		string(valid),
		`{"alias_contributions":[]}`,
		`{"alias_contributions":[{"id":"authn.shortcut","namespace":"authn","source":"order.create/v1","alias":"orders.start/v1","target":"order.create/v1","exposure":{"go":false,"http":false,"javascript":false}}]}`,
		`{"alias_contributions":[{"id":"authn.shortcut","namespace":"authn","source":"order.create/v1","alias":"orders.start/v2","target":"order.create/v1"}]}`,
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
		first, firstErr := generation.NormalizeOutput(context, output)
		second, secondErr := generation.NormalizeOutput(context, output)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("NormalizeOutput changed result: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, generation.ErrInvalidOutput) || !errors.Is(secondErr, generation.ErrInvalidOutput) {
				t.Fatalf("NormalizeOutput errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
			t.Fatal("Alias contribution output is nondeterministic")
		}
	})
}
