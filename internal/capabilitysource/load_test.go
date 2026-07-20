package capabilitysource_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
)

func TestLoadReadsExactConventionalSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := mustID(t, "account.register/v2")
	data := []byte("id: account.register/v2\r\ndescription: Registers an account.\r\nrequest: {}\r\nresponse: {}\r\nsemantics:\r\n  kind: query\r\n  effects: none\r\n  idempotency: {mode: inherent}\r\n  retry: {safety: safe}\r\n  cancellation: {mode: best-effort}\r\n  completion: {mode: completed-before-return}\r\n  ordering: {mode: none}\r\n  data: {request: public, response: public}\r\n")
	wantPath := writeSource(t, root, id, data)
	source, err := capabilitysource.Load(root, id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if source.ID() != id || source.RelativePath() != "capabilities/account.register/v2/capability.yaml" || source.Path() != wantPath || !bytes.Equal(source.Data(), data) {
		t.Fatalf("Source = ID %q, relative %q, path %q, data %q", source.ID(), source.RelativePath(), source.Path(), source.Data())
	}
	returned := source.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, source.Data()) {
		t.Fatal("Data exposed mutable source storage")
	}
}

func TestLoadRejectsInvalidOrMismatchedIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      string
		wantError error
	}{
		{name: "mismatch", data: "id: account.register/v2\n" + querySemanticsYAML, wantError: capabilitysource.ErrIdentityMismatch},
		{name: "invalid", data: "id: account.register/v1\nunknown: true\n" + querySemanticsYAML, wantError: capabilitymeta.ErrInvalidManifest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			expected := mustID(t, "account.register/v1")
			writeSource(t, root, expected, []byte(test.data))
			source, err := capabilitysource.Load(root, expected)
			if !errors.Is(err, capabilitysource.ErrLoad) || !errors.Is(err, test.wantError) {
				t.Fatalf("Load error = %v, want ErrLoad and %v", err, test.wantError)
			}
			if source.ID().String() != "" || source.Path() != "" || len(source.Data()) != 0 {
				t.Fatalf("invalid Load returned %#v", source)
			}
		})
	}
}

const querySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func TestLoadRejectsMissingOversizedAndNonRegularSources(t *testing.T) {
	t.Parallel()

	id := mustID(t, "account.register/v1")
	if _, err := capabilitysource.Load("", id); !errors.Is(err, capabilitysource.ErrLoad) {
		t.Fatalf("Load(empty path) error = %v", err)
	}
	if _, err := capabilitysource.Load(t.TempDir(), capabilityid.Identifier{}); !errors.Is(err, capabilitysource.ErrLoad) {
		t.Fatalf("Load(empty ID) error = %v", err)
	}
	if _, err := capabilitysource.Load(t.TempDir(), id); !errors.Is(err, capabilitysource.ErrLoad) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(missing) error = %v", err)
	}
	rootFile := filepath.Join(t.TempDir(), "plugin")
	if err := os.WriteFile(rootFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile(root): %v", err)
	}
	if _, err := capabilitysource.Load(rootFile, id); !errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(file root) error = %v", err)
	}

	oversizedRoot := t.TempDir()
	writeSource(t, oversizedRoot, id, []byte(strings.Repeat("x", capabilitymeta.MaximumSize+1)))
	if _, err := capabilitysource.Load(oversizedRoot, id); !errors.Is(err, capabilitysource.ErrLoad) || errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(oversized) error = %v", err)
	}

	directoryRoot := t.TempDir()
	directoryPath := sourcePath(directoryRoot, id)
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(source directory): %v", err)
	}
	if _, err := capabilitysource.Load(directoryRoot, id); !errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(directory source) error = %v", err)
	}

	parentFileRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(parentFileRoot, "capabilities"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile(parent): %v", err)
	}
	if _, err := capabilitysource.Load(parentFileRoot, id); !errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(file parent) error = %v", err)
	}
}

func TestLoadRejectsSymbolicRootsParentsAndFiles(t *testing.T) {
	t.Parallel()

	id := mustID(t, "account.register/v1")
	realRoot := t.TempDir()
	writeSource(t, realRoot, id, []byte("id: account.register/v1\n"))
	linkedRoot := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := capabilitysource.Load(linkedRoot, id); !errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(symbolic root) error = %v", err)
	}

	parentRoot := t.TempDir()
	if err := os.Symlink(filepath.Join(realRoot, "capabilities"), filepath.Join(parentRoot, "capabilities")); err != nil {
		t.Fatalf("Symlink(parent): %v", err)
	}
	if _, err := capabilitysource.Load(parentRoot, id); !errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(symbolic parent) error = %v", err)
	}

	fileRoot := t.TempDir()
	filePath := sourcePath(fileRoot, id)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(file parent): %v", err)
	}
	if err := os.Symlink(sourcePath(realRoot, id), filePath); err != nil {
		t.Fatalf("Symlink(file): %v", err)
	}
	if _, err := capabilitysource.Load(fileRoot, id); !errors.Is(err, capabilitysource.ErrUnsafePath) {
		t.Fatalf("Load(symbolic file) error = %v", err)
	}
}

func writeSource(t *testing.T, pluginRoot string, id capabilityid.Identifier, data []byte) string {
	t.Helper()
	name := sourcePath(pluginRoot, id)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(pluginRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return sourcePath(canonicalRoot, id)
}

func sourcePath(root string, id capabilityid.Identifier) string {
	return filepath.Join(root, "capabilities", id.Name(), "v"+strconv.FormatUint(id.Major(), 10), "capability.yaml")
}

func mustID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}
