//go:build !linux && !darwin && !windows

package privatefile

import "os"

func mkdirRoot(*os.Root, string) error { return ErrPrivate }
