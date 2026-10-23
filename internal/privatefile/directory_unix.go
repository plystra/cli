//go:build linux || darwin

package privatefile

import "os"

func mkdirRoot(root *os.Root, name string) error { return root.Mkdir(name, 0o700) }
