//go:build !linux && !darwin && !windows

package privatefile

import "os"

func create(string) (*os.File, error)                   { return nil, ErrPrivate }
func permissions(*os.File, os.FileInfo) (string, error) { return "", ErrPrivate }
