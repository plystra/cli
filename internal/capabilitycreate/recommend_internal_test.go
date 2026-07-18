package capabilitycreate

import (
	"slices"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
)

func TestNearbyCapabilitiesReturnsHighestDeterministicExactVersions(t *testing.T) {
	t.Parallel()

	reference := mustRecommendationReference(t, "record.creat")
	visible := []capabilityid.Identifier{
		mustRecommendationID(t, "records.creat/v2"),
		mustRecommendationID(t, "record.create/v1"),
		mustRecommendationID(t, "records.creat/v4"),
		mustRecommendationID(t, "unrelated.name/v9"),
		mustRecommendationID(t, "record.create/v1"),
	}
	got := nearbyCapabilities(reference, visible)
	want := []capabilityid.Identifier{
		mustRecommendationID(t, "record.create/v1"),
		mustRecommendationID(t, "records.creat/v4"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("nearbyCapabilities = %v, want %v", got, want)
	}

	exactVisible := append(visible, mustRecommendationID(t, "record.creat/v3"))
	if got := nearbyCapabilities(reference, exactVisible); len(got) != 0 {
		t.Fatalf("same-name version progression recommended nearby contracts: %v", got)
	}
}

func TestOneEditApartRecognizesConservativeTypoForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		left  string
		right string
		want  bool
	}{
		{left: "email.sned", right: "email.send", want: true},
		{left: "record.create", right: "records.create", want: true},
		{left: "records.create", right: "record.create", want: true},
		{left: "email.send", right: "email.sent", want: true},
		{left: "email.send", right: "email.send"},
		{left: "email.send", right: "emails.sent"},
		{left: "record.create", right: "records.creates"},
	}
	for _, test := range tests {
		if got := oneEditApart(test.left, test.right); got != test.want {
			t.Errorf("oneEditApart(%q, %q) = %t, want %t", test.left, test.right, got, test.want)
		}
	}
}

func TestRecommendationAccessorsAreDefensive(t *testing.T) {
	t.Parallel()

	recommendation := mustRecommendationID(t, "email.send/v1")
	plan := Plan{recommendations: []capabilityid.Identifier{recommendation}}
	result := Result{recommendations: []capabilityid.Identifier{recommendation}}
	planValues := plan.Recommendations()
	resultValues := result.Recommendations()
	planValues[0] = capabilityid.Identifier{}
	resultValues[0] = capabilityid.Identifier{}
	if plan.Recommendations()[0] != recommendation || result.Recommendations()[0] != recommendation {
		t.Fatal("recommendation accessors exposed mutable storage")
	}
}

func mustRecommendationReference(t *testing.T, value string) capabilityid.Reference {
	t.Helper()
	reference, err := capabilityid.ParseReference(value)
	if err != nil {
		t.Fatalf("ParseReference(%q): %v", value, err)
	}
	return reference
}

func mustRecommendationID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return identifier
}
