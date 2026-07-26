package javascriptgen_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufmodel"
)

func TestBuildPublicAPIUsesRenderedInterfaceNamesAndCallerVisibleTypes(t *testing.T) {
	t.Parallel()

	input := javascriptInterfaceProjectionInput(t)
	options := javascriptOptionsWithInterfaces(
		t,
		"@acme/project-sdk",
		nil,
		nil,
		[]protobufmodel.InterfaceInput{input},
	)
	model := javascriptModel(t)
	api, err := javascriptgen.BuildPublicAPI(
		options.PackageName,
		model,
		options.Transport.InterfaceProjection,
	)
	if err != nil || !api.Valid() {
		t.Fatalf("BuildPublicAPI = %#v, %v", api, err)
	}
	const (
		wantPackageDigest = "sha256:b1a54d408b90e311da8bcc90ce067d81eeb559f5c6b79fb6280976593a502062"
		wantSurfaceDigest = "sha256:1e349062c883a95d9431328653050ac8e7efb9464b6e269ac93a9938c503c712"
		wantTypesDigest   = "sha256:a0b62b98c6cb4137eb59ce893d553ec53de94989895ba8677074dbcfe6887fc5"
		wantErrorsDigest  = "sha256:01177aceea6027c3781dd05b2f1020da1e5f7f7102ce022a832378de3865130c"
	)
	interfaces := api.Interfaces()
	if api.PackageDigest() != wantPackageDigest ||
		len(interfaces) != 1 ||
		interfaces[0].ID() != "records.echo/v1" ||
		interfaces[0].SurfaceDigest() != wantSurfaceDigest ||
		interfaces[0].TypesDigest() != wantTypesDigest ||
		interfaces[0].SemanticErrorsDigest() != wantErrorsDigest {
		t.Fatalf(
			"public API = package %q, interfaces %#v, canonical %s",
			api.PackageDigest(),
			interfaces,
			api.CanonicalJSON(),
		)
	}
	for _, forbidden := range []string{
		input.PackagePath,
		input.Source,
		input.ContractDigest,
		"Config",
		"Secret",
		"Implementation",
	} {
		if bytes.Contains(api.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("public API contains forbidden value %q: %s", forbidden, api.CanonicalJSON())
		}
	}

	files, err := javascriptgen.Render(options, model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	index := generatedJavaScriptFile(t, files, "generated/sdk/javascript/src/index.ts")
	module := generatedJavaScriptFile(t, files, "generated/sdk/javascript/src/interfaces/records/echo/v1.ts")
	for _, fragment := range []string{
		"export { createRecordsEchoV1 };",
		"RecordsEchoV1ErrorCode",
		"RecordsEchoV1Request",
		"RecordsEchoV1Response",
		"RecordsEchoV1Envelope",
		`readonly "records":`,
		`readonly "echo":`,
		`readonly "v1": RecordsEchoV1Operation`,
	} {
		if !bytes.Contains(index, []byte(fragment)) {
			t.Fatalf("generated index omits %q:\n%s", fragment, index)
		}
	}
	for _, fragment := range []string{
		`readonly "active": boolean;`,
		`readonly "count_32"?: number;`,
		`readonly "count_64"?: bigint;`,
		`readonly "payload": Uint8Array;`,
		`readonly "tags"?: ReadonlyArray<string>;`,
		`readonly "scores"?: Readonly<Record<string, bigint>>;`,
		`readonly "at": string;`,
		`readonly "delay": string;`,
		`export type ErrorCode = "record_rejected";`,
	} {
		if !bytes.Contains(module, []byte(fragment)) {
			t.Fatalf("generated Interface module omits %q:\n%s", fragment, module)
		}
	}

	canonical := api.CanonicalJSON()
	canonical[0] ^= 0xff
	interfaces[0] = javascriptgen.PublicInterfaceAPI{}
	if !api.Valid() || api.Interfaces()[0].ID() != "records.echo/v1" {
		t.Fatal("public API exposed mutable storage")
	}
}

func TestBuildPublicAPIClassifiesPackageTypesAndSemanticErrorsIndependently(t *testing.T) {
	t.Parallel()

	build := func(packageName, source string, errors []string) javascriptgen.PublicAPI {
		t.Helper()
		input := javascriptInterfaceProjectionInputFromSource(
			t,
			"example.com/interfaces/records/echo/v1",
			"example.com/interfaces@v1.0.0/records/echo/v1/interface.go:8:1",
			source,
			errors,
		)
		options := javascriptOptionsWithInterfaces(
			t,
			packageName,
			nil,
			nil,
			[]protobufmodel.InterfaceInput{input},
		)
		api, err := javascriptgen.BuildPublicAPI(
			options.PackageName,
			javascriptModel(t),
			options.Transport.InterfaceProjection,
		)
		if err != nil {
			t.Fatalf("BuildPublicAPI: %v", err)
		}
		return api
	}

	base := build("@acme/project-sdk", javascriptInterfaceSource, []string{"record_rejected"})
	packageChanged := build("@acme/other-sdk", javascriptInterfaceSource, []string{"record_rejected"})
	typeChanged := build(
		"@acme/project-sdk",
		strings.Replace(javascriptInterfaceSource, "Count32     int32", "Count32     int64", 1),
		[]string{"record_rejected"},
	)
	errorChanged := build(
		"@acme/project-sdk",
		javascriptInterfaceSource,
		[]string{"record_rejected", "record_unavailable"},
	)
	baseInterface := base.Interfaces()[0]
	if packageChanged.PackageDigest() == base.PackageDigest() ||
		packageChanged.Interfaces()[0].SurfaceDigest() != baseInterface.SurfaceDigest() ||
		packageChanged.Interfaces()[0].TypesDigest() != baseInterface.TypesDigest() ||
		packageChanged.Interfaces()[0].SemanticErrorsDigest() != baseInterface.SemanticErrorsDigest() {
		t.Fatal("package identity did not change only the shared package API")
	}
	if typeChanged.PackageDigest() != base.PackageDigest() ||
		typeChanged.Interfaces()[0].SurfaceDigest() != baseInterface.SurfaceDigest() ||
		typeChanged.Interfaces()[0].TypesDigest() == baseInterface.TypesDigest() ||
		typeChanged.Interfaces()[0].SemanticErrorsDigest() != baseInterface.SemanticErrorsDigest() {
		t.Fatal("TypeScript mapping change did not isolate the public-types class")
	}
	if errorChanged.PackageDigest() != base.PackageDigest() ||
		errorChanged.Interfaces()[0].SurfaceDigest() != baseInterface.SurfaceDigest() ||
		errorChanged.Interfaces()[0].TypesDigest() != baseInterface.TypesDigest() ||
		errorChanged.Interfaces()[0].SemanticErrorsDigest() == baseInterface.SemanticErrorsDigest() {
		t.Fatal("semantic-error change did not isolate the error-union class")
	}
}

func TestBuildPublicAPIIsIndependentOfInterfaceDiscoveryOrder(t *testing.T) {
	t.Parallel()

	echo := javascriptInterfaceProjectionInput(t)
	audit := javascriptInterfaceProjectionInputFromSource(
		t,
		"example.com/interfaces/audit/record/v1",
		"example.com/interfaces@v1.0.0/audit/record/v1/interface.go:5:1",
		javascriptAuditInterfaceSource,
		nil,
	)
	build := func(inputs []protobufmodel.InterfaceInput) javascriptgen.PublicAPI {
		t.Helper()
		options := javascriptOptionsWithInterfaces(t, "@acme/project-sdk", nil, nil, inputs)
		api, err := javascriptgen.BuildPublicAPI(
			options.PackageName,
			javascriptModel(t),
			options.Transport.InterfaceProjection,
		)
		if err != nil {
			t.Fatalf("BuildPublicAPI: %v", err)
		}
		return api
	}
	first := build([]protobufmodel.InterfaceInput{echo, audit})
	second := build([]protobufmodel.InterfaceInput{audit, echo})
	if first.Digest() != second.Digest() ||
		!bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) ||
		!reflect.DeepEqual(first.Interfaces(), second.Interfaces()) {
		t.Fatalf("discovery order changed public API:\n%s\n%s", first.CanonicalJSON(), second.CanonicalJSON())
	}
}

func generatedJavaScriptFile(t testing.TB, files []javascriptgen.File, path string) []byte {
	t.Helper()
	for _, file := range files {
		if file.Path() == path {
			return file.Data()
		}
	}
	t.Fatalf("generated JavaScript file %s is absent", path)
	return nil
}
