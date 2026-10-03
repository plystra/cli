package runtimebaseline_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/runtimebaseline"
)

func baseline(t testing.TB, value string) []byte {
	t.Helper()
	contract := json.RawMessage(`{"module":"example.com/app"}`)
	data, err := runtimebaseline.Encode(runtimebaseline.Document{Schema: runtimebaseline.Schema, ContractID: runtimebaseline.ContractID(contract), Contract: contract, Defaults: map[string]json.RawMessage{"constructor": json.RawMessage(fmt.Sprintf(`{"/value":%q}`, value))}, Templates: []runtimebaseline.Template{}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCanonicalPrivateRoundTrip(t *testing.T) {
	data := baseline(t, "PRIVATE_SENTINEL")
	document, err := runtimebaseline.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := runtimebaseline.Encode(document)
	if err != nil || !bytes.Equal(encoded, data) {
		t.Fatal("round trip changed")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
		if strings.Contains(fmt.Sprintf(format, document), "PRIVATE_SENTINEL") {
			t.Fatal("format exposed baseline")
		}
	}
	changed, err := runtimebaseline.Decode(baseline(t, "DIFFERENT_PRIVATE_VALUE"))
	if err != nil || changed.ContractID != document.ContractID {
		t.Fatal("private value changed public identity")
	}
	for _, invalid := range [][]byte{
		append(bytes.Clone(data), []byte("{}")...),
		bytes.Replace(data, []byte(`"schema":`), []byte(`"unknown":`), 1),
		bytes.Replace(data, []byte(`"module": "example.com/app"`), []byte(`"module": "example.com/other"`), 1),
		bytes.Replace(data, []byte(`"/value": "PRIVATE_SENTINEL"`), []byte(`"/value": "PRIVATE_SENTINEL", "/value": "PRIVATE_SENTINEL"`), 1),
		bytes.Repeat([]byte(" "), runtimebaseline.MaximumBytes+1),
	} {
		if _, err := runtimebaseline.Decode(invalid); !errors.Is(err, runtimebaseline.ErrBaseline) || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
			t.Fatal("accepted invalid or unredacted baseline", err)
		}
	}
}

func TestCanonicalContractSupportsCompiledSchemaDepth(t *testing.T) {
	document, err := runtimebaseline.Decode(baseline(t, "private"))
	if err != nil {
		t.Fatal(err)
	}
	document.Contract = json.RawMessage(strings.Repeat(`{"field":`, 200) + "0" + strings.Repeat("}", 200))
	document.ContractID = runtimebaseline.ContractID(document.Contract)
	data, err := runtimebaseline.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimebaseline.Decode(data); err != nil {
		t.Fatalf("valid compiled schema depth rejected: %v", err)
	}
	document.Contract = json.RawMessage(strings.Repeat(`{"field":`, 257) + "0" + strings.Repeat("}", 257))
	document.ContractID = runtimebaseline.ContractID(document.Contract)
	if _, err := runtimebaseline.Encode(document); !errors.Is(err, runtimebaseline.ErrBaseline) {
		t.Fatal("unbounded depth accepted")
	}
}

func TestTemplateEnvelopeIsPrivateAndRejectsObsoleteExports(t *testing.T) {
	document, err := runtimebaseline.Decode(baseline(t, "private"))
	if err != nil {
		t.Fatal(err)
	}
	document.Templates = []runtimebaseline.Template{{Module: "example.com/template", Version: "v1.0.0", YAML: "config: {private: PRIVATE_TEMPLATE_VALUE}\n"}}
	encoded, err := runtimebaseline.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := runtimebaseline.Decode(encoded)
	if err != nil || len(roundTrip.Templates) != 1 || roundTrip.Templates[0].YAML != document.Templates[0].YAML {
		t.Fatal("lost private template layer", err)
	}
	for _, value := range []any{document, document.Templates, document.Templates[0]} {
		for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
			if strings.Contains(fmt.Sprintf(format, value), "PRIVATE_TEMPLATE_VALUE") {
				t.Fatal("format exposed private template")
			}
		}
	}
	obsolete := bytes.Replace(encoded, []byte(`"template_ancestry"`), []byte(`"dependency_exports"`), 1)
	if _, err := runtimebaseline.Decode(obsolete); !errors.Is(err, runtimebaseline.ErrBaseline) {
		t.Fatal("accepted obsolete envelope", err)
	}
	document.Templates = nil
	if _, err := runtimebaseline.Encode(document); !errors.Is(err, runtimebaseline.ErrBaseline) {
		t.Fatal("accepted missing ancestry", err)
	}
}

func TestPrivateTransactionalInstallationAndRollback(t *testing.T) {
	root := t.TempDir()
	old := baseline(t, "OLD_PRIVATE")
	install := func(data []byte, validate func(string) error) error {
		writes, err := runtimebaseline.Writes(root, data)
		if err != nil {
			return err
		}
		return atomicfs.WriteFiles(root, writes, validate)
	}
	check := func(root string) error {
		_, err := runtimebaseline.Read(filepath.Join(root, runtimebaseline.Path))
		return err
	}
	if err := install(old, check); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, runtimebaseline.Path)
	before, _ := os.ReadFile(filepath.Join(root, "dist/.gitignore"))
	failure := errors.New("validation failed")
	if err := install(baseline(t, "NEW_PRIVATE"), func(root string) error {
		if err := check(root); err != nil {
			t.Fatal(err)
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatal("rollback lost original")
	}
	if err := check(root); err != nil {
		t.Fatal(err)
	}
	if err := install(old, check); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "dist/.gitignore"))
	if !bytes.Equal(before, after) {
		t.Fatal("ignore file not stable")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := privatefile.Check(file); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := install(baseline(t, "NEXT_PRIVATE"), func(string) error { return os.WriteFile(path, []byte("concurrent user edit"), 0o600) }); !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("concurrent edit: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "concurrent user edit" {
		t.Fatal("concurrent edit was overwritten")
	}
}

func TestReadRejectsMissingMalformedAndSymbolicFiles(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, path := range []string{"missing", "../outside", rootPath} {
		if _, err := runtimebaseline.ReadAt(root, path); !errors.Is(err, runtimebaseline.ErrBaseline) {
			t.Fatalf("accepted %s", path)
		}
	}
	file, err := privatefile.Create(filepath.Join(rootPath, "private"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(baseline(t, "private")); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := runtimebaseline.ReadAt(root, "private"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rootPath, "private"), filepath.Join(rootPath, "link")); err == nil {
		if _, err := runtimebaseline.ReadAt(root, "link"); !errors.Is(err, runtimebaseline.ErrBaseline) {
			t.Fatal("accepted symlink")
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(baseline(f, "private"))
	f.Add([]byte(`{"schema":"wrong"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		document, err := runtimebaseline.Decode(data)
		if err != nil {
			if !errors.Is(err, runtimebaseline.ErrBaseline) {
				t.Fatal(err)
			}
			return
		}
		encoded, err := runtimebaseline.Encode(document)
		if err != nil || !bytes.Equal(data, encoded) {
			t.Fatal("noncanonical document accepted")
		}
	})
}
