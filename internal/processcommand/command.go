// Package processcommand owns cancellation of trusted command process trees.
package processcommand

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// ErrUnsettled means process-tree termination could not be established.
var ErrUnsettled = errors.New("command process tree may still be active")

// Run starts and waits for a trusted CommandContext, including its output pipes.
// It reports whether the process started, independently of its final outcome.
func Run(command *exec.Cmd) (bool, error) {
	cleanup := configure(command)
	type pipe struct {
		reader *os.File
		writer *os.File
		target io.Writer
	}
	var pipes []pipe
	defer func() {
		for _, pipe := range pipes {
			_ = pipe.reader.Close()
			_ = pipe.writer.Close()
		}
	}()
	for _, stream := range []*io.Writer{&command.Stdout, &command.Stderr} {
		if *stream == nil {
			continue
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			return false, err
		}
		pipes = append(pipes, pipe{reader, writer, *stream})
		*stream = writer
	}
	if err := command.Start(); err != nil {
		return false, err
	}
	finished := make(chan error, len(pipes))
	for _, pipe := range pipes {
		_ = pipe.writer.Close()
		go func() {
			_, err := io.Copy(pipe.target, pipe.reader)
			_ = pipe.reader.Close()
			finished <- err
		}()
	}
	err := command.Wait()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = errors.Join(err, ErrUnsettled)
	}
	// Cmd.Wait can discard ErrWaitDelay when the parent exits nonzero. Own the
	// pipes so their completion is independent of that exit status.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for remaining := len(pipes); remaining > 0; remaining-- {
		select {
		case copyErr := <-finished:
			if copyErr != nil {
				err = errors.Join(err, ErrUnsettled, copyErr)
			}
		case <-timer.C:
			err = errors.Join(err, ErrUnsettled)
			for _, pipe := range pipes {
				_ = pipe.reader.Close()
			}
			for ; remaining > 0; remaining-- {
				<-finished
			}
		}
	}
	return true, errors.Join(err, cleanup())
}

func configure(command *exec.Cmd) func() error {
	configureProcess(command)
	var unsettled atomic.Bool
	command.Cancel = func() error {
		err := terminateProcessTree(command)
		if errors.Is(err, ErrUnsettled) || err != nil && !errors.Is(err, os.ErrProcessDone) {
			unsettled.Store(true)
		}
		return err
	}
	command.WaitDelay = 5 * time.Second
	return func() error {
		if unsettled.Load() {
			return ErrUnsettled
		}
		return nil
	}
}
