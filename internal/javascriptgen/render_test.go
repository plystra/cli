package javascriptgen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/sdkmodel"
)

var updateJavaScriptGolden = flag.Bool("update", false, "update generated JavaScript SDK golden files")

const javascriptEmailSchema = `id: email.send/v1
request:
  to: {type: string, required: true}
  tags: {type: array, items: string, required: true}
  retries: {type: integer, enum: [-1, 0, 2]}
  priority: {type: string, required: true, enum: [normal, urgent]}
  metadata: {type: object}
response:
  accepted: {type: boolean, required: true}
  latency: {type: number}
errors: [temporarily_unavailable, invalid_recipient]
extensions:
  authn: {authenticated: true}
  testing: {marker: provider-only-marker}
`

func TestRenderCanonicalJavaScriptPackage(t *testing.T) {
	t.Parallel()

	model := javascriptModel(t,
		javascriptTarget(t, javascriptEmailSchema),
		javascriptTarget(t, "id: account.profile.get/v2\n"),
		javascriptTarget(t, "id: alpha.beta/v1\n"),
		javascriptTarget(t, "id: alpha.beta.v1.check/v1\n"),
		javascriptTarget(t, "id: foo-bar.send/v1\n"),
		javascriptTarget(t, "id: foo.bar-send/v1\n"),
	)
	files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "@acme/project-sdk"}, model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantPaths := []string{
		"generated/sdk/javascript/README.md",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/index.ts",
		"generated/sdk/javascript/src/operations/account/profile/get/v2.ts",
		"generated/sdk/javascript/src/operations/alpha/beta/v1.ts",
		"generated/sdk/javascript/src/operations/alpha/beta/v1/check/v1.ts",
		"generated/sdk/javascript/src/operations/email/send/v1.ts",
		"generated/sdk/javascript/src/operations/foo-bar/send/v1.ts",
		"generated/sdk/javascript/src/operations/foo/bar-send/v1.ts",
		"generated/sdk/javascript/src/runtime.ts",
		"generated/sdk/javascript/tsconfig.json",
	}
	if got := filePaths(files); !slices.Equal(got, wantPaths) {
		t.Fatalf("Paths = %v, want %v", got, wantPaths)
	}
	combined := joinFiles(files)
	for _, required := range []string{
		`export function createPlystraClient(options: ClientOptions): PlystraClient`,
		`readonly "email":`,
		`readonly "send":`,
		`readonly "v1": EmailSendV1Operation`,
		`createEmailSendV1`,
		`api/v1/capabilities/email.send/v1/invoke`,
		`export type ErrorCode = "invalid_recipient" | "temporarily_unavailable";`,
		`Number.isSafeInteger`,
		`getAccessToken`,
		`"typescript": "7.0.2"`,
	} {
		if !bytes.Contains(combined, []byte(required)) {
			t.Fatalf("generated package omits %q", required)
		}
	}
	for _, forbidden := range []string{"provider-only-marker", "generated/go", "kernelinvocation", "plugin_id", "secret_value"} {
		if bytes.Contains(bytes.ToLower(combined), []byte(forbidden)) {
			t.Fatalf("generated package contains forbidden provider/server value %q", forbidden)
		}
	}
	assertGoldenPackage(t, files)

	repeated, err := javascriptgen.Render(javascriptgen.Options{PackageName: "@acme/project-sdk"}, model)
	if err != nil || !equalFiles(repeated, files) {
		t.Fatalf("repeated Render differs: %v", err)
	}
	returned := files[0].Data()
	returned[0] = 'x'
	if bytes.Equal(returned, files[0].Data()) {
		t.Fatal("File.Data exposed mutable generated storage")
	}
	files[0] = javascriptgen.File{}
	if repeated[0].Path() == "" {
		t.Fatal("Render exposed shared File slice storage")
	}
}

func TestRenderCanonicalJavaScriptHandlesDeepAndCollidingNames(t *testing.T) {
	t.Parallel()

	model := javascriptModel(t,
		javascriptTarget(t, "id: alpha.beta/v1\n"),
		javascriptTarget(t, "id: alpha.beta.v1.check/v1\n"),
		javascriptTarget(t, "id: foo-bar.send/v1\n"),
		javascriptTarget(t, "id: foo.bar-send/v1\n"),
	)
	files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "application-sdk"}, model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	index := fileData(t, files, "generated/sdk/javascript/src/index.ts")
	for _, required := range []string{
		`Object.freeze(Object.assign(bindAlphaBetaV1(runtime), {`,
		`"v1": Object.freeze(Object.assign(`,
		`FooBarSendV1_666f6f2d6261722e73656e642f7631`,
		`FooBarSendV1_666f6f2e6261722d73656e642f7631`,
	} {
		if !bytes.Contains(index, []byte(required)) {
			t.Fatalf("index omits %q:\n%s", required, index)
		}
	}
}

func TestRenderCanonicalJavaScriptSupportsEmptySDK(t *testing.T) {
	t.Parallel()

	model, err := sdkmodel.BuildCanonical(nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "empty-sdk"}, model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	index := fileData(t, files, "generated/sdk/javascript/src/index.ts")
	if !bytes.Contains(index, []byte("Readonly<Record<string, never>>")) || bytes.Contains(index, []byte("src/operations")) {
		t.Fatalf("empty index:\n%s", index)
	}
	if len(files) != 5 {
		t.Fatalf("empty package file count = %d", len(files))
	}
}

func TestRenderCanonicalJavaScriptRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	model := javascriptModel(t, javascriptTarget(t, javascriptEmailSchema))
	for _, name := range []string{"", "@acme", "@Acme/app", "@acme/app/extra", ".hidden", "Upper", "name/", "a..b"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files, err := javascriptgen.Render(javascriptgen.Options{PackageName: name}, model)
			if !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "npm package name") {
				t.Fatalf("Render(%q) = %#v, %v", name, files, err)
			}
		})
	}
	if files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "valid-sdk"}, sdkmodel.Model{}); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "SDK model") {
		t.Fatalf("Render(zero model) = %#v, %v", files, err)
	}
	unsafeInteger := javascriptModel(t, javascriptTarget(t, "id: metrics.record/v1\nrequest:\n  count: {type: integer, required: true, enum: [9007199254740992]}\n"))
	if files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "valid-sdk"}, unsafeInteger); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "safe-integer") {
		t.Fatalf("Render(unsafe integer enum) = %#v, %v", files, err)
	}
}

func FuzzRenderCanonicalJavaScriptPackageName(f *testing.F) {
	model := javascriptModel(f, javascriptTarget(f, javascriptEmailSchema))
	for _, seed := range []string{"@acme/project-sdk", "application-sdk", "", "@Bad/name", "a..b", "name_with.parts"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, packageName string) {
		if len(packageName) > 512 {
			return
		}
		first, firstErr := javascriptgen.Render(javascriptgen.Options{PackageName: packageName}, model)
		second, secondErr := javascriptgen.Render(javascriptgen.Options{PackageName: packageName}, model)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("Render changed result: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, javascriptgen.ErrRender) || !errors.Is(secondErr, javascriptgen.ErrRender) {
				t.Fatalf("Render errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		if !equalFiles(first, second) {
			t.Fatal("Render is nondeterministic")
		}
	})
}

type javascriptTargetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
}

func (v javascriptTargetView) ID() generation.CapabilityID { return v.id }
func (v javascriptTargetView) ContractJSON() []byte        { return append([]byte(nil), v.contract...) }
func (v javascriptTargetView) ContractDigest() string      { return v.digest }
func (v javascriptTargetView) Exposure() generation.Exposure {
	return generation.Exposure{HTTP: true, JavaScript: true}
}

func javascriptTarget(t testing.TB, schema string) javascriptTargetView {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	metadata, err := capabilitymeta.Parse(canonical)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	id, err := generation.ParseCapabilityID(metadata.ID().String())
	if err != nil {
		t.Fatalf("ParseCapabilityID: %v", err)
	}
	return javascriptTargetView{id: id, contract: canonical, digest: hash(canonical)}
}

func javascriptModel(t testing.TB, targets ...javascriptTargetView) sdkmodel.Model {
	t.Helper()
	views := make([]sdkmodel.CanonicalTargetView, len(targets))
	for index, target := range targets {
		views[index] = target
	}
	model, err := sdkmodel.BuildCanonical(views)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	return model
}

func assertGoldenPackage(t *testing.T, files []javascriptgen.File) {
	t.Helper()
	root := filepath.Join("testdata", "canonical")
	for _, file := range files {
		relative := strings.TrimPrefix(file.Path(), "generated/sdk/javascript/")
		if relative == file.Path() {
			t.Fatalf("generated file %q is outside JavaScript SDK root", file.Path())
		}
		goldenPath := filepath.Join(root, filepath.FromSlash(relative))
		if *updateJavaScriptGolden {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatalf("MkdirAll(%s): %v", goldenPath, err)
			}
			if err := os.WriteFile(goldenPath, file.Data(), 0o644); err != nil {
				t.Fatalf("WriteFile(%s): %v", goldenPath, err)
			}
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", goldenPath, err)
		}
		if !bytes.Equal(file.Data(), want) {
			t.Fatalf("%s differs from golden:\n%s\nwant:\n%s", file.Path(), file.Data(), want)
		}
	}
}

func filePaths(files []javascriptgen.File) []string {
	result := make([]string, len(files))
	for index, file := range files {
		result[index] = file.Path()
	}
	return result
}

func fileData(t testing.TB, files []javascriptgen.File, wanted string) []byte {
	t.Helper()
	for _, file := range files {
		if file.Path() == wanted {
			return file.Data()
		}
	}
	t.Fatalf("generated package omits %s", wanted)
	return nil
}

func joinFiles(files []javascriptgen.File) []byte {
	var result []byte
	for _, file := range files {
		result = append(result, file.Data()...)
	}
	return result
}

func equalFiles(left, right []javascriptgen.File) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path() != right[index].Path() || !bytes.Equal(left[index].Data(), right[index].Data()) {
			return false
		}
	}
	return true
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
