//go:build windows

package processcommand

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCancellationRetainsUnsettledAfterParentExit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestProcessCommandHarness$")
	command.Env = append(os.Environ(), "PLYSTRA_PROCESS_COMMAND_HELPER=exit")
	cleanup := configure(command)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	err = command.Cancel()
	if !errors.Is(err, ErrUnsettled) || !errors.Is(cleanup(), ErrUnsettled) {
		t.Fatalf("failed tree termination was hidden by a completed parent: %v, %v", err, cleanup())
	}
}

func TestCancellationBoundsUnresponsiveTerminator(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(filepath.Join(directory, "taskkill.exe"))
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	if err := errors.Join(copyErr, target.Close()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("PLYSTRA_PROCESS_COMMAND_TERMINATOR", "hang")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestProcessCommandHarness$")
	ready := filepath.Join(directory, "ready")
	command.Env = append(os.Environ(), "PLYSTRA_PROCESS_COMMAND_HELPER=wait", "READY_FILE="+ready)
	finished := make(chan error, 1)
	go func() { _, err := Run(command); finished <- err }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("owned parent exited before readiness: %v", err)
		case <-deadline.C:
			cancel()
			<-finished
			t.Fatal("owned parent did not start")
		case <-ticker.C:
		}
	}
	before := time.Now()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrUnsettled) || time.Since(before) < 5*time.Second {
			t.Fatalf("unresponsive terminator did not preserve bounded uncertainty: %v", err)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("unresponsive terminator prevented bounded cleanup")
	}
}
