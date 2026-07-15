//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package generationexec

import (
	"os"
	"os/exec"
)

func configureProcess(*exec.Cmd) {}

func terminateProcessTree(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	return command.Process.Kill()
}
