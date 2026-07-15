package pluginindex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/pluginscan"
)

func TestStableIndexComparisonsDetectFileAndInventoryChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	account := filepath.Join(root, "account")
	if err := os.Mkdir(account, 0o755); err != nil {
		t.Fatalf("Mkdir(account): %v", err)
	}
	marker := filepath.Join(account, "plugin.yaml")
	if err := os.WriteFile(marker, []byte("id: acme.app.account\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(account): %v", err)
	}
	beforeFile, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("Stat(before): %v", err)
	}
	beforeDirectories, err := pluginscan.ScanRoot(root)
	if err != nil {
		t.Fatalf("ScanRoot(before): %v", err)
	}
	if !sameFileState(beforeFile, beforeFile) {
		t.Fatal("sameFileState rejected identical metadata")
	}

	replacement := filepath.Join(account, "replacement.yaml")
	if err := os.WriteFile(replacement, []byte("id: acme.app.profile\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(replacement): %v", err)
	}
	afterFile, err := os.Stat(replacement)
	if err != nil {
		t.Fatalf("Stat(after): %v", err)
	}
	if sameFileState(beforeFile, afterFile) {
		t.Fatal("sameFileState accepted different file identity")
	}

	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0o755); err != nil {
		t.Fatalf("Mkdir(profile): %v", err)
	}
	if err := os.WriteFile(filepath.Join(profile, "plugin.yaml"), []byte("id: acme.app.profile\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}
	afterDirectories, err := pluginscan.ScanRoot(root)
	if err != nil {
		t.Fatalf("ScanRoot(after): %v", err)
	}
	if sameDirectories(beforeDirectories.Directories(), afterDirectories.Directories()) {
		t.Fatal("sameDirectories accepted an added plugin")
	}
	if !sameDirectories(afterDirectories.Directories(), afterDirectories.Directories()) {
		t.Fatal("sameDirectories rejected an identical inventory")
	}
}
