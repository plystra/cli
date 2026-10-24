package projectstage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
)

func TestMain(main *testing.M) {
	if mode := os.Getenv("PLYSTRA_PROJECTSTAGE_HELPER"); mode != "" {
		if !slices.Equal(os.Args[1:], []string{"env", "-json", "GOWORK", "GOFLAGS"}) {
			os.Exit(3)
		}
		switch mode {
		case "valid":
			_, _ = fmt.Fprintln(os.Stdout, `{"GOWORK":"off","GOFLAGS":""}`)
		case "failure":
			os.Exit(4)
		case "invalid-json":
			_, _ = fmt.Fprintln(os.Stdout, "invalid metadata")
		case "oversized":
			_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", (1<<20)+1))
		case "unsafe-flags":
			_, _ = fmt.Fprintln(os.Stdout, `{"GOWORK":"off","GOFLAGS":"-overlay=private.json"}`)
		default:
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(main.Run())
}

func TestCreateObservesActualEffects(t *testing.T) {
	root, parent := fixture(t)
	write(t, root, "assets/private.txt", "PRIVATE_CAPTURED_VALUE")
	before := tree(t, root)
	var events []projectstage.Event
	stage, err := projectstage.Create(t.Context(), projectstage.Options{
		Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off"),
		Observe: func(event projectstage.Event) {
			events = append(events, event)
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Error(err)
				return
			}
			switch event {
			case projectstage.GoEnvironmentStarted:
				if len(entries) != 0 {
					t.Error("temporary tree already exists at Go environment observation")
				}
			case projectstage.TemporaryTreeCreated:
				if len(entries) != 1 || !entries[0].IsDir() {
					t.Errorf("temporary creation observed without allocated tree: %v", entries)
					return
				}
				children, err := os.ReadDir(filepath.Join(parent, entries[0].Name()))
				if err != nil || len(children) != 0 {
					t.Errorf("copy preceded temporary creation observation: %v, %v", children, err)
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if !slices.Equal(events, []projectstage.Event{projectstage.GoEnvironmentStarted, projectstage.TemporaryTreeCreated}) {
		t.Fatalf("events = %v", events)
	}
	if !reflect.DeepEqual(before, tree(t, root)) {
		t.Fatal("observation changed original input")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("cleanup left temporary output: %v, %v", entries, err)
	}
}

func TestCreateObservesEffectsOnFailure(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"nil-context", "cancelled", "missing-project", "missing-command",
		"failure", "invalid-json", "oversized", "unsafe-flags",
		"missing-parent", "nested-parent", "capture-failure", "cancel-after-environment", "cancel-after-allocation",
	} {
		t.Run(name, func(t *testing.T) {
			root, parent := fixture(t)
			options := projectstage.Options{Start: root, TemporaryParent: parent, GoCommand: command}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var inputContext context.Context = ctx
			mode := "valid"
			want := []projectstage.Event{projectstage.GoEnvironmentStarted}
			var external string
			switch name {
			case "nil-context":
				inputContext, want = nil, nil
			case "cancelled":
				cancel()
				want = nil
			case "missing-project":
				options.Start = parent
				want = nil
			case "missing-command":
				options.GoCommand = filepath.Join(parent, "missing-go")
				want = nil
			case "failure", "invalid-json", "oversized", "unsafe-flags":
				mode = name
			case "missing-parent":
				options.TemporaryParent = filepath.Join(parent, "missing")
			case "nested-parent":
				options.TemporaryParent = root
			case "capture-failure":
				external = t.TempDir()
				write(t, external, "private.txt", "EXTERNAL_PRIVATE_VALUE")
				if err := os.Symlink(filepath.Join(external, "private.txt"), filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
				want = append(want, projectstage.TemporaryTreeCreated)
			case "cancel-after-allocation":
				want = append(want, projectstage.TemporaryTreeCreated)
			}
			options.Environment = environment("GOWORK=off", "PLYSTRA_PROJECTSTAGE_HELPER="+mode)
			var events []projectstage.Event
			options.Observe = func(event projectstage.Event) {
				events = append(events, event)
				if name == "cancel-after-environment" && event == projectstage.GoEnvironmentStarted ||
					name == "cancel-after-allocation" && event == projectstage.TemporaryTreeCreated {
					cancel()
				}
			}
			before := tree(t, root)
			stage, err := projectstage.Create(inputContext, options)
			if stage != nil {
				t.Cleanup(func() { _ = stage.Close() })
			}
			if !errors.Is(err, projectstage.ErrStage) || stage != nil {
				t.Fatalf("Create = %v, %v; want cleaned error", stage, err)
			}
			if !slices.Equal(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			if !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal("failed observed creation changed original input")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatalf("failed observed creation left temporary output: %v, %v", entries, err)
			}
			if external != "" {
				if data, err := os.ReadFile(filepath.Join(external, "private.txt")); err != nil || string(data) != "EXTERNAL_PRIVATE_VALUE" {
					t.Fatal("failed capture changed external input")
				}
			}
		})
	}
}
