package interfacecompatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAssessEvolutionClassifiesStableVersionRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(testing.TB, *EvolutionInput)
		want       []string
		wantShared bool
	}{
		{
			name: "clean",
			mutate: func(testing.TB, *EvolutionInput) {
			},
		},
		{
			name:       "new Interface and projections",
			mutate:     addEvolutionInterface,
			wantShared: true,
		},
		{
			name:   "Go shape and exact contract",
			mutate: changeEvolutionContract,
			want: []string{
				"records.echo/v1:go_shape:changed",
				"records.echo/v1:contract:changed",
			},
		},
		{
			name:   "documentation examples and generated documentation",
			mutate: changeEvolutionPresentation,
		},
		{
			name:   "transport and JavaScript projections",
			mutate: changeEvolutionProjections,
			want: []string{
				"records.echo/v1:protobuf_descriptor:changed",
				"records.echo/v1:javascript_types:changed",
			},
		},
		{
			name:       "exposure-only projection removal",
			mutate:     removeEvolutionExposure,
			wantShared: true,
		},
		{
			name:       "canonical Interface removal",
			mutate:     removeEvolutionInterface,
			wantShared: true,
			want: []string{
				"records.echo/v1:go_shape:removed",
				"records.echo/v1:contract:removed",
				"records.echo/v1:protobuf_descriptor:removed",
				"records.echo/v1:connect_procedure:removed",
				"records.echo/v1:wire_map:removed",
				"records.echo/v1:javascript_surface:removed",
				"records.echo/v1:javascript_types:removed",
				"records.echo/v1:javascript_semantic_errors:removed",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := cleanEvolutionInput()
			test.mutate(t, &input)
			assessment, err := AssessEvolution(input)
			if err != nil || !assessment.Valid() {
				t.Fatalf("AssessEvolution = %#v, %v", assessment, err)
			}
			if got := evolutionRequirementSummaries(assessment); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("requirements = %#v, want %#v", got, test.want)
			}
			if assessment.RequiresNewVersion() != (len(test.want) != 0) {
				t.Fatalf("RequiresNewVersion = %t, want %t", assessment.RequiresNewVersion(), len(test.want) != 0)
			}
			if assessment.SharedJavaScriptChanged() != test.wantShared {
				t.Fatalf("SharedJavaScriptChanged = %t, want %t", assessment.SharedJavaScriptChanged(), test.wantShared)
			}
			stableErr := assessment.ValidateStableVersioning()
			if len(test.want) == 0 {
				if stableErr != nil {
					t.Fatalf("ValidateStableVersioning = %v", stableErr)
				}
				return
			}
			if !errors.Is(stableErr, ErrStableVersionRequired) {
				t.Fatalf("ValidateStableVersioning error = %v", stableErr)
			}
		})
	}
}

func TestEvolutionAssessmentIsCanonicalOrderedAndDefensive(t *testing.T) {
	t.Parallel()

	input := cleanEvolutionInput()
	changeEvolutionContract(t, &input)
	input.Shape.changes = append([]Change{
		{
			kind:           ChangeChanged,
			id:             "accounts.read/v1",
			previousDigest: evolutionTestDigest("accounts-shape-before"),
			currentDigest:  evolutionTestDigest("accounts-shape-after"),
		},
	}, input.Shape.changes...)
	input.Metadata.changes = append([]MetadataChange{
		changedEvolutionMetadata("accounts.read/v1", "accounts-before", "accounts-after", MetadataClassContract),
	}, input.Metadata.changes...)

	first, err := AssessEvolution(input)
	if err != nil || !first.Valid() {
		t.Fatalf("AssessEvolution(first) = %#v, %v", first, err)
	}
	second, err := AssessEvolution(input)
	if err != nil || !second.Valid() {
		t.Fatalf("AssessEvolution(second) = %#v, %v", second, err)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) ||
		first.Digest() != second.Digest() {
		t.Fatalf(
			"equivalent assessments differ:\n%s\n%s\n%s\n%s",
			first.CanonicalJSON(),
			second.CanonicalJSON(),
			first.Digest(),
			second.Digest(),
		)
	}
	if !json.Valid(first.CanonicalJSON()) ||
		bytes.Contains(first.CanonicalJSON(), []byte(`"digest"`)) {
		t.Fatalf("canonical assessment is invalid or self-digested: %s", first.CanonicalJSON())
	}
	const wantDigest = "sha256:e60c2d005a929d0704e27302ba3518a59b86e7d962d2d8944c6a604ae6b46de5"
	if first.Digest() != wantDigest {
		t.Fatalf("assessment digest = %q; canonical = %s", first.Digest(), first.CanonicalJSON())
	}

	requirements := first.Requirements()
	if got := []string{requirements[0].ID(), requirements[1].ID()}; !reflect.DeepEqual(
		got,
		[]string{"accounts.read/v1", "records.echo/v1"},
	) {
		t.Fatalf("requirement order = %#v", got)
	}
	canonical := first.CanonicalJSON()
	canonical[0] = 'x'
	changes := requirements[0].Changes()
	requirements[0] = VersionRequirement{}
	changes[0] = VersionChange{}
	if !first.Valid() ||
		first.Requirements()[0].ID() != "accounts.read/v1" ||
		first.Requirements()[0].Changes()[0].Surface() != VersionSurfaceGoShape ||
		first.CanonicalJSON()[0] != '{' {
		t.Fatal("EvolutionAssessment exposed mutable internal storage")
	}
}

func TestStableVersionErrorPreservesActionableTypedEvidence(t *testing.T) {
	t.Parallel()

	input := cleanEvolutionInput()
	changeEvolutionContract(t, &input)
	assessment, err := AssessEvolution(input)
	if err != nil {
		t.Fatalf("AssessEvolution: %v", err)
	}
	stableErr := assessment.ValidateStableVersioning()
	if !errors.Is(stableErr, ErrStableVersionRequired) ||
		errors.Is(stableErr, ErrEvolution) {
		t.Fatalf("stable error categories = %v", stableErr)
	}
	var typed *StableVersionError
	if !errors.As(stableErr, &typed) {
		t.Fatalf("stable error type = %T", stableErr)
	}
	for _, text := range []string{
		"records.echo/v1",
		"go_shape changed",
		"contract changed",
		"new higher /vN",
	} {
		if !strings.Contains(stableErr.Error(), text) {
			t.Fatalf("stable error %q omits %q", stableErr, text)
		}
	}
	requirements := typed.Requirements()
	if len(requirements) != 1 || requirements[0].ID() != "records.echo/v1" {
		t.Fatalf("typed requirements = %#v", requirements)
	}
	requirements[0] = VersionRequirement{}
	changes := typed.Requirements()[0].Changes()
	changes[0] = VersionChange{}
	if typed.Requirements()[0].ID() != "records.echo/v1" ||
		typed.Requirements()[0].Changes()[0].Surface() != VersionSurfaceGoShape {
		t.Fatal("StableVersionError exposed mutable requirement evidence")
	}

	if err := (EvolutionAssessment{}).ValidateStableVersioning(); !errors.Is(err, ErrEvolution) {
		t.Fatalf("zero assessment stable validation error = %v", err)
	}
	if assessment, err := AssessEvolution(EvolutionInput{}); !errors.Is(err, ErrEvolution) ||
		assessment.Valid() {
		t.Fatalf("AssessEvolution(zero) = %#v, %v", assessment, err)
	}
}

func cleanEvolutionInput() EvolutionInput {
	shapeDigest := evolutionTestDigest("shape-baseline")
	metadataDigest := evolutionTestDigest("metadata-baseline")
	transportDigest := evolutionTestDigest("transport-baseline")
	javaScriptDigest := evolutionTestDigest("javascript-baseline")
	javaScriptPackageDigest := evolutionTestDigest("javascript-package")
	documentationDigest := evolutionTestDigest("documentation-baseline")
	return EvolutionInput{
		Shape: Comparison{
			previousDigest: shapeDigest,
			currentDigest:  shapeDigest,
			changes:        []Change{},
			prepared:       true,
		},
		Metadata: MetadataComparison{
			previousDigest: metadataDigest,
			currentDigest:  metadataDigest,
			changes:        []MetadataChange{},
			prepared:       true,
		},
		Transport: TransportComparison{
			previousDigest: transportDigest,
			currentDigest:  transportDigest,
			changes:        []TransportChange{},
			prepared:       true,
		},
		JavaScript: JavaScriptComparison{
			previousDigest:        javaScriptDigest,
			currentDigest:         javaScriptDigest,
			previousPackageDigest: javaScriptPackageDigest,
			currentPackageDigest:  javaScriptPackageDigest,
			changes:               []JavaScriptChange{},
			prepared:              true,
		},
		Documentation: DocumentationComparison{
			previousDigest: documentationDigest,
			currentDigest:  documentationDigest,
			changes:        []DocumentationChange{},
			prepared:       true,
		},
	}
}

func addEvolutionInterface(_ testing.TB, input *EvolutionInput) {
	const identifier = "records.echo/v1"
	input.Shape.currentDigest = evolutionTestDigest("shape-baseline-added")
	input.Shape.changes = []Change{{
		kind:          ChangeAdded,
		id:            identifier,
		currentDigest: evolutionTestDigest("shape-added"),
	}}
	input.Metadata.currentDigest = evolutionTestDigest("metadata-baseline-added")
	input.Metadata.changes = []MetadataChange{{
		kind:    ChangeAdded,
		id:      identifier,
		classes: allMetadataClasses(),
		current: evolutionMetadataInterface(identifier, "added"),
	}}
	input.Transport.currentDigest = evolutionTestDigest("transport-baseline-added")
	input.Transport.changes = []TransportChange{{
		kind:    ChangeAdded,
		id:      identifier,
		classes: allTransportClasses(),
		current: evolutionTransportInterface(identifier, "added"),
	}}
	input.JavaScript.currentDigest = evolutionTestDigest("javascript-baseline-added")
	input.JavaScript.currentPackageDigest = evolutionTestDigest("javascript-package-added")
	input.JavaScript.packageChanged = true
	input.JavaScript.changes = []JavaScriptChange{{
		kind:    ChangeAdded,
		id:      identifier,
		classes: allJavaScriptClasses(),
		current: evolutionJavaScriptInterface(identifier, "added"),
	}}
}

func changeEvolutionContract(_ testing.TB, input *EvolutionInput) {
	const identifier = "records.echo/v1"
	input.Shape.currentDigest = evolutionTestDigest("shape-baseline-changed")
	input.Shape.changes = []Change{{
		kind:           ChangeChanged,
		id:             identifier,
		previousDigest: evolutionTestDigest("shape-before"),
		currentDigest:  evolutionTestDigest("shape-after"),
	}}
	input.Metadata.currentDigest = evolutionTestDigest("metadata-baseline-contract-changed")
	input.Metadata.changes = []MetadataChange{
		changedEvolutionMetadata(identifier, "contract-before", "contract-after", MetadataClassContract),
	}
}

func changeEvolutionPresentation(t testing.TB, input *EvolutionInput) {
	t.Helper()

	const identifier = "records.echo/v1"
	input.Metadata.currentDigest = evolutionTestDigest("metadata-baseline-presentation-changed")
	previous := evolutionMetadataInterface(identifier, "presentation-before")
	current := previous
	current.DocumentationDigest = evolutionTestDigest("presentation-after-documentation")
	current.ExampleDigest = evolutionTestDigest("presentation-after-examples")
	input.Metadata.changes = []MetadataChange{{
		kind:     ChangeChanged,
		id:       identifier,
		classes:  []MetadataClass{MetadataClassDocumentation, MetadataClassExamples},
		previous: previous,
		current:  current,
	}}
	previousDocumentation, err := NewDocumentation([]DocumentationInput{{
		Path: "generated/docs/api.md",
		Kind: DocumentationKindInterfaceReference,
		Data: []byte("# Before\n"),
	}})
	if err != nil {
		t.Fatalf("NewDocumentation(previous): %v", err)
	}
	currentDocumentation, err := NewDocumentation([]DocumentationInput{{
		Path: "generated/docs/api.md",
		Kind: DocumentationKindInterfaceReference,
		Data: []byte("# After\n"),
	}})
	if err != nil {
		t.Fatalf("NewDocumentation(current): %v", err)
	}
	input.Documentation, err = CompareDocumentation(previousDocumentation, currentDocumentation)
	if err != nil {
		t.Fatalf("CompareDocumentation: %v", err)
	}
}

func changeEvolutionProjections(_ testing.TB, input *EvolutionInput) {
	const identifier = "records.echo/v1"
	previousTransport := evolutionTransportInterface(identifier, "projection-before")
	currentTransport := previousTransport
	currentTransport.DescriptorDigest = evolutionTestDigest("projection-after-descriptor")
	input.Transport.currentDigest = evolutionTestDigest("transport-baseline-projection-changed")
	input.Transport.changes = []TransportChange{{
		kind:     ChangeChanged,
		id:       identifier,
		classes:  []TransportClass{TransportClassDescriptor},
		previous: previousTransport,
		current:  currentTransport,
	}}
	previousJavaScript := evolutionJavaScriptInterface(identifier, "projection-before")
	currentJavaScript := previousJavaScript
	currentJavaScript.TypesDigest = evolutionTestDigest("projection-after-types")
	input.JavaScript.currentDigest = evolutionTestDigest("javascript-baseline-projection-changed")
	input.JavaScript.changes = []JavaScriptChange{{
		kind:     ChangeChanged,
		id:       identifier,
		classes:  []JavaScriptClass{JavaScriptClassTypes},
		previous: previousJavaScript,
		current:  currentJavaScript,
	}}
}

func removeEvolutionExposure(_ testing.TB, input *EvolutionInput) {
	const identifier = "records.echo/v1"
	input.Transport.currentDigest = evolutionTestDigest("transport-baseline-exposure-removed")
	input.Transport.changes = []TransportChange{{
		kind:     ChangeRemoved,
		id:       identifier,
		classes:  allTransportClasses(),
		previous: evolutionTransportInterface(identifier, "exposed"),
	}}
	input.JavaScript.currentDigest = evolutionTestDigest("javascript-baseline-exposure-removed")
	input.JavaScript.currentPackageDigest = evolutionTestDigest("javascript-package-exposure-removed")
	input.JavaScript.packageChanged = true
	input.JavaScript.changes = []JavaScriptChange{{
		kind:     ChangeRemoved,
		id:       identifier,
		classes:  allJavaScriptClasses(),
		previous: evolutionJavaScriptInterface(identifier, "exposed"),
	}}
}

func removeEvolutionInterface(t testing.TB, input *EvolutionInput) {
	t.Helper()

	removeEvolutionExposure(t, input)
	const identifier = "records.echo/v1"
	input.Shape.currentDigest = evolutionTestDigest("shape-baseline-removed")
	input.Shape.changes = []Change{{
		kind:           ChangeRemoved,
		id:             identifier,
		previousDigest: evolutionTestDigest("shape-removed"),
	}}
	input.Metadata.currentDigest = evolutionTestDigest("metadata-baseline-removed")
	input.Metadata.changes = []MetadataChange{{
		kind:     ChangeRemoved,
		id:       identifier,
		classes:  allMetadataClasses(),
		previous: evolutionMetadataInterface(identifier, "removed"),
	}}
}

func changedEvolutionMetadata(
	identifier string,
	previousSuffix string,
	currentSuffix string,
	class MetadataClass,
) MetadataChange {
	previous := evolutionMetadataInterface(identifier, previousSuffix)
	current := previous
	switch class {
	case MetadataClassContract:
		current.ContractDigest = evolutionTestDigest(currentSuffix + "-contract")
	case MetadataClassDocumentation:
		current.DocumentationDigest = evolutionTestDigest(currentSuffix + "-documentation")
	case MetadataClassExamples:
		current.ExampleDigest = evolutionTestDigest(currentSuffix + "-examples")
	}
	return MetadataChange{
		kind:     ChangeChanged,
		id:       identifier,
		classes:  []MetadataClass{class},
		previous: previous,
		current:  current,
	}
}

func evolutionMetadataInterface(identifier, suffix string) metadataWireInterface {
	return metadataWireInterface{
		ID:                  identifier,
		ContractDigest:      evolutionTestDigest(suffix + "-contract"),
		DocumentationDigest: evolutionTestDigest(suffix + "-documentation"),
		ExampleDigest:       evolutionTestDigest(suffix + "-examples"),
	}
}

func evolutionTransportInterface(identifier, suffix string) transportWireInterface {
	return transportWireInterface{
		ID:               identifier,
		DescriptorDigest: evolutionTestDigest(suffix + "-descriptor"),
		ProcedureDigest:  evolutionTestDigest(suffix + "-procedure"),
		WireMapDigest:    evolutionTestDigest(suffix + "-wire-map"),
	}
}

func evolutionJavaScriptInterface(identifier, suffix string) javaScriptWireInterface {
	return javaScriptWireInterface{
		ID:                   identifier,
		SurfaceDigest:        evolutionTestDigest(suffix + "-surface"),
		TypesDigest:          evolutionTestDigest(suffix + "-types"),
		SemanticErrorsDigest: evolutionTestDigest(suffix + "-semantic-errors"),
	}
}

func evolutionRequirementSummaries(assessment EvolutionAssessment) []string {
	var result []string
	for _, requirement := range assessment.Requirements() {
		for _, change := range requirement.Changes() {
			result = append(
				result,
				requirement.ID()+":"+string(change.Surface())+":"+string(change.Kind()),
			)
		}
	}
	return result
}

func evolutionTestDigest(value string) string {
	return digest([]byte(value))
}
