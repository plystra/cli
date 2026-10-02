//go:build linux || darwin

package privatefile

import (
	"os"
	"strconv"
	"syscall"
)

func create(name string) (*os.File, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := Check(file); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func permissions(file *os.File, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(stat.Uid) != uint64(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return "", ErrPrivate
	}
	acl, err := aclPermissions(file)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(stat.Uid), 10) + ":" + strconv.FormatUint(uint64(stat.Gid), 10) + ":" + strconv.FormatUint(uint64(info.Mode()), 10) + ":" + acl, nil
}
