package privatefile

import "embed"

// Source contains the platform permission checks emitted with runtime bootstrap.
//
//go:embed file.go file_unix.go file_linux.go file_darwin.go file_windows.go file_other.go
var Source embed.FS
