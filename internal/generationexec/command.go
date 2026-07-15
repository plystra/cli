package generationexec

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

const processWaitDelay = 5 * time.Second

type commandResult struct {
	stdout         []byte
	stderr         []byte
	stdoutExceeded bool
	stderrExceeded bool
	err            error
}

func runCommand(ctx context.Context, name string, arguments []string, directory string, environment []string, stdin []byte, stdoutLimit, stderrLimit int) commandResult {
	stdout := newBoundedWriter(stdoutLimit)
	stderr := newBoundedWriter(stderrLimit)
	command := exec.CommandContext(ctx, name, arguments...)
	configureProcess(command)
	command.Cancel = func() error {
		return terminateProcessTree(command)
	}
	command.WaitDelay = processWaitDelay
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	return commandResult{
		stdout:         stdout.Bytes(),
		stderr:         stderr.Bytes(),
		stdoutExceeded: stdout.Exceeded(),
		stderrExceeded: stderr.Exceeded(),
		err:            err,
	}
}

type boundedWriter struct {
	data     []byte
	limit    int
	exceeded bool
}

func newBoundedWriter(limit int) *boundedWriter {
	return &boundedWriter{data: make([]byte, 0, min(limit, 4096)), limit: limit}
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	written := len(data)
	remaining := w.limit - len(w.data)
	if remaining > 0 {
		if len(data) > remaining {
			w.data = append(w.data, data[:remaining]...)
		} else {
			w.data = append(w.data, data...)
		}
	}
	if len(data) > remaining {
		w.exceeded = true
	}
	return written, nil
}

func (w *boundedWriter) Bytes() []byte {
	return append([]byte(nil), w.data...)
}

func (w *boundedWriter) Exceeded() bool { return w.exceeded }
