package interfacecompatibility

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/sdkmodel"
)

func TestJavaScriptBaselineStoresClassifiedPublicAPIDigests(t *testing.T) {
	t.Parallel()

	api := javaScriptAPI(t, "@acme/project-sdk", "string", []string{"record_rejected"})
	baseline, err := NewJavaScript(api)
	if err != nil || !baseline.Valid() {
		t.Fatalf("NewJavaScript = %#v, %v", baseline, err)
	}
	if baseline.Schema() != JavaScriptSchema ||
		baseline.PackageDigest() != api.PackageDigest() ||
		len(baseline.Interfaces()) != 1 ||
		baseline.Interfaces()[0].ID() != "records.echo/v1" ||
		!validDigest(baseline.Interfaces()[0].SurfaceDigest()) ||
		!validDigest(baseline.Interfaces()[0].TypesDigest()) ||
		!validDigest(baseline.Interfaces()[0].SemanticErrorsDigest()) {
		t.Fatalf("JavaScript baseline = package %q, interfaces %#v", baseline.PackageDigest(), baseline.Interfaces())
	}
	for _, forbidden := range []string{
		"@acme/project-sdk",
		"example.com/javascript-api",
		"RecordsEchoV1",
		"record_rejected",
		"Implementation",
		"Secret",
		"Config",
	} {
		if bytes.Contains(baseline.RecordJSON(), []byte(forbidden)) {
			t.Fatalf("JavaScript baseline contains forbidden value %q: %s", forbidden, baseline.RecordJSON())
		}
	}

	record := baseline.RecordJSON()
	decoded, err := DecodeJavaScript(record)
	if err != nil || !decoded.Valid() || decoded.Digest() != baseline.Digest() ||
		!bytes.Equal(decoded.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("DecodeJavaScript = %#v, %v", decoded, err)
	}
	record[0] ^= 0xff
	interfaces := baseline.Interfaces()
	interfaces[0] = JavaScriptInterface{}
	if !baseline.Valid() || baseline.Interfaces()[0].ID() != "records.echo/v1" {
		t.Fatal("JavaScript baseline exposed mutable storage")
	}
}

func TestCompareJavaScriptSeparatesPackageAndInterfaceClasses(t *testing.T) {
	t.Parallel()

	baseline := func(packageName, fieldType string, semanticErrors []string) JavaScriptBaseline {
		t.Helper()
		result, err := NewJavaScript(javaScriptAPI(t, packageName, fieldType, semanticErrors))
		if err != nil {
			t.Fatalf("NewJavaScript: %v", err)
		}
		return result
	}
	base := baseline("@acme/project-sdk", "string", []string{"record_rejected"})
	tests := []struct {
		name           string
		current        JavaScriptBaseline
		packageChanged bool
		classes        []JavaScriptClass
	}{
		{
			name:           "package",
			current:        baseline("@acme/other-sdk", "string", []string{"record_rejected"}),
			packageChanged: true,
		},
		{
			name:    "types",
			current: baseline("@acme/project-sdk", "int64", []string{"record_rejected"}),
			classes: []JavaScriptClass{JavaScriptClassTypes},
		},
		{
			name:    "semantic errors",
			current: baseline("@acme/project-sdk", "string", []string{"record_rejected", "record_unavailable"}),
			classes: []JavaScriptClass{JavaScriptClassSemanticErrors},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			comparison, err := CompareJavaScript(base, test.current)
			if err != nil || !comparison.Valid() || comparison.Clean() ||
				comparison.PackageChanged() != test.packageChanged {
				t.Fatalf("CompareJavaScript = %#v, %v", comparison, err)
			}
			if test.packageChanged {
				if len(comparison.Changes()) != 0 ||
					comparison.PreviousPackageDigest() == comparison.CurrentPackageDigest() {
					t.Fatalf("package comparison = %#v", comparison)
				}
				return
			}
			changes := comparison.Changes()
			if len(changes) != 1 ||
				changes[0].Kind() != ChangeChanged ||
				changes[0].ID() != "records.echo/v1" ||
				!reflect.DeepEqual(changes[0].Classes(), test.classes) {
				t.Fatalf("JavaScript changes = %#v", changes)
			}
			previous, previousExists := changes[0].PreviousDigest(test.classes[0])
			current, currentExists := changes[0].CurrentDigest(test.classes[0])
			if !previousExists || !currentExists || previous == current {
				t.Fatalf("class digests = %q, %t -> %q, %t", previous, previousExists, current, currentExists)
			}
		})
	}
}

func TestCompareJavaScriptReportsDeterministicAddedAndRemovedInterfaces(t *testing.T) {
	t.Parallel()

	emptyAPI, err := javascriptgen.BuildPublicAPIEmpty()
	if err != nil {
		t.Fatalf("BuildPublicAPIEmpty: %v", err)
	}
	empty, err := NewJavaScript(emptyAPI)
	if err != nil {
		t.Fatalf("NewJavaScript(empty): %v", err)
	}
	current, err := NewJavaScript(javaScriptAPI(t, "@acme/project-sdk", "string", nil))
	if err != nil {
		t.Fatalf("NewJavaScript(current): %v", err)
	}
	added, err := CompareJavaScript(empty, current)
	if err != nil || !added.PackageChanged() || len(added.Changes()) != 1 ||
		added.Changes()[0].Kind() != ChangeAdded ||
		!reflect.DeepEqual(added.Changes()[0].Classes(), allJavaScriptClasses()) {
		t.Fatalf("added comparison = %#v, %v", added, err)
	}
	removed, err := CompareJavaScript(current, empty)
	if err != nil || !removed.PackageChanged() || len(removed.Changes()) != 1 ||
		removed.Changes()[0].Kind() != ChangeRemoved ||
		!reflect.DeepEqual(removed.Changes()[0].Classes(), allJavaScriptClasses()) {
		t.Fatalf("removed comparison = %#v, %v", removed, err)
	}
}

func TestDecodeJavaScriptRejectsMalformedOwnedHistory(t *testing.T) {
	t.Parallel()

	baseline, err := NewJavaScript(javaScriptAPI(t, "@acme/project-sdk", "string", nil))
	if err != nil {
		t.Fatalf("NewJavaScript: %v", err)
	}
	record := baseline.RecordJSON()
	interfaceDigest := baseline.Interfaces()[0].TypesDigest()
	tests := map[string][]byte{
		"empty":          nil,
		"unknown schema": bytes.Replace(record, []byte(JavaScriptSchema), []byte("plystra.interface-javascript-baseline/v2"), 1),
		"unknown field":  bytes.Replace(record, []byte(`"digest":`), []byte(`"unknown":true,"digest":`), 1),
		"tampered class": bytes.Replace(record, []byte(interfaceDigest), []byte(digest([]byte("tampered"))), 1),
		"nil interfaces": []byte(`{"schema":"` + JavaScriptSchema + `","package_digest":"` + baseline.PackageDigest() + `","interfaces":null,"digest":"` + baseline.Digest() + `"}`),
		"trailing":       append(append([]byte(nil), record...), '\n'),
		"oversized":      bytes.Repeat([]byte{'x'}, int(JavaScriptMaximumBytes)+1),
	}
	for name, data := range tests {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decoded, err := DecodeJavaScript(data)
			if !errors.Is(err, ErrHistory) || decoded.Valid() {
				t.Fatalf("DecodeJavaScript(%s) = %#v, %v", name, decoded, err)
			}
		})
	}
}

func TestReconcileJavaScriptUsesStrictOwnedHistory(t *testing.T) {
	t.Parallel()

	api := javaScriptAPI(t, "@acme/project-sdk", "string", nil)
	initial, comparison, err := ReconcileJavaScript(api, nil, false)
	if err != nil || !initial.Valid() || !comparison.Valid() || comparison.Clean() ||
		!comparison.PackageChanged() ||
		len(comparison.Changes()) != 1 ||
		comparison.Changes()[0].Kind() != ChangeAdded {
		t.Fatalf("ReconcileJavaScript(initial) = %#v, %#v, %v", initial, comparison, err)
	}
	repeated, comparison, err := ReconcileJavaScript(api, initial.RecordJSON(), true)
	if err != nil || repeated.Digest() != initial.Digest() ||
		!comparison.Valid() || !comparison.Clean() {
		t.Fatalf("ReconcileJavaScript(repeated) = %#v, %#v, %v", repeated, comparison, err)
	}
	if _, _, err := ReconcileJavaScript(api, []byte("{}"), false); !errors.Is(err, ErrHistory) {
		t.Fatalf("ReconcileJavaScript(absent bytes) error = %v", err)
	}
	if _, _, err := ReconcileJavaScript(api, []byte("{}"), true); !errors.Is(err, ErrHistory) {
		t.Fatalf("ReconcileJavaScript(malformed) error = %v", err)
	}
}

func javaScriptAPI(
	t testing.TB,
	packageName string,
	fieldType string,
	semanticErrors []string,
) javascriptgen.PublicAPI {
	t.Helper()
	const packagePath = "example.com/javascript-api/interfaces/records/echo/v1"
	source := transportInterfaceSource(fieldType)
	declarations, err := interfacedecl.ParseFile("interface.go", []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("interfacedecl.ParseFile = %#v, %v", declarations, err)
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "interface.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}
	checked, err := (&types.Config{Importer: importer.Default()}).Check(
		packagePath,
		files,
		[]*ast.File{file},
		nil,
	)
	if err != nil {
		t.Fatalf("types.Check: %v", err)
	}
	contract, err := interfacecontract.Validate(declarations[0], checked)
	if err != nil {
		t.Fatalf("interfacecontract.Validate: %v", err)
	}
	sum := sha256.Sum256([]byte(source))
	interfaceModel, err := protobufmodel.BuildInterfaces(true, []protobufmodel.InterfaceInput{{
		InterfaceID:    contract.ID(),
		PackagePath:    packagePath,
		Source:         packagePath + "/interface.go:5:1",
		Contract:       contract,
		ContractDigest: "sha256:" + hex.EncodeToString(sum[:]),
		SemanticErrors: semanticErrors,
	}})
	if err != nil {
		t.Fatalf("protobufmodel.BuildInterfaces: %v", err)
	}
	model, err := sdkmodel.Build(nil, nil)
	if err != nil {
		t.Fatalf("sdkmodel.Build: %v", err)
	}
	api, err := javascriptgen.BuildPublicAPI(packageName, model, interfaceModel)
	if err != nil {
		t.Fatalf("javascriptgen.BuildPublicAPI: %v", err)
	}
	return api
}
