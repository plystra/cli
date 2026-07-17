package sdkmodel_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/sdkmodel"
)

var (
	_ sdkmodel.CanonicalTargetView = generation.CapabilityView{}
	_ sdkmodel.AliasView           = aliasresolution.Alias{}
)

const sdkEmailSchema = `id: email.send/v1
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
`

func TestBuildCanonicalNormalizesImmutableSDKOperations(t *testing.T) {
	t.Parallel()

	email := sdkTarget(t, sdkEmailSchema, generation.Exposure{HTTP: true, JavaScript: true})
	profile := sdkTarget(t, "id: account.profile.get/v2\n", generation.Exposure{Go: true, HTTP: true, JavaScript: true})
	model, err := sdkmodel.BuildCanonical([]sdkmodel.CanonicalTargetView{profile, email})
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	operations := model.Operations()
	if len(operations) != 2 || operations[0].ID().String() != "account.profile.get/v2" || operations[1].ID().String() != "email.send/v1" {
		t.Fatalf("Operations = %#v", operations)
	}
	operation := operations[1]
	if operation.ContractDigest() != email.digest {
		t.Fatalf("ContractDigest = %q, want %q", operation.ContractDigest(), email.digest)
	}
	request := operation.Request()
	if got := fieldNames(request); !slices.Equal(got, []string{"metadata", "priority", "retries", "tags", "to"}) {
		t.Fatalf("Request fields = %v", got)
	}
	if request[0].Kind() != sdkmodel.KindObject || request[0].Required() || request[0].Items() != "" {
		t.Fatalf("metadata field = %#v", request[0])
	}
	if request[3].Kind() != sdkmodel.KindArray || request[3].Items() != sdkmodel.KindString || !request[3].Required() {
		t.Fatalf("tags field = %#v", request[3])
	}
	if got := rawStrings(request[1].EnumJSON()); !slices.Equal(got, []string{`"normal"`, `"urgent"`}) {
		t.Fatalf("priority enum = %v", got)
	}
	if got := rawStrings(request[2].EnumJSON()); !slices.Equal(got, []string{"-1", "0", "2"}) {
		t.Fatalf("retries enum = %v", got)
	}
	if got := fieldNames(operation.Response()); !slices.Equal(got, []string{"accepted", "latency"}) {
		t.Fatalf("Response fields = %v", got)
	}
	if got := operation.Errors(); !slices.Equal(got, []string{"invalid_recipient", "temporarily_unavailable"}) {
		t.Fatalf("Errors = %v", got)
	}
	canonical := model.CanonicalJSON()
	for _, required := range []string{
		`{"operations":[{"id":"account.profile.get/v2"`,
		`"id":"email.send/v1"`,
		`"contract_digest":"` + email.digest + `"`,
		`"name":"priority","kind":"string","required":true,"enum":["normal","urgent"]`,
		`"name":"tags","kind":"array","items":"string","required":true`,
	} {
		if !bytes.Contains(canonical, []byte(required)) {
			t.Fatalf("CanonicalJSON %s omits %s", canonical, required)
		}
	}
	if model.Digest() != digest(canonical) {
		t.Fatalf("Digest = %q, want %q", model.Digest(), digest(canonical))
	}

	repeated, err := sdkmodel.BuildCanonical([]sdkmodel.CanonicalTargetView{email, profile})
	if err != nil || !bytes.Equal(repeated.CanonicalJSON(), canonical) || repeated.Digest() != model.Digest() {
		t.Fatalf("reordered BuildCanonical = %s, %q, %v", repeated.CanonicalJSON(), repeated.Digest(), err)
	}
	operations[0] = sdkmodel.Operation{}
	request[0] = sdkmodel.Field{}
	errorsCopy := operation.Errors()
	errorsCopy[0] = "changed"
	enumCopy := operation.Request()[1].EnumJSON()
	enumCopy[0][0] = 'x'
	canonical[0] = 'x'
	fresh := model.Operations()[1]
	if fresh.ID().String() != "email.send/v1" || fresh.Request()[0].Name() != "metadata" || fresh.Errors()[0] != "invalid_recipient" || string(fresh.Request()[1].EnumJSON()[0]) != `"normal"` || model.CanonicalJSON()[0] != '{' {
		t.Fatal("Model exposed mutable normalized storage")
	}
}

func TestBuildNormalizesImmutableJavaScriptAliases(t *testing.T) {
	t.Parallel()

	email := sdkTarget(t, sdkEmailSchema, generation.Exposure{Go: true, HTTP: true, JavaScript: true})
	profile := sdkTarget(t, "id: account.profile.get/v2\n", generation.Exposure{HTTP: true, JavaScript: true})
	aliases := []sdkmodel.AliasView{
		sdkAlias(t, "mail.deliver/v1", email, generation.Exposure{HTTP: true, JavaScript: true}, "Use email.send/v1 instead."),
		sdkAlias(t, "internal.send/v1", email, generation.Exposure{Go: true}, ""),
		sdkAlias(t, "compat.send/v1", email, generation.Exposure{Go: true, HTTP: true, JavaScript: true}, ""),
	}
	model, err := sdkmodel.Build([]sdkmodel.CanonicalTargetView{profile, email}, aliases)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := model.Aliases()
	if len(got) != 2 || got[0].ID().String() != "compat.send/v1" || got[1].ID().String() != "mail.deliver/v1" {
		t.Fatalf("Aliases = %#v", got)
	}
	for _, alias := range got {
		if alias.Target() != email.id || alias.TargetContractDigest() != email.digest || alias.TargetOperation().ID() != email.id {
			t.Fatalf("Alias %s target = %s, %s, %s", alias.ID(), alias.Target(), alias.TargetContractDigest(), alias.TargetOperation().ID())
		}
		if !slices.Equal(fieldNames(alias.TargetOperation().Request()), []string{"metadata", "priority", "retries", "tags", "to"}) || !slices.Equal(alias.TargetOperation().Errors(), []string{"invalid_recipient", "temporarily_unavailable"}) {
			t.Fatalf("Alias %s did not reuse canonical target contract", alias.ID())
		}
	}
	if got[0].Deprecated() != "" || got[1].Deprecated() != "Use email.send/v1 instead." || !got[0].Exposure().Go {
		t.Fatalf("Alias metadata = %#v", got)
	}
	canonical := model.CanonicalJSON()
	for _, required := range []string{
		`"aliases":[{"id":"compat.send/v1","target":"email.send/v1"`,
		`"target_contract_digest":"` + email.digest + `"`,
		`"exposure":{"go":true,"http":true,"javascript":true}`,
		`"id":"mail.deliver/v1"`,
		`"deprecated":"Use email.send/v1 instead."`,
	} {
		if !bytes.Contains(canonical, []byte(required)) {
			t.Fatalf("CanonicalJSON %s omits %s", canonical, required)
		}
	}
	if bytes.Contains(canonical, []byte("internal.send/v1")) || model.Digest() != digest(canonical) {
		t.Fatalf("CanonicalJSON = %s, digest %q", canonical, model.Digest())
	}
	repeated, err := sdkmodel.Build(
		[]sdkmodel.CanonicalTargetView{email, profile},
		[]sdkmodel.AliasView{aliases[2], aliases[0], aliases[1]},
	)
	if err != nil || !bytes.Equal(repeated.CanonicalJSON(), canonical) || repeated.Digest() != model.Digest() {
		t.Fatalf("reordered Build = %s, %q, %v", repeated.CanonicalJSON(), repeated.Digest(), err)
	}
	got[0] = sdkmodel.Alias{}
	targetRequest := got[1].TargetOperation().Request()
	targetRequest[0] = sdkmodel.Field{}
	canonical[0] = 'x'
	fresh := model.Aliases()[0]
	if fresh.ID().String() != "compat.send/v1" || fresh.TargetOperation().Request()[0].Name() != "metadata" || model.CanonicalJSON()[0] != '{' {
		t.Fatal("Model exposed mutable Alias storage")
	}
}

func TestBuildRejectsInvalidJavaScriptAliases(t *testing.T) {
	t.Parallel()

	target := sdkTarget(t, sdkEmailSchema, generation.Exposure{HTTP: true, JavaScript: true})
	valid := sdkAlias(t, "mail.deliver/v1", target, generation.Exposure{HTTP: true, JavaScript: true}, "")
	tests := []struct {
		name    string
		aliases []sdkmodel.AliasView
		want    string
	}{
		{name: "absent", aliases: []sdkmodel.AliasView{nil}, want: "view is absent"},
		{name: "invalid ID", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.id = generation.CapabilityID{} })}, want: "ID"},
		{name: "invalid target", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.target = generation.CapabilityID{} })}, want: "target ID"},
		{name: "missing target", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.target = sdkCapabilityID(t, "email.queue/v1") })}, want: "not a JavaScript-exposed canonical operation"},
		{name: "canonical collision", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.id = target.id })}, want: "collides with a canonical Capability"},
		{name: "reserved", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.id = sdkCapabilityID(t, "kernel.compat/v1") })}, want: "reserved kernel.*"},
		{name: "version mismatch", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.id = sdkCapabilityID(t, "mail.deliver/v2") })}, want: "same version"},
		{name: "missing HTTP transport", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.exposure.HTTP = false })}, want: "without its required HTTP transport"},
		{name: "digest mismatch", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.digest = "sha256:" + strings.Repeat("0", 64) })}, want: "target digest"},
		{name: "exposure broadening", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.exposure.Go = true })}, want: "exposure broadens"},
		{name: "oversized deprecation", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.deprecated = strings.Repeat("x", 1025) })}, want: "deprecation metadata"},
		{name: "NUL deprecation", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.deprecated = "unsafe\x00message" })}, want: "deprecation metadata"},
		{name: "invalid UTF-8 deprecation", aliases: []sdkmodel.AliasView{withSDKAlias(valid, func(value *sdkAliasView) { value.deprecated = string([]byte{0xff}) })}, want: "deprecation metadata"},
		{name: "duplicate", aliases: []sdkmodel.AliasView{valid, valid}, want: "duplicates Capability Alias"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, err := sdkmodel.Build([]sdkmodel.CanonicalTargetView{target}, test.aliases)
			if !errors.Is(err, sdkmodel.ErrNormalize) || !errors.Is(err, sdkmodel.ErrAlias) || !strings.Contains(err.Error(), test.want) || model.CanonicalJSON() != nil || model.Digest() != "" || model.Operations() != nil || model.Aliases() != nil {
				t.Fatalf("Build = %#v, %v; want %q", model, err, test.want)
			}
		})
	}

	hidden := withSDKAlias(valid, func(value *sdkAliasView) {
		value.target = sdkCapabilityID(t, "not.exposed/v1")
		value.exposure = generation.Exposure{Go: true}
	})
	model, err := sdkmodel.Build([]sdkmodel.CanonicalTargetView{target}, []sdkmodel.AliasView{hidden})
	if err != nil || len(model.Aliases()) != 0 {
		t.Fatalf("Build hidden Alias = %#v, %v", model.Aliases(), err)
	}
}

func TestBuildCanonicalSupportsEmptyApplicationSDK(t *testing.T) {
	t.Parallel()

	model, err := sdkmodel.BuildCanonical(nil)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	if len(model.Operations()) != 0 || string(model.CanonicalJSON()) != `{"operations":[]}` || model.Digest() != digest(model.CanonicalJSON()) {
		t.Fatalf("empty Model = %s, %q, %#v", model.CanonicalJSON(), model.Digest(), model.Operations())
	}
}

func TestBuildCanonicalRejectsInvalidSDKTargets(t *testing.T) {
	t.Parallel()

	valid := sdkTarget(t, sdkEmailSchema, generation.Exposure{HTTP: true, JavaScript: true})
	otherID, err := generation.ParseCapabilityID("mail.send/v1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		targets []sdkmodel.CanonicalTargetView
		want    string
	}{
		{name: "absent", targets: []sdkmodel.CanonicalTargetView{nil}, want: "view is absent"},
		{name: "zero", targets: []sdkmodel.CanonicalTargetView{sdkTargetView{}}, want: "ID"},
		{name: "not JavaScript exposed", targets: []sdkmodel.CanonicalTargetView{withSDKTarget(valid, func(value *sdkTargetView) { value.exposure.JavaScript = false })}, want: "not exposed to JavaScript"},
		{name: "missing HTTP transport", targets: []sdkmodel.CanonicalTargetView{withSDKTarget(valid, func(value *sdkTargetView) { value.exposure.HTTP = false })}, want: "without its required HTTP transport"},
		{name: "invalid contract", targets: []sdkmodel.CanonicalTargetView{withSDKTarget(valid, func(value *sdkTargetView) { value.contract = []byte("invalid") })}, want: "contract is invalid"},
		{name: "noncanonical contract", targets: []sdkmodel.CanonicalTargetView{withSDKTarget(valid, func(value *sdkTargetView) { value.contract = append([]byte(" "), value.contract...) })}, want: "not in canonical encoding"},
		{name: "identity mismatch", targets: []sdkmodel.CanonicalTargetView{withSDKTarget(valid, func(value *sdkTargetView) { value.id = otherID })}, want: "does not match contract identity"},
		{name: "digest mismatch", targets: []sdkmodel.CanonicalTargetView{withSDKTarget(valid, func(value *sdkTargetView) { value.digest = "sha256:" + strings.Repeat("0", 64) })}, want: "contract digest"},
		{name: "duplicate", targets: []sdkmodel.CanonicalTargetView{valid, valid}, want: "duplicates canonical Capability"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, err := sdkmodel.BuildCanonical(test.targets)
			if !errors.Is(err, sdkmodel.ErrNormalize) || !errors.Is(err, sdkmodel.ErrTarget) || !strings.Contains(err.Error(), test.want) || model.CanonicalJSON() != nil || model.Digest() != "" || model.Operations() != nil {
				t.Fatalf("BuildCanonical = %#v, %v; want %q", model, err, test.want)
			}
		})
	}

	tooMany := make([]sdkmodel.CanonicalTargetView, 4097)
	if _, err := sdkmodel.BuildCanonical(tooMany); !errors.Is(err, sdkmodel.ErrNormalize) || !errors.Is(err, sdkmodel.ErrTarget) || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("BuildCanonical oversized error = %v", err)
	}
}

func FuzzBuildCanonical(f *testing.F) {
	for _, seed := range []string{
		sdkEmailSchema,
		"id: kernel.health/v1\nresponse:\n  healthy: {type: boolean, required: true}\n",
		"id: authn.login.oidc.complete/v12\nrequest:\n  code: {type: string, required: true}\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, schema string) {
		if len(schema) > 1<<20 {
			return
		}
		canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
		if err != nil {
			return
		}
		metadata, err := capabilitymeta.Parse(canonical)
		if err != nil {
			t.Fatalf("Parse normalized schema: %v", err)
		}
		id, err := generation.ParseCapabilityID(metadata.ID().String())
		if err != nil {
			t.Fatalf("ParseCapabilityID: %v", err)
		}
		target := sdkTargetView{id: id, contract: canonical, digest: digest(canonical), exposure: generation.Exposure{HTTP: true, JavaScript: true}}
		first, err := sdkmodel.BuildCanonical([]sdkmodel.CanonicalTargetView{target})
		if err != nil {
			t.Fatalf("BuildCanonical: %v", err)
		}
		second, err := sdkmodel.BuildCanonical([]sdkmodel.CanonicalTargetView{target})
		if err != nil || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
			t.Fatalf("BuildCanonical is nondeterministic: %s then %s, %v", first.CanonicalJSON(), second.CanonicalJSON(), err)
		}
	})
}

func FuzzBuildJavaScriptAliasDeprecation(f *testing.F) {
	for _, seed := range []string{
		"",
		"Use email.send/v1 instead.",
		"First line.\nSecond line.",
		"close */ comment",
		"invalid\x00message",
		string([]byte{0xff}),
	} {
		f.Add(seed)
	}
	target := sdkTarget(f, sdkEmailSchema, generation.Exposure{HTTP: true, JavaScript: true})
	f.Fuzz(func(t *testing.T, deprecated string) {
		if len(deprecated) > 2048 {
			return
		}
		alias := sdkAlias(t, "mail.deliver/v1", target, generation.Exposure{HTTP: true, JavaScript: true}, deprecated)
		first, err := sdkmodel.Build([]sdkmodel.CanonicalTargetView{target}, []sdkmodel.AliasView{alias})
		invalid := len(deprecated) > 1024 || !utf8.ValidString(deprecated) || strings.ContainsRune(deprecated, '\x00')
		if invalid {
			if !errors.Is(err, sdkmodel.ErrNormalize) || !errors.Is(err, sdkmodel.ErrAlias) {
				t.Fatalf("Build invalid deprecation error = %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		second, err := sdkmodel.Build([]sdkmodel.CanonicalTargetView{target}, []sdkmodel.AliasView{alias})
		if err != nil || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() || len(first.Aliases()) != 1 || first.Aliases()[0].Deprecated() != deprecated {
			t.Fatalf("Build Alias is not deterministic: %s then %s, %v", first.CanonicalJSON(), second.CanonicalJSON(), err)
		}
	})
}

type sdkTargetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
	exposure generation.Exposure
}

type sdkAliasView struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	digest     string
	exposure   generation.Exposure
	deprecated string
}

func (v sdkAliasView) ID() generation.CapabilityID     { return v.id }
func (v sdkAliasView) Target() generation.CapabilityID { return v.target }
func (v sdkAliasView) TargetContractDigest() string    { return v.digest }
func (v sdkAliasView) Exposure() generation.Exposure   { return v.exposure }
func (v sdkAliasView) Deprecated() string              { return v.deprecated }

func sdkAlias(t testing.TB, id string, target sdkTargetView, exposure generation.Exposure, deprecated string) sdkAliasView {
	t.Helper()
	return sdkAliasView{
		id:         sdkCapabilityID(t, id),
		target:     target.id,
		digest:     target.digest,
		exposure:   exposure,
		deprecated: deprecated,
	}
}

func withSDKAlias(value sdkAliasView, edit func(*sdkAliasView)) sdkAliasView {
	edit(&value)
	return value
}

func sdkCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}

func (v sdkTargetView) ID() generation.CapabilityID   { return v.id }
func (v sdkTargetView) ContractJSON() []byte          { return append([]byte(nil), v.contract...) }
func (v sdkTargetView) ContractDigest() string        { return v.digest }
func (v sdkTargetView) Exposure() generation.Exposure { return v.exposure }

func sdkTarget(t testing.TB, schema string, exposure generation.Exposure) sdkTargetView {
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
	return sdkTargetView{id: id, contract: canonical, digest: digest(canonical), exposure: exposure}
}

func withSDKTarget(value sdkTargetView, edit func(*sdkTargetView)) sdkTargetView {
	edit(&value)
	return value
}

func fieldNames(values []sdkmodel.Field) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Name()
	}
	return result
}

func rawStrings(values []json.RawMessage) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
