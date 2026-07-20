package apidocgen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/sdkmodel"
	"github.com/plystra/cli/internal/transportprovenance"
)

var (
	updateAPIDocGolden                     = flag.Bool("update", false, "update generated application API documentation golden files")
	_                  apidocgen.AliasView = aliasresolution.Alias{}
)

const apiEmailSchema = `id: email.send/v1
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

func TestRenderApplicationAPIDocumentation(t *testing.T) {
	t.Parallel()

	email := apiTarget(t, apiEmailSchema)
	targets := []apiTargetView{
		email,
		apiTarget(t, "id: account.profile.get/v2\n"),
		apiTarget(t, "id: foo-bar.send/v1\n"),
		apiTarget(t, "id: foo.bar-send/v1\n"),
	}
	aliases := []apiAliasView{
		apiAlias(t, "mail.deliver/v1", email, generation.Exposure{HTTP: true, JavaScript: true}, "Use email.send/v1 instead."),
		apiAlias(t, "compat.send/v1", email, generation.Exposure{HTTP: true, JavaScript: true}, ""),
		apiAlias(t, "browser.hidden/v1", email, generation.Exposure{HTTP: true}, ""),
		apiAlias(t, "go.only/v1", email, generation.Exposure{Go: true}, ""),
	}
	model := apiModel(t, targets, aliases)
	provenance := apiConfigurationProvenance(t, generation.ConfigurationModeDefault)
	files, err := apidocgen.Render(model, apiAliasViews(aliases), provenance)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := filePaths(files); !slices.Equal(got, []string{"generated/docs/api.md", "generated/docs/openapi.json"}) {
		t.Fatalf("Paths = %v", got)
	}
	combined := joinFiles(files)
	for _, required := range []string{
		"POST /api/v1/capabilities/email.send/v1/invoke",
		"`compat.send/v1`",
		"`browser.hidden/v1`",
		"`mail.deliver/v1` [deprecated]",
		"Application-local Alias of `email.send/v1`",
		`"/api/v1/capabilities/compat.send/v1/invoke"`,
		`"x-plystra-canonical-target": "email.send/v1"`,
		`"deprecated": true`,
		`"additionalProperties": false`,
		`"x-plystra-semantic-errors"`,
	} {
		if !bytes.Contains(combined, []byte(required)) {
			t.Fatalf("generated documentation omits %q", required)
		}
	}
	for _, forbidden := range []string{"go.only/v1", "provider-only-marker", "plugin_id", "secret_value", "runtime_config"} {
		if bytes.Contains(bytes.ToLower(combined), []byte(forbidden)) {
			t.Fatalf("generated documentation contains forbidden value %q", forbidden)
		}
	}
	var openAPI struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]struct {
			Post struct {
				OperationID     string `json:"operationId"`
				Deprecated      bool   `json:"deprecated"`
				CapabilityID    string `json:"x-plystra-capability-id"`
				CanonicalTarget string `json:"x-plystra-canonical-target"`
			} `json:"post"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(fileData(t, files, "generated/docs/openapi.json"), &openAPI); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	if openAPI.OpenAPI != "3.1.0" || len(openAPI.Paths) != 7 {
		t.Fatalf("OpenAPI = version %q, %d paths", openAPI.OpenAPI, len(openAPI.Paths))
	}
	deprecated := openAPI.Paths["/api/v1/capabilities/mail.deliver/v1/invoke"].Post
	if !deprecated.Deprecated || deprecated.CapabilityID != "mail.deliver/v1" || deprecated.CanonicalTarget != "email.send/v1" {
		t.Fatalf("deprecated Alias operation = %#v", deprecated)
	}
	left := openAPI.Paths["/api/v1/capabilities/foo-bar.send/v1/invoke"].Post.OperationID
	right := openAPI.Paths["/api/v1/capabilities/foo.bar-send/v1/invoke"].Post.OperationID
	if left == right || !strings.HasPrefix(left, "invoke_foo_bar_send_v1_") || !strings.HasPrefix(right, "invoke_foo_bar_send_v1_") {
		t.Fatalf("colliding operation IDs = %q and %q", left, right)
	}
	assertGoldenDocumentation(t, files)

	repeated, err := apidocgen.Render(model, apiAliasViews([]apiAliasView{aliases[2], aliases[0], aliases[3], aliases[1]}), provenance)
	if err != nil || !equalFiles(files, repeated) {
		t.Fatalf("reordered Render differs: %v", err)
	}
	environmentFiles, err := apidocgen.Render(model, apiAliasViews(aliases), apiConfigurationProvenance(t, generation.ConfigurationModeEnvironment))
	if err != nil || !equalFiles(files, environmentFiles) {
		t.Fatalf("environment selection changed equal-model API documentation: %v", err)
	}
	returned := files[0].Data()
	returned[0] = 'x'
	if bytes.Equal(returned, files[0].Data()) {
		t.Fatal("File.Data exposed mutable generated storage")
	}
}

func TestRenderApplicationAPIDocumentationSupportsNoExposedCapabilities(t *testing.T) {
	t.Parallel()

	model, err := sdkmodel.BuildCanonical(nil)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	files, err := apidocgen.Render(model, nil, apiConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(files) != 2 || !bytes.Contains(fileData(t, files, "generated/docs/api.md"), []byte("No canonical HTTP Capabilities are exposed.")) {
		t.Fatalf("empty documentation = %#v\n%s", filePaths(files), joinFiles(files))
	}
	var openAPI struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(fileData(t, files, "generated/docs/openapi.json"), &openAPI); err != nil || len(openAPI.Paths) != 0 {
		t.Fatalf("empty OpenAPI paths = %#v, %v", openAPI.Paths, err)
	}
}

func TestRenderApplicationAPIDocumentationRequiresConfigurationProvenance(t *testing.T) {
	t.Parallel()

	model, err := sdkmodel.BuildCanonical(nil)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	if files, err := apidocgen.Render(model, nil, transportprovenance.Provenance{}); !errors.Is(err, apidocgen.ErrRender) || !strings.Contains(err.Error(), "configuration provenance") || len(files) != 0 {
		t.Fatalf("Render(missing provenance) = %#v, %v", files, err)
	}
}

func TestRenderApplicationAPIDocumentationRejectsInvalidAliases(t *testing.T) {
	t.Parallel()

	target := apiTarget(t, apiEmailSchema)
	canonicalModel := apiModel(t, []apiTargetView{target}, nil)
	valid := apiAlias(t, "mail.deliver/v1", target, generation.Exposure{HTTP: true}, "")
	tests := []struct {
		name    string
		aliases []apidocgen.AliasView
		want    string
	}{
		{name: "absent", aliases: []apidocgen.AliasView{nil}, want: "view is absent"},
		{name: "invalid ID", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.id = generation.CapabilityID{} })}, want: "ID"},
		{name: "invalid target", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.target = generation.CapabilityID{} })}, want: "target ID"},
		{name: "missing target", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.target = apiCapabilityID(t, "email.queue/v1") })}, want: "not a documented canonical HTTP operation"},
		{name: "canonical collision", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.id = target.id })}, want: "collides with a canonical Capability"},
		{name: "reserved", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.id = apiCapabilityID(t, "kernel.compat/v1") })}, want: "reserved kernel.*"},
		{name: "version mismatch", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.id = apiCapabilityID(t, "mail.deliver/v2") })}, want: "same version"},
		{name: "digest mismatch", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.digest = "sha256:" + strings.Repeat("0", 64) })}, want: "target digest"},
		{name: "oversized deprecation", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.deprecated = strings.Repeat("x", 1025) })}, want: "deprecation metadata"},
		{name: "NUL deprecation", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.deprecated = "unsafe\x00message" })}, want: "deprecation metadata"},
		{name: "invalid UTF-8 deprecation", aliases: []apidocgen.AliasView{withAPIAlias(valid, func(value *apiAliasView) { value.deprecated = string([]byte{0xff}) })}, want: "deprecation metadata"},
		{name: "duplicate", aliases: []apidocgen.AliasView{valid, valid}, want: "duplicates or collides"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files, err := apidocgen.Render(canonicalModel, test.aliases, apiConfigurationProvenance(t, generation.ConfigurationModeDefault))
			if !errors.Is(err, apidocgen.ErrRender) || !strings.Contains(err.Error(), test.want) || len(files) != 0 {
				t.Fatalf("Render = %#v, %v; want %q", files, err, test.want)
			}
		})
	}

	javaScriptAlias := apiAlias(t, "compat.send/v1", target, generation.Exposure{HTTP: true, JavaScript: true}, "")
	model := apiModel(t, []apiTargetView{target}, []apiAliasView{javaScriptAlias})
	if files, err := apidocgen.Render(model, nil, apiConfigurationProvenance(t, generation.ConfigurationModeDefault)); !errors.Is(err, apidocgen.ErrRender) || !strings.Contains(err.Error(), "disagrees") || len(files) != 0 {
		t.Fatalf("Render missing final Alias = %#v, %v", files, err)
	}
	if files, err := apidocgen.Render(sdkmodel.Model{}, nil, apiConfigurationProvenance(t, generation.ConfigurationModeDefault)); !errors.Is(err, apidocgen.ErrRender) || !strings.Contains(err.Error(), "model") || len(files) != 0 {
		t.Fatalf("Render zero model = %#v, %v", files, err)
	}
	goOnly := withAPIAlias(valid, func(value *apiAliasView) { value.exposure = generation.Exposure{Go: true} })
	files, err := apidocgen.Render(canonicalModel, []apidocgen.AliasView{goOnly}, apiConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil || bytes.Contains(joinFiles(files), []byte("mail.deliver/v1")) {
		t.Fatalf("Render Go-only Alias = %v\n%s", err, joinFiles(files))
	}
}

func FuzzRenderApplicationAPIDeprecation(f *testing.F) {
	for _, seed := range []string{
		"",
		"Use email.send/v1 instead.",
		"First line.\nSecond line.",
		"<script>alert(1)</script>|table",
		"invalid\x00message",
		string([]byte{0xff}),
	} {
		f.Add(seed)
	}
	target := apiTarget(f, apiEmailSchema)
	model := apiModel(f, []apiTargetView{target}, nil)
	f.Fuzz(func(t *testing.T, deprecated string) {
		if len(deprecated) > 2048 {
			return
		}
		alias := apiAlias(t, "mail.deliver/v1", target, generation.Exposure{HTTP: true}, deprecated)
		first, firstErr := apidocgen.Render(model, []apidocgen.AliasView{alias}, apiConfigurationProvenance(t, generation.ConfigurationModeDefault))
		invalid := len(deprecated) > 1024 || !utf8.ValidString(deprecated) || strings.ContainsRune(deprecated, '\x00')
		if invalid {
			if !errors.Is(firstErr, apidocgen.ErrRender) {
				t.Fatalf("Render invalid deprecation error = %v", firstErr)
			}
			return
		}
		second, secondErr := apidocgen.Render(model, []apidocgen.AliasView{alias}, apiConfigurationProvenance(t, generation.ConfigurationModeDefault))
		if firstErr != nil || secondErr != nil || !equalFiles(first, second) {
			t.Fatalf("Render is not deterministic: %v then %v", firstErr, secondErr)
		}
		var document any
		if err := json.Unmarshal(fileData(t, first, "generated/docs/openapi.json"), &document); err != nil {
			t.Fatalf("OpenAPI JSON: %v", err)
		}
		markdown := fileData(t, first, "generated/docs/api.md")
		if strings.Contains(deprecated, "<script>") && bytes.Contains(markdown, []byte("<script>")) {
			t.Fatalf("Markdown contains unescaped HTML:\n%s", markdown)
		}
	})
}

type apiTargetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
}

func (v apiTargetView) ID() generation.CapabilityID { return v.id }
func (v apiTargetView) ContractJSON() []byte        { return append([]byte(nil), v.contract...) }
func (v apiTargetView) ContractDigest() string      { return v.digest }
func (v apiTargetView) Exposure() generation.Exposure {
	return generation.Exposure{HTTP: true, JavaScript: true}
}

type apiAliasView struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	digest     string
	exposure   generation.Exposure
	deprecated string
}

func (v apiAliasView) ID() generation.CapabilityID     { return v.id }
func (v apiAliasView) Target() generation.CapabilityID { return v.target }
func (v apiAliasView) TargetContractDigest() string    { return v.digest }
func (v apiAliasView) Exposure() generation.Exposure   { return v.exposure }
func (v apiAliasView) Deprecated() string              { return v.deprecated }

func apiTarget(t testing.TB, schema string) apiTargetView {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	metadata, err := capabilitymeta.Parse(canonical)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return apiTargetView{
		id:       apiCapabilityID(t, metadata.ID().String()),
		contract: canonical,
		digest:   hash(canonical),
	}
}

func apiAlias(t testing.TB, id string, target apiTargetView, exposure generation.Exposure, deprecated string) apiAliasView {
	t.Helper()
	return apiAliasView{
		id:         apiCapabilityID(t, id),
		target:     target.id,
		digest:     target.digest,
		exposure:   exposure,
		deprecated: deprecated,
	}
}

func withAPIAlias(value apiAliasView, edit func(*apiAliasView)) apiAliasView {
	edit(&value)
	return value
}

func apiCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}

func apiModel(t testing.TB, targets []apiTargetView, aliases []apiAliasView) sdkmodel.Model {
	t.Helper()
	canonical := make([]sdkmodel.CanonicalTargetView, len(targets))
	for index, target := range targets {
		canonical[index] = target
	}
	model, err := sdkmodel.Build(canonical, sdkAliasViews(aliases))
	if err != nil {
		t.Fatalf("Build SDK model: %v", err)
	}
	return model
}

func sdkAliasViews(values []apiAliasView) []sdkmodel.AliasView {
	result := make([]sdkmodel.AliasView, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func apiAliasViews(values []apiAliasView) []apidocgen.AliasView {
	result := make([]apidocgen.AliasView, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func assertGoldenDocumentation(t *testing.T, files []apidocgen.File) {
	t.Helper()
	root := filepath.Join("testdata")
	for _, file := range files {
		relative := strings.TrimPrefix(file.Path(), "generated/docs/")
		if relative == file.Path() {
			t.Fatalf("generated file %q is outside documentation root", file.Path())
		}
		goldenPath := filepath.Join(root, filepath.FromSlash(relative))
		if *updateAPIDocGolden {
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

func filePaths(files []apidocgen.File) []string {
	result := make([]string, len(files))
	for index, file := range files {
		result[index] = file.Path()
	}
	return result
}

func fileData(t testing.TB, files []apidocgen.File, wanted string) []byte {
	t.Helper()
	for _, file := range files {
		if file.Path() == wanted {
			return file.Data()
		}
	}
	t.Fatalf("generated documentation omits %s", wanted)
	return nil
}

func joinFiles(files []apidocgen.File) []byte {
	var result []byte
	for _, file := range files {
		result = append(result, file.Data()...)
	}
	return result
}

func equalFiles(left, right []apidocgen.File) bool {
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

func apiConfigurationProvenance(t testing.TB, mode generation.ConfigurationMode) transportprovenance.Provenance {
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
