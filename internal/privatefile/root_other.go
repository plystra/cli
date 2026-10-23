//go:build !linux && !darwin && !windows

package privatefile

import "os"

func createRoot(*os.Root, string) (*os.File, error) { return nil, ErrPrivate }
