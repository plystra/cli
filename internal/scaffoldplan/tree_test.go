package scaffoldplan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadCapturedFileRejectsDriftAfterFinalScan(t *testing.T) {
	for _, change := range []string{"content with restored timestamp", "replacement", "removal", "cancellation"} {
		t.Run(change, func(t *testing.T) {
			directory := t.TempDir()
			name := filepath.Join(directory, "private.txt")
			if err := os.WriteFile(name, []byte("PRIVATE_BEFORE"), 0o600); err != nil {
				t.Fatal(err)
			}
			files, err := capture(t.Context(), directory)
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if data, err := readCapturedFile(t.Context(), root, "private.txt", files["private.txt"]); err != nil || !bytes.Equal(data, []byte("PRIVATE_BEFORE")) {
				t.Fatal("unchanged captured file rejected")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch change {
			case "content with restored timestamp":
				if err := os.WriteFile(name, []byte("PRIVATE_AFTER!"), 0o600); err != nil {
					t.Fatal(err)
				}
				stamp := files["private.txt"].info.ModTime()
				if err := os.Chtimes(name, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := os.Rename(name, name+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte("PRIVATE_BEFORE"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "removal":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			case "cancellation":
				cancel()
			}
			data, err := readCapturedFile(ctx, root, "private.txt", files["private.txt"])
			if err == nil || data != nil {
				t.Fatal("changed final file yielded a private postimage")
			}
			if change == "cancellation" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation classification lost")
			}
		})
	}
}
