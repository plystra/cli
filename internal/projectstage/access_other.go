//go:build !windows && !linux && !darwin

package projectstage

import "os"

func nativeAccess(*os.File, os.FileInfo) (accessState, error) {
	return accessState{}, ErrStage
}
