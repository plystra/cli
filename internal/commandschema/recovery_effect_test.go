package commandschema_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
)

func TestRecoverySupportsClosedKindsAndCanonicalExecution(t *testing.T) {
	t.Parallel()

	verification := commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "check"}}
	tests := []commandschema.RecoveryInput{
		{
			ID:               "rerun-check",
			Kind:             commandschema.RecoveryExecute,
			Target:           commandschema.RecoveryTarget{Kind: "project", ID: "current"},
			Verification:     verification,
			WorkingDirectory: ".",
			Argv:             []string{"plystra", "check", "--format", "json"},
		},
		{
			ID:           "edit-project-source",
			Kind:         commandschema.RecoveryEditSource,
			Target:       commandschema.RecoveryTarget{Kind: "source", ID: "plystra.yaml"},
			Owner:        &commandschema.Owner{Module: "example.com/acme/app", Path: "plystra.yaml"},
			Verification: verification,
		},
		{
			ID:           "choose-provider",
			Kind:         commandschema.RecoveryChoose,
			Target:       commandschema.RecoveryTarget{Kind: "interface_selection", ID: "records.read/v1"},
			Verification: verification,
			Options: []commandschema.RecoveryOption{
				{Value: "example.com/acme/memory.New", WorkingDirectory: ".", Argv: []string{"plystra", "use", "records.read/v1", "example.com/acme/memory.New"}},
				{Value: "example.com/acme/sql.New"},
			},
		},
		{
			ID:           "install-git",
			Kind:         commandschema.RecoverySatisfyPrerequisite,
			Target:       commandschema.RecoveryTarget{Kind: "tool", ID: "git"},
			Verification: commandschema.Verification{WorkingDirectory: ".", Argv: []string{"git", "--version"}},
		},
		{
			ID:           "correct-invocation",
			Kind:         commandschema.RecoveryManual,
			Target:       commandschema.RecoveryTarget{Kind: "command", ID: "new"},
			Verification: commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "new", "--help"}},
		},
	}
	for _, input := range tests {
		input := input
		t.Run(input.ID, func(t *testing.T) {
			t.Parallel()
			action, err := commandschema.NewRecovery(input)
			if err != nil || !action.Valid() || action.ID() != input.ID || action.Kind() != input.Kind {
				t.Fatalf("NewRecovery = %#v, %v", action, err)
			}
			for _, required := range [][]byte{[]byte(`"schema":"plystra.recovery/v1"`), []byte(`"owner":`), []byte(`"provenance":[]`), []byte(`"selector":`), []byte(`"preconditions":[]`), []byte(`"effects":[]`), []byte(`"verification":`)} {
				if !bytes.Contains(action.CanonicalJSON(), required) {
					t.Fatalf("CanonicalJSON omits %s: %s", required, action.CanonicalJSON())
				}
			}
		})
	}
}

func TestRecoveryRejectsPlaceholderBearingExecutableCommands(t *testing.T) {
	t.Parallel()

	tests := []commandschema.RecoveryInput{
		{
			ID:               "placeholder-execute",
			Kind:             commandschema.RecoveryExecute,
			Target:           commandschema.RecoveryTarget{Kind: "command", ID: "new"},
			Verification:     commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "check"}},
			WorkingDirectory: ".",
			Argv:             []string{"plystra", "new", "<project-name>"},
		},
		{
			ID:           "placeholder-choice",
			Kind:         commandschema.RecoveryChoose,
			Target:       commandschema.RecoveryTarget{Kind: "command", ID: "new"},
			Verification: commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "check"}},
			Options:      []commandschema.RecoveryOption{{Value: "new-name", WorkingDirectory: ".", Argv: []string{"plystra", "new", "${PROJECT_NAME}"}}},
		},
	}
	for _, input := range tests {
		action, err := commandschema.NewRecovery(input)
		if !errors.Is(err, commandschema.ErrRecovery) || action.Valid() {
			t.Fatalf("NewRecovery(%s) = %#v, %v; want ErrRecovery", input.ID, action, err)
		}
	}
}

func TestRecoveryAcceptsFullyBoundComparisonArgument(t *testing.T) {
	t.Parallel()

	action, err := commandschema.NewRecovery(commandschema.RecoveryInput{
		ID:               "add-compatible-version",
		Kind:             commandschema.RecoveryExecute,
		Target:           commandschema.RecoveryTarget{Kind: "dependency", ID: "example.com/acme/platform"},
		Verification:     commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "check"}},
		WorkingDirectory: ".",
		Argv:             []string{"plystra", "add", "example.com/acme/platform@<v2.0.0"},
	})
	if err != nil || !action.Valid() {
		t.Fatalf("NewRecovery = %#v, %v", action, err)
	}
}

func TestEffectSupportsEveryClosedClassAndStableDispositionOrdering(t *testing.T) {
	t.Parallel()

	classes := []commandschema.EffectClass{
		commandschema.EffectProjectWrite,
		commandschema.EffectTemporaryFile,
		commandschema.EffectCacheMaterialization,
		commandschema.EffectDownload,
		commandschema.EffectTrustedCodeExecution,
		commandschema.EffectProcessStartup,
		commandschema.EffectBackendRead,
		commandschema.EffectBackendWrite,
		commandschema.EffectPublication,
	}
	effects := make([]commandschema.Effect, len(classes))
	for index, class := range classes {
		effect, err := commandschema.NewEffect(commandschema.EffectInput{
			ID:            "effect-" + effectClassSuffix(class),
			Class:         class,
			Phase:         "execute",
			Target:        "artifact",
			Owner:         commandschema.Owner{Module: "example.com/acme/app", Path: "."},
			Reason:        "requested_operation",
			Reversibility: "manual",
			Verification:  []string{"plystra", "check"},
		})
		if err != nil || !effect.Valid() {
			t.Fatalf("NewEffect(%q) = %#v, %v", class, effect, err)
		}
		effects[len(classes)-1-index] = effect
	}
	accounting := newEffects(t, commandschema.EffectsInput{Observed: effects})
	ordered := accounting.Observed()
	for index := 1; index < len(ordered); index++ {
		if string(ordered[index-1].Class()) > string(ordered[index].Class()) {
			t.Fatalf("observed effects are not stable: %q before %q", ordered[index-1].Class(), ordered[index].Class())
		}
	}
	if !accounting.Valid() || len(accounting.Planned()) != 0 || len(accounting.Skipped()) != 0 || len(accounting.Unverified()) != 0 {
		t.Fatalf("effect accounting is incomplete: %#v", accounting)
	}
}

func TestEffectAccountingRejectsDuplicatesAndUnsafeTargets(t *testing.T) {
	t.Parallel()

	effect := newProjectWriteEffect(t)
	if accounting, err := commandschema.NewEffects(commandschema.EffectsInput{Observed: []commandschema.Effect{effect}, Planned: []commandschema.Effect{effect}}); !errors.Is(err, commandschema.ErrEffects) || accounting.Valid() {
		t.Fatalf("duplicate accounting = %#v, %v", accounting, err)
	}
	unsafe, err := commandschema.NewEffect(commandschema.EffectInput{
		ID:            "unsafe-target",
		Class:         commandschema.EffectProjectWrite,
		Phase:         "commit",
		Target:        `C:\\Users\\secret\\app`,
		Owner:         commandschema.Owner{Module: "example.com/acme/app", Path: "."},
		Reason:        "requested_operation",
		Reversibility: "manual",
		Verification:  []string{"plystra", "check"},
	})
	if !errors.Is(err, commandschema.ErrEffect) || unsafe.Valid() {
		t.Fatalf("unsafe effect = %#v, %v", unsafe, err)
	}
}

func TestProjectOwnedSchemasAcceptInitialLocalModulePath(t *testing.T) {
	t.Parallel()

	verification := commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "check"}}
	recovery, err := commandschema.NewRecovery(commandschema.RecoveryInput{
		ID:           "edit-local-project",
		Kind:         commandschema.RecoveryEditSource,
		Target:       commandschema.RecoveryTarget{Kind: "source", ID: "plystra.yaml"},
		Owner:        &commandschema.Owner{Module: "my-app", Path: "plystra.yaml"},
		Provenance:   []commandschema.RecoverySource{{Module: "my-app", Path: "plystra.yaml", Kind: "configuration"}},
		Verification: verification,
	})
	if err != nil || !recovery.Valid() {
		t.Fatalf("NewRecovery(local module) = %#v, %v", recovery, err)
	}

	effect, err := commandschema.NewEffect(commandschema.EffectInput{
		ID:            "create-local-project",
		Class:         commandschema.EffectProjectWrite,
		Phase:         "commit",
		Target:        "my-app",
		Owner:         commandschema.Owner{Module: "my-app", Path: "."},
		Reason:        "requested_operation",
		Reversibility: "manual",
		Verification:  []string{"plystra", "check"},
	})
	if err != nil || !effect.Valid() {
		t.Fatalf("NewEffect(local module) = %#v, %v", effect, err)
	}
}

func effectClassSuffix(class commandschema.EffectClass) string {
	result := make([]byte, 0, len(class))
	for _, character := range []byte(class) {
		if character == '_' {
			character = '-'
		}
		result = append(result, character)
	}
	return string(result)
}
