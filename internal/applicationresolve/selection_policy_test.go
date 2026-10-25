package applicationresolve_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/interfaceinventory"
)

func TestExecutableCandidateRechecksInputsAfterPolicyTooling(t *testing.T) {
	for _, mode := range []string{"unchanged", "changed document", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResolvedInterfaceProject(t)
			writeFile(t, filepath.Join(filepath.Dir(root), "kernel", "invocation", "policy.go"), "package invocation\n")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			options := selectionOptions(root)
			started := false
			options.ObservePolicySupport = func(event interfaceinventory.PackageLoadEvent) {
				if !event.GoStarted || started {
					return
				}
				started = true
				switch mode {
				case "changed document":
					writeFile(t, filepath.Join(root, "plystra.yaml"), "# concurrently changed\n{}\n")
				case "cancelled":
					cancel()
				}
			}
			inputs, err := applicationresolve.DiscoverSelectionInputs(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := inputs.ComposeCandidate(inputs.SelectedSnapshot().Data())
			if err != nil {
				t.Fatal(err)
			}
			err = inputs.ValidateExecutableCandidate(ctx, candidate)
			want := applicationresolve.ErrPolicyNotEnforced
			if mode == "changed document" {
				want = applicationresolve.ErrConcurrentChange
			} else if mode == "cancelled" {
				want = context.Canceled
			}
			if !started || !errors.Is(err, want) {
				t.Fatalf("policy query started=%v, error=%v; want %v", started, err, want)
			}
			if mode != "unchanged" && errors.Is(err, applicationresolve.ErrPolicyNotEnforced) {
				t.Fatalf("invalidated inputs retained a policy compatibility verdict: %v", err)
			}
		})
	}
}

func TestExecutableCandidateRejectsMissingContextAndInputs(t *testing.T) {
	for _, ctx := range []context.Context{nil, t.Context()} {
		if err := (applicationresolve.SelectionInputs{}).ValidateExecutableCandidate(ctx, applicationmeta.Composition{}); !errors.Is(err, applicationresolve.ErrResolve) {
			t.Fatalf("empty candidate validation = %v", err)
		}
	}
}
