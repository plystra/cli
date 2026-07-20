package applicationgen_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/protobufwiremap"
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

func TestEnvironmentOverlayDigestAcceptsOnlySparseOverlayValidation(t *testing.T) {
	t.Parallel()

	data := []byte("http: {cors: {allow_credentials: false}}\n")
	if _, err := applicationgen.EnvironmentOverlayDigest(data); err != nil {
		t.Fatalf("EnvironmentOverlayDigest: %v", err)
	}
	if _, err := applicationgen.ConfigurationDigest(data); err == nil || !strings.Contains(err.Error(), "allowed_origins is required") {
		t.Fatalf("ConfigurationDigest(sparse overlay) error = %v", err)
	}
}

func TestManifestProvenanceRetainsStrictPerSelectionBaselines(t *testing.T) {
	t.Parallel()

	defaultResolution := emptyApplication(t)
	defaultOptions := emptyOptions(applicationModulePath)
	defaultModelOptions := applicationgen.ApplicationModelOptions{
		ModulePath:          defaultOptions.ModulePath,
		KernelModuleVersion: defaultOptions.KernelModuleVersion,
		KernelBuildIdentity: defaultOptions.KernelBuildIdentity,
		Resolution:          defaultResolution,
	}
	projection, err := applicationgen.ProtobufProjection(defaultModelOptions.HTTPTransports, defaultModelOptions.Resolution)
	if err != nil {
		t.Fatalf("ProtobufProjection(default): %v", err)
	}
	wireMap, err := protobufwiremap.Build(projection, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build(default): %v", err)
	}
	defaultModelOptions.ProtobufWireMap = wireMap
	defaultModel, err := applicationgen.ApplicationModelDigest(defaultModelOptions)
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
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: defaultModel,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(default): %v", err)
	}
	environmentProvenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeEnvironment,
		Environment:            "production",
		RootPath:               "plystra.yaml",
		RootData:               []byte("config: {acme.business: {legacy: 'C:/private/root-config', password: {env: ROOT_PRIVATE_TOKEN}}}\n"),
		SelectedPath:           "plystra.production.yaml",
		SelectedData:           []byte("config: {acme.business: {password: {env: PRODUCTION_PRIVATE_TOKEN}}}\n"),
		Composition:            dependencyComposition(t),
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: defaultModel,
		Previous:               defaultProvenance,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(environment): %v", err)
	}
	environmentData, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), environmentProvenance)
	if err != nil {
		t.Fatalf("RenderManifest(environment): %v", err)
	}
	if !bytes.Contains(environmentData, []byte(`"mode":"environment","environment":"production"`)) ||
		!bytes.Contains(environmentData, []byte(`"overlay":{"path":"plystra.production.yaml","digest":"sha256:`)) ||
		bytes.Contains(environmentData, []byte(`"selected":`)) {
		t.Fatalf("environment provenance = %s", environmentData)
	}
	decodedEnvironment, err := applicationgen.DecodeManifestProvenance(environmentData)
	if err != nil || decodedEnvironment.Environment() != "production" || decodedEnvironment.SelectedPath() != "plystra.production.yaml" {
		t.Fatalf("DecodeManifestProvenance(environment) = environment %q path %q, %v", decodedEnvironment.Environment(), decodedEnvironment.SelectedPath(), err)
	}
	environmentBaseline, environmentExists := decodedEnvironment.BaselineForSelection(applicationgen.ConfigurationModeEnvironment, "plystra.production.yaml")
	defaultEnvironmentBaseline, defaultEnvironmentExists := decodedEnvironment.BaselineForSelection(applicationgen.ConfigurationModeDefault, "plystra.yaml")
	if !environmentExists || !defaultEnvironmentExists || environmentBaseline.Digest() != defaultEnvironmentBaseline.Digest() {
		t.Fatalf("shared environment baseline = environment %q/%t default %q/%t", environmentBaseline.Digest(), environmentExists, defaultEnvironmentBaseline.Digest(), defaultEnvironmentExists)
	}
	explicitProvenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeExplicit,
		RootPath:               "plystra.yaml",
		RootData:               []byte("config: {acme.business: {legacy: 'C:/private/root-config', password: {env: ROOT_PRIVATE_TOKEN}}}\n"),
		SelectedPath:           "deploy/customer-a.yaml",
		SelectedData:           []byte("# independent complete configuration\nconfig: {acme.business: {legacy: customer-runtime-value, password: {env: CUSTOMER_PRIVATE_TOKEN}}}\n"),
		Composition:            testComposition(),
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: defaultModel,
		Previous:               environmentProvenance,
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
		"PRODUCTION_PRIVATE_TOKEN",
		"customer-runtime-value",
		"independent complete configuration",
		"resolved-super-secret",
	} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("generated manifest leaked %q: %s", forbidden, data)
		}
	}
	oldSchema := bytes.Replace(data, []byte(`"version":4`), []byte(`"version":3`), 1)
	if _, err := applicationgen.DecodeManifestProvenance(oldSchema); err == nil || !strings.Contains(err.Error(), "must use version 4") {
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
	withoutDigest, err := applicationModelDigest(t, base)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(without Aliases): %v", err)
	}
	base.Resolution = withAliases
	withDigest, err := applicationModelDigest(t, base)
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

func TestApplicationModelDigestIncludesHTTPTransportsDeterministically(t *testing.T) {
	t.Parallel()

	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, ""),
	}
	connectDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(Connect): %v", err)
	}
	repeatedDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(repeated Connect): %v", err)
	}
	if repeatedDigest != connectDigest {
		t.Fatalf("equal transport selections produced different digests: %q != %q", repeatedDigest, connectDigest)
	}

	options.HTTPTransports = applicationmeta.HTTPTransports{REST: true}
	restDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(REST): %v", err)
	}
	if restDigest == connectDigest {
		t.Fatal("Connect-only and REST-only transport selections produced the same application-model digest")
	}
}

func TestApplicationModelDigestIncludesNormalizedHTTPCORS(t *testing.T) {
	t.Parallel()

	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, ""),
	}
	absentDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(absent CORS): %v", err)
	}

	reordered := applicationmeta.HTTPCORS{
		AllowedOrigins: []string{
			"https://B.example:443",
			"https://a.example",
			"https://a.example:443",
		},
	}
	options.HTTPCORS = &reordered
	reorderedDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(reordered CORS): %v", err)
	}
	if !slices.Equal(reordered.AllowedOrigins, []string{"https://B.example:443", "https://a.example", "https://a.example:443"}) {
		t.Fatalf("ApplicationModelDigest mutated CORS input: %#v", reordered)
	}

	normalized := applicationmeta.HTTPCORS{AllowedOrigins: []string{"https://a.example", "https://b.example"}}
	options.HTTPCORS = &normalized
	normalizedDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(normalized CORS): %v", err)
	}
	if normalizedDigest != reorderedDigest {
		t.Fatalf("equivalent CORS policies produced different digests: %q != %q", normalizedDigest, reorderedDigest)
	}
	if normalizedDigest == absentDigest {
		t.Fatal("present and absent CORS policies produced the same application-model digest")
	}

	credentials := applicationmeta.HTTPCORS{
		AllowedOrigins:   []string{"https://a.example", "https://b.example"},
		AllowCredentials: true,
	}
	options.HTTPCORS = &credentials
	credentialsDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(credentialed CORS): %v", err)
	}
	if credentialsDigest == normalizedDigest {
		t.Fatal("allow_credentials change did not alter the application-model digest")
	}

	changedOrigin := applicationmeta.HTTPCORS{AllowedOrigins: []string{"https://api.example"}}
	options.HTTPCORS = &changedOrigin
	changedOriginDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(changed CORS origin): %v", err)
	}
	if changedOriginDigest == normalizedDigest {
		t.Fatal("allowed_origins change did not alter the application-model digest")
	}

	for _, test := range []struct {
		name string
		cors applicationmeta.HTTPCORS
		want string
	}{
		{name: "missing origins", cors: applicationmeta.HTTPCORS{}, want: "allowed_origins"},
		{name: "malformed origin", cors: applicationmeta.HTTPCORS{AllowedOrigins: []string{"https://example.com/path"}}, want: "scheme, host, and optional port"},
		{name: "credentialed wildcard", cors: applicationmeta.HTTPCORS{AllowedOrigins: []string{"*"}, AllowCredentials: true}, want: "wildcard origin"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			invalid := options
			invalid.HTTPCORS = &test.cors
			_, err := applicationModelDigest(t, invalid)
			if !errors.Is(err, applicationgen.ErrResolution) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ApplicationModelDigest error = %v, want ErrResolution containing %q", err, test.want)
			}
		})
	}
}

func TestApplicationModelDigestPinsNormalizedConnectProtobufProjection(t *testing.T) {
	t.Parallel()

	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, "capabilities:\n  aliases: {billing.tax-rate/v1: email.send/v1}\n"),
	}
	digest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(Connect Protobuf projection): %v", err)
	}
	const expected = "sha256:c8cac60ef0fb00f087f0382f9137b587f7f0aee8356f06caef35ce6cdfb1a567"
	if digest != expected {
		t.Fatalf("Connect Protobuf projection application-model digest = %q; want %q", digest, expected)
	}
}

func TestApplicationModelDigestIncludesActiveWireHistory(t *testing.T) {
	t.Parallel()

	currentResolution := resolvedApplication(t, "")
	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Providers:           selectedProviderInputs(),
		Resolution:          currentResolution,
	}
	currentProjection, err := applicationgen.ProtobufProjection(options.HTTPTransports, currentResolution)
	if err != nil {
		t.Fatalf("ProtobufProjection(current): %v", err)
	}
	cleanMap, err := protobufwiremap.Build(currentProjection, nil, false, "")
	if err != nil {
		t.Fatalf("Build(clean): %v", err)
	}
	options.ProtobufWireMap = cleanMap
	cleanDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(clean): %v", err)
	}

	historicalResolution := resolvedApplicationWithEmail(t, "", `id: email.send/v1
request:
  alpha: {type: string}
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`, defaultConfigurationProvenance(t, testComposition()))
	historicalProjection, err := applicationgen.ProtobufProjection(options.HTTPTransports, historicalResolution)
	if err != nil {
		t.Fatalf("ProtobufProjection(historical): %v", err)
	}
	history, err := protobufwiremap.Build(historicalProjection, nil, false, "")
	if err != nil {
		t.Fatalf("Build(history): %v", err)
	}
	reconciled, err := protobufwiremap.Build(currentProjection, history.CanonicalJSON(), true, history.Digest())
	if err != nil {
		t.Fatalf("Build(reconciled): %v", err)
	}
	if bytes.Equal(cleanMap.ActiveJSON(), reconciled.ActiveJSON()) || cleanMap.Digest() == reconciled.Digest() {
		t.Fatal("historical assignment did not alter active or committed wire-map evidence")
	}
	options.ProtobufWireMap = reconciled
	historicalDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(historical): %v", err)
	}
	if historicalDigest == cleanDigest {
		t.Fatal("different active Protobuf field assignments produced the same application-model digest")
	}
}

func TestApplicationModelDigestIncludesActiveEnumWireHistory(t *testing.T) {
	t.Parallel()

	currentResolution := resolvedApplicationWithEmail(t, "", `id: email.send/v1
request:
  to: {type: string, required: true}
  priority: {type: string, enum: [normal, urgent]}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`, defaultConfigurationProvenance(t, testComposition()))
	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Providers:           selectedProviderInputs(),
		Resolution:          currentResolution,
	}
	currentProjection, err := applicationgen.ProtobufProjection(options.HTTPTransports, currentResolution)
	if err != nil {
		t.Fatalf("ProtobufProjection(current): %v", err)
	}
	cleanMap, err := protobufwiremap.Build(currentProjection, nil, false, "")
	if err != nil {
		t.Fatalf("Build(clean): %v", err)
	}
	options.ProtobufWireMap = cleanMap
	cleanDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(clean): %v", err)
	}

	historicalResolution := resolvedApplicationWithEmail(t, "", `id: email.send/v1
request:
  to: {type: string, required: true}
  priority: {type: string, enum: [alpha, normal, urgent]}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`, defaultConfigurationProvenance(t, testComposition()))
	historicalProjection, err := applicationgen.ProtobufProjection(options.HTTPTransports, historicalResolution)
	if err != nil {
		t.Fatalf("ProtobufProjection(historical): %v", err)
	}
	history, err := protobufwiremap.Build(historicalProjection, nil, false, "")
	if err != nil {
		t.Fatalf("Build(history): %v", err)
	}
	reconciled, err := protobufwiremap.Build(currentProjection, history.CanonicalJSON(), true, history.Digest())
	if err != nil {
		t.Fatalf("Build(reconciled): %v", err)
	}
	if bytes.Equal(cleanMap.ActiveJSON(), reconciled.ActiveJSON()) || cleanMap.Digest() == reconciled.Digest() {
		t.Fatal("historical enum assignment did not alter active or committed wire-map evidence")
	}
	options.ProtobufWireMap = reconciled
	historicalDigest, err := applicationgen.ApplicationModelDigest(options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(historical): %v", err)
	}
	if historicalDigest == cleanDigest {
		t.Fatal("different active Protobuf enum assignments produced the same application-model digest")
	}
}

func TestApplicationModelDigestNormalizesConnectContractFieldInput(t *testing.T) {
	t.Parallel()

	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Providers:           selectedProviderInputs(),
	}
	options.Resolution = resolvedApplicationWithEmail(t, "", `id: email.send/v1
request:
  to: {type: string, required: true}
  priority: {type: integer}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`, defaultConfigurationProvenance(t, testComposition()))
	first, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(first contract): %v", err)
	}
	options.Resolution = resolvedApplicationWithEmail(t, "", `response:
  accepted: {required: true, type: boolean}
errors: [invalid_recipient]
request:
  priority: {type: integer}
  to: {required: true, type: string}
id: email.send/v1
`, defaultConfigurationProvenance(t, testComposition()))
	reordered, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(reordered contract): %v", err)
	}
	if reordered != first {
		t.Fatalf("contract declaration order changed digest: %q != %q", reordered, first)
	}
	options.Resolution = resolvedApplicationWithEmail(t, "", `id: email.send/v1
request:
  to: {type: string, required: true}
  priority: {type: number}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`, defaultConfigurationProvenance(t, testComposition()))
	changed, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(changed contract): %v", err)
	}
	if changed == first {
		t.Fatal("Connect contract field change did not alter the application-model digest")
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
	stringDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(string schema): %v", err)
	}
	options.Providers[0].ConfigurationSchema = reorderedSchema
	reorderedDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(reordered schema): %v", err)
	}
	if reorderedDigest != stringDigest {
		t.Fatalf("schema declaration order changed model digest: %q != %q", reorderedDigest, stringDigest)
	}
	options.Providers[0].ConfigurationSchema = integerSchema
	integerDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(integer schema): %v", err)
	}
	if integerDigest == stringDigest {
		t.Fatal("dependency Plugin configuration schema change did not alter the application-model digest")
	}
}
