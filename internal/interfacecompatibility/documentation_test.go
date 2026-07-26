package interfacecompatibility_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecompatibility"
)

func TestDocumentationBaselineKnownAnswerAndStrictRoundTrip(t *testing.T) {
	t.Parallel()

	content := []byte("# Interface reference\n")
	input := interfacecompatibility.DocumentationInput{
		Path: "generated/docs/api.md",
		Kind: interfacecompatibility.DocumentationKindInterfaceReference,
		Data: content,
	}
	baseline, err := interfacecompatibility.NewDocumentation(
		[]interfacecompatibility.DocumentationInput{input},
	)
	if err != nil || !baseline.Valid() {
		t.Fatalf("NewDocumentation = %#v, %v", baseline, err)
	}
	const (
		wantContentDigest    = "sha256:1ba9cf1cbcd6dc1f6af76a2368a3757a8ef0612e84a9b60de46b8113d0e01807"
		wantProjectionDigest = "sha256:4f8ef97ae540e21db2d41bb74b3a3854866a359d275b0c533cdc8964a4cdeff7"
		wantDigest           = "sha256:66eb45e1207ae688a77481047d317d5c6ba481b00e663fd31bf0fcbf2b0cebc1"
	)
	if baseline.Schema() != interfacecompatibility.DocumentationSchema ||
		baseline.Digest() != wantDigest {
		t.Fatalf(
			"documentation schema = %q, digest = %q, canonical = %s",
			baseline.Schema(),
			baseline.Digest(),
			baseline.CanonicalJSON(),
		)
	}
	wantRecord := fmt.Sprintf(
		`{"schema":"%s","artifacts":[{"path":"generated/docs/api.md","kind":"interface_reference","content_digest":"%s","projection_digest":"%s"}],"digest":"%s"}`,
		interfacecompatibility.DocumentationSchema,
		wantContentDigest,
		wantProjectionDigest,
		wantDigest,
	)
	if string(baseline.RecordJSON()) != wantRecord {
		t.Fatalf("documentation record = %s, want %s", baseline.RecordJSON(), wantRecord)
	}
	artifacts := baseline.Artifacts()
	if len(artifacts) != 1 ||
		!artifacts[0].Valid() ||
		artifacts[0].Path() != input.Path ||
		artifacts[0].Kind() != input.Kind ||
		artifacts[0].ContentDigest() != wantContentDigest ||
		artifacts[0].ProjectionDigest() != wantProjectionDigest {
		t.Fatalf("documentation artifacts = %#v", artifacts)
	}
	if bytes.Contains(baseline.RecordJSON(), content) {
		t.Fatalf("documentation record retained raw artifact bytes: %s", baseline.RecordJSON())
	}

	decoded, err := interfacecompatibility.DecodeDocumentation(baseline.RecordJSON())
	if err != nil ||
		!decoded.Valid() ||
		decoded.Digest() != baseline.Digest() ||
		!bytes.Equal(decoded.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("DecodeDocumentation = %#v, %v", decoded, err)
	}
}

func TestDocumentationBaselineIsDeterministicEmptyAndDefensive(t *testing.T) {
	t.Parallel()

	empty, err := interfacecompatibility.NewDocumentation(nil)
	if err != nil || !empty.Valid() {
		t.Fatalf("NewDocumentation(empty) = %#v, %v", empty, err)
	}
	const (
		wantEmptyDigest = "sha256:90c95bc238cd6ae856953b171a0f43f2cd2dfa2443b1f53a98c7e22d90922054"
		wantEmptyRecord = `{"schema":"plystra.interface-documentation-baseline/v1","artifacts":[],"digest":"sha256:90c95bc238cd6ae856953b171a0f43f2cd2dfa2443b1f53a98c7e22d90922054"}`
	)
	if empty.Digest() != wantEmptyDigest ||
		string(empty.RecordJSON()) != wantEmptyRecord ||
		len(empty.Artifacts()) != 0 {
		t.Fatalf("empty documentation baseline = %s, digest %q", empty.RecordJSON(), empty.Digest())
	}

	referenceData := []byte("reference")
	inputs := []interfacecompatibility.DocumentationInput{
		{
			Path: "generated/docs/openapi.json",
			Kind: interfacecompatibility.DocumentationKindOpenAPI,
			Data: []byte(`{"openapi":"3.1.0"}`),
		},
		{
			Path: "generated/docs/api.md",
			Kind: interfacecompatibility.DocumentationKindInterfaceReference,
			Data: referenceData,
		},
	}
	first, err := interfacecompatibility.NewDocumentation(inputs)
	if err != nil {
		t.Fatalf("NewDocumentation(first): %v", err)
	}
	second, err := interfacecompatibility.NewDocumentation(
		[]interfacecompatibility.DocumentationInput{inputs[1], inputs[0]},
	)
	if err != nil {
		t.Fatalf("NewDocumentation(second): %v", err)
	}
	if first.Digest() != second.Digest() ||
		!bytes.Equal(first.RecordJSON(), second.RecordJSON()) {
		t.Fatalf(
			"input order changed documentation baseline:\n%s\n%s",
			first.RecordJSON(),
			second.RecordJSON(),
		)
	}
	if got := []string{
		first.Artifacts()[0].Path(),
		first.Artifacts()[1].Path(),
	}; !reflect.DeepEqual(got, []string{
		"generated/docs/api.md",
		"generated/docs/openapi.json",
	}) {
		t.Fatalf("ordered documentation paths = %#v", got)
	}

	digestBeforeMutation := first.Digest()
	referenceData[0] ^= 0xff
	inputs[1].Data = []byte("replacement")
	record := first.RecordJSON()
	canonical := first.CanonicalJSON()
	artifacts := first.Artifacts()
	record[0] ^= 0xff
	canonical[0] ^= 0xff
	artifacts[0] = interfacecompatibility.DocumentationArtifact{}
	if !first.Valid() ||
		first.Digest() != digestBeforeMutation ||
		first.Artifacts()[0].Path() != "generated/docs/api.md" {
		t.Fatal("DocumentationBaseline exposed mutable input or internal storage")
	}
}

func TestDocumentationComparisonClassifiesAddedRemovedAndChangedArtifacts(t *testing.T) {
	t.Parallel()

	previous := documentationBaseline(t,
		documentationInput(
			"generated/docs/api.md",
			interfacecompatibility.DocumentationKindInterfaceReference,
			"reference-v1",
		),
		documentationInput(
			"generated/docs/legacy.json",
			interfacecompatibility.DocumentationKindOpenAPI,
			"legacy",
		),
	)
	current := documentationBaseline(t,
		documentationInput(
			"generated/docs/api.md",
			interfacecompatibility.DocumentationKindInterfaceReference,
			"reference-v2",
		),
		documentationInput(
			"generated/docs/openapi.json",
			interfacecompatibility.DocumentationKindOpenAPI,
			"openapi",
		),
	)
	comparison, err := interfacecompatibility.CompareDocumentation(previous, current)
	if err != nil || comparison.Clean() || !comparison.Valid() {
		t.Fatalf("CompareDocumentation = %#v, %v", comparison.Changes(), err)
	}
	changes := comparison.Changes()
	got := make([]string, len(changes))
	for index, change := range changes {
		got[index] = string(change.Kind()) + ":" + change.Path() + ":" +
			documentationClassSummary(change.Classes())
	}
	if !reflect.DeepEqual(got, []string{
		"changed:generated/docs/api.md:content",
		"removed:generated/docs/legacy.json:kind,content",
		"added:generated/docs/openapi.json:kind,content",
	}) {
		t.Fatalf("documentation changes = %#v", got)
	}
	if _, exists := changes[1].CurrentArtifact(); exists {
		t.Fatal("removed documentation artifact unexpectedly has a current view")
	}
	if _, exists := changes[2].PreviousArtifact(); exists {
		t.Fatal("added documentation artifact unexpectedly has a previous view")
	}
	classes := changes[0].Classes()
	classes[0] = interfacecompatibility.DocumentationClass("mutated")
	changes[0] = interfacecompatibility.DocumentationChange{}
	if comparison.Changes()[0].Classes()[0] !=
		interfacecompatibility.DocumentationClassContent {
		t.Fatal("DocumentationComparison exposed mutable change storage")
	}
}

func TestDocumentationComparisonIsolatesKindAndContentClasses(t *testing.T) {
	t.Parallel()

	baseInput := documentationInput(
		"generated/docs/api.md",
		interfacecompatibility.DocumentationKindInterfaceReference,
		"same",
	)
	base := documentationBaseline(t, baseInput)
	tests := []struct {
		name    string
		current interfacecompatibility.DocumentationInput
		classes []interfacecompatibility.DocumentationClass
	}{
		{
			name: "kind",
			current: documentationInput(
				baseInput.Path,
				interfacecompatibility.DocumentationKindOpenAPI,
				"same",
			),
			classes: []interfacecompatibility.DocumentationClass{
				interfacecompatibility.DocumentationClassKind,
			},
		},
		{
			name: "content",
			current: documentationInput(
				baseInput.Path,
				baseInput.Kind,
				"different",
			),
			classes: []interfacecompatibility.DocumentationClass{
				interfacecompatibility.DocumentationClassContent,
			},
		},
		{
			name: "kind and content",
			current: documentationInput(
				baseInput.Path,
				interfacecompatibility.DocumentationKindOpenAPI,
				"different",
			),
			classes: []interfacecompatibility.DocumentationClass{
				interfacecompatibility.DocumentationClassKind,
				interfacecompatibility.DocumentationClassContent,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			current := documentationBaseline(t, test.current)
			comparison, err := interfacecompatibility.CompareDocumentation(base, current)
			if err != nil || comparison.Clean() || !comparison.Valid() {
				t.Fatalf("CompareDocumentation = %#v, %v", comparison.Changes(), err)
			}
			changes := comparison.Changes()
			if len(changes) != 1 ||
				changes[0].Kind() != interfacecompatibility.ChangeChanged ||
				!reflect.DeepEqual(changes[0].Classes(), test.classes) {
				t.Fatalf("documentation classes = %#v", changes)
			}
			previous, previousExists := changes[0].PreviousArtifact()
			currentArtifact, currentExists := changes[0].CurrentArtifact()
			if !previousExists ||
				!currentExists ||
				!previous.Valid() ||
				!currentArtifact.Valid() ||
				previous.ProjectionDigest() == currentArtifact.ProjectionDigest() {
				t.Fatalf(
					"documentation artifact views = %#v, %t -> %#v, %t",
					previous,
					previousExists,
					currentArtifact,
					currentExists,
				)
			}
		})
	}
}

func TestDocumentationPathChangesAreRemovedAndAdded(t *testing.T) {
	t.Parallel()

	previous := documentationBaseline(t, documentationInput(
		"generated/docs/api.md",
		interfacecompatibility.DocumentationKindInterfaceReference,
		"same",
	))
	current := documentationBaseline(t, documentationInput(
		"generated/docs/reference.md",
		interfacecompatibility.DocumentationKindInterfaceReference,
		"same",
	))
	comparison, err := interfacecompatibility.CompareDocumentation(previous, current)
	if err != nil || !comparison.Valid() {
		t.Fatalf("CompareDocumentation = %#v, %v", comparison, err)
	}
	changes := comparison.Changes()
	if len(changes) != 2 ||
		changes[0].Kind() != interfacecompatibility.ChangeRemoved ||
		changes[1].Kind() != interfacecompatibility.ChangeAdded {
		t.Fatalf("path-change documentation comparison = %#v", changes)
	}
}

func TestNewDocumentationRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	valid := documentationInput(
		"generated/docs/api.md",
		interfacecompatibility.DocumentationKindInterfaceReference,
		"reference",
	)
	tests := map[string][]interfacecompatibility.DocumentationInput{
		"unknown kind": {
			{Path: valid.Path, Kind: interfacecompatibility.DocumentationKind("html"), Data: valid.Data},
		},
		"outside generated docs": {
			{Path: "generated/api.md", Kind: valid.Kind, Data: valid.Data},
		},
		"absolute": {
			{Path: "/generated/docs/api.md", Kind: valid.Kind, Data: valid.Data},
		},
		"traversal": {
			{Path: "generated/docs/../secret", Kind: valid.Kind, Data: valid.Data},
		},
		"backslash": {
			{Path: `generated\docs\api.md`, Kind: valid.Kind, Data: valid.Data},
		},
		"nested backslash": {
			{Path: `generated/docs/nested\api.md`, Kind: valid.Kind, Data: valid.Data},
		},
		"invalid UTF-8": {
			{
				Path: "generated/docs/" + string([]byte{0xff}) + ".md",
				Kind: valid.Kind,
				Data: valid.Data,
			},
		},
		"root only": {
			{Path: "generated/docs/", Kind: valid.Kind, Data: valid.Data},
		},
		"duplicate": {valid, valid},
	}
	for name, inputs := range tests {
		name, inputs := name, inputs
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			baseline, err := interfacecompatibility.NewDocumentation(inputs)
			if !errors.Is(err, interfacecompatibility.ErrInvalid) || baseline.Valid() {
				t.Fatalf("NewDocumentation(%s) = %#v, %v", name, baseline, err)
			}
		})
	}

	oversized := make([]interfacecompatibility.DocumentationInput, 4097)
	for index := range oversized {
		oversized[index] = documentationInput(
			fmt.Sprintf("generated/docs/%04d.md", index),
			interfacecompatibility.DocumentationKindInterfaceReference,
			"",
		)
	}
	if baseline, err := interfacecompatibility.NewDocumentation(oversized); !errors.Is(
		err,
		interfacecompatibility.ErrInvalid,
	) || baseline.Valid() {
		t.Fatalf("NewDocumentation(oversized) = %#v, %v", baseline, err)
	}
}

func TestDecodeDocumentationRejectsInvalidOwnedHistory(t *testing.T) {
	t.Parallel()

	baseline := documentationBaseline(t,
		documentationInput(
			"generated/docs/api.md",
			interfacecompatibility.DocumentationKindInterfaceReference,
			"reference",
		),
		documentationInput(
			"generated/docs/openapi.json",
			interfacecompatibility.DocumentationKindOpenAPI,
			"openapi",
		),
	)
	record := baseline.RecordJSON()
	artifacts := documentationRecordArtifacts(t, record)
	duplicate := documentationRecordWithArtifacts(t, record, []any{
		artifacts[0],
		artifacts[0],
	})
	unsorted := documentationRecordWithArtifacts(t, record, []any{
		artifacts[1],
		artifacts[0],
	})
	nilArtifacts := documentationRecordWithArtifacts(t, record, nil)

	first := baseline.Artifacts()[0]
	tests := map[string][]byte{
		"empty": nil,
		"unknown top-level field": bytes.Replace(
			record,
			[]byte(`"schema":`),
			[]byte(`"unknown":true,"schema":`),
			1,
		),
		"unknown artifact field": bytes.Replace(
			record,
			[]byte(`"path":`),
			[]byte(`"unknown":true,"path":`),
			1,
		),
		"unknown schema": bytes.Replace(
			record,
			[]byte(interfacecompatibility.DocumentationSchema),
			[]byte("plystra.interface-documentation-baseline/v2"),
			1,
		),
		"unknown kind": bytes.Replace(
			record,
			[]byte(`"kind":"interface_reference"`),
			[]byte(`"kind":"html"`),
			1,
		),
		"duplicate path": duplicate,
		"unsorted paths": unsorted,
		"unsafe path": bytes.Replace(
			record,
			[]byte(`generated/docs/api.md`),
			[]byte(`generated/docs/../secret`),
			1,
		),
		"invalid content digest": bytes.Replace(
			record,
			[]byte(first.ContentDigest()),
			[]byte("sha256:not-a-digest"),
			1,
		),
		"tampered projection digest": bytes.Replace(
			record,
			[]byte(first.ProjectionDigest()),
			[]byte(testDigest("tampered-projection")),
			1,
		),
		"tampered top-level digest": tamperLastDigest(record),
		"noncanonical whitespace":   append(append([]byte(nil), record...), '\n'),
		"trailing JSON": append(
			append([]byte(nil), record...),
			[]byte(` {}`)...,
		),
		"malformed JSON": []byte(`{"schema":`),
		"nil artifacts":  nilArtifacts,
		"oversized": bytes.Repeat(
			[]byte{'x'},
			int(interfacecompatibility.DocumentationMaximumBytes)+1,
		),
	}
	for name, data := range tests {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decoded, err := interfacecompatibility.DecodeDocumentation(data)
			if !errors.Is(err, interfacecompatibility.ErrHistory) || decoded.Valid() {
				t.Fatalf("DecodeDocumentation(%s) = %#v, %v", name, decoded, err)
			}
		})
	}
}

func TestReconcileDocumentationRequiresExactPriorOwnershipAndCanBeClean(t *testing.T) {
	t.Parallel()

	input := documentationInput(
		"generated/docs/api.md",
		interfacecompatibility.DocumentationKindInterfaceReference,
		"reference",
	)
	initial, comparison, err := interfacecompatibility.ReconcileDocumentation(
		[]interfacecompatibility.DocumentationInput{input},
		nil,
		false,
	)
	if err != nil ||
		!initial.Valid() ||
		!comparison.Valid() ||
		comparison.Clean() ||
		len(comparison.Changes()) != 1 ||
		comparison.Changes()[0].Kind() != interfacecompatibility.ChangeAdded {
		t.Fatalf(
			"ReconcileDocumentation(initial) = %#v, %#v, %v",
			initial,
			comparison,
			err,
		)
	}
	repeated, comparison, err := interfacecompatibility.ReconcileDocumentation(
		[]interfacecompatibility.DocumentationInput{input},
		initial.RecordJSON(),
		true,
	)
	if err != nil ||
		!repeated.Valid() ||
		repeated.Digest() != initial.Digest() ||
		!comparison.Valid() ||
		!comparison.Clean() {
		t.Fatalf(
			"ReconcileDocumentation(repeated) = %#v, %#v, %v",
			repeated,
			comparison,
			err,
		)
	}
	if _, _, err := interfacecompatibility.ReconcileDocumentation(
		[]interfacecompatibility.DocumentationInput{input},
		[]byte("{}"),
		false,
	); !errors.Is(err, interfacecompatibility.ErrHistory) {
		t.Fatalf("ReconcileDocumentation(absent bytes) error = %v", err)
	}
	if _, _, err := interfacecompatibility.ReconcileDocumentation(
		[]interfacecompatibility.DocumentationInput{input},
		[]byte("{}"),
		true,
	); !errors.Is(err, interfacecompatibility.ErrHistory) {
		t.Fatalf("ReconcileDocumentation(malformed history) error = %v", err)
	}
	if comparison, err := interfacecompatibility.CompareDocumentation(
		interfacecompatibility.DocumentationBaseline{},
		initial,
	); !errors.Is(err, interfacecompatibility.ErrInvalid) || comparison.Valid() {
		t.Fatalf("CompareDocumentation(invalid) = %#v, %v", comparison, err)
	}
}

func documentationBaseline(
	t testing.TB,
	inputs ...interfacecompatibility.DocumentationInput,
) interfacecompatibility.DocumentationBaseline {
	t.Helper()
	baseline, err := interfacecompatibility.NewDocumentation(inputs)
	if err != nil {
		t.Fatalf("NewDocumentation: %v", err)
	}
	return baseline
}

func documentationInput(
	artifactPath string,
	kind interfacecompatibility.DocumentationKind,
	content string,
) interfacecompatibility.DocumentationInput {
	return interfacecompatibility.DocumentationInput{
		Path: artifactPath,
		Kind: kind,
		Data: []byte(content),
	}
}

func documentationClassSummary(
	classes []interfacecompatibility.DocumentationClass,
) string {
	values := make([]string, len(classes))
	for index, class := range classes {
		values[index] = string(class)
	}
	return strings.Join(values, ",")
}

func documentationRecordArtifacts(t testing.TB, record []byte) []any {
	t.Helper()
	var value struct {
		Artifacts []any `json:"artifacts"`
	}
	if err := json.Unmarshal(record, &value); err != nil {
		t.Fatalf("json.Unmarshal(documentation record): %v", err)
	}
	return value.Artifacts
}

func documentationRecordWithArtifacts(
	t testing.TB,
	record []byte,
	artifacts []any,
) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(record, &value); err != nil {
		t.Fatalf("json.Unmarshal(documentation record): %v", err)
	}
	value["artifacts"] = artifacts
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(documentation record): %v", err)
	}
	return result
}

func tamperLastDigest(record []byte) []byte {
	result := append([]byte(nil), record...)
	index := bytes.LastIndex(result, []byte(`"digest":"sha256:`))
	if index < 0 {
		panic("record has no top-level digest")
	}
	index += len(`"digest":"sha256:`)
	if result[index] == '0' {
		result[index] = '1'
	} else {
		result[index] = '0'
	}
	return result
}
