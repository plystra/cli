//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package generationexec

import (
	"syscall"
)

func terminateTestProcess(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
