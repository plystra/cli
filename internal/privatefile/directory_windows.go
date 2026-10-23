package privatefile

import "os"

func mkdirRoot(root *os.Root, name string) error {
	file, err := createRootKind(root, name, true)
	if err != nil {
		return err
	}
	return file.Close()
}
