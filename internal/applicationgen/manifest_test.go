package applicationgen_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationadaptergen"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/interfaceproxygen"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/transporttoolchain"
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
  example.com/acme/mailer.New:
    retries: 1
    enabled: true
`)
	right := []byte(`config:
  example.com/acme/mailer.New: {enabled: true, retries: 01}
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
	wireMap, err := protobufwiremap.Build(projection, emptyInterfaceWireProjection(t, projection), nil, false, "")
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
		RootDigest:             "sha256:" + strings.Repeat("3", 64),
		SelectedPath:           "plystra.yaml",
		SelectedDigest:         "sha256:" + strings.Repeat("3", 64),
		Composition:            dependencyComposition(t),
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: defaultModel,
		InterfaceProvenance:    emptyInterfaceProvenance(t),
		TransportToolchain:     currentTransportToolchain(t),
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(default): %v", err)
	}
	environmentProvenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeEnvironment,
		Environment:            "production",
		RootPath:               "plystra.yaml",
		RootDigest:             "sha256:" + strings.Repeat("3", 64),
		SelectedPath:           "plystra.production.yaml",
		SelectedDigest:         "sha256:" + strings.Repeat("4", 64),
		Composition:            dependencyComposition(t),
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: defaultModel,
		InterfaceProvenance:    emptyInterfaceProvenance(t),
		TransportToolchain:     currentTransportToolchain(t),
		Previous:               defaultProvenance,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(environment): %v", err)
	}
	environmentData, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), defaultResolution.Context(), environmentProvenance)
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
		RootDigest:             "sha256:" + strings.Repeat("3", 64),
		SelectedPath:           "deploy/customer-a.yaml",
		SelectedDigest:         "sha256:" + strings.Repeat("5", 64),
		Composition:            testComposition(),
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: defaultModel,
		InterfaceProvenance:    emptyInterfaceProvenance(t),
		TransportToolchain:     currentTransportToolchain(t),
		Previous:               environmentProvenance,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance(explicit): %v", err)
	}
	data, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), defaultResolution.Context(), explicitProvenance)
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

func TestGeneratedManifestRecordsCanonicalConstraintProjection(t *testing.T) {
	t.Parallel()

	type constraintField struct {
		Path        string          `json:"path"`
		Type        string          `json:"type"`
		Constraints json.RawMessage `json:"constraints"`
	}
	type capabilityConstraints struct {
		ID               string            `json:"id"`
		ContractDigest   string            `json:"contract_digest"`
		ConstraintDigest string            `json:"constraint_digest"`
		Fields           []constraintField `json:"fields"`
	}
	type manifestDocument struct {
		ConstraintProjection struct {
			Version      int                     `json:"version"`
			Digest       string                  `json:"digest"`
			Capabilities []capabilityConstraints `json:"capabilities"`
		} `json:"constraint_projection"`
	}
	render := func(source string) ([]byte, manifestDocument) {
		t.Helper()
		resolution := resolvedApplicationWithEmail(t, "", source, defaultConfigurationProvenance(t, testComposition()))
		options := withManifestProvenance(t, resolvedOptions(), resolution)
		output, err := applicationgen.Render(options, resolution)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		data := outputData(t, output, "generated/manifest.json")
		var document manifestDocument
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decode generated manifest: %v", err)
		}
		return data, document
	}

	source := `id: email.send/v1
request:
  email: {type: string, required: true, constraints: {min_length: 3, max_length: 254, pattern: '^[^@]+@[^@]+$'}}
  attempt: {type: integer, constraints: {minimum: 1, maximum: 10}}
  label: {type: string, constraints: {min_length: 0, pattern: ''}}
  recipients: {type: array, items: string, constraints: {min_items: 1, max_items: 100}}
  untouched: {type: boolean}
response:
  latency: {type: number, constraints: {minimum: 0.5, maximum: 30.5}}
errors: [invalid_recipient]
`
	data, document := render(source)
	projection := document.ConstraintProjection
	if projection.Version != 1 || len(projection.Digest) != 71 || !strings.HasPrefix(projection.Digest, "sha256:") {
		t.Fatalf("constraint projection identity = version %d digest %q", projection.Version, projection.Digest)
	}
	if len(projection.Capabilities) != 2 {
		t.Fatalf("constraint projection capabilities = %#v", projection.Capabilities)
	}
	email := projection.Capabilities[0]
	if email.ID != "email.send/v1" || len(email.ContractDigest) != 71 || len(email.ConstraintDigest) != 71 {
		t.Fatalf("email constraint record = %#v", email)
	}
	wantFields := []struct {
		path        string
		kind        string
		constraints string
	}{
		{"request.attempt", "integer", `{"minimum":1,"maximum":10}`},
		{"request.email", "string", `{"min_length":3,"max_length":254,"pattern":"^[^@]+@[^@]+$"}`},
		{"request.label", "string", `{"min_length":0,"pattern":""}`},
		{"request.recipients", "array", `{"min_items":1,"max_items":100}`},
		{"response.latency", "number", `{"minimum":0.5,"maximum":30.5}`},
	}
	if len(email.Fields) != len(wantFields) {
		t.Fatalf("email constraint fields = %#v", email.Fields)
	}
	for index, want := range wantFields {
		got := email.Fields[index]
		if got.Path != want.path || got.Type != want.kind || string(got.Constraints) != want.constraints {
			t.Fatalf("email constraint field %d = %#v, want path=%q type=%q constraints=%s", index, got, want.path, want.kind, want.constraints)
		}
	}
	health := projection.Capabilities[1]
	if health.ID != "kernel.health/v1" || health.Fields == nil || len(health.Fields) != 0 || len(health.ConstraintDigest) != 71 {
		t.Fatalf("unconstrained intrinsic record = %#v", health)
	}
	if _, err := applicationgen.DecodeManifestProvenance(data); err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}

	reordered, reorderedDocument := render(`response:
  latency: {constraints: {maximum: 30.5, minimum: 0.5}, type: number}
errors: [invalid_recipient]
request:
  untouched: {type: boolean}
  recipients: {constraints: {max_items: 100, min_items: 1}, items: string, type: array}
  label: {constraints: {pattern: '', min_length: 0}, type: string}
  attempt: {constraints: {maximum: 10, minimum: 1}, type: integer}
  email: {constraints: {pattern: '^[^@]+@[^@]+$', max_length: 254, min_length: 3}, required: true, type: string}
id: email.send/v1
`)
	if !bytes.Equal(data, reordered) || reorderedDocument.ConstraintProjection.Digest != projection.Digest {
		t.Fatal("declaration order changed generated constraint projection")
	}
	changed, changedDocument := render(strings.Replace(source, "max_length: 254", "max_length: 255", 1))
	if bytes.Equal(data, changed) || changedDocument.ConstraintProjection.Digest == projection.Digest ||
		changedDocument.ConstraintProjection.Capabilities[0].ConstraintDigest == email.ConstraintDigest {
		t.Fatal("constraint change did not alter generated manifest projection digests")
	}

	tampered := bytes.Replace(data, []byte(`"max_length":254`), []byte(`"max_length":255`), 1)
	if _, err := applicationgen.DecodeManifestProvenance(tampered); err == nil || !strings.Contains(err.Error(), "constraint digest is inconsistent") {
		t.Fatalf("DecodeManifestProvenance(tampered constraint) error = %v", err)
	}
	var withoutProjection map[string]json.RawMessage
	if err := json.Unmarshal(data, &withoutProjection); err != nil {
		t.Fatalf("decode manifest for missing projection case: %v", err)
	}
	delete(withoutProjection, "constraint_projection")
	missingProjection, err := json.Marshal(withoutProjection)
	if err != nil {
		t.Fatalf("encode missing projection case: %v", err)
	}
	if _, err := applicationgen.DecodeManifestProvenance(missingProjection); err == nil || !strings.Contains(err.Error(), "constraint_projection") {
		t.Fatalf("DecodeManifestProvenance(missing projection) error = %v", err)
	}
}

func TestGeneratedManifestRequiresAndValidatesTransportToolchain(t *testing.T) {
	t.Parallel()

	current := currentTransportToolchain(t)
	options := applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               "plystra.yaml",
		RootDigest:             "sha256:" + strings.Repeat("3", 64),
		SelectedPath:           "plystra.yaml",
		SelectedDigest:         "sha256:" + strings.Repeat("3", 64),
		Composition:            testComposition(),
		ProtobufWireMapDigest:  "sha256:" + strings.Repeat("2", 64),
		ApplicationModelDigest: "sha256:" + strings.Repeat("1", 64),
		InterfaceProvenance:    emptyInterfaceProvenance(t),
		TransportToolchain:     current,
	}
	provenance, err := applicationgen.NewManifestProvenance(options)
	if err != nil {
		t.Fatalf("NewManifestProvenance: %v", err)
	}
	resolution := emptyApplication(t)
	data, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), resolution.Context(), provenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	decoded, err := applicationgen.DecodeManifestProvenance(data)
	if err != nil || !decoded.TransportToolchain().Valid() || decoded.TransportToolchain().Digest() != current.Digest() {
		t.Fatalf("DecodeManifestProvenance transport toolchain = %#v, %v", decoded.TransportToolchain(), err)
	}
	if !bytes.Contains(data, []byte(`"transport_toolchain":{"schema":"plystra.transport-toolchain/v2"`)) {
		t.Fatalf("generated manifest omits top-level transport_toolchain: %s", data)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode manifest document: %v", err)
	}
	delete(document, "transport_toolchain")
	missing, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode missing transport toolchain: %v", err)
	}
	if _, err := applicationgen.DecodeManifestProvenance(missing); err == nil || !strings.Contains(err.Error(), "transport_toolchain") {
		t.Fatalf("DecodeManifestProvenance(missing toolchain) error = %v", err)
	}

	var toolchainRecord map[string]json.RawMessage
	if err := json.Unmarshal(current.RecordJSON(), &toolchainRecord); err != nil {
		t.Fatalf("decode toolchain record: %v", err)
	}
	toolchainRecord["digest"] = json.RawMessage(`"sha256:0000000000000000000000000000000000000000000000000000000000000000"`)
	document["transport_toolchain"], err = json.Marshal(toolchainRecord)
	if err != nil {
		t.Fatalf("encode tampered transport toolchain: %v", err)
	}
	tampered, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode tampered manifest: %v", err)
	}
	if _, err := applicationgen.DecodeManifestProvenance(tampered); err == nil ||
		!strings.Contains(err.Error(), "transport_toolchain") ||
		!strings.Contains(err.Error(), "digest") {
		t.Fatalf("DecodeManifestProvenance(tampered toolchain) error = %v", err)
	}

	changedInputs := transportToolchainInputs(current)
	for index := range changedInputs {
		if changedInputs[index].Kind == transporttoolchain.KindGenerator && changedInputs[index].Name == "connect" {
			changedInputs[index].Version = "plystra.connect-generator/v2"
		}
	}
	changedToolchain, err := transporttoolchain.New(changedInputs)
	if err != nil {
		t.Fatalf("transporttoolchain.New(changed): %v", err)
	}
	options.TransportToolchain = changedToolchain
	changedProvenance, err := applicationgen.NewManifestProvenance(options)
	if err != nil {
		t.Fatalf("NewManifestProvenance(changed): %v", err)
	}
	changedData, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), resolution.Context(), changedProvenance)
	if err != nil {
		t.Fatalf("RenderManifest(changed): %v", err)
	}
	if bytes.Equal(data, changedData) || !bytes.Contains(changedData, []byte(`"version":"plystra.connect-generator/v2"`)) {
		t.Fatal("transport toolchain change did not alter generated manifest bytes")
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

func TestGeneratedManifestRequiresAndValidatesInterfaceProvenance(t *testing.T) {
	t.Parallel()

	interfaceRecord := manifestInterfaceProvenance(t)
	options := applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               "plystra.yaml",
		RootDigest:             "sha256:" + strings.Repeat("3", 64),
		SelectedPath:           "plystra.yaml",
		SelectedDigest:         "sha256:" + strings.Repeat("3", 64),
		Composition:            testComposition(),
		ProtobufWireMapDigest:  "sha256:" + strings.Repeat("2", 64),
		ApplicationModelDigest: "sha256:" + strings.Repeat("1", 64),
		InterfaceProvenance:    interfaceRecord,
		TransportToolchain:     currentTransportToolchain(t),
	}
	provenance, err := applicationgen.NewManifestProvenance(options)
	if err != nil {
		t.Fatalf("NewManifestProvenance: %v", err)
	}
	resolution := emptyApplication(t)
	data, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), resolution.Context(), provenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	decoded, err := applicationgen.DecodeManifestProvenance(data)
	if err != nil ||
		!decoded.InterfaceProvenance().Valid() ||
		decoded.InterfaceProvenance().Digest() != interfaceRecord.Digest() ||
		len(decoded.InterfaceProvenance().Intrinsics()) != 2 {
		t.Fatalf("DecodeManifestProvenance Interface provenance = %#v, %v", decoded.InterfaceProvenance(), err)
	}

	render := func(t *testing.T, record json.RawMessage, present bool) []byte {
		t.Helper()
		var document map[string]json.RawMessage
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decode valid manifest: %v", err)
		}
		if present {
			document["interface_provenance"] = append(json.RawMessage(nil), record...)
		} else {
			delete(document, "interface_provenance")
		}
		result, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode edited manifest: %v", err)
		}
		return result
	}
	assertRejected := func(t *testing.T, edited []byte, want string) {
		t.Helper()
		if _, err := applicationgen.DecodeManifestProvenance(edited); err == nil ||
			!strings.Contains(err.Error(), "interface_provenance") ||
			!strings.Contains(err.Error(), want) {
			t.Fatalf("DecodeManifestProvenance error = %v; want interface_provenance containing %q", err, want)
		}
	}

	t.Run("missing", func(t *testing.T) {
		assertRejected(t, render(t, nil, false), "record must contain")
	})
	t.Run("malformed", func(t *testing.T) {
		assertRejected(t, render(t, json.RawMessage(`{}`), true), "schema")
	})
	t.Run("unknown field", func(t *testing.T) {
		unknown := bytes.Replace(interfaceRecord.RecordJSON(), []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1)
		assertRejected(t, render(t, unknown, true), "unknown field")
	})
	t.Run("reordered", func(t *testing.T) {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(interfaceRecord.RecordJSON(), &record); err != nil {
			t.Fatalf("decode Interface provenance: %v", err)
		}
		var intrinsics []json.RawMessage
		if err := json.Unmarshal(record["intrinsics"], &intrinsics); err != nil {
			t.Fatalf("decode Interface intrinsics: %v", err)
		}
		slices.Reverse(intrinsics)
		record["intrinsics"], err = json.Marshal(intrinsics)
		if err != nil {
			t.Fatalf("encode reordered Interface intrinsics: %v", err)
		}
		reordered, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode reordered Interface provenance: %v", err)
		}
		assertRejected(t, render(t, reordered, true), "sorted by exact Interface ID")
	})
	t.Run("tampered digest", func(t *testing.T) {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(interfaceRecord.RecordJSON(), &record); err != nil {
			t.Fatalf("decode Interface provenance: %v", err)
		}
		record["digest"] = json.RawMessage(`"sha256:0000000000000000000000000000000000000000000000000000000000000000"`)
		tampered, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode tampered Interface provenance: %v", err)
		}
		assertRejected(t, render(t, tampered, true), "digest")
	})
}

func manifestInterfaceProvenance(t testing.TB) interfaceprovenance.Provenance {
	t.Helper()
	intrinsic := func(identifier, packagePath, character string) interfaceprovenance.IntrinsicInput {
		return interfaceprovenance.IntrinsicInput{
			Interface: interfaceprovenance.InterfaceInput{
				ID:                  identifier,
				PackagePath:         packagePath,
				ModulePath:          "github.com/plystra/kernel",
				ModuleVersion:       "v0.0.1-rc.1",
				DirectiveSource:     packagePath + "/interface.go:7:1",
				ShapeDigest:         "sha256:" + strings.Repeat(character, 64),
				ContractDigest:      "sha256:" + strings.Repeat(character, 64),
				DocumentationDigest: "sha256:" + strings.Repeat(character, 64),
				ExampleDigest:       "sha256:" + strings.Repeat(character, 64),
			},
			RequirementSources: []string{packagePath + " //plystra:interface " + identifier},
			ExposureSources:    []string{},
			Policy: interfaceprovenance.PolicyInput{
				Timeout: "30s",
				Sources: []string{"built-in Plystra default Interface invocation timeout"},
			},
		}
	}
	provenance, err := interfaceprovenance.New(interfaceprovenance.Input{
		Intrinsics: []interfaceprovenance.IntrinsicInput{
			intrinsic("kernel.info/v1", "github.com/plystra/kernel/interfaces/kernel/info/v1", "2"),
			intrinsic("kernel.health/v1", "github.com/plystra/kernel/interfaces/kernel/health/v1", "1"),
		},
	})
	if err != nil {
		t.Fatalf("interfaceprovenance.New: %v", err)
	}
	return provenance
}

func transportToolchainInputs(identity transporttoolchain.Identity) []transporttoolchain.ComponentInput {
	components := identity.Components()
	result := make([]transporttoolchain.ComponentInput, len(components))
	for index, component := range components {
		result[index] = transporttoolchain.ComponentInput{
			Kind:    component.Kind(),
			Name:    component.Name(),
			Version: component.Version(),
		}
	}
	return result
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

func TestApplicationModelDigestIncludesNormalizedInterfacePolicies(t *testing.T) {
	t.Parallel()

	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, ""),
	}
	withoutPolicy, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(without policy): %v", err)
	}
	first, err := applicationmeta.Parse([]byte("interfaces: {policies: {email.send/v1: {timeout: 5000ms}}}\n"))
	if err != nil {
		t.Fatalf("applicationmeta.Parse(first policy): %v", err)
	}
	options.InterfacePolicies = first.InterfacePolicies()
	withPolicy, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(with policy): %v", err)
	}
	if withPolicy == withoutPolicy {
		t.Fatal("Interface policy did not change the application-model digest")
	}
	second, err := applicationmeta.Parse([]byte("interfaces: {policies: {email.send/v1: {timeout: 5s}}}\n"))
	if err != nil {
		t.Fatalf("applicationmeta.Parse(second policy): %v", err)
	}
	options.InterfacePolicies = second.InterfacePolicies()
	normalized, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(normalized policy): %v", err)
	}
	if normalized != withPolicy {
		t.Fatalf("equivalent Interface timeouts changed model digest: %q != %q", normalized, withPolicy)
	}
	options.InterfacePolicies = append(options.InterfacePolicies, options.InterfacePolicies[0])
	if _, err := applicationModelDigest(t, options); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("ApplicationModelDigest(duplicate policy) error = %v", err)
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
	const expected = "sha256:4078ff041297d1d6b89b7f69f8403571cb0774b686bffda3a3ec1f6e1a7e42a8"
	if digest != expected {
		t.Fatalf("Connect Protobuf projection application-model digest = %q; want %q", digest, expected)
	}
}

func TestApplicationModelDigestIncludesTypedInterfaceProxiesDeterministically(t *testing.T) {
	t.Parallel()

	parse := func(value string) interfaceid.Identifier {
		identifier, err := interfaceid.Parse(value)
		if err != nil {
			t.Fatalf("interfaceid.Parse(%q): %v", value, err)
		}
		return identifier
	}
	order := interfaceproxygen.Input{
		InterfaceID:  parse("order.create/v1"),
		PackagePath:  "example.com/acme/application/interfaces/order/create/v1",
		MethodName:   "Create",
		RequestName:  "Request",
		ResponseName: "Response",
	}
	audit := interfaceproxygen.Input{
		InterfaceID:  parse("audit.write/v1"),
		PackagePath:  "example.com/acme/application/interfaces/audit/write/v1",
		MethodName:   "Write",
		RequestName:  "Request",
		ResponseName: "Response",
	}
	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, ""),
	}
	withoutProxies, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(without proxies): %v", err)
	}
	options.InterfaceProxies = []interfaceproxygen.Input{order, audit}
	withProxies, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(with proxies): %v", err)
	}
	if withProxies == withoutProxies {
		t.Fatal("typed Interface proxies did not alter the application-model digest")
	}
	options.InterfaceProxies = []interfaceproxygen.Input{audit, order}
	reordered, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(reordered proxies): %v", err)
	}
	if reordered != withProxies {
		t.Fatalf("reordered equal proxy inputs changed digest: %q != %q", reordered, withProxies)
	}
	changed := order
	changed.PackagePath = "example.com/acme/application/interfaces/order/create/v1beta"
	options.InterfaceProxies = []interfaceproxygen.Input{audit, changed}
	changedDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(changed proxy): %v", err)
	}
	if changedDigest == withProxies {
		t.Fatal("changed proxy contract package did not alter the application-model digest")
	}
}

func TestApplicationModelDigestIncludesImplementationAdaptersDeterministically(t *testing.T) {
	t.Parallel()

	parseID := func(value string) interfaceid.Identifier {
		identifier, err := interfaceid.Parse(value)
		if err != nil {
			t.Fatalf("interfaceid.Parse(%q): %v", value, err)
		}
		return identifier
	}
	parseConstructor := func(value string) constructorsymbol.Symbol {
		symbol, err := constructorsymbol.Parse(value)
		if err != nil {
			t.Fatalf("constructorsymbol.Parse(%q): %v", value, err)
		}
		return symbol
	}
	constructor := parseConstructor("example.com/acme/application/orders.New")
	order := implementationadaptergen.Input{
		InterfaceID:    parseID("order.create/v1"),
		PackagePath:    "example.com/acme/application/interfaces/order/create/v1",
		MethodName:     "Create",
		RequestName:    "Request",
		ResponseName:   "Response",
		Constructor:    constructor,
		ConcreteType:   "*example.com/acme/application/orders.service",
		SemanticErrors: []string{"order_invalid", "order_already_exists"},
	}
	audit := implementationadaptergen.Input{
		InterfaceID:  parseID("audit.write/v1"),
		PackagePath:  "example.com/acme/application/interfaces/audit/write/v1",
		MethodName:   "Write",
		RequestName:  "Request",
		ResponseName: "Response",
		Constructor:  parseConstructor("example.com/acme/application/audit.New"),
		ConcreteType: "*example.com/acme/application/audit.Service",
	}
	options := applicationgen.ApplicationModelOptions{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		Providers:           selectedProviderInputs(),
		Resolution:          resolvedApplication(t, ""),
	}
	withoutAdapters, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(without adapters): %v", err)
	}
	options.ImplementationAdapters = []implementationadaptergen.Input{order, audit}
	withAdapters, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(with adapters): %v", err)
	}
	if withAdapters == withoutAdapters {
		t.Fatal("Implementation adapters did not alter the application-model digest")
	}
	options.ImplementationAdapters = []implementationadaptergen.Input{audit, order}
	reordered, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(reordered adapters): %v", err)
	}
	if reordered != withAdapters {
		t.Fatalf("reordered equal adapter inputs changed digest: %q != %q", reordered, withAdapters)
	}
	changed := order
	changed.SemanticErrors = []string{"order_already_exists", "order_rejected"}
	options.ImplementationAdapters = []implementationadaptergen.Input{audit, changed}
	changedDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(changed adapter): %v", err)
	}
	if changedDigest == withAdapters {
		t.Fatal("changed adapter semantic contract did not alter the application-model digest")
	}
	changed = order
	changed.ConcreteType = "*example.com/acme/application/orders.replacement"
	options.ImplementationAdapters = []implementationadaptergen.Input{audit, changed}
	concreteDigest, err := applicationModelDigest(t, options)
	if err != nil {
		t.Fatalf("ApplicationModelDigest(changed concrete provenance): %v", err)
	}
	if concreteDigest == withAdapters {
		t.Fatal("changed adapter concrete provenance did not alter the application-model digest")
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
	cleanMap, err := protobufwiremap.Build(currentProjection, emptyInterfaceWireProjection(t, currentProjection), nil, false, "")
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
	history, err := protobufwiremap.Build(historicalProjection, emptyInterfaceWireProjection(t, historicalProjection), nil, false, "")
	if err != nil {
		t.Fatalf("Build(history): %v", err)
	}
	reconciled, err := protobufwiremap.Build(currentProjection, emptyInterfaceWireProjection(t, currentProjection), history.CanonicalJSON(), true, history.Digest())
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
	cleanMap, err := protobufwiremap.Build(currentProjection, emptyInterfaceWireProjection(t, currentProjection), nil, false, "")
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
	history, err := protobufwiremap.Build(historicalProjection, emptyInterfaceWireProjection(t, historicalProjection), nil, false, "")
	if err != nil {
		t.Fatalf("Build(history): %v", err)
	}
	reconciled, err := protobufwiremap.Build(currentProjection, emptyInterfaceWireProjection(t, currentProjection), history.CanonicalJSON(), true, history.Digest())
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
