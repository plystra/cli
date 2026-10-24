package projectstage_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/modfile"
)

func replacementFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root, parent := fixture(t)
	write(t, filepath.Join(filepath.Dir(root), "dependency"), "go.mod", "module example.com/dependency\n\ngo 1.26\n")
	write(t, filepath.Join(filepath.Dir(root), "dependency"), "value.go", "package dependency\nconst Value = 1\n")
	// Deliberately retain an authored quote, redundant path component, CRLF,
	// comments, and spacing that modfile.Format would change during relocation.
	module := "module example.com/app\r\n\r\ngo 1.26\r\n\r\nrequire example.com/dependency v1.0.0\r\n\r\n// authored target\r\nreplace example.com/dependency => \".././dependency\" // keep spelling\r\n"
	vendor := "# example.com/dependency v1.0.0 => .././dependency\r\n## explicit; go 1.26\r\nexample.com/dependency\r\n# example.com/dependency => .././dependency\r\n"
	write(t, root, "go.mod", module)
	write(t, root, "vendor/modules.txt", vendor)
	write(t, root, "vendor/example.com/dependency/value.go", "package dependency\nconst Value = 1\n")
	return root, parent, module, vendor
}

func readStage(t *testing.T, stage *projectstage.Stage, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stage.Root(), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCreateRelocatesOnlyModuleReplacementTokens(t *testing.T) {
	root, parent := fixture(t)
	base := filepath.Dir(root)
	write(t, filepath.Join(base, "first local"), "go.mod", "module example.com/one\n\ngo 1.26\n")
	write(t, filepath.Join(base, "second"), "go.mod", "module example.com/two\n\ngo 1.26\n")
	original := "module  \"example.com/app\"\r\n\r\ngo 1.26\r\nrequire example.com/query v1\r\nexclude example.com/excluded v1\r\n// caf\u00e9; ../second is only comment text\r\nreplace (\r\n  \"example.com/one\"  => \".././first local\" // keep target spelling\r\n  example.com/two v1 => ../second\r\n  example.com/remote v1 => example.com/upstream v1.2\r\n)\r\n"
	write(t, root, "go.mod", original)
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV=off", "GOPROXY=off", "GOSUMDB=off")
	data := readStage(t, stage, "go.mod")
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil || len(module.Replace) != 3 {
		t.Fatalf("staged replacements: %v", err)
	}
	expected := strings.Replace(original, `".././first local"`, modfile.AutoQuote(module.Replace[0].New.Path), 1)
	expected = strings.Replace(expected, "=> ../second\r\n", "=> "+modfile.AutoQuote(module.Replace[1].New.Path)+"\r\n", 1)
	if string(data) != expected {
		t.Fatalf("staged bytes outside local targets changed: %q", data)
	}
	final := append(data, []byte("// real candidate edit\r\n")...)
	restored, err := stage.OriginalProjectBytes("go.mod", final)
	if err != nil || string(restored) != original+"// real candidate edit\r\n" {
		t.Fatalf("reverse relocation changed candidate bytes: %q, %v", restored, err)
	}
	if authored, err := os.ReadFile(filepath.Join(root, "go.mod")); err != nil || string(authored) != original {
		t.Fatal("relocation changed authored module bytes")
	}
	if err := stage.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalProjectBytesAndPreimagesPreserveAuthoredModuleAndVendor(t *testing.T) {
	root, parent, module, vendor := replacementFixture(t)
	for name, original := range map[string]string{"go.mod": module, "vendor/modules.txt": vendor} {
		stage := capturedStage(t, root, parent)
		inputs := stage.Inputs()
		staged := readStage(t, stage, name)
		if bytes.Equal(staged, []byte(original)) || bytes.Contains(staged, []byte(".././dependency")) {
			t.Fatalf("test did not exercise relocated %s", name)
		}
		image, err := inputs.ProjectFile(t.Context(), name)
		if err != nil || string(image.Data()) != original {
			t.Fatalf("authored %s preimage = %v, %v", name, image, err)
		}
		restored, err := stage.OriginalProjectBytes(name, staged)
		if err != nil || string(restored) != original {
			t.Fatalf("exact original %s was not restored: %v", name, err)
		}
		restored[0] = '!'
		again, err := stage.OriginalProjectBytes(name, staged)
		if err != nil || string(again) != original {
			t.Fatal("OriginalProjectBytes exposed mutable baseline bytes")
		}
		if err := os.WriteFile(filepath.Join(stage.Root(), filepath.FromSlash(name)), []byte("CANDIDATE_EDIT"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
		afterClose, err := inputs.ProjectFile(t.Context(), name)
		if err != nil || string(afterClose.Data()) != original {
			t.Fatalf("original %s after close = %v, %v", name, afterClose, err)
		}
	}
}

func TestOriginalProjectBytesChangesOnlyReplacementTargetBytes(t *testing.T) {
	root, parent, _, _ := replacementFixture(t)
	stage := capturedStage(t, root, parent)
	module, err := modfile.Parse("go.mod", readStage(t, stage, "go.mod"), nil)
	if err != nil {
		t.Fatal(err)
	}
	target := modfile.AutoQuote(module.Replace[0].New.Path)
	final := "module  \"example.com/app\"\r\n\r\ngo 1.26\r\n\r\nrequire (\r\n  example.com/dependency   v1.2.0 // final dependency edit\r\n)\r\n\r\nreplace (\r\n  \"example.com/dependency\"  => " + target + " // keep final formatting\r\n)\r\n"
	expected := strings.Replace(final, target, `".././dependency"`, 1)
	restored, err := stage.OriginalProjectBytes("go.mod", []byte(final))
	if err != nil || string(restored) != expected {
		t.Fatalf("restoration changed bytes outside the target: %q, %v", restored, err)
	}
}

func TestOriginalProjectBytesRetainsRealGetAndTidyEdits(t *testing.T) {
	root, parent, original, _ := replacementFixture(t)
	write(t, root, "app.go", "package app\nimport \"example.com/dependency\"\nvar Value = dependency.Value\n")
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOSUMDB=off")
	options := gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}
	if err := gocommand.Run(t.Context(), options, "get", "example.com/dependency@v1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := gocommand.Run(t.Context(), options, "mod", "edit", "-require=example.com/dependency/unused@v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := gocommand.Run(t.Context(), options, "mod", "tidy"); err != nil {
		t.Fatal(err)
	}
	final := readStage(t, stage, "go.mod")
	restored, err := stage.OriginalProjectBytes("go.mod", final)
	if err != nil {
		t.Fatal(err)
	}
	module, err := modfile.Parse("go.mod", restored, nil)
	if err != nil || len(module.Require) != 1 || module.Require[0].Mod.Path != "example.com/dependency" || module.Require[0].Mod.Version != "v1.1.0" {
		t.Fatalf("dependency edits were lost: %s, %v", restored, err)
	}
	if len(module.Replace) != 1 || module.Replace[0].New.Path != ".././dependency" || !bytes.Contains(restored, []byte(`".././dependency"`)) || bytes.Contains(restored, []byte("plystra-stage-")) {
		t.Fatalf("authored replacement target/spelling was not restored: %q", restored)
	}
	if err := gocommand.Run(t.Context(), options, "mod", "vendor"); err != nil {
		t.Fatal(err)
	}
	vendor, err := stage.OriginalProjectBytes("vendor/modules.txt", readStage(t, stage, "vendor/modules.txt"))
	if err != nil || !bytes.Contains(vendor, []byte("v1.1.0 => .././dependency")) || bytes.Contains(vendor, []byte("plystra-stage-")) {
		t.Fatalf("updated vendor restoration failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err != nil || string(data) != original {
		t.Fatal("get/tidy changed the original Project")
	}
	if err := stage.Inputs().Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalProjectBytesScopesReplacementSpellingsToCurrentProject(t *testing.T) {
	root, parent, _, _ := replacementFixture(t)
	base := filepath.Dir(root)
	member := filepath.Join(base, "z-member")
	write(t, member, "go.mod", "module example.com/member\n\ngo 1.26\nreplace example.com/dependency => ../dependency\n")
	write(t, base, "go.work", "go 1.26\nuse (\n ./app\n ./z-member\n)\nreplace example.com/dependency => ./dependency\n")
	write(t, base, "vendor/modules.txt", "## workspace\n# example.com/dependency v1.0.0 => ./dependency\n## explicit; go 1.26\n# example.com/dependency => ./dependency\n")
	stage := capturedStage(t, root, parent, "GOWORK="+filepath.Join(base, "go.work"))
	for _, name := range []string{"go.mod", "vendor/modules.txt"} {
		final := bytes.ReplaceAll(readStage(t, stage, name), []byte("v1.0.0"), []byte("v1.1.0"))
		restored, err := stage.OriginalProjectBytes(name, final)
		if err != nil || !bytes.Contains(restored, []byte(".././dependency")) || !bytes.Contains(restored, []byte("v1.1.0")) || bytes.Contains(restored, []byte("plystra-stage-")) {
			t.Fatalf("Project-owned %s mapping = %s, %v", name, restored, err)
		}
	}
	// A sibling workspace vendor inventory is never a Project-relative preimage.
	image, err := stage.Inputs().ProjectFile(t.Context(), "vendor/modules.txt")
	if err != nil || bytes.Contains(image.Data(), []byte("## workspace")) {
		t.Fatal("workspace vendor replaced the Project vendor preimage")
	}
}

func TestOriginalProjectBytesVersionedAndWildcardMappings(t *testing.T) {
	root, parent := fixture(t)
	base := filepath.Dir(root)
	for _, name := range []string{"default", "exact"} {
		write(t, filepath.Join(base, name), "go.mod", "module example.com/dependency\n\ngo 1.26\n")
	}
	write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nreplace (\nexample.com/dependency => .././default\nexample.com/dependency v1.0.0 => .././exact\n)\n")
	write(t, root, "vendor/modules.txt", "# example.com/dependency v1.0.0 => .././exact\n# example.com/dependency v1.2.0 => .././default\n# example.com/dependency => .././default\n")
	stage := capturedStage(t, root, parent)
	final := append(readStage(t, stage, "vendor/modules.txt"), []byte("# final dependency edit\n")...)
	restored, err := stage.OriginalProjectBytes("vendor/modules.txt", final)
	if err != nil || string(restored) != "# example.com/dependency v1.0.0 => .././exact\n# example.com/dependency v1.2.0 => .././default\n# example.com/dependency => .././default\n# final dependency edit\n" {
		t.Fatalf("version-specific vendor mapping = %s, %v", restored, err)
	}
	module := append(readStage(t, stage, "go.mod"), []byte("\nrequire example.com/new v1.0.0\n")...)
	restored, err = stage.OriginalProjectBytes("go.mod", module)
	if err != nil || !bytes.Contains(restored, []byte(".././default")) || !bytes.Contains(restored, []byte(".././exact")) || !bytes.Contains(restored, []byte("example.com/new v1.0.0")) {
		t.Fatalf("version-specific module mapping = %s, %v", restored, err)
	}
}

func TestOriginalProjectBytesPreservesShadowedAndNewVendorPaths(t *testing.T) {
	root, parent := fixture(t)
	base := filepath.Dir(root)
	write(t, filepath.Join(base, "dependency"), "go.mod", "module example.com/dependency\n\ngo 1.26\n")
	write(t, base, "go.work", "go 1.26\nuse ./app\nreplace example.com/dependency => ./dependency\n")
	original := "module example.com/app\n\ngo 1.26\nreplace example.com/dependency => ../missing-shadowed\n"
	write(t, root, "go.mod", original)
	stage := capturedStage(t, root, parent, "GOWORK="+filepath.Join(base, "go.work"))
	final := append(readStage(t, stage, "go.mod"), []byte("\nrequire example.com/dependency v1.1.0\n")...)
	restored, err := stage.OriginalProjectBytes("go.mod", final)
	if err != nil || !bytes.Contains(restored, []byte("../missing-shadowed")) || !bytes.Contains(restored, []byte("v1.1.0")) {
		t.Fatalf("shadowed authored replacement lost: %s, %v", restored, err)
	}
	// Project absence remains distinct even if a workspace vendor is present.
	missing, err := stage.Inputs().ProjectFile(t.Context(), "vendor/modules.txt")
	if err != nil || missing.Exists() {
		t.Fatalf("original Project vendor absence = %v, %v", missing, err)
	}
	// A newly produced vendor inventory can also reverse a captured module map.
	write(t, root, "go.mod", strings.ReplaceAll(original, "../missing-shadowed", ".././dependency"))
	stage = capturedStage(t, root, parent)
	parsed, err := modfile.Parse("go.mod", readStage(t, stage, "go.mod"), nil)
	if err != nil {
		t.Fatal(err)
	}
	vendor := []byte("# example.com/dependency v1.1.0 => " + parsed.Replace[0].New.Path + "\n")
	restored, err = stage.OriginalProjectBytes("vendor/modules.txt", vendor)
	if err != nil || string(restored) != "# example.com/dependency v1.1.0 => .././dependency\n" {
		t.Fatalf("new vendor map = %s, %v", restored, err)
	}
}

func TestOriginalProjectBytesRejectsUnknownStageMappingsAndCopiesOtherFiles(t *testing.T) {
	root, parent, _, _ := replacementFixture(t)
	stage := capturedStage(t, root, parent)
	module, err := modfile.Parse("go.mod", readStage(t, stage, "go.mod"), nil)
	if err != nil {
		t.Fatal(err)
	}
	mapped := module.Replace[0].New.Path
	for _, test := range []struct{ name, data string }{
		{"go.mod", "module example.com/app\ngo 1.26\nreplace example.com/unknown => " + modfile.AutoQuote(mapped) + "\n"},
		{"go.mod", "module example.com/app\ngo 1.26\nreplace example.com/dependency => " + modfile.AutoQuote(filepath.Join(stage.Root(), "unknown")) + "\n"},
		{"go.mod", "module example.com/app\ngo 1.26\nreplace example.com/dependency => ./unknown\n"},
		{"go.mod", "malformed module file"},
		{"vendor/modules.txt", "# example.com/unknown v1.0.0 => " + mapped + "\n"},
		{"vendor/modules.txt", "# example.com/dependency v1.0.0 => " + filepath.Join(stage.Root(), "unknown") + "\n"},
		{"vendor/modules.txt", "# unknown header " + mapped + "\n"},
	} {
		data, err := stage.OriginalProjectBytes(test.name, []byte(test.data))
		if !errors.Is(err, projectstage.ErrStage) || data != nil {
			t.Fatalf("unsafe %s map = %v", test.name, err)
		}
	}
	for _, name := range []string{"go.sum", "assets/module.txt", "nested/go.mod", "dist/runtime-baseline.json"} {
		original := []byte("PRIVATE_FILE " + stage.Root())
		copy, err := stage.OriginalProjectBytes(name, original)
		if err != nil || !bytes.Equal(original, copy) {
			t.Fatalf("ordinary %s bytes changed: %v", name, err)
		}
		copy[0] = '!'
		if original[0] == '!' {
			t.Fatal("ordinary file bytes were not copied")
		}
	}
	for _, path := range []string{"../go.mod", "", ".", "/go.mod", `..\go.mod`} {
		if _, err := stage.OriginalProjectBytes(path, nil); err == nil {
			t.Fatalf("unsafe original path %q accepted", path)
		}
	}
	var nilStage *projectstage.Stage
	for _, invalid := range []*projectstage.Stage{nilStage, {}} {
		if _, err := invalid.OriginalProjectBytes("go.mod", nil); err == nil {
			t.Fatal("uninitialized Stage produced original bytes")
		}
	}
}
