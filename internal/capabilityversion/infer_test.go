package capabilityversion_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilityversion"
)

func TestInferPlansCapabilityVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		reference    string
		visible      []string
		target       string
		source       string
		highest      string
		action       capabilityversion.Action
		caution      capabilityversion.Caution
		confirmation bool
	}{
		{
			name:      "first implicit version",
			reference: "account.register",
			target:    "account.register/v1",
			action:    capabilityversion.ActionCreate,
		},
		{
			name:      "next version above highest without filling gaps",
			reference: "account.register",
			visible:   []string{"profile.get/v9", "account.register/v3", "account.register/v1"},
			target:    "account.register/v4",
			source:    "account.register/v3",
			highest:   "account.register/v3",
			action:    capabilityversion.ActionCreate,
		},
		{
			name:      "explicit first version",
			reference: "account.register/v1",
			target:    "account.register/v1",
			action:    capabilityversion.ActionCreate,
		},
		{
			name:      "explicit next version",
			reference: "account.register/v3",
			visible:   []string{"account.register/v1", "account.register/v2"},
			target:    "account.register/v3",
			source:    "account.register/v2",
			highest:   "account.register/v2",
			action:    capabilityversion.ActionCreate,
		},
		{
			name:         "explicit skipped version",
			reference:    "account.register/v5",
			visible:      []string{"account.register/v1", "account.register/v2"},
			target:       "account.register/v5",
			source:       "account.register/v2",
			highest:      "account.register/v2",
			action:       capabilityversion.ActionCreate,
			caution:      capabilityversion.CautionSkipped,
			confirmation: true,
		},
		{
			name:         "explicit version skips v1 without history",
			reference:    "account.register/v2",
			target:       "account.register/v2",
			action:       capabilityversion.ActionCreate,
			caution:      capabilityversion.CautionSkipped,
			confirmation: true,
		},
		{
			name:         "explicit old missing version does not copy backward",
			reference:    "account.register/v2",
			visible:      []string{"account.register/v1", "account.register/v3"},
			target:       "account.register/v2",
			highest:      "account.register/v3",
			action:       capabilityversion.ActionCreate,
			caution:      capabilityversion.CautionOlder,
			confirmation: true,
		},
		{
			name:         "existing exact version becomes implementation",
			reference:    "account.register/v2",
			visible:      []string{"account.register/v3", "account.register/v2", "account.register/v2"},
			target:       "account.register/v2",
			source:       "account.register/v2",
			highest:      "account.register/v3",
			action:       capabilityversion.ActionImplement,
			caution:      capabilityversion.CautionExisting,
			confirmation: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := capabilityversion.Infer(mustReference(t, test.reference), mustIdentifiers(t, test.visible))
			if err != nil {
				t.Fatalf("Infer: %v", err)
			}
			source, hasSource := plan.Source()
			highest, hasHighest := plan.HighestVisible()
			if plan.Target().String() != test.target || source.String() != test.source || hasSource != (test.source != "") || highest.String() != test.highest || hasHighest != (test.highest != "") || plan.Action() != test.action || plan.Caution() != test.caution || plan.RequiresConfirmation() != test.confirmation {
				t.Fatalf("plan = target %q, source %q/%t, highest %q/%t, action %q, caution %q, confirmation %t", plan.Target(), source, hasSource, highest, hasHighest, plan.Action(), plan.Caution(), plan.RequiresConfirmation())
			}
		})
	}
}

func TestInferRejectsInvalidInputsAndOverflow(t *testing.T) {
	t.Parallel()

	if plan, err := capabilityversion.Infer(capabilityid.Reference{}, nil); !errors.Is(err, capabilityversion.ErrInfer) || plan.Target().String() != "" {
		t.Fatalf("Infer(zero reference) = %#v, %v", plan, err)
	}
	if plan, err := capabilityversion.Infer(mustReference(t, "account.register"), []capabilityid.Identifier{{}}); !errors.Is(err, capabilityversion.ErrInfer) || plan.Target().String() != "" {
		t.Fatalf("Infer(zero visible) = %#v, %v", plan, err)
	}
	maximum := "account.register/v18446744073709551615"
	if plan, err := capabilityversion.Infer(mustReference(t, "account.register"), mustIdentifiers(t, []string{maximum})); !errors.Is(err, capabilityversion.ErrInfer) || !errors.Is(err, capabilityversion.ErrOverflow) || plan.Target().String() != "" {
		t.Fatalf("Infer(overflow) = %#v, %v", plan, err)
	}
	plan, err := capabilityversion.Infer(mustReference(t, maximum), mustIdentifiers(t, []string{maximum}))
	if err != nil || plan.Action() != capabilityversion.ActionImplement || plan.Target().String() != maximum {
		t.Fatalf("Infer(existing maximum) = %#v, %v", plan, err)
	}
}

func FuzzInfer(f *testing.F) {
	for _, seed := range []struct {
		reference string
		first     uint64
		second    uint64
		third     uint64
	}{
		{reference: "account.register", first: 0, second: 0, third: 0},
		{reference: "account.register", first: 1, second: 3, third: 3},
		{reference: "account.register/v2", first: 1, second: 3, third: 0},
		{reference: "bad", first: 1, second: 2, third: 3},
	} {
		f.Add(seed.reference, seed.first, seed.second, seed.third)
	}
	f.Fuzz(func(t *testing.T, referenceValue string, first, second, third uint64) {
		reference, err := capabilityid.ParseReference(referenceValue)
		if err != nil {
			return
		}
		visible := make([]capabilityid.Identifier, 0, 4)
		for _, major := range []uint64{first, second, third} {
			if major == 0 {
				continue
			}
			identifier, err := capabilityid.New(reference.Name(), major)
			if err != nil {
				t.Fatalf("New visible identifier: %v", err)
			}
			visible = append(visible, identifier)
		}
		other, err := capabilityid.New("other.operation", 99)
		if err != nil {
			t.Fatalf("New other identifier: %v", err)
		}
		visible = append(visible, other)

		plan, err := capabilityversion.Infer(reference, visible)
		if err != nil {
			if !errors.Is(err, capabilityversion.ErrInfer) {
				t.Fatalf("Infer returned unexpected error: %v", err)
			}
			return
		}
		if plan.Target().Name() != reference.Name() || plan.Target().Major() == 0 {
			t.Fatalf("target = %q for reference %q", plan.Target(), reference)
		}
		if plan.Action() != capabilityversion.ActionCreate && plan.Action() != capabilityversion.ActionImplement {
			t.Fatalf("unknown action %q", plan.Action())
		}
		if source, ok := plan.Source(); ok && source.Name() != reference.Name() {
			t.Fatalf("source = %q for reference %q", source, reference)
		}
		if highest, ok := plan.HighestVisible(); ok && highest.Name() != reference.Name() {
			t.Fatalf("highest = %q for reference %q", highest, reference)
		}
	})
}

func mustReference(t *testing.T, value string) capabilityid.Reference {
	t.Helper()
	reference, err := capabilityid.ParseReference(value)
	if err != nil {
		t.Fatalf("ParseReference(%q): %v", value, err)
	}
	return reference
}

func mustIdentifiers(t *testing.T, values []string) []capabilityid.Identifier {
	t.Helper()
	identifiers := make([]capabilityid.Identifier, len(values))
	for index, value := range values {
		identifier, err := capabilityid.Parse(value)
		if err != nil {
			t.Fatalf("Parse(%q): %v", value, err)
		}
		identifiers[index] = identifier
	}
	return identifiers
}
