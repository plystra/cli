package generatedfiles

import (
	"encoding/json"
	"io/fs"
	"testing"
)

func FuzzManagedOutputPathsAndBytes(f *testing.F) {
	for _, seed := range []struct {
		path string
		data []byte
	}{
		{path: "generated/go/contracts/email/send/v1/contract_gen.go", data: []byte("package emailsendv1\n")},
		{path: "generated/docs/api.md", data: nil},
		{path: "../outside", data: []byte("unsafe")},
		{path: ManifestPath, data: []byte("self")},
		{path: "generated/sdk/javascript/node_modules/pkg/index.js", data: []byte("ignored")},
	} {
		f.Add(seed.path, seed.data)
	}
	f.Fuzz(func(t *testing.T, filePath string, data []byte) {
		if len(filePath) > 4096 || len(data) > 1<<20 {
			return
		}
		kind := ArtifactKindGoSource
		if filePath == ApplicationManifestPath {
			kind = ArtifactKindApplicationManifest
		}
		file, err := NewFile(filePath, data, ArtifactInput{
			Generator:      "plystra.fuzz-generator/v1",
			Kind:           kind,
			InputRecordIDs: []string{"fuzz:" + filePath},
			Sources:        []string{"fuzz input"},
		})
		if err != nil {
			return
		}
		output, err := NewOutput([]File{file})
		if err != nil {
			if filePath == ApplicationManifestPath && !json.Valid(data) {
				return
			}
			t.Fatalf("NewOutput accepted NewFile then failed: %v", err)
		}
		artifacts := output.Artifacts()
		if len(output.Files()) != 1 || output.Files()[0].Path() != filePath || len(output.ManifestJSON()) == 0 ||
			len(artifacts) != 2 || !artifacts[0].Valid() || !artifacts[1].Valid() {
			t.Fatalf("prepared output lost accepted file %q", filePath)
		}
		repeated, err := NewOutput(output.Files())
		if err != nil || string(repeated.ManifestJSON()) != string(output.ManifestJSON()) {
			t.Fatalf("repeated output is not deterministic: %v", err)
		}
	})
}

func FuzzOwnershipManifestDecoder(f *testing.F) {
	empty, err := NewOutput(nil)
	if err != nil {
		f.Fatalf("NewOutput(empty): %v", err)
	}
	one, err := NewOutput([]File{mustFuzzFile(f, "generated/a", []byte("a\n"))})
	if err != nil {
		f.Fatalf("NewOutput(one): %v", err)
	}
	for _, seed := range [][]byte{
		empty.ManifestJSON(),
		one.ManifestJSON(),
		[]byte(`{"version":2,"files":[]}`),
		[]byte(`{"version":3,"files":null}`),
		[]byte(`not-json`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		actual := map[string]actualFile{
			ManifestPath: {mode: fs.FileMode(0o644), size: int64(len(data)), data: append([]byte(nil), data...)},
		}
		previous, err := decodePreviousManifest(actual)
		if err != nil {
			return
		}
		for filePath, fileDigest := range previous {
			if !validManagedPath(filePath) || !validDigest(fileDigest) {
				t.Fatalf("decoder returned invalid record %q %q", filePath, fileDigest)
			}
		}
	})
}

func mustFuzzFile(f *testing.F, filePath string, data []byte) File {
	f.Helper()
	file, err := NewFile(filePath, data, ArtifactInput{
		Generator:      "plystra.fuzz-generator/v1",
		Kind:           ArtifactKindGoSource,
		InputRecordIDs: []string{"fuzz:" + filePath},
		Sources:        []string{"fuzz input"},
	})
	if err != nil {
		f.Fatalf("NewFile(%s): %v", filePath, err)
	}
	return file
}
