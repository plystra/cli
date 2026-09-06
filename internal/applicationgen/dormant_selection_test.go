package applicationgen

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestDormantImplementationSelectionsNormalizeAndRejectNoncanonicalRecords(t *testing.T) {
	t.Parallel()

	alpha := testDormantImplementationSelection("alpha.read/v1", "alpha")
	beta := testDormantImplementationSelection("beta.read/v1", "beta")
	paths := []string{alpha.selectionPath, beta.selectionPath}

	normalized, err := normalizeDormantImplementationSelections([]DormantImplementationSelection{beta, alpha})
	if err != nil {
		t.Fatalf("normalizeDormantImplementationSelections: %v", err)
	}
	if len(normalized) != 2 || normalized[0].interfaceID != alpha.interfaceID || normalized[1].interfaceID != beta.interfaceID {
		t.Fatalf("normalized dormant selections = %#v", normalized)
	}
	if err := validateDormantImplementationSelections(normalized, ConfigurationModeDefault, "plystra.yaml", "plystra.yaml", paths); err != nil {
		t.Fatalf("validate normalized dormant selections: %v", err)
	}
	firstDigest, err := dormantImplementationSelectionsDigest(normalized)
	if err != nil {
		t.Fatalf("digest normalized dormant selections: %v", err)
	}
	repeated, err := normalizeDormantImplementationSelections([]DormantImplementationSelection{alpha, beta})
	if err != nil {
		t.Fatalf("normalize repeated dormant selections: %v", err)
	}
	repeatedDigest, err := dormantImplementationSelectionsDigest(repeated)
	if err != nil || repeatedDigest != firstDigest {
		t.Fatalf("reordered dormant selection digest = %q, %v; want %q", repeatedDigest, err, firstDigest)
	}

	reordered := []DormantImplementationSelection{beta, alpha}
	if err := validateDormantImplementationSelections(reordered, ConfigurationModeDefault, "plystra.yaml", "plystra.yaml", paths); err == nil || !strings.Contains(err.Error(), "canonically ordered") {
		t.Fatalf("reordered dormant selections error = %v", err)
	}
	if _, err := normalizeDormantImplementationSelections([]DormantImplementationSelection{alpha, alpha}); err == nil || !strings.Contains(err.Error(), "repeats Interface") {
		t.Fatalf("duplicate dormant selections error = %v", err)
	}

	withHistory := beta
	withHistory.contributions = append([]DormantSelectionContribution{
		{
			owner:      string(resolutionevidence.ConfigurationOwnerDependency),
			precedence: 1,
			digest:     "sha256:" + strings.Repeat("1", 64),
			summary:    "redacted",
			sources: []DormantSelectionSource{{
				module: "example.com/platform", path: "plystra.yaml", kind: "configuration-value", line: 1, column: 1,
			}},
		},
	}, withHistory.contributions...)
	if err := validateDormantImplementationSelection(withHistory); err != nil {
		t.Fatalf("validate dormant selection composition history: %v", err)
	}
	slices.Reverse(withHistory.contributions)
	if err := validateDormantImplementationSelection(withHistory); err == nil || !strings.Contains(err.Error(), "canonically ordered") {
		t.Fatalf("reordered dormant contributions error = %v", err)
	}

	withRemoval := beta
	withRemoval.selectionOwner = string(resolutionevidence.ConfigurationOwnerEnvironment)
	withRemoval.contributions = []DormantSelectionContribution{
		{
			owner:      string(resolutionevidence.ConfigurationOwnerDependency),
			precedence: 1,
			digest:     "sha256:" + strings.Repeat("1", 64),
			summary:    "redacted",
			sources: []DormantSelectionSource{{
				module: "example.com/platform", path: "plystra.yaml", kind: "configuration-value", line: 1, column: 1,
			}},
		},
		{
			owner:      string(resolutionevidence.ConfigurationOwnerRoot),
			precedence: 2,
			digest:     "sha256:" + strings.Repeat("2", 64),
			summary:    string(applicationmeta.ConfigurationSummaryRemoval),
			removed:    true,
			sources: []DormantSelectionSource{{
				module: "example.com/app", path: "plystra.yaml", kind: "configuration-removal", line: 1, column: 1,
			}},
		},
		{
			owner:      string(resolutionevidence.ConfigurationOwnerEnvironment),
			precedence: 3,
			digest:     withRemoval.selectionDigest,
			summary:    string(applicationmeta.ConfigurationSummaryImplementation),
			effective:  true,
			sources: []DormantSelectionSource{{
				module: "example.com/app", path: "plystra.production.yaml", kind: "configuration-value", line: 1, column: 1,
			}},
		},
	}
	if err := validateDormantImplementationSelections(
		[]DormantImplementationSelection{withRemoval},
		ConfigurationModeEnvironment,
		"plystra.yaml",
		"plystra.production.yaml",
		[]string{},
	); err != nil {
		t.Fatalf("validate dormant selection with removal history: %v", err)
	}
	restored := restoreDormantImplementationSelections(dormantImplementationSelectionWires([]DormantImplementationSelection{withRemoval}))
	if !reflect.DeepEqual(restored, []DormantImplementationSelection{withRemoval}) || !restored[0].contributions[1].removed {
		t.Fatalf("dormant selection removal round trip = %#v", restored)
	}

	duplicateSource := alpha
	duplicateSource.contributions[0].sources = append(duplicateSource.contributions[0].sources, duplicateSource.contributions[0].sources[0])
	if err := validateDormantImplementationSelection(duplicateSource); err == nil || !strings.Contains(err.Error(), "sources must be unique") {
		t.Fatalf("duplicate dormant source error = %v", err)
	}

	invalidSource := alpha
	invalidSource.constructorSource = "example.com/app@local/other/implementation.go:7:1"
	if err := validateDormantImplementationSelection(invalidSource); err == nil || !strings.Contains(err.Error(), "constructor source") {
		t.Fatalf("constructor source/package mismatch error = %v", err)
	}
}

func TestEmptyDormantImplementationSelectionRecordIsCanonical(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeDormantImplementationSelections(nil)
	if err != nil || normalized == nil || len(normalized) != 0 {
		t.Fatalf("normalize empty dormant selections = %#v, %v", normalized, err)
	}
	if err := validateDormantImplementationSelections(normalized, ConfigurationModeDefault, "plystra.yaml", "plystra.yaml", []string{}); err != nil {
		t.Fatalf("validate empty dormant selections: %v", err)
	}
	digest, err := dormantImplementationSelectionsDigest(normalized)
	if err != nil || digest != "sha256:f781509b1c7204b29c108dddeaeba732605c9ea41e0412205c03777d995e6681" {
		t.Fatalf("empty dormant selection digest = %q, %v", digest, err)
	}
	if err := validateDormantImplementationSelections(nil, ConfigurationModeDefault, "plystra.yaml", "plystra.yaml", []string{}); err == nil || !strings.Contains(err.Error(), "must be an array") {
		t.Fatalf("missing dormant selection array error = %v", err)
	}
}

func testDormantImplementationSelection(interfaceID, packageName string) DormantImplementationSelection {
	constructor := "example.com/app/" + packageName + ".New"
	digest := dormantImplementationChoiceDigest(interfaceID, constructor)
	return DormantImplementationSelection{
		interfaceID:              interfaceID,
		constructor:              constructor,
		constructorModulePath:    "example.com/app",
		constructorModuleVersion: "local",
		constructorSource:        "example.com/app@local/" + packageName + "/implementation.go:7:1",
		selectionPath:            "interfaces.use[\"" + interfaceID + "\"]",
		selectionDigest:          digest,
		selectionOwner:           string(resolutionevidence.ConfigurationOwnerRoot),
		contributions: []DormantSelectionContribution{{
			owner:      string(resolutionevidence.ConfigurationOwnerRoot),
			precedence: 2,
			digest:     digest,
			summary:    string(applicationmeta.ConfigurationSummaryImplementation),
			effective:  true,
			sources: []DormantSelectionSource{{
				module: "example.com/app", path: "plystra.yaml", kind: "configuration-value", line: 1, column: 1,
			}},
		}},
	}
}
