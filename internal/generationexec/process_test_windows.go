//go:build windows

package generationexec

import (
	"os/exec"
	"strconv"
)

func terminateTestProcess(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
