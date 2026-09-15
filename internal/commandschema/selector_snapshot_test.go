package commandschema_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/commandschema"
)

func TestSelectorSnapshotIsCanonicalClosedAndDefensive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input commandschema.SelectorSnapshotInput
		want  string
	}{
		{
			name:  "default",
			input: commandschema.SelectorSnapshotInput{Mode: generation.ConfigurationModeDefault},
			want:  `{"selector":{"mode":"default"}}`,
		},
		{
			name:  "environment",
			input: commandschema.SelectorSnapshotInput{Mode: generation.ConfigurationModeEnvironment, Name: "production"},
			want:  `{"selector":{"mode":"environment","name":"production"}}`,
		},
		{
			name:  "explicit",
			input: commandschema.SelectorSnapshotInput{Mode: generation.ConfigurationModeExplicit, Path: "deploy/customer.yaml"},
			want:  `{"selector":{"mode":"explicit-config","path":"deploy/customer.yaml"}}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			snapshot, err := commandschema.NewSelectorSnapshot(test.input)
			if err != nil || !snapshot.Valid() {
				t.Fatalf("NewSelectorSnapshot = %#v, %v", snapshot, err)
			}
			if got := string(snapshot.CanonicalJSON()); got != test.want {
				t.Fatalf("CanonicalJSON = %s, want %s", got, test.want)
			}
			if snapshot.Mode() != test.input.Mode || snapshot.Name() != test.input.Name || snapshot.Path() != test.input.Path {
				t.Fatalf("snapshot accessors = mode %q name %q path %q", snapshot.Mode(), snapshot.Name(), snapshot.Path())
			}
			canonical := snapshot.CanonicalJSON()
			canonical[0] = '['
			if !snapshot.Valid() || bytes.HasPrefix(snapshot.CanonicalJSON(), []byte("[")) {
				t.Fatal("snapshot exposed mutable canonical JSON")
			}
		})
	}
}

func TestSelectorSnapshotRejectsInvalidModesAndValues(t *testing.T) {
	t.Parallel()

	tests := []commandschema.SelectorSnapshotInput{
		{},
		{Mode: "future"},
		{Mode: generation.ConfigurationModeDefault, Name: "production"},
		{Mode: generation.ConfigurationModeDefault, Path: "plystra.yaml"},
		{Mode: generation.ConfigurationModeEnvironment},
		{Mode: generation.ConfigurationModeEnvironment, Name: " "},
		{Mode: generation.ConfigurationModeEnvironment, Name: "."},
		{Mode: generation.ConfigurationModeEnvironment, Name: ".."},
		{Mode: generation.ConfigurationModeEnvironment, Name: "deploy/production"},
		{Mode: generation.ConfigurationModeEnvironment, Name: "production", Path: "plystra.production.yaml"},
		{Mode: generation.ConfigurationModeEnvironment, Name: "prod\nblue"},
		{Mode: generation.ConfigurationModeEnvironment, Name: strings.Repeat("x", 201)},
		{Mode: generation.ConfigurationModeExplicit},
		{Mode: generation.ConfigurationModeExplicit, Path: "."},
		{Mode: generation.ConfigurationModeExplicit, Path: "../deploy.yaml"},
		{Mode: generation.ConfigurationModeExplicit, Path: "/tmp/deploy.yaml"},
		{Mode: generation.ConfigurationModeExplicit, Path: `C:\\tmp\\deploy.yaml`},
		{Mode: generation.ConfigurationModeExplicit, Path: `deploy\\customer.yaml`},
		{Mode: generation.ConfigurationModeExplicit, Name: "production", Path: "deploy/customer.yaml"},
	}
	for _, input := range tests {
		snapshot, err := commandschema.NewSelectorSnapshot(input)
		if !errors.Is(err, commandschema.ErrSelectorSnapshot) || snapshot.Valid() {
			t.Fatalf("NewSelectorSnapshot(%#v) = %#v, %v; want ErrSelectorSnapshot", input, snapshot, err)
		}
	}
	if (commandschema.SelectorSnapshot{}).Valid() {
		t.Fatal("zero selector snapshot is valid")
	}
}
