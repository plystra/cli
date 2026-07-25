package javascriptgen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
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
  attempt: {type: integer, constraints: {minimum: 1, maximum: 3}}
  checkpoints: {type: array, items: integer, constraints: {min_items: 1, max_items: 2}}
  label: {type: string, constraints: {max_length: 1}}
  offset: {type: integer, constraints: {minimum: -9223372036854775808, maximum: 9223372036854775807}}
  to: {type: string, required: true, constraints: {min_length: 2, max_length: 254, pattern: '^[^@]+@[^@]+$'}}
  tags: {type: array, items: string, required: true, constraints: {min_items: 0, max_items: 2}}
  retries: {type: integer, enum: [-9223372036854775808, 0, 9223372036854775807]}
  priority: {type: string, required: true, enum: [normal, urgent]}
  metadata: {type: object}
response:
  accepted: {type: boolean, required: true}
  attempt: {type: integer, constraints: {minimum: 1, maximum: 3}}
  latency: {type: number, constraints: {minimum: 0.5, maximum: 1.5}}
  positions: {type: array, items: integer, constraints: {min_items: 1, max_items: 2}}
  revision: {type: integer, constraints: {minimum: -9223372036854775808, maximum: 9223372036854775807}}
errors: [temporarily_unavailable, invalid_recipient]
extensions:
  authn: {authenticated: true}
  testing: {marker: provider-only-marker}
`

const javascriptQuerySemantics = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

const javascriptInterfaceSource = `package echov1

import (
	"context"
	"time"
)

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
	Value Envelope ` + "`json:\"value\" plystra:\"1,required\"`" + `
}

type Response struct {
	Value Envelope ` + "`json:\"value\" plystra:\"1,required\"`" + `
}

type Envelope struct {
	Active      bool              ` + "`json:\"active\" plystra:\"1,required\"`" + `
	Count32     int32             ` + "`json:\"count_32\" plystra:\"2\"`" + `
	Count64     int64             ` + "`json:\"count_64\" plystra:\"3\"`" + `
	Unsigned32  uint32            ` + "`json:\"unsigned_32\" plystra:\"4\"`" + `
	Unsigned64  uint64            ` + "`json:\"unsigned_64\" plystra:\"5\"`" + `
	Ratio32     float32           ` + "`json:\"ratio_32\" plystra:\"6\"`" + `
	Ratio64     float64           ` + "`json:\"ratio_64\" plystra:\"7\"`" + `
	Name        string            ` + "`json:\"name\" plystra:\"8,required\"`" + `
	Payload     []byte            ` + "`json:\"payload\" plystra:\"9,required\"`" + `
	Tags        []string          ` + "`json:\"tags\" plystra:\"10\"`" + `
	Scores      map[string]int64  ` + "`json:\"scores\" plystra:\"11\"`" + `
	Detail      Detail            ` + "`json:\"detail\" plystra:\"12,required\"`" + `
	Items       []Detail          ` + "`json:\"items\" plystra:\"13\"`" + `
	Lookup      map[string]Detail ` + "`json:\"lookup\" plystra:\"14\"`" + `
	At          time.Time         ` + "`json:\"at\" plystra:\"15,required\"`" + `
	Delay       time.Duration     ` + "`json:\"delay\" plystra:\"16,required\"`" + `
	Payloads    [][]byte          ` + "`json:\"payloads\" plystra:\"17\"`" + `
	Identifiers map[int64]uint64  ` + "`json:\"identifiers\" plystra:\"18\"`" + `
	DefaultName string            ` + "`plystra:\"19\"`" + `
}

type Detail struct {
	Code   string ` + "`json:\"code\" plystra:\"1,required\"`" + `
	Amount int64  ` + "`json:\"amount\" plystra:\"2\"`" + `
}
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
	interfaceInput := javascriptInterfaceProjectionInput(t)
	options := javascriptOptionsWithInterfaces(t, "@acme/project-sdk", targets, aliases, []protobufmodel.InterfaceInput{interfaceInput})
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
		"generated/sdk/javascript/src/interfaces/records/echo/v1.ts",
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
		`RecordsEchoV1Request,`,
		`RecordsEchoV1Response,`,
		`RecordsEchoV1Envelope,`,
		`createEmailSendV1`,
		`plystra.generated.email.send.v1.EmailSendV1Service`,
		`plystra.generated.compat.send.v1.CompatSendV1Service`,
		`targetCapabilityID = "email.send/v1"`,
		`bindOperationMethod as bindCanonicalOperationMethod`,
		`resolveUnaryMethod`,
		`resolveMessage`,
		`plystra.generated.transport.v1.PlystraErrorDetail`,
		`export type PlystraErrorDetail`,
		`readonly detail: PlystraErrorDetail | undefined`,
		`semanticErrorCodes`,
		`requestedCapabilityID`,
		`/** @deprecated Use email.send/v1 instead. */`,
		`export type ErrorCode = "invalid_recipient" | "temporarily_unavailable";`,
		`isSignedInteger`,
		`isStringWithinUnicodeScalarBounds`,
		`@plystraConstraints {"min_length":2,"max_length":254,"pattern":"^[^@]+@[^@]+$"}`,
		`isStringWithinUnicodeScalarBounds(value["to"], 2, 254)`,
		`value["attempt"] >= 1n`,
		`value["attempt"] <= 3n`,
		`value["offset"] >= -9223372036854775808n`,
		`value["offset"] <= 9223372036854775807n`,
		`value["checkpoints"].length >= 1`,
		`value["checkpoints"].length <= 2`,
		`value["latency"] >= 0.5`,
		`value["latency"] <= 1.5`,
		`value["positions"].length >= 1`,
		`value["positions"].length <= 2`,
		`value["revision"] >= -9223372036854775808n`,
		`value["revision"] <= 9223372036854775807n`,
		`bigint`,
		`readonly "count_32"?: number;`,
		`readonly "count_64"?: bigint;`,
		`readonly "unsigned_32"?: number;`,
		`readonly "unsigned_64"?: bigint;`,
		`readonly "payload": Uint8Array;`,
		`readonly "payloads"?: ReadonlyArray<Uint8Array>;`,
		`readonly "scores"?: Readonly<Record<string, bigint>>;`,
		`readonly "identifiers"?: Readonly<Record<string, bigint>>;`,
		`readonly "DefaultName"?: string;`,
		`readonly "at": string;`,
		`readonly "delay": string;`,
		`Canonical Interface types preserve exact authored widths`,
		`9223372036854775807n`,
		`getAccessToken`,
		`raw token without the Bearer scheme`,
		"Pass an `AbortSignal`",
		"PlystraError` code `cancelled`",
		"does not promise Provider rollback",
		`"typescript": "7.0.2"`,
		`"@bufbuild/protobuf": "2.12.1"`,
		`"@connectrpc/connect": "2.1.2"`,
		`"@connectrpc/connect-web": "2.1.2"`,
		`"stripInternal": true`,
		`/** @internal */`,
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
	if bytes.Contains(combined, []byte("detailCode")) {
		t.Fatal("generated JavaScript SDK retains the superseded loose detailCode API")
	}
	descriptors := fileData(t, files, "generated/sdk/javascript/src/descriptors.ts")
	encodedDescriptorSet := base64.StdEncoding.EncodeToString(options.Transport.DescriptorSet)
	if bytes.Count(descriptors, []byte(encodedDescriptorSet)) != 1 {
		t.Fatal("generated JavaScript descriptors do not embed the exact deterministic Protobuf descriptor set once")
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

func TestRenderCanonicalInterfaceTypesSupersedeExactLegacySurface(t *testing.T) {
	t.Parallel()

	legacy := javascriptTarget(t, `id: records.echo/v1
request:
  value: {type: string, required: true}
response:
  value: {type: string, required: true}
`)
	model := javascriptModel(t, legacy)
	options := javascriptOptionsWithInterfaces(
		t,
		"records-sdk",
		[]javascriptTargetView{legacy},
		nil,
		[]protobufmodel.InterfaceInput{javascriptInterfaceProjectionInput(t)},
	)
	files, err := javascriptgen.Render(options, model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertFilePathMissing(t, files, "generated/sdk/javascript/src/operations/records/echo/v1.ts")
	types := fileData(t, files, "generated/sdk/javascript/src/interfaces/records/echo/v1.ts")
	if !bytes.Contains(types, []byte(`export interface RecordsEchoV1Request`)) {
		t.Fatalf("canonical Interface types are absent:\n%s", types)
	}
	index := fileData(t, files, "generated/sdk/javascript/src/index.ts")
	if bytes.Count(index, []byte("RecordsEchoV1Request")) != 1 ||
		bytes.Contains(index, []byte("RecordsEchoV1Operation")) ||
		bytes.Contains(index, []byte(`readonly "records"`)) {
		t.Fatalf("exact-ID legacy surface competes with canonical Interface types:\n%s", index)
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
			`bindCanonicalOperationMethod(runtime, method, capabilityID)`,
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
	if count := bytes.Count(deprecated, []byte("/** @deprecated Use email.send/v1 instead. */")); count != 1 {
		t.Fatalf("deprecated Alias marker count = %d:\n%s", count, deprecated)
	}
	if !bytes.Contains(deprecated, []byte("/** @internal */\nexport function bindOperation")) {
		t.Fatalf("deprecated Alias exposes its internal binder:\n%s", deprecated)
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

func TestRenderJavaScriptPreservesSigned64BitIntegers(t *testing.T) {
	t.Parallel()

	target := javascriptTarget(t, `id: metrics.record/v1
request:
  count: {type: integer, required: true}
  mode: {type: integer, required: true, enum: [-9223372036854775808, 9223372036854775807]}
  offsets: {type: array, items: integer, required: true}
response:
  count: {type: integer, required: true}
`)
	model := javascriptModel(t, target)
	files, err := javascriptgen.Render(javascriptOptions(t, "metrics-sdk", []javascriptTargetView{target}, nil), model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	source := fileData(t, files, "generated/sdk/javascript/src/operations/metrics/record/v1.ts")
	for _, required := range []string{
		`readonly "count": bigint;`,
		`readonly "mode": -9223372036854775808n | 9223372036854775807n;`,
		`readonly "offsets": ReadonlyArray<bigint>;`,
		`isSignedInteger(value["count"])`,
		`{ canonical: -9223372036854775808n,`,
		`{ canonical: 9223372036854775807n,`,
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("generated signed-integer operation omits %q:\n%s", required, source)
		}
	}
	readme := fileData(t, files, "generated/sdk/javascript/README.md")
	if !bytes.Contains(readme, []byte(`client.metrics.record.v1({"count":0n,"mode":-9223372036854775808n,"offsets":[]})`)) {
		t.Fatalf("generated README does not use bigint request literals:\n%s", readme)
	}
}

func TestRenderJavaScriptDeclaresPatternWithoutReinterpretingOrTerminatingJSDoc(t *testing.T) {
	t.Parallel()

	target := javascriptTarget(t, "id: text.match/v1\nrequest:\n  value: {type: string, required: true, constraints: {pattern: 'a*/b'}}\n")
	model := javascriptModel(t, target)
	files, err := javascriptgen.Render(javascriptOptions(t, "text-sdk", []javascriptTargetView{target}, nil), model)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	source := fileData(t, files, "generated/sdk/javascript/src/operations/text/match/v1.ts")
	if !bytes.Contains(source, []byte(`@plystraConstraints {"pattern":"a*\/b"}`)) {
		t.Fatalf("generated pattern declaration is absent or unsafe:\n%s", source)
	}
	for _, forbidden := range [][]byte{[]byte("new RegExp"), []byte("RegExp("), []byte("/a*/b/")} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("generated JavaScript reinterprets canonical Go pattern through %q:\n%s", forbidden, source)
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
	if files, err := javascriptgen.Render(javascriptgen.Options{PackageName: "valid-sdk", ConfigurationProvenance: provenance}, model); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "transport projection") {
		t.Fatalf("Render(missing transport) = %#v, %v", files, err)
	}
	corrupt := javascriptgen.Options{PackageName: "valid-sdk", ConfigurationProvenance: provenance, Transport: transport}
	corrupt.Transport.DescriptorSet = []byte{0xff}
	if files, err := javascriptgen.Render(corrupt, model); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "Connect descriptors") {
		t.Fatalf("Render(corrupt descriptors) = %#v, %v", files, err)
	}
	extra := javascriptTarget(t, "id: audit.record/v1\n")
	mismatched := javascriptgen.Options{PackageName: "valid-sdk", ConfigurationProvenance: provenance, Transport: transport}
	mismatched.Transport.DescriptorSet = javascriptOptions(t, "valid-sdk", []javascriptTargetView{target, extra}, nil).Transport.DescriptorSet
	if files, err := javascriptgen.Render(mismatched, model); !errors.Is(err, javascriptgen.ErrRender) || len(files) != 0 || !strings.Contains(err.Error(), "do not exactly match") {
		t.Fatalf("Render(valid mismatched descriptors) = %#v, %v", files, err)
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
		wantTerminators := 1
		if deprecated != "" {
			wantTerminators++
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
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema + javascriptQuerySemantics))
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
	return javascriptOptionsWithInterfaces(t, packageName, targets, aliases, nil)
}

func javascriptOptionsWithInterfaces(
	t testing.TB,
	packageName string,
	targets []javascriptTargetView,
	aliases []javascriptAliasView,
	interfaceInputs []protobufmodel.InterfaceInput,
) javascriptgen.Options {
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
	interfaceProjection, err := protobufmodel.BuildInterfaces(true, interfaceInputs)
	if err != nil {
		t.Fatalf("protobufmodel.BuildInterfaces: %v", err)
	}
	wireMap, err := protobufwiremap.Build(projection, interfaceProjection, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}
	evidence, err := protobufdescriptor.BuildWithInterfaces(projection, wireMap, interfaceProjection)
	if err != nil {
		t.Fatalf("protobufdescriptor.BuildWithInterfaces: %v", err)
	}
	return javascriptgen.Options{
		PackageName:             packageName,
		ConfigurationProvenance: javascriptConfigurationProvenance(t, generation.ConfigurationModeDefault),
		Transport: javascriptgen.TransportOptions{
			Projection:          projection,
			InterfaceProjection: interfaceProjection,
			WireMap:             wireMap,
			DescriptorSet:       evidence.DescriptorSet(),
		},
	}
}

func javascriptInterfaceProjectionInput(t testing.TB) protobufmodel.InterfaceInput {
	t.Helper()
	const packagePath = "example.com/interfaces/records/echo/v1"
	declarations, err := interfacedecl.ParseFile("interface.go", []byte(javascriptInterfaceSource))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("interfacedecl.ParseFile = %#v, %v", declarations, err)
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "interface.go", javascriptInterfaceSource, parser.AllErrors)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}
	checked, err := (&types.Config{Importer: importer.Default()}).Check(packagePath, files, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("types.Check: %v", err)
	}
	contract, err := interfacecontract.Validate(declarations[0], checked)
	if err != nil {
		t.Fatalf("interfacecontract.Validate: %v", err)
	}
	return protobufmodel.InterfaceInput{
		InterfaceID:    contract.ID(),
		PackagePath:    packagePath,
		Source:         "example.com/interfaces@v1.0.0/records/echo/v1/interface.go:8:1",
		Contract:       contract,
		ContractDigest: hash([]byte("records.echo/v1 Interface contract")),
		SemanticErrors: []string{"record_rejected"},
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

func assertFilePathMissing(t testing.TB, files []javascriptgen.File, unwanted string) {
	t.Helper()
	for _, file := range files {
		if file.Path() == unwanted {
			t.Fatalf("generated package unexpectedly contains %s", unwanted)
		}
	}
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
