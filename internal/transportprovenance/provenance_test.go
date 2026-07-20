package transportprovenance_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/transportprovenance"
)

func TestProvenanceValidatesEverySelectionModeDeterministically(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       transportprovenance.Input
		environment string
		selected    string
	}{
		{
			name:     "default",
			input:    defaultInput(),
			selected: "plystra.yaml",
		},
		{
			name: "environment",
			input: withInput(defaultInput(), func(input *transportprovenance.Input) {
				input.Mode = generation.ConfigurationModeEnvironment
				input.Environment = "production"
				input.SelectedPath = "plystra.production.yaml"
				input.SelectedDigest = testDigest("4")
			}),
			environment: "production",
			selected:    "plystra.production.yaml",
		},
		{
			name: "explicit config",
			input: withInput(defaultInput(), func(input *transportprovenance.Input) {
				input.Mode = generation.ConfigurationModeExplicit
				input.SelectedPath = "deploy/customer-a.yaml"
				input.SelectedDigest = testDigest("5")
			}),
			selected: "deploy/customer-a.yaml",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := transportprovenance.New(test.input)
			if err != nil || !first.Valid() {
				t.Fatalf("New = %#v, %v", first, err)
			}
			second, err := transportprovenance.New(test.input)
			if err != nil || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
				t.Fatalf("repeated New = %q/%s, %q/%s, %v", first.Digest(), first.CanonicalJSON(), second.Digest(), second.CanonicalJSON(), err)
			}
			if first.Mode() != test.input.Mode || first.Environment() != test.environment || first.RootPath() != "plystra.yaml" || first.RootDigest() != test.input.RootDigest || first.SelectedPath() != test.selected || first.SelectedDigest() != test.input.SelectedDigest || first.DependencyCompositionDigest() != test.input.DependencyCompositionDigest || first.ApplicationModelDigest() != test.input.ApplicationModelDigest {
				t.Fatalf("Provenance accessors do not preserve normalized input: %#v", first)
			}
			canonical := first.CanonicalJSON()
			canonical[0] = '!'
			if !first.Valid() || bytes.Equal(canonical, first.CanonicalJSON()) {
				t.Fatal("CanonicalJSON did not return a defensive copy")
			}
		})
	}
}

func TestProvenanceRejectsUnsafeOrInconsistentIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*transportprovenance.Input)
		want   string
	}{
		{name: "missing", mutate: func(input *transportprovenance.Input) { *input = transportprovenance.Input{} }, want: "root path"},
		{name: "absolute slash path", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeExplicit
			input.SelectedPath = "/private/deploy.yaml"
		}, want: "Project-relative"},
		{name: "absolute drive path", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeExplicit
			input.SelectedPath = "C:/private/deploy.yaml"
		}, want: "Project-relative"},
		{name: "traversal", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeExplicit
			input.SelectedPath = "../deploy.yaml"
		}, want: "Project-relative"},
		{name: "backslash", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeExplicit
			input.SelectedPath = `deploy\customer.yaml`
		}, want: "Project-relative"},
		{name: "invalid root digest", mutate: func(input *transportprovenance.Input) { input.RootDigest = "sha256:ABC" }, want: "root digest"},
		{name: "invalid selected digest", mutate: func(input *transportprovenance.Input) { input.SelectedDigest = testDigest("g") }, want: "selected digest"},
		{name: "invalid dependency digest", mutate: func(input *transportprovenance.Input) { input.DependencyCompositionDigest = "" }, want: "dependency-composition"},
		{name: "invalid model digest", mutate: func(input *transportprovenance.Input) { input.ApplicationModelDigest = "" }, want: "application-model"},
		{name: "unsupported mode", mutate: func(input *transportprovenance.Input) { input.Mode = "profile" }, want: "not supported"},
		{name: "default environment", mutate: func(input *transportprovenance.Input) { input.Environment = "production" }, want: "environment must be empty"},
		{name: "default selected path", mutate: func(input *transportprovenance.Input) { input.SelectedPath = "deploy/customer.yaml" }, want: "exact root"},
		{name: "default selected digest", mutate: func(input *transportprovenance.Input) { input.SelectedDigest = testDigest("4") }, want: "exact root"},
		{name: "unsafe environment", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeEnvironment
			input.Environment = "../production"
			input.SelectedPath = "plystra.../production.yaml"
		}, want: "safe filename"},
		{name: "environment path mismatch", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeEnvironment
			input.Environment = "production"
			input.SelectedPath = "plystra.staging.yaml"
		}, want: "production"},
		{name: "explicit environment", mutate: func(input *transportprovenance.Input) {
			input.Mode = generation.ConfigurationModeExplicit
			input.Environment = "production"
		}, want: "explicit-config"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := defaultInput()
			test.mutate(&input)
			provenance, err := transportprovenance.New(input)
			if !errors.Is(err, transportprovenance.ErrInvalid) || !strings.Contains(err.Error(), test.want) || provenance.Valid() {
				t.Fatalf("New = %#v, %v; want ErrInvalid containing %q", provenance, err, test.want)
			}
		})
	}
}

func TestProvenanceCanonicalFormCannotCarryConfigurationOrSecrets(t *testing.T) {
	t.Parallel()

	input := defaultInput()
	provenance, err := transportprovenance.New(input)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	canonical := string(provenance.CanonicalJSON())
	for _, forbidden := range []string{
		"PRIVATE_APPLICATION_TOKEN",
		"resolved-secret-value",
		`"config"`,
		`"value"`,
		`"secret"`,
		`C:\\private`,
		`generated/go`,
	} {
		if strings.Contains(canonical, forbidden) {
			t.Fatalf("canonical provenance contains forbidden configuration content %q: %s", forbidden, canonical)
		}
	}
}

func defaultInput() transportprovenance.Input {
	rootDigest := testDigest("1")
	return transportprovenance.Input{
		Mode:                        generation.ConfigurationModeDefault,
		RootPath:                    "plystra.yaml",
		RootDigest:                  rootDigest,
		SelectedPath:                "plystra.yaml",
		SelectedDigest:              rootDigest,
		DependencyCompositionDigest: testDigest("2"),
		ApplicationModelDigest:      testDigest("3"),
	}
}

func withInput(input transportprovenance.Input, mutate func(*transportprovenance.Input)) transportprovenance.Input {
	mutate(&input)
	return input
}

func testDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
