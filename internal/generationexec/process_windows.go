//go:build windows

package generationexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func terminateProcessTree(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	treeErr := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	killErr := command.Process.Kill()
	if treeErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	if killErr == nil {
		return nil
	}
	return fmt.Errorf("terminate process tree: %v; terminate process: %w", treeErr, killErr)
}
