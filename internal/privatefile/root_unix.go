//go:build linux || darwin

package privatefile

import "os"

func createRoot(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
}
