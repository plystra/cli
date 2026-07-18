package generationexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	processTreeHelperEnvironment  = "PLYSTRA_PROCESS_TREE_HELPER"
	processTreePIDFileEnvironment = "PLYSTRA_PROCESS_TREE_PID_FILE"
)

func TestProcessTreeHarness(t *testing.T) {
	switch os.Getenv(processTreeHelperEnvironment) {
	case "":
		return
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeHarness$")
		child.Env = replaceEnvironment(os.Environ(), processTreeHelperEnvironment, "child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "start descendant: %v", err)
			os.Exit(71)
		}
		pidFile := os.Getenv(processTreePIDFileEnvironment)
		temporaryPIDFile := pidFile + ".tmp"
		if err := os.WriteFile(temporaryPIDFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write descendant pid: %v", err)
			os.Exit(72)
		}
		if err := os.Rename(temporaryPIDFile, pidFile); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "publish descendant pid: %v", err)
			os.Exit(72)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "child":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(73)
	}
}

func TestRunCommandTerminatesDescendantProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	environment := replaceEnvironment(os.Environ(), processTreeHelperEnvironment, "parent")
	environment = replaceEnvironment(environment, processTreePIDFileEnvironment, pidFile)
	ctx, cancel := context.WithCancel(t.Context())
	resultChannel := make(chan commandResult, 1)
	go func() {
		resultChannel <- runCommand(ctx, executable, []string{"-test.run=^TestProcessTreeHarness$"}, "", environment, nil, 4096, 4096)
	}()

	var pid int
	var lastReadError error
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for pid == 0 {
		select {
		case result := <-resultChannel:
			cancel()
			t.Fatalf("process-tree harness exited before spawning a descendant: %v, stdout=%q, stderr=%q", result.err, result.stdout, result.stderr)
		case <-deadline.C:
			cancel()
			if lastReadError != nil {
				t.Fatalf("process-tree harness PID file remained unreadable: %v", lastReadError)
			}
			t.Fatal("process-tree harness did not spawn a descendant")
		case <-time.After(20 * time.Millisecond):
			data, readErr := os.ReadFile(pidFile)
			if readErr != nil {
				lastReadError = readErr
				continue
			}
			lastReadError = nil
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				cancel()
				t.Fatalf("descendant pid = %q, %v", data, err)
			}
		}
	}
	defer terminateTestProcess(pid)

	started := time.Now()
	cancel()
	select {
	case result := <-resultChannel:
		if result.err == nil {
			t.Fatal("cancelled process-tree command unexpectedly succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process-tree command did not terminate promptly")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("process-tree termination took %v", elapsed)
	}
}
