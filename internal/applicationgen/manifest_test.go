package applicationgen_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/kernel/plugin/manifest"
)

func TestConfigurationDigestUsesSemanticYAML(t *testing.T) {
	t.Parallel()

	left := []byte(`# formatting and mapping order are not semantic
capabilities:
  use: {email.send/v1: acme.mailer}
  require:
    - email.send/v1
http:
  expose: []
config:
  acme.mailer:
    retries: 1
    enabled: true
`)
	right := []byte(`config:
  acme.mailer: {enabled: true, retries: 01}
http: {expose: []}
capabilities:
  require: [email.send/v1]
  use:
    email.send/v1: acme.mailer
`)
	leftDigest, err := applicationgen.ConfigurationDigest(left)
	if err != nil {
		t.Fatalf("ConfigurationDigest(left): %v", err)
	}
	rightDigest, err := applicationgen.ConfigurationDigest(right)
	if err != nil {
		t.Fatalf("ConfigurationDigest(right): %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("semantic digests differ: %q != %q", leftDigest, rightDigest)
	}
	changedSequence, err := applicationgen.ConfigurationDigest([]byte("capabilities:\n  require: [email.send/v1, audit.write/v1]\n"))
	if err != nil {
		t.Fatalf("ConfigurationDigest(changed sequence): %v", err)
	}
	reversedSequence, err := applicationgen.ConfigurationDigest([]byte("capabilities:\n  require: [audit.write/v1, email.send/v1]\n"))
	if err != nil {
		t.Fatalf("ConfigurationDigest(reversed sequence): %v", err)
	}
	if changedSequence == reversedSequence {
		t.Fatal("sequence order did not enter the semantic digest")
	}
	tombstone, err := applicationgen.ConfigurationDigest([]byte("capabilities:\n  use: {email.send/v1: null}\n"))
	if err != nil {
		t.Fatalf("ConfigurationDigest(tombstone): %v", err)
	}
	omitted, err := applicationgen.ConfigurationDigest([]byte("capabilities:\n  use: {}\n"))
	if err != nil {
		t.Fatalf("ConfigurationDigest(omitted): %v", err)
	}
	if tombstone == omitted {
		t.Fatal("explicit removal did not enter the semantic digest")
	}
}

func TestManifestProvenanceRetainsStrictPerSelectionBaselines(t *testing.T) {
	t.Parallel()

	defaultResolution := emptyApplication(t)
	defaultOptions := emptyOptions(applicationModulePath)
	defaultModel, err := applicationgen.ApplicationModelDigest(applicationgen.ApplicationModelOptions{
		ModulePath:          defaultOptions.ModulePath,
		KernelModuleVersion: defaultOptions.KernelModuleVersion,
		KernelBuildIdentity: defaultOptions.KernelBuildIdentity,
		Resolution:          defaultResolution,
	})
	if err != nil {
		t.Fatalf("ApplicationModelDigest(default): %v", err)
	}
	defaultProvenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               "plystra.yaml",
		RootData:               []byte("config: {acme.business: {legacy: 'C:/private/root-config', password: {env: ROOT_PRIVATE_TOKEN}}}\n"),
		SelectedPath:           "plystra.yaml",
		SelectedData:           []byte("config: {acme.business: {legacy: 'C:/private/root-config', password: {env: ROOT_PRIVATE_TOKEN}}}\n"),
		Composition:            dependencyComposition(t),
		ApplicationModelDigest: defaultModel,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(default): %v", err)
	}
	explicitProvenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeExplicit,
		RootPath:               "plystra.yaml",
		RootData:               []byte("config: {acme.business: {legacy: 'C:/private/root-config', password: {env: ROOT_PRIVATE_TOKEN}}}\n"),
		SelectedPath:           "deploy/customer-a.yaml",
		SelectedData:           []byte("# independent complete configuration\nconfig: {acme.business: {legacy: customer-runtime-value, password: {env: CUSTOMER_PRIVATE_TOKEN}}}\n"),
		Composition:            testComposition(),
		ApplicationModelDigest: defaultModel,
		Previous:               defaultProvenance,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(explicit): %v", err)
	}
	data, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), explicitProvenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	decoded, err := applicationgen.DecodeManifestProvenance(data)
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	defaultBaseline, defaultExists := decoded.BaselineForSelection(applicationgen.ConfigurationModeDefault, "plystra.yaml")
	explicitBaseline, explicitExists := decoded.BaselineForSelection(applicationgen.ConfigurationModeExplicit, "deploy/customer-a.yaml")
	if !defaultExists || !explicitExists || defaultBaseline.Digest() != defaultProvenance.DependencyBaseline().Digest() || explicitBaseline.Digest() != explicitProvenance.DependencyBaseline().Digest() {
		t.Fatalf("retained baselines = default %q/%t explicit %q/%t", defaultBaseline.Digest(), defaultExists, explicitBaseline.Digest(), explicitExists)
	}
	for _, forbidden := range []string{
		"C:/private/root-config",
		"ROOT_PRIVATE_TOKEN",
		"CUSTOMER_PRIVATE_TOKEN",
		"customer-runtime-value",
		"independent complete configuration",
		"resolved-super-secret",
	} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("generated manifest leaked %q: %s", forbidden, data)
		}
	}
	oldSchema := bytes.Replace(data, []byte(`"version":2`), []byte(`"version":1`), 1)
	if _, err := applicationgen.DecodeManifestProvenance(oldSchema); err == nil || !strings.Contains(err.Error(), "must use version 2") {
		t.Fatalf("DecodeManifestProvenance(old schema) error = %v", err)
	}
	unknown := bytes.Replace(data, []byte(`"mode":"explicit-config"`), []byte(`"unknown":true,"mode":"explicit-config"`), 1)
	if _, err := applicationgen.DecodeManifestProvenance(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeManifestProvenance(unknown field) error = %v", err)
	}
}

func TestApplicationModelDigestIncludesAliasesAndExcludesSelectionPath(t *testing.T) {
	t.Parallel()

	withoutAliases := resolvedApplication(t, "")
	withAliases := resolvedApplication(t, "capabilities:\n  aliases: {compat.send/v1: email.send/v1}\n")
	base := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		Providers:           selectedProviderInputs(),
	}
	base.Resolution = withoutAliases
	withoutDigest, err := applicationgen.ApplicationModelDigest(base)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(without Aliases): %v", err)
	}
	base.Resolution = withAliases
	withDigest, err := applicationgen.ApplicationModelDigest(base)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(with Aliases): %v", err)
	}
	if withoutDigest == withDigest {
		t.Fatal("Alias change did not alter the application-model digest")
	}
	if !strings.HasPrefix(withDigest, "sha256:") || len(withDigest) != 71 {
		t.Fatalf("application-model digest = %q", withDigest)
	}
}

func TestApplicationModelDigestIncludesDependencyConfigurationSchema(t *testing.T) {
	t.Parallel()

	stringSchema, err := manifest.ParseConfig([]byte("endpoint: {type: string}\nretries: {type: integer}\n"))
	if err != nil {
		t.Fatalf("ParseConfig(string schema): %v", err)
	}
	reorderedSchema, err := manifest.ParseConfig([]byte("retries: {type: integer}\nendpoint: {type: string}\n"))
	if err != nil {
		t.Fatalf("ParseConfig(reordered schema): %v", err)
	}
	integerSchema, err := manifest.ParseConfig([]byte("endpoint: {type: integer}\nretries: {type: integer}\n"))
	if err != nil {
		t.Fatalf("ParseConfig(integer schema): %v", err)
	}
	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, ""),
	}
	options.Providers[0].ConfigurationSchema = stringSchema
	stringDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(string schema): %v", err)
	}
	options.Providers[0].ConfigurationSchema = reorderedSchema
	reorderedDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(reordered schema): %v", err)
	}
	if reorderedDigest != stringDigest {
		t.Fatalf("schema declaration order changed model digest: %q != %q", reorderedDigest, stringDigest)
	}
	options.Providers[0].ConfigurationSchema = integerSchema
	integerDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(integer schema): %v", err)
	}
	if integerDigest == stringDigest {
		t.Fatal("dependency Plugin configuration schema change did not alter the application-model digest")
	}
}
