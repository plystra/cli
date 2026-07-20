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
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/sdkmodel"
	"github.com/plystra/cli/internal/transportprovenance"
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

	email := javascriptTarget(t, javascriptEmailSchema)
	targets := []javascriptTargetView{
		email,
		javascriptTarget(t, "id: account.profile.get/v2\n"),
		javascriptTarget(t, "id: alpha.beta/v1\n"),
		javascriptTarget(t, "id: alpha.beta.v1.check/v1\n"),
		javascriptTarget(t, "id: foo-bar.send/v1\n"),
		javascriptTarget(t, "id: foo.bar-send/v1\n"),
	}
	aliases := []javascriptAliasView{
		javascriptAlias(t, "mail.deliver/v1", email, "Use email.send/v1 instead."),
		javascriptAlias(t, "compat.send/v1", email, ""),
		javascriptHiddenAlias(t, "internal.send/v1", email),
	}
	model := javascriptModelWithAliases(t, targets, aliases)
	options := javascriptOptions(t, "@acme/project-sdk", targets, aliases)
	files, err := javascriptgen.Render(options, model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantPaths := []string{
		"generated/sdk/javascript/.npmrc",
		"generated/sdk/javascript/README.md",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/descriptors.ts",
		"generated/sdk/javascript/src/index.ts",
		"generated/sdk/javascript/src/operations/account/profile/get/v2.ts",
		"generated/sdk/javascript/src/operations/alpha/beta/v1.ts",
		"generated/sdk/javascript/src/operations/alpha/beta/v1/check/v1.ts",
		"generated/sdk/javascript/src/operations/compat/send/v1.ts",
		"generated/sdk/javascript/src/operations/email/send/v1.ts",
		"generated/sdk/javascript/src/operations/foo-bar/send/v1.ts",
		"generated/sdk/javascript/src/operations/foo/bar-send/v1.ts",
		"generated/sdk/javascript/src/operations/mail/deliver/v1.ts",
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
		`plystra.generated.email.send.v1.EmailSendV1Service`,
		`plystra.generated.compat.send.v1.CompatSendV1Service`,
		`targetCapabilityID = "email.send/v1"`,
		`bindOperationMethod as bindCanonicalOperationMethod`,
		`resolveUnaryMethod`,
		`/** @deprecated Use email.send/v1 instead. */`,
		`export type ErrorCode = "invalid_recipient" | "temporarily_unavailable";`,
		`Number.isSafeInteger`,
		`getAccessToken`,
		`raw token without the Bearer scheme`,
		`"typescript": "7.0.2"`,
		`"@bufbuild/protobuf": "2.12.1"`,
		`"@connectrpc/connect": "2.1.2"`,
		`"@connectrpc/connect-web": "2.1.2"`,
		`package-lock=false`,
		`npm install --ignore-scripts --no-audit --no-fund`,
	} {
		if !bytes.Contains(combined, []byte(required)) {
			t.Fatalf("generated package omits %q", required)
		}
	}
	for _, forbidden := range []string{"provider-only-marker", "generated/go", "kernelinvocation", "plugin_id", "secret_value", "api/v1/capabilities", `Accept: "application/json"`} {
		if bytes.Contains(bytes.ToLower(combined), []byte(forbidden)) {
			t.Fatalf("generated package contains forbidden provider/server value %q", forbidden)
		}
	}
	assertGoldenPackage(t, files)

	repeated, err := javascriptgen.Render(options, model)
	if err != nil || !equalFiles(repeated, files) {
		t.Fatalf("repeated Render differs: %v", err)
	}
	environmentOptions := options
	environmentOptions.ConfigurationProvenance = javascriptConfigurationProvenance(t, generation.ConfigurationModeEnvironment)
	environmentFiles, err := javascriptgen.Render(environmentOptions, model)
	if err != nil || !equalFiles(environmentFiles, files) {
		t.Fatalf("environment selection changed equal-model JavaScript source: %v", err)
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

func TestRenderJavaScriptAliasesReuseCanonicalOperation(t *testing.T) {
	t.Parallel()

	email := javascriptTarget(t, javascriptEmailSchema)
	targets := []javascriptTargetView{email}
	aliases := []javascriptAliasView{
		javascriptAlias(t, "compat.send/v1", email, ""),
		javascriptAlias(t, "mail.deliver/v1", email, "Use email.send/v1 instead."),
	}
	model := javascriptModelWithAliases(t, targets, aliases)
	files, err := javascriptgen.Render(javascriptOptions(t, "alias-sdk", targets, aliases), model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compat := fileData(t, files, "generated/sdk/javascript/src/operations/compat/send/v1.ts")
	deprecated := fileData(t, files, "generated/sdk/javascript/src/operations/mail/deliver/v1.ts")
	index := fileData(t, files, "generated/sdk/javascript/src/index.ts")
	for _, alias := range [][]byte{compat, deprecated} {
		for _, required := range []string{
			`from "../../../operations/email/send/v1.js"`,
			`bindCanonicalOperationMethod(runtime, method)`,
			`export type { ErrorCode, Operation, Request, Response }`,
		} {
			if !bytes.Contains(alias, []byte(required)) {
				t.Fatalf("Alias operation omits %q:\n%s", required, alias)
			}
		}
		for _, forbidden := range []string{"function isRequest", "function isResponse", "invoke(runtime", "interface Request", "interface Response", "requestCodec", "responseCodec"} {
			if bytes.Contains(alias, []byte(forbidden)) {
				t.Fatalf("Alias operation duplicates canonical concern %q:\n%s", forbidden, alias)
			}
		}
	}
	if count := bytes.Count(deprecated, []byte("/** @deprecated Use email.send/v1 instead. */")); count != 2 {
		t.Fatalf("deprecated Alias marker count = %d:\n%s", count, deprecated)
	}
	if count := bytes.Count(index, []byte("/** @deprecated Use email.send/v1 instead. */")); count != 2 {
		t.Fatalf("deprecated index marker count = %d:\n%s", count, index)
	}
	for _, required := range []string{
		`readonly "compat"`,
		`readonly "mail"`,
		`export const createMailDeliverV1 = createMailDeliverV1Operation;`,
	} {
		if !bytes.Contains(index, []byte(required)) {
			t.Fatalf("index omits %q:\n%s", required, index)
		}
	}
}

func TestRenderCanonicalJavaScriptHandlesDeepAndCollidingNames(t *testing.T) {
	t.Parallel()

	targets := []javascriptTargetView{
		javascriptTarget(t, "id: alpha.beta/v1\n"),
		javascriptTarget(t, "id: alpha.beta.v1.check/v1\n"),
		javascriptTarget(t, "id: foo-bar.send/v1\n"),
		javascriptTarget(t, "id: foo.bar-send/v1\n"),
	}
	model := javascriptModel(t, targets...)
	files, err := javascriptgen.Render(javascriptOptions(t, "application-sdk", targets, nil), model)
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
	files, err := javascriptgen.Render(javascriptOptions(t, "empty-sdk", nil, nil), model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	index := fileData(t, files, "generated/sdk/javascript/src/index.ts")
	if !bytes.Contains(index, []byte("Readonly<Record<string, never>>")) || bytes.Contains(index, []byte("src/operations")) {
		t.Fatalf("empty index:\n%s", index)
	}
	if len(files) != 7 {
		t.Fatalf("empty package file count = %d", len(files))
	}
}

func TestRenderCanonicalJavaScriptRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	target := javascriptTarget(t, javascriptEmailSchema)
	model := javascriptModel(t, target)
	transport := javascriptOptions(t, "valid-sdk", []javascriptTargetView{target}, nil).Transport
	provenance := javascriptConfigurationProvenance(t, generation.ConfigurationModeDefault)
	if files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "valid-sdk", Transport: transport}, model); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "configuration provenance") {
		t.Fatalf("Render(missing provenance) = %#v, %v", files, err)
	}
	for _, name := range []string{"", "@acme", "@Acme/app", "@acme/app/extra", ".hidden", "Upper", "name/", "a..b"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files, err := javascriptgen.Render(javascriptgen.Options{PackageName: name, ConfigurationProvenance: provenance, Transport: transport}, model)
			if !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "npm package name") {
				t.Fatalf("Render(%q) = %#v, %v", name, files, err)
			}
		})
	}
	if files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "valid-sdk", ConfigurationProvenance: provenance, Transport: transport}, sdkmodel.Model{}); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "SDK model") {
		t.Fatalf("Render(zero model) = %#v, %v", files, err)
	}
	unsafeTarget := javascriptTarget(t, "id: metrics.record/v1\nrequest:\n  count: {type: integer, required: true, enum: [9007199254740992]}\n")
	unsafeInteger := javascriptModel(t, unsafeTarget)
	if files, err := javascriptgen.Render(javascriptOptions(t, "valid-sdk", []javascriptTargetView{unsafeTarget}, nil), unsafeInteger); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "safe-integer") {
		t.Fatalf("Render(unsafe integer enum) = %#v, %v", files, err)
	}
	if files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "valid-sdk", ConfigurationProvenance: provenance}, model); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "transport projection") {
		t.Fatalf("Render(missing transport) = %#v, %v", files, err)
	}
	corrupt := javascriptgen.Options{PackageName: "valid-sdk", ConfigurationProvenance: provenance, Transport: transport}
	corrupt.Transport.DescriptorSet = []byte{0xff}
	if files, err := javascriptgen.Render(corrupt, model); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "Connect descriptors") {
		t.Fatalf("Render(corrupt descriptors) = %#v, %v", files, err)
	}
}

func FuzzRenderCanonicalJavaScriptPackageName(f *testing.F) {
	target := javascriptTarget(f, javascriptEmailSchema)
	model := javascriptModel(f, target)
	transport := javascriptOptions(f, "application-sdk", []javascriptTargetView{target}, nil).Transport
	provenance := javascriptConfigurationProvenance(f, generation.ConfigurationModeDefault)
	for _, seed := range []string{"@acme/project-sdk", "application-sdk", "", "@Bad/name", "a..b", "name_with.parts"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, packageName string) {
		if len(packageName) > 512 {
			return
		}
		first, firstErr := javascriptgen.Render(javascriptgen.Options{PackageName: packageName, ConfigurationProvenance: provenance, Transport: transport}, model)
		second, secondErr := javascriptgen.Render(javascriptgen.Options{PackageName: packageName, ConfigurationProvenance: provenance, Transport: transport}, model)
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

func FuzzRenderJavaScriptAliasDeprecation(f *testing.F) {
	for _, seed := range []string{
		"",
		"Use email.send/v1 instead.",
		"First line.\nSecond line.",
		"close */ comment",
		"carriage\rreturn",
		"invalid\x00message",
		string([]byte{0xff}),
	} {
		f.Add(seed)
	}
	target := javascriptTarget(f, javascriptEmailSchema)
	f.Fuzz(func(t *testing.T, deprecated string) {
		if len(deprecated) > 2048 {
			return
		}
		alias := javascriptAlias(t, "mail.deliver/v1", target, deprecated)
		model, err := sdkmodel.Build(
			[]sdkmodel.CanonicalTargetView{target},
			[]sdkmodel.AliasView{alias},
		)
		invalid := len(deprecated) > 1024 || !utf8.ValidString(deprecated) || strings.ContainsRune(deprecated, '\x00')
		if invalid {
			if !errors.Is(err, sdkmodel.ErrAlias) {
				t.Fatalf("Build invalid deprecation error = %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		targets := []javascriptTargetView{target}
		aliases := []javascriptAliasView{alias}
		options := javascriptOptions(t, "alias-sdk", targets, aliases)
		first, firstErr := javascriptgen.Render(options, model)
		second, secondErr := javascriptgen.Render(options, model)
		if firstErr != nil || secondErr != nil || !equalFiles(first, second) {
			t.Fatalf("Render Alias is not deterministic: %v then %v", firstErr, secondErr)
		}
		generated := fileData(t, first, "generated/sdk/javascript/src/operations/mail/deliver/v1.ts")
		wantTerminators := 0
		if deprecated != "" {
			wantTerminators = 2
		}
		if got := bytes.Count(generated, []byte("*/")); got != wantTerminators {
			t.Fatalf("Alias JSDoc terminators = %d, want %d:\n%s", got, wantTerminators, generated)
		}
	})
}

type javascriptTargetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
}

type javascriptAliasView struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	digest     string
	exposure   generation.Exposure
	deprecated string
}

func (v javascriptTargetView) ID() generation.CapabilityID { return v.id }
func (v javascriptTargetView) ContractJSON() []byte        { return append([]byte(nil), v.contract...) }
func (v javascriptTargetView) ContractDigest() string      { return v.digest }
func (v javascriptTargetView) Sources() []string {
	return []string{"test@local/" + v.id.String() + "/capability.yaml"}
}
func (v javascriptTargetView) Exposure() generation.Exposure {
	return generation.Exposure{HTTP: true, JavaScript: true}
}

func (v javascriptAliasView) ID() generation.CapabilityID     { return v.id }
func (v javascriptAliasView) Target() generation.CapabilityID { return v.target }
func (v javascriptAliasView) TargetContractDigest() string    { return v.digest }
func (v javascriptAliasView) Exposure() generation.Exposure   { return v.exposure }
func (v javascriptAliasView) Deprecated() string              { return v.deprecated }

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

func javascriptAlias(t testing.TB, id string, target javascriptTargetView, deprecated string) javascriptAliasView {
	t.Helper()
	return javascriptAliasView{
		id:         javascriptCapabilityID(t, id),
		target:     target.id,
		digest:     target.digest,
		exposure:   generation.Exposure{HTTP: true, JavaScript: true},
		deprecated: deprecated,
	}
}

func javascriptHiddenAlias(t testing.TB, id string, target javascriptTargetView) javascriptAliasView {
	t.Helper()
	return javascriptAliasView{
		id:       javascriptCapabilityID(t, id),
		target:   target.id,
		digest:   target.digest,
		exposure: generation.Exposure{Go: true},
	}
}

func javascriptCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}

func javascriptModel(t testing.TB, targets ...javascriptTargetView) sdkmodel.Model {
	t.Helper()
	return javascriptModelWithAliases(t, targets, nil)
}

func javascriptModelWithAliases(t testing.TB, targets []javascriptTargetView, aliases []javascriptAliasView) sdkmodel.Model {
	t.Helper()
	views := make([]sdkmodel.CanonicalTargetView, len(targets))
	for index, target := range targets {
		views[index] = target
	}
	aliasViews := make([]sdkmodel.AliasView, len(aliases))
	for index, alias := range aliases {
		aliasViews[index] = alias
	}
	model, err := sdkmodel.Build(views, aliasViews)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return model
}

func javascriptOptions(t testing.TB, packageName string, targets []javascriptTargetView, aliases []javascriptAliasView) javascriptgen.Options {
	t.Helper()
	targetViews := make([]protobufmodel.CanonicalTargetView, len(targets))
	for index, target := range targets {
		targetViews[index] = target
	}
	aliasViews := make([]protobufmodel.AliasView, len(aliases))
	for index, alias := range aliases {
		aliasViews[index] = alias
	}
	projection, err := protobufmodel.Build(true, targetViews, aliasViews)
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	wireMap, err := protobufwiremap.Build(projection, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}
	evidence, err := protobufdescriptor.Build(projection, wireMap)
	if err != nil {
		t.Fatalf("protobufdescriptor.Build: %v", err)
	}
	return javascriptgen.Options{
		PackageName:             packageName,
		ConfigurationProvenance: javascriptConfigurationProvenance(t, generation.ConfigurationModeDefault),
		Transport: javascriptgen.TransportOptions{
			Projection:    projection,
			WireMap:       wireMap,
			DescriptorSet: evidence.DescriptorSet(),
		},
	}
}

func javascriptConfigurationProvenance(t testing.TB, mode generation.ConfigurationMode) transportprovenance.Provenance {
	t.Helper()
	rootDigest := "sha256:" + strings.Repeat("1", 64)
	input := transportprovenance.Input{
		Mode:                        mode,
		RootPath:                    "plystra.yaml",
		RootDigest:                  rootDigest,
		SelectedPath:                "plystra.yaml",
		SelectedDigest:              rootDigest,
		DependencyCompositionDigest: "sha256:" + strings.Repeat("2", 64),
		ApplicationModelDigest:      "sha256:" + strings.Repeat("3", 64),
	}
	if mode == generation.ConfigurationModeEnvironment {
		input.Environment = "production"
		input.SelectedPath = "plystra.production.yaml"
		input.SelectedDigest = "sha256:" + strings.Repeat("4", 64)
	}
	provenance, err := transportprovenance.New(input)
	if err != nil {
		t.Fatalf("transportprovenance.New: %v", err)
	}
	return provenance
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
