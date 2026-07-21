package resolutionevidence_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestBuildConstructsDeterministicNormalizedModelEvidence(t *testing.T) {
	t.Parallel()

	firstContext := selectedContext(t, false, "a", true)
	secondContext := selectedContext(t, true, "a", true)
	first, err := resolutionevidence.Build(firstContext)
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(secondContext)
	if err != nil {
		t.Fatalf("Build(second): %v", err)
	}
	if !first.Valid() || !second.Valid() {
		t.Fatal("Build returned invalid evidence")
	}
	if first.SchemaVersion() != 1 || first.GenerationAPIVersion() != generation.Version {
		t.Fatalf("evidence versions = schema %d generation %q", first.SchemaVersion(), first.GenerationAPIVersion())
	}
	if first.SelectedModelDigest() != firstContext.Digest() || first.BuildModelDigest() != firstContext.BuildModelDigest() {
		t.Fatalf("evidence model digests = selected %q build %q", first.SelectedModelDigest(), first.BuildModelDigest())
	}
	if first.SelectedPluginCount() != 1 || first.CanonicalCapabilityCount() != 2 || first.RequirementCount() != 2 || first.SelectedProviderCount() != 1 || first.CapabilityAliasCount() != 1 {
		t.Fatalf("evidence counts = plugins %d capabilities %d requirements %d providers %d aliases %d", first.SelectedPluginCount(), first.CanonicalCapabilityCount(), first.RequirementCount(), first.SelectedProviderCount(), first.CapabilityAliasCount())
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("input permutation changed evidence:\nfirst:  %s %s\nsecond: %s %s", first.CanonicalJSON(), first.Digest(), second.CanonicalJSON(), second.Digest())
	}
	want := fmt.Sprintf(`{"version":1,"generation_api":"v1","selected_model_digest":%q,"build_model_digest":%q,"counts":{"selected_plugins":1,"canonical_capabilities":2,"requirements":2,"selected_providers":1,"capability_aliases":1}}`, firstContext.Digest(), firstContext.BuildModelDigest())
	if string(first.CanonicalJSON()) != want {
		t.Fatalf("CanonicalJSON = %s\nwant = %s", first.CanonicalJSON(), want)
	}
	for _, forbidden := range []string{"example.smtp", "email.send/v1", "capability.yaml", "safe_name"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("bounded evidence contains detailed model value %q: %s", forbidden, first.CanonicalJSON())
		}
	}

	mutated := first.CanonicalJSON()
	mutated[0] = '['
	if first.CanonicalJSON()[0] != '{' || !first.Valid() {
		t.Fatal("CanonicalJSON exposed mutable evidence storage")
	}
}

func TestBuildSeparatesSelectionProvenanceFromBuildState(t *testing.T) {
	t.Parallel()

	baseContext := selectedContext(t, false, "a", true)
	selectionContext := selectedContext(t, false, "c", true)
	buildContext := selectedContext(t, false, "a", false)
	base, err := resolutionevidence.Build(baseContext)
	if err != nil {
		t.Fatalf("Build(base): %v", err)
	}
	selection, err := resolutionevidence.Build(selectionContext)
	if err != nil {
		t.Fatalf("Build(selection): %v", err)
	}
	build, err := resolutionevidence.Build(buildContext)
	if err != nil {
		t.Fatalf("Build(build): %v", err)
	}
	if base.SelectedModelDigest() == selection.SelectedModelDigest() || base.Digest() == selection.Digest() {
		t.Fatal("selected-configuration provenance did not alter evidence identity")
	}
	if base.BuildModelDigest() != selection.BuildModelDigest() {
		t.Fatal("selected-configuration provenance altered build-model identity")
	}
	if base.BuildModelDigest() == build.BuildModelDigest() || base.Digest() == build.Digest() {
		t.Fatal("build-visible exposure change did not alter evidence identity")
	}
}

func TestBuildRejectsAnAbsentNormalizedModel(t *testing.T) {
	t.Parallel()

	evidence, err := resolutionevidence.Build(generation.Context{})
	if !errors.Is(err, resolutionevidence.ErrBuild) || evidence.Valid() {
		t.Fatalf("Build(zero context) = %#v, %v", evidence, err)
	}
}

func selectedContext(t testing.TB, reverse bool, selectedDigestCharacter string, exposed bool) generation.Context {
	t.Helper()
	health := normalizeContract(t, `id: kernel.health/v1
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
`)
	email := normalizeContract(t, `id: email.send/v1
request:
  idempotency_key: {type: string, required: true}
  partition: {type: integer, required: true}
response: {}
semantics:
  kind: command
  effects: external-write
  idempotency: {mode: keyed, request_field: idempotency_key}
  retry: {safety: requires-idempotency-key}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: per-key, request_field: partition}
  data: {request: confidential, response: restricted}
`)
	exposure := generation.Exposure{Go: true, HTTP: exposed, JavaScript: exposed}
	capabilities := []generation.CapabilityInput{
		{ContractJSON: email, Sources: []string{"plugins/email/capabilities/email.send/v1/capability.yaml"}, Exposure: exposure},
		{ContractJSON: health, Sources: []string{"kernel:kernel.health/v1"}, Intrinsic: true, Exposure: generation.Exposure{Go: true}},
	}
	requirements := []string{"email.send/v1", "kernel.health/v1"}
	if reverse {
		capabilities[0], capabilities[1] = capabilities[1], capabilities[0]
		requirements[0], requirements[1] = requirements[1], requirements[0]
	}
	selectedDigest := "sha256:" + strings.Repeat(selectedDigestCharacter, 64)
	context, err := generation.NewContext(generation.Input{
		ConfigurationProvenance: &generation.ConfigurationProvenanceInput{
			Mode:                        generation.ConfigurationModeDefault,
			RootPath:                    "plystra.yaml",
			RootDigest:                  selectedDigest,
			SelectedPath:                "plystra.yaml",
			SelectedDigest:              selectedDigest,
			DependencyCompositionDigest: "sha256:" + strings.Repeat("b", 64),
		},
		Plugins: []generation.PluginInput{{
			ID:                "example.smtp",
			ModulePath:        "example.com/smtp",
			Provides:          []string{"email.send/v1"},
			Requires:          []string{"kernel.health/v1"},
			BuildMetadataJSON: []byte(`{"safe_name":"smtp"}`),
		}},
		Capabilities: capabilities,
		Requirements: requirements,
		Providers:    []generation.ProviderInput{{Capability: "email.send/v1", Plugin: "example.smtp"}},
		CapabilityAliases: []generation.CapabilityAliasInput{{
			ID:       "mail.send/v1",
			Target:   "email.send/v1",
			Exposure: generation.Exposure{HTTP: exposed, JavaScript: exposed},
			Sources:  []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}},
		}},
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	return context
}

func normalizeContract(t testing.TB, source string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}
