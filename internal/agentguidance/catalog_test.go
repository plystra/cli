package agentguidance

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/version"
)

func TestRenderProducesDeterministicVersionedProjection(t *testing.T) {
	t.Parallel()

	const modulePath = "example.com/acme/application"
	first, err := Render(modulePath)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	second, err := Render(modulePath)
	if err != nil {
		t.Fatalf("Render again: %v", err)
	}
	if !reflect.DeepEqual(first.Files(), second.Files()) || !reflect.DeepEqual(first.Manifest(), second.Manifest()) {
		t.Fatal("repeated guidance rendering differed")
	}

	wantPaths := []string{
		Root + "/SKILL.md",
		Root + "/tasks/project-and-dependencies.md",
		Root + "/tasks/interfaces-and-implementations.md",
		Root + "/tasks/configuration-and-secrets.md",
		Root + "/tasks/resources-and-data.md",
		Root + "/tasks/diagnostics-and-recovery.md",
		Root + "/tasks/verify-build-and-release.md",
		ManifestPath,
	}
	files := first.Files()
	gotPaths := make([]string, len(files))
	byPath := make(map[string][]byte, len(files))
	for index, file := range files {
		gotPaths[index] = file.Path()
		byPath[file.Path()] = file.Data()
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("projection paths = %v, want %v", gotPaths, wantPaths)
	}

	skill := byPath[Root+"/SKILL.md"]
	if len(skill) == 0 || len(skill) > 4096 {
		t.Fatalf("SKILL.md size = %d, want 1..4096", len(skill))
	}
	for _, phrase := range []string{
		"name: plystra",
		"Project module: `" + modulePath + "`",
		"CLI `" + version.Current + "`",
		"Kernel `" + version.KernelVersion + "`",
		"specification revision `" + version.SpecificationRevision + "`",
		"tasks/project-and-dependencies.md",
		"tasks/resources-and-data.md",
		"optional `local.md`",
	} {
		if !bytes.Contains(skill, []byte(phrase)) {
			t.Fatalf("SKILL.md omits %q:\n%s", phrase, skill)
		}
	}
	for _, forbidden := range []string{"TODO", "create a feature branch", "open a pull request", "push the change"} {
		for name, data := range byPath {
			if strings.Contains(strings.ToLower(string(data)), strings.ToLower(forbidden)) {
				t.Fatalf("%s contains process guidance %q", name, forbidden)
			}
		}
	}

	manifest, err := ParseManifest(byPath[ManifestPath])
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Schema != Schema || manifest.CLIVersion != version.Current || manifest.KernelVersion != version.KernelVersion || manifest.SpecificationRevision != version.SpecificationRevision || !validDigest(manifest.CatalogDigest) {
		t.Fatalf("manifest release facts = %#v", manifest)
	}
	if len(manifest.Files) != len(files)-1 {
		t.Fatalf("manifest file count = %d, want %d", len(manifest.Files), len(files)-1)
	}
	for _, owned := range manifest.Files {
		data, exists := byPath[owned.Path]
		if !exists || owned.SHA256 != digest(data) {
			t.Fatalf("manifest entry %q = %q, file exists %t", owned.Path, owned.SHA256, exists)
		}
	}
}

func TestProjectionReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	projection, err := Render("application")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	files := projection.Files()
	files[0].data[0] = 'X'
	files[0].path = "changed"
	manifest := projection.Manifest()
	manifest.Files[0].Path = "changed"

	freshFiles := projection.Files()
	freshManifest := projection.Manifest()
	if freshFiles[0].Path() != Root+"/SKILL.md" || freshFiles[0].Data()[0] != '-' || freshManifest.Files[0].Path != Root+"/SKILL.md" {
		t.Fatalf("projection was mutable: %#v %#v", freshFiles[0], freshManifest.Files[0])
	}
}

func TestParseManifestRejectsUnsafeOrNonCanonicalInput(t *testing.T) {
	t.Parallel()

	projection, err := Render("application")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	valid := projection.Manifest()
	encode := func(t *testing.T, manifest Manifest) []byte {
		t.Helper()
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return data
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown schema", data: encode(t, func() Manifest { value := valid; value.Schema = "plystra.agent-guidance/v2"; return value }())},
		{name: "invalid catalog digest", data: encode(t, func() Manifest { value := valid; value.CatalogDigest = "sha256:bad"; return value }())},
		{name: "manifest owns itself", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{{Path: ManifestPath, SHA256: digest(nil)}}
			return value
		}())},
		{name: "manifest owns local", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{{Path: Root + "/local.md", SHA256: digest(nil)}}
			return value
		}())},
		{name: "path traversal", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{{Path: Root + "/tasks/../local.md", SHA256: digest(nil)}}
			return value
		}())},
		{name: "unsorted", data: encode(t, func() Manifest {
			value := valid
			value.Files[0], value.Files[1] = value.Files[1], value.Files[0]
			return value
		}())},
		{name: "unknown field", data: []byte(`{"schema":"plystra.agent-guidance/v1","unknown":true}`)},
		{name: "trailing", data: append(encode(t, valid), []byte(" {}")...)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseManifest(test.data); err == nil {
				t.Fatalf("ParseManifest(%s) succeeded", test.data)
			}
		})
	}
}

func TestRenderRejectsUnsafeModuleLabel(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", " application", "application\nsecret", "application`escape"} {
		if _, err := Render(value); err == nil {
			t.Fatalf("Render(%q) succeeded", value)
		}
	}
}
