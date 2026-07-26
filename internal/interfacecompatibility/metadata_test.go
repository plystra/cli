package interfacecompatibility_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecompatibility"
)

func TestMetadataBaselineKnownAnswerAndStrictRoundTrip(t *testing.T) {
	t.Parallel()

	input := metadataInput("records.echo/v1", "v1")
	baseline, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{input})
	if err != nil || !baseline.Valid() {
		t.Fatalf("NewMetadata = %#v, %v", baseline, err)
	}
	const wantDigest = "sha256:afb51698d6075718bdd62fbb0f7daf1beb779d9048219353a2f255de4899a2ef"
	if baseline.Schema() != interfacecompatibility.MetadataSchema ||
		baseline.Digest() != wantDigest {
		t.Fatalf("metadata baseline schema = %q, digest = %q, canonical = %s", baseline.Schema(), baseline.Digest(), baseline.CanonicalJSON())
	}
	wantRecord := strings.TrimSpace(fmt.Sprintf(`
{"schema":"plystra.interface-metadata-baseline/v1","interfaces":[{"id":"records.echo/v1","contract_digest":"%s","documentation_digest":"%s","example_digest":"%s"}],"digest":"%s"}
`, input.ContractDigest, input.DocumentationDigest, input.ExampleDigest, wantDigest))
	if string(baseline.RecordJSON()) != wantRecord {
		t.Fatalf("metadata record = %s, want %s", baseline.RecordJSON(), wantRecord)
	}

	interfaces := baseline.Interfaces()
	if len(interfaces) != 1 ||
		interfaces[0].ID() != input.ID ||
		interfaces[0].ContractDigest() != input.ContractDigest ||
		interfaces[0].DocumentationDigest() != input.DocumentationDigest ||
		interfaces[0].ExampleDigest() != input.ExampleDigest {
		t.Fatalf("metadata Interfaces = %#v", interfaces)
	}
	interfaces[0] = interfacecompatibility.MetadataInterface{}
	if !baseline.Valid() || baseline.Interfaces()[0].ID() != input.ID {
		t.Fatal("MetadataBaseline exposed mutable internal storage")
	}

	decoded, err := interfacecompatibility.DecodeMetadata(baseline.RecordJSON())
	if err != nil || !decoded.Valid() ||
		decoded.Digest() != baseline.Digest() ||
		!bytes.Equal(decoded.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("DecodeMetadata = %#v, %v", decoded, err)
	}
}

func TestMetadataBaselineIsDeterministicAndComparesEachClass(t *testing.T) {
	t.Parallel()

	firstInput := metadataInput("records.echo/v1", "v1")
	secondInput := metadataInput("accounts.read/v1", "v1")
	first, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{firstInput, secondInput})
	if err != nil {
		t.Fatalf("NewMetadata(first): %v", err)
	}
	second, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{secondInput, firstInput})
	if err != nil {
		t.Fatalf("NewMetadata(second): %v", err)
	}
	if first.Digest() != second.Digest() || !bytes.Equal(first.RecordJSON(), second.RecordJSON()) {
		t.Fatalf("input order changed metadata baseline:\n%s\n%s", first.RecordJSON(), second.RecordJSON())
	}
	if got := []string{first.Interfaces()[0].ID(), first.Interfaces()[1].ID()}; !reflect.DeepEqual(got, []string{"accounts.read/v1", "records.echo/v1"}) {
		t.Fatalf("ordered Interface IDs = %#v", got)
	}

	base, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{firstInput})
	if err != nil {
		t.Fatalf("NewMetadata(base): %v", err)
	}
	tests := []struct {
		name  string
		class interfacecompatibility.MetadataClass
		edit  func(*interfacecompatibility.MetadataInput)
	}{
		{
			name:  "contract",
			class: interfacecompatibility.MetadataClassContract,
			edit: func(input *interfacecompatibility.MetadataInput) {
				input.ContractDigest = testDigest("contract-v2")
			},
		},
		{
			name:  "documentation",
			class: interfacecompatibility.MetadataClassDocumentation,
			edit: func(input *interfacecompatibility.MetadataInput) {
				input.DocumentationDigest = testDigest("documentation-v2")
			},
		},
		{
			name:  "examples",
			class: interfacecompatibility.MetadataClassExamples,
			edit: func(input *interfacecompatibility.MetadataInput) {
				input.ExampleDigest = testDigest("examples-v2")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			changedInput := firstInput
			test.edit(&changedInput)
			changed, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{changedInput})
			if err != nil {
				t.Fatalf("NewMetadata(changed): %v", err)
			}
			comparison, err := interfacecompatibility.CompareMetadata(base, changed)
			if err != nil || comparison.Clean() || !comparison.Valid() {
				t.Fatalf("CompareMetadata = %#v, %v", comparison.Changes(), err)
			}
			changes := comparison.Changes()
			if len(changes) != 1 ||
				changes[0].Kind() != interfacecompatibility.ChangeChanged ||
				changes[0].ID() != firstInput.ID ||
				!reflect.DeepEqual(changes[0].Classes(), []interfacecompatibility.MetadataClass{test.class}) {
				t.Fatalf("metadata changes = %#v", changes)
			}
			previousDigest, previousExists := changes[0].PreviousDigest(test.class)
			currentDigest, currentExists := changes[0].CurrentDigest(test.class)
			if !previousExists || !currentExists || previousDigest == currentDigest {
				t.Fatalf("class %s digests = %q, %t -> %q, %t", test.class, previousDigest, previousExists, currentDigest, currentExists)
			}
			classes := changes[0].Classes()
			classes[0] = interfacecompatibility.MetadataClass("mutated")
			if changes[0].Classes()[0] != test.class {
				t.Fatal("MetadataChange exposed mutable class storage")
			}
		})
	}
}

func TestMetadataComparisonClassifiesAddedRemovedAndChangedInterfaces(t *testing.T) {
	t.Parallel()

	recordsV1 := metadataInput("records.echo/v1", "v1")
	recordsV2 := metadataInput("records.echo/v1", "v2")
	accounts := metadataInput("accounts.read/v1", "v1")
	orders := metadataInput("orders.create/v1", "v1")
	previous, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{recordsV1, accounts})
	if err != nil {
		t.Fatalf("NewMetadata(previous): %v", err)
	}
	current, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{recordsV2, orders})
	if err != nil {
		t.Fatalf("NewMetadata(current): %v", err)
	}
	comparison, err := interfacecompatibility.CompareMetadata(previous, current)
	if err != nil || comparison.Clean() || !comparison.Valid() {
		t.Fatalf("CompareMetadata = %#v, %v", comparison.Changes(), err)
	}
	changes := comparison.Changes()
	got := make([]string, len(changes))
	for index, change := range changes {
		got[index] = string(change.Kind()) + ":" + change.ID() + ":" + metadataClassSummary(change.Classes())
	}
	if !reflect.DeepEqual(got, []string{
		"removed:accounts.read/v1:contract,documentation,examples",
		"added:orders.create/v1:contract,documentation,examples",
		"changed:records.echo/v1:contract,documentation,examples",
	}) {
		t.Fatalf("metadata changes = %#v", got)
	}
	if _, exists := changes[0].CurrentDigest(interfacecompatibility.MetadataClassContract); exists {
		t.Fatal("removed Interface unexpectedly has a current digest")
	}
	if _, exists := changes[1].PreviousDigest(interfacecompatibility.MetadataClassContract); exists {
		t.Fatal("added Interface unexpectedly has a previous digest")
	}
}

func TestDecodeMetadataRejectsMalformedTamperedAndNoncanonicalHistory(t *testing.T) {
	t.Parallel()

	baseline, err := interfacecompatibility.NewMetadata([]interfacecompatibility.MetadataInput{
		metadataInput("records.echo/v1", "v1"),
	})
	if err != nil {
		t.Fatalf("NewMetadata: %v", err)
	}
	record := baseline.RecordJSON()
	tamperedDigest := append([]byte(nil), record...)
	digestIndex := bytes.LastIndex(tamperedDigest, []byte(`"digest":"sha256:`))
	if digestIndex < 0 {
		t.Fatal("record has no top-level digest")
	}
	digestIndex += len(`"digest":"sha256:`)
	if tamperedDigest[digestIndex] == '0' {
		tamperedDigest[digestIndex] = '1'
	} else {
		tamperedDigest[digestIndex] = '0'
	}

	tests := map[string][]byte{
		"empty":          nil,
		"unknown field":  bytes.Replace(record, []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1),
		"unknown schema": bytes.Replace(record, []byte(interfacecompatibility.MetadataSchema), []byte("plystra.interface-metadata-baseline/v2"), 1),
		"invalid ID":     bytes.Replace(record, []byte("records.echo/v1"), []byte("records/v1"), 1),
		"invalid class digest": bytes.Replace(
			record,
			[]byte(`"contract_digest":"sha256:`),
			[]byte(`"contract_digest":"invalid:`),
			1,
		),
		"tampered digest": tamperedDigest,
		"whitespace":      append(append([]byte(nil), record...), '\n'),
		"trailing JSON":   append(append([]byte(nil), record...), []byte(` {}`)...),
		"malformed JSON":  []byte(`{"schema":`),
		"oversized":       bytes.Repeat([]byte{'x'}, int(interfacecompatibility.MetadataMaximumBytes)+1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decoded, err := interfacecompatibility.DecodeMetadata(data)
			if !errors.Is(err, interfacecompatibility.ErrHistory) || decoded.Valid() {
				t.Fatalf("DecodeMetadata = %#v, %v", decoded, err)
			}
		})
	}
}

func TestReconcileMetadataRequiresExactPriorOwnershipBytes(t *testing.T) {
	t.Parallel()

	input := metadataInput("records.echo/v1", "v1")
	initial, comparison, err := interfacecompatibility.ReconcileMetadata(
		[]interfacecompatibility.MetadataInput{input},
		nil,
		false,
	)
	if err != nil || !initial.Valid() || comparison.Clean() || !comparison.Valid() {
		t.Fatalf("ReconcileMetadata(initial) = %#v, %#v, %v", initial, comparison, err)
	}
	if changes := comparison.Changes(); len(changes) != 1 ||
		changes[0].Kind() != interfacecompatibility.ChangeAdded {
		t.Fatalf("initial metadata changes = %#v", changes)
	}
	repeated, comparison, err := interfacecompatibility.ReconcileMetadata(
		[]interfacecompatibility.MetadataInput{input},
		initial.RecordJSON(),
		true,
	)
	if err != nil || !repeated.Valid() || !comparison.Clean() || !comparison.Valid() {
		t.Fatalf("ReconcileMetadata(repeated) = %#v, %#v, %v", repeated, comparison, err)
	}
	if _, _, err := interfacecompatibility.ReconcileMetadata(
		[]interfacecompatibility.MetadataInput{input},
		[]byte("{}"),
		false,
	); !errors.Is(err, interfacecompatibility.ErrHistory) {
		t.Fatalf("ReconcileMetadata(absent bytes) error = %v", err)
	}
}

func metadataInput(identifier, version string) interfacecompatibility.MetadataInput {
	return interfacecompatibility.MetadataInput{
		ID:                  identifier,
		ContractDigest:      testDigest("contract-" + version),
		DocumentationDigest: testDigest("documentation-" + version),
		ExampleDigest:       testDigest("examples-" + version),
	}
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum)
}

func metadataClassSummary(classes []interfacecompatibility.MetadataClass) string {
	values := make([]string, len(classes))
	for index, class := range classes {
		values[index] = string(class)
	}
	return strings.Join(values, ",")
}
