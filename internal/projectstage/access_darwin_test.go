package projectstage_test

import (
	"os/exec"
	"testing"
)

func changeNativeAccess(t *testing.T, path string) {
	t.Helper()
	if output, err := exec.Command("chmod", "+a#", "0", "everyone allow read", path).CombinedOutput(); err != nil {
		t.Fatalf("add ACL: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("chmod", "-a#", "0", path).CombinedOutput(); err != nil {
			t.Errorf("remove ACL: %v: %s", err, output)
		}
	})
}
