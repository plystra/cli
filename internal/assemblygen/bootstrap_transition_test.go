package assemblygen_test

const generatedBootstrapTransitionTest = `package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/assemblydependency/lifecycleevents"
	remotestore "example.com/assemblydependency/remote-store"
	kernellifecycle "github.com/plystra/kernel/lifecycle"
)

func TestApplicationRejectsOverlappingTransitions(t *testing.T) {
	for _, phase := range []string{"startup", "rollback", "shutdown"} {
		for _, copyHandle := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/original", true: "/copy"}[copyHandle], func(t *testing.T) {
				t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
				remotestore.Reset()
				lifecycleevents.Reset()
				writeRuntimeDocument(t, validRuntimeDocument)
				application, err := New(context.Background(), RuntimeOptions{Arguments: []string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}})
				if err != nil { t.Fatal(err) }
				if phase == "shutdown" {
					if err := application.Start(context.Background()); err != nil { t.Fatal(err) }
				}
				contender := application
				if copyHandle { copied := *application; contender = &copied }
				entered, release := make(chan struct{}), make(chan struct{})
				block := func(context.Context) error { close(entered); <-release; return nil }
				switch phase {
				case "startup": remotestore.SetHooks(block, nil)
				case "rollback": remotestore.SetHooks(func(context.Context) error { return errors.New("private-hook-failure") }, block)
				case "shutdown": remotestore.SetHooks(nil, block)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					if phase == "shutdown" { done <- application.Stop(ctx) } else { done <- application.Start(ctx) }
				}()
				finished := false
				defer func() {
					if !finished { close(release); <-done }
					remotestore.SetHooks(nil, nil)
					if err := application.Stop(context.Background()); err != nil { t.Errorf("final cleanup: %v", err) }
				}()
				select {
				case <-entered:
				case <-ctx.Done(): t.Fatal("lifecycle hook did not enter")
				}
				staticState, legacyState := application.interfaces.State(), application.lifecycle.State()
				before := lifecycleevents.Snapshot()
				for _, operation := range []struct {
					name string
					call func(context.Context) error
					boundary error
				}{
					{"Start", contender.Start, ErrApplicationStart},
					{"Stop", contender.Stop, ErrApplicationStop},
				} {
					if err := operation.call(ctx); !errors.Is(err, operation.boundary) || !errors.Is(err, kernellifecycle.ErrState) {
						t.Fatalf("overlapping %s = %v", operation.name, err)
					}
					if application.interfaces.State() != staticState || application.lifecycle.State() != legacyState || !reflect.DeepEqual(lifecycleevents.Snapshot(), before) {
						t.Fatalf("overlapping %s changed active transition: static %s -> %s, legacy %s -> %s, events %v -> %v", operation.name, staticState, application.interfaces.State(), legacyState, application.lifecycle.State(), before, lifecycleevents.Snapshot())
					}
				}
				close(release)
				err = <-done
				finished = true
				if phase == "rollback" {
					if !errors.Is(err, ErrApplicationStart) || !errors.Is(err, kernellifecycle.ErrStart) || strings.Contains(err.Error(), "private") { t.Fatalf("rollback result = %v", err) }
				} else if err != nil { t.Fatal(err) }
				remotestore.SetHooks(nil, nil)
				if err := contender.Stop(context.Background()); err != nil { t.Fatalf("shutdown after transition = %v", err) }
				if application.State() != kernellifecycle.StateStopped { t.Fatalf("state = %s", application.State()) }
				if err := contender.Start(context.Background()); !errors.Is(err, kernellifecycle.ErrState) { t.Fatalf("restart after stop = %v", err) }
			})
		}
	}
}

func TestApplicationRejectsReentrantTransitions(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	remotestore.Reset()
	writeRuntimeDocument(t, validRuntimeDocument)
	application, err := New(context.Background(), RuntimeOptions{Arguments: []string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}})
	if err != nil { t.Fatal(err) }
	defer remotestore.SetHooks(nil, nil)
	var failures []string
	hooks := 0
	hook := func(ctx context.Context) error {
		hooks++
		beforeStatic, beforeLegacy := application.interfaces.State(), application.lifecycle.State()
		if err := application.Start(ctx); !errors.Is(err, ErrApplicationStart) || !errors.Is(err, kernellifecycle.ErrState) { failures = append(failures, "reentrant Start") }
		if err := application.Stop(ctx); !errors.Is(err, ErrApplicationStop) || !errors.Is(err, kernellifecycle.ErrState) { failures = append(failures, "reentrant Stop") }
		if application.interfaces.State() != beforeStatic || application.lifecycle.State() != beforeLegacy { failures = append(failures, "reentrant mutation") }
		return nil
	}
	remotestore.SetHooks(hook, hook)
	if err := application.Start(context.Background()); err != nil { t.Fatal(err) }
	if err := application.Stop(context.Background()); err != nil { t.Fatal(err) }
	if hooks != 2 || len(failures) != 0 { t.Fatalf("hooks = %d, failures = %v", hooks, failures) }
}

func TestApplicationTransitionGuardsAreIndependent(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	remotestore.Reset()
	writeRuntimeDocument(t, validRuntimeDocument)
	first, err := New(context.Background(), RuntimeOptions{Arguments: []string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}})
	if err != nil { t.Fatal(err) }
	second, err := New(context.Background(), RuntimeOptions{Arguments: []string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}})
	if err != nil { t.Fatal(err) }
	type blockingKey struct{}
	entered, release := make(chan struct{}), make(chan struct{})
	remotestore.SetHooks(func(ctx context.Context) error {
		if ctx.Value(blockingKey{}) == true { close(entered); <-release }
		return nil
	}, nil)
	done := make(chan error, 1)
	go func() { done <- first.Start(context.WithValue(context.Background(), blockingKey{}, true)) }()
	defer func() {
		close(release)
		if err := <-done; err != nil { t.Errorf("first Start: %v", err) }
		remotestore.SetHooks(nil, nil)
		if err := first.Stop(context.Background()); err != nil { t.Errorf("first Stop: %v", err) }
		if err := second.Stop(context.Background()); err != nil { t.Errorf("second Stop: %v", err) }
	}()
	select {
	case <-entered:
	case <-time.After(10*time.Second): t.Fatal("first hook did not enter")
	}
	if err := second.Start(context.Background()); err != nil { t.Fatal(err) }
	if err := second.Stop(context.Background()); err != nil { t.Fatal(err) }
	if first.State() != kernellifecycle.StateStarting { t.Fatalf("first State = %s", first.State()) }
}
`
