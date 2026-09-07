package applicationgen

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestDormantConstructorConfigurationsNormalizeRoundTripAndDefendStorage(t *testing.T) {
	t.Parallel()

	alphaSelection := testDormantImplementationSelection("alpha.read/v1", "alpha")
	betaSelection := testDormantImplementationSelection("beta.read/v1", "beta")
	alpha := testDormantConstructorConfiguration(alphaSelection)
	beta := testDormantConstructorConfiguration(betaSelection)

	reversedAlpha := cloneDormantConstructorConfigurations([]DormantConstructorConfiguration{alpha})[0]
	slices.Reverse(reversedAlpha.fields)
	for fieldIndex := range reversedAlpha.fields {
		slices.Reverse(reversedAlpha.fields[fieldIndex].contributions)
		for contributionIndex := range reversedAlpha.fields[fieldIndex].contributions {
			slices.Reverse(reversedAlpha.fields[fieldIndex].contributions[contributionIndex].sources)
		}
	}
	normalized, err := normalizeDormantConstructorConfigurations([]DormantConstructorConfiguration{beta, reversedAlpha})
	if err != nil {
		t.Fatalf("normalizeDormantConstructorConfigurations: %v", err)
	}
	if len(normalized) != 2 || normalized[0].constructor != alpha.constructor || normalized[1].constructor != beta.constructor {
		t.Fatalf("normalized dormant constructor configurations = %#v", normalized)
	}
	for _, configuration := range normalized {
		if !slices.IsSortedFunc(configuration.fields, func(left, right DormantConfigurationField) int {
			return strings.Compare(left.path, right.path)
		}) {
			t.Fatalf("configuration fields are not canonical: %#v", configuration.fields)
		}
	}
	paths := []string{
		alpha.fields[0].path,
		alpha.fields[1].path,
		beta.fields[0].path,
		beta.fields[1].path,
	}
	if err := validateDormantConstructorConfigurations(
		normalized,
		[]DormantImplementationSelection{alphaSelection, betaSelection},
		map[string]struct{}{},
		ConfigurationModeDefault,
		"plystra.yaml",
		"plystra.yaml",
		paths,
	); err != nil {
		t.Fatalf("validate normalized dormant constructor configurations: %v", err)
	}
	digest, err := dormantConstructorConfigurationsDigest(normalized)
	if err != nil {
		t.Fatalf("dormantConstructorConfigurationsDigest: %v", err)
	}
	repeated, err := normalizeDormantConstructorConfigurations([]DormantConstructorConfiguration{alpha, beta})
	if err != nil {
		t.Fatalf("normalize repeated dormant constructor configurations: %v", err)
	}
	repeatedDigest, err := dormantConstructorConfigurationsDigest(repeated)
	if err != nil || repeatedDigest != digest {
		t.Fatalf("reordered dormant configuration digest = %q, %v; want %q", repeatedDigest, err, digest)
	}

	restored := restoreDormantConstructorConfigurations(dormantConstructorConfigurationWires(normalized))
	if !reflect.DeepEqual(restored, normalized) {
		t.Fatalf("dormant constructor configuration round trip = %#v, want %#v", restored, normalized)
	}
	copyOfConfigurations := cloneDormantConstructorConfigurations(normalized)
	copyOfConfigurations[0].fields[0].path = "changed"
	copyOfConfigurations[0].fields[0].contributions[0].sources[0].path = "changed.yaml"
	if normalized[0].fields[0].path == "changed" || normalized[0].fields[0].contributions[0].sources[0].path == "changed.yaml" {
		t.Fatal("cloneDormantConstructorConfigurations exposed mutable storage")
	}
	fields := normalized[0].Fields()
	fields[0].path = "changed"
	fields[0].contributions[0].sources[0].path = "changed.yaml"
	if normalized[0].Fields()[0].path == "changed" || normalized[0].Fields()[0].contributions[0].sources[0].path == "changed.yaml" {
		t.Fatal("DormantConstructorConfiguration.Fields exposed mutable storage")
	}
}

func TestDormantConstructorConfigurationsRejectMalformedOrInconsistentRecords(t *testing.T) {
	t.Parallel()

	selection := testDormantImplementationSelection("alpha.read/v1", "alpha")
	configuration := testDormantConstructorConfiguration(selection)
	paths := []string{configuration.fields[0].path, configuration.fields[1].path}
	validate := func(values []DormantConstructorConfiguration, selections []DormantImplementationSelection, active map[string]struct{}, currentPaths []string) error {
		return validateDormantConstructorConfigurations(values, selections, active, ConfigurationModeDefault, "plystra.yaml", "plystra.yaml", currentPaths)
	}

	if err := validate(nil, []DormantImplementationSelection{selection}, map[string]struct{}{}, paths); err == nil || !strings.Contains(err.Error(), "must be an array") {
		t.Fatalf("missing dormant constructor configuration array error = %v", err)
	}
	if _, err := normalizeDormantConstructorConfigurations([]DormantConstructorConfiguration{configuration, configuration}); err == nil || !strings.Contains(err.Error(), "repeats constructor") {
		t.Fatalf("duplicate dormant constructor configuration error = %v", err)
	}
	if err := validate([]DormantConstructorConfiguration{configuration}, nil, map[string]struct{}{}, paths); err == nil || !strings.Contains(err.Error(), "no dormant explicit selection") {
		t.Fatalf("orphan dormant constructor configuration error = %v", err)
	}
	if err := validate([]DormantConstructorConfiguration{configuration}, []DormantImplementationSelection{selection}, map[string]struct{}{selection.constructor: {}}, paths); err == nil || !strings.Contains(err.Error(), "duplicates active reachable constructor") {
		t.Fatalf("active dormant constructor configuration error = %v", err)
	}
	if err := validate([]DormantConstructorConfiguration{configuration}, []DormantImplementationSelection{selection}, map[string]struct{}{}, paths[:1]); err == nil || !strings.Contains(err.Error(), "current-project ownership") {
		t.Fatalf("missing dormant current-project ownership error = %v", err)
	}

	reordered := cloneDormantConstructorConfigurations([]DormantConstructorConfiguration{configuration})[0]
	slices.Reverse(reordered.fields)
	if err := validateDormantConstructorConfiguration(reordered); err == nil || !strings.Contains(err.Error(), "canonically ordered") {
		t.Fatalf("reordered dormant configuration fields error = %v", err)
	}
	tampered := cloneDormantConstructorConfigurations([]DormantConstructorConfiguration{configuration})[0]
	tampered.fields[0].digest = "sha256:" + strings.Repeat("f", 64)
	if err := validateDormantConstructorConfiguration(tampered); err == nil || !strings.Contains(err.Error(), "disagrees with the field") {
		t.Fatalf("tampered dormant configuration field error = %v", err)
	}
	suppressed := cloneDormantConstructorConfigurations([]DormantConstructorConfiguration{configuration})[0]
	suppressed.fields[1].effective = false
	if err := validateDormantConstructorConfiguration(suppressed); err == nil || !strings.Contains(err.Error(), "suppressed field carries effective state") {
		t.Fatalf("malformed suppressed dormant configuration field error = %v", err)
	}
}

func TestEmptyDormantConstructorConfigurationRecordIsCanonical(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeDormantConstructorConfigurations(nil)
	if err != nil || normalized == nil || len(normalized) != 0 {
		t.Fatalf("normalize empty dormant constructor configurations = %#v, %v", normalized, err)
	}
	if err := validateDormantConstructorConfigurations(normalized, nil, map[string]struct{}{}, ConfigurationModeDefault, "plystra.yaml", "plystra.yaml", []string{}); err != nil {
		t.Fatalf("validate empty dormant constructor configurations: %v", err)
	}
	digest, err := dormantConstructorConfigurationsDigest(normalized)
	if err != nil || digest != "sha256:2c55f3bad8a359bcfc8f72af010d653ec12eba9785a3e73b7b92b988cb80ca41" {
		t.Fatalf("empty dormant constructor configuration digest = %q, %v", digest, err)
	}
}

func testDormantConstructorConfiguration(selection DormantImplementationSelection) DormantConstructorConfiguration {
	root := "config[" + strconv.Quote(selection.constructor) + "]"
	rootDigest := "sha256:" + strings.Repeat("a", 64)
	fieldDigest := "sha256:" + strings.Repeat("b", 64)
	rootSource := DormantConfigurationSource{
		module: "example.com/app", path: "plystra.yaml", kind: "configuration-value", line: 1, column: 1,
	}
	return DormantConstructorConfiguration{
		constructor:              selection.constructor,
		constructorModulePath:    selection.constructorModulePath,
		constructorModuleVersion: selection.constructorModuleVersion,
		constructorSource:        selection.constructorSource,
		configurationPath:        root,
		fields: []DormantConfigurationField{
			{
				path: root, digest: rootDigest, summary: string(applicationmeta.ConfigurationSummaryObject),
				owner: string(resolutionevidence.ConfigurationOwnerRoot), effective: true,
				contributions: []DormantConfigurationContribution{{
					owner: string(resolutionevidence.ConfigurationOwnerRoot), precedence: 2,
					digest: rootDigest, summary: string(applicationmeta.ConfigurationSummaryObject), effective: true,
					sources: []DormantConfigurationSource{rootSource},
				}},
			},
			{
				path: root + `["endpoint"]`, digest: fieldDigest, summary: string(applicationmeta.ConfigurationSummaryString),
				owner: string(resolutionevidence.ConfigurationOwnerRoot), effective: true,
				contributions: []DormantConfigurationContribution{{
					owner: string(resolutionevidence.ConfigurationOwnerRoot), precedence: 2,
					digest: fieldDigest, summary: string(applicationmeta.ConfigurationSummaryString), effective: true,
					sources: []DormantConfigurationSource{rootSource},
				}},
			},
		},
	}
}
