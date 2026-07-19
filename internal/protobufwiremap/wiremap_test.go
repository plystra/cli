package protobufwiremap

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/protobufmodel"
)

func TestBuildAllocatesAndPreservesCanonicalFieldHistory(t *testing.T) {
	t.Parallel()

	initialModel := wireModel(t, true, wireTarget(t, `id: customer.enroll/v1
request:
  beta: {type: string}
  alpha: {type: string}
response:
  result: {type: boolean}
errors: []
`))
	initial, err := Build(initialModel, nil, false, "")
	if err != nil || !initial.Valid() {
		t.Fatalf("Build(initial) = %#v, %v", initial, err)
	}
	initialDocument := decodeTestDocument(t, initial.CanonicalJSON())
	entry := initialDocument.Capabilities["customer.enroll/v1"]
	if !entry.Active || entry.Request.Fields["alpha"].Number != 1 || entry.Request.Fields["beta"].Number != 2 || entry.Response.Fields["result"].Number != 1 {
		t.Fatalf("initial assignments = %#v", entry)
	}
	if !slices.Equal(entry.Provenance, []string{"example.com/contracts@v1/customer.enroll/v1/capability.yaml"}) {
		t.Fatalf("initial provenance = %v", entry.Provenance)
	}

	reordered := wireModel(t, true, wireTarget(t, `errors: []
response: {result: {type: boolean}}
request:
  gamma: {type: integer}
  alpha: {type: string}
  beta: {type: string}
id: customer.enroll/v1
`))
	added, err := Build(reordered, initial.CanonicalJSON(), true, initial.Digest())
	if err != nil {
		t.Fatalf("Build(added): %v", err)
	}
	addedEntry := decodeTestDocument(t, added.CanonicalJSON()).Capabilities["customer.enroll/v1"]
	if addedEntry.Request.Fields["alpha"].Number != 1 || addedEntry.Request.Fields["beta"].Number != 2 || addedEntry.Request.Fields["gamma"].Number != 3 {
		t.Fatalf("added assignments = %#v", addedEntry.Request.Fields)
	}

	removedModel := wireModel(t, true, wireTarget(t, `id: customer.enroll/v1
request:
  delta: {type: boolean}
  gamma: {type: integer}
  alpha: {type: string}
response: {result: {type: boolean}}
errors: []
`))
	removed, err := Build(removedModel, added.CanonicalJSON(), true, added.Digest())
	if err != nil {
		t.Fatalf("Build(removed): %v", err)
	}
	removedEntry := decodeTestDocument(t, removed.CanonicalJSON()).Capabilities["customer.enroll/v1"]
	if removedEntry.Request.Fields["alpha"].Number != 1 || removedEntry.Request.Fields["gamma"].Number != 3 || removedEntry.Request.Fields["delta"].Number != 4 {
		t.Fatalf("post-removal assignments = %#v", removedEntry.Request.Fields)
	}
	if !slices.Equal(removedEntry.Request.ReservedNumbers, []int{2}) || !slices.Equal(removedEntry.Request.ReservedNames, []string{"beta"}) {
		t.Fatalf("reservations = numbers %v names %v", removedEntry.Request.ReservedNumbers, removedEntry.Request.ReservedNames)
	}

	disabledModel := wireModel(t, false, wireTarget(t, `id: ignored.invalid/v1`))
	disabled, err := Build(disabledModel, removed.CanonicalJSON(), true, removed.Digest())
	if err != nil {
		t.Fatalf("Build(disabled): %v", err)
	}
	inactive := decodeTestDocument(t, disabled.CanonicalJSON()).Capabilities["customer.enroll/v1"]
	if inactive.Active || inactive.Request.Fields["delta"].Number != 4 || !slices.Equal(inactive.Request.ReservedNumbers, []int{2}) {
		t.Fatalf("inactive history = %#v", inactive)
	}
	var active activeDocument
	if err := json.Unmarshal(disabled.ActiveJSON(), &active); err != nil || len(active.Capabilities) != 0 {
		t.Fatalf("disabled ActiveJSON = %s, %v", disabled.ActiveJSON(), err)
	}

	reusedName := wireModel(t, true, wireTarget(t, `id: customer.enroll/v1
request:
  beta: {type: number}
  delta: {type: boolean}
  gamma: {type: integer}
  alpha: {type: string}
response: {result: {type: boolean}}
errors: []
`))
	if result, err := Build(reusedName, disabled.CanonicalJSON(), true, disabled.Digest()); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "permanently reserved") {
		t.Fatalf("Build(reused name) = %#v, %v", result, err)
	}
}

func TestBuildIsDeterministicDefensiveAndAllocatesNoAliasLedger(t *testing.T) {
	t.Parallel()
	target := wireTarget(t, `id: customer.profile.get/v1
request: {profile_id: {type: string}}
response: {found: {type: boolean}}
errors: []
`)
	alias := wireAlias(t, "account.profile/v1", target)
	model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, []protobufmodel.AliasView{alias})
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	first, err := Build(model, nil, false, "")
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := Build(model, nil, false, "")
	if err != nil || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() || first.ActiveDigest() != second.ActiveDigest() {
		t.Fatalf("Build(second) = %s, %v", second.CanonicalJSON(), err)
	}
	document := decodeTestDocument(t, first.CanonicalJSON())
	if len(document.Capabilities) != 1 {
		t.Fatalf("Capabilities = %#v", document.Capabilities)
	}
	if _, allocated := document.Capabilities["account.profile/v1"]; allocated {
		t.Fatal("Alias allocated a field-number ledger")
	}
	canonical := first.CanonicalJSON()
	active := first.ActiveJSON()
	canonical[0] = 'x'
	active[0] = 'x'
	if first.CanonicalJSON()[0] != '{' || first.ActiveJSON()[0] != '{' || first.ProjectionDigest() != model.Digest() {
		t.Fatal("Map exposed mutable or inconsistent state")
	}
}

func TestBuildRejectsMissingCorruptAndInconsistentHistory(t *testing.T) {
	t.Parallel()
	model := wireModel(t, true, wireTarget(t, `id: email.send/v1
request: {to: {type: string}}
response: {}
errors: []
`))
	valid, err := Build(model, nil, false, "")
	if err != nil {
		t.Fatalf("Build(valid): %v", err)
	}
	for _, test := range []struct {
		name     string
		data     []byte
		exists   bool
		digest   string
		contains string
	}{
		{name: "owned without baseline", data: valid.CanonicalJSON(), exists: true, contains: "no valid generated-manifest baseline"},
		{name: "missing owned history", exists: false, digest: valid.Digest(), contains: "is missing"},
		{name: "modified history", data: append(valid.CanonicalJSON(), ' '), exists: true, digest: valid.Digest(), contains: "does not match"},
		{name: "corrupt canonical history", data: []byte("{}\n"), exists: true, digest: digest([]byte("{}\n")), contains: "projection_schema"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := Build(model, test.data, test.exists, test.digest)
			if !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Build = %#v, %v", result, err)
			}
		})
	}

	inconsistent := decodeTestDocument(t, valid.CanonicalJSON())
	record := inconsistent.Capabilities["email.send/v1"]
	record.Request.Message = "WrongRequest"
	inconsistent.Capabilities["email.send/v1"] = record
	data, err := encode(inconsistent, true)
	if err != nil {
		t.Fatalf("encode inconsistent: %v", err)
	}
	if result, err := Build(model, data, true, digest(data)); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "message identities") {
		t.Fatalf("Build(inconsistent) = %#v, %v", result, err)
	}
}

func TestNextFieldNumberSkipsProtobufReservedRange(t *testing.T) {
	t.Parallel()
	used := make(map[int]struct{}, reservedRangeStart-minimumFieldNumber)
	for number := minimumFieldNumber; number < reservedRangeStart; number++ {
		used[number] = struct{}{}
	}
	if number, err := nextFieldNumber(used); err != nil || number != reservedRangeEnd+1 {
		t.Fatalf("nextFieldNumber = %d, %v", number, err)
	}
}

type wireTargetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
	sources  []string
	exposure generation.Exposure
}

func (v wireTargetView) ID() generation.CapabilityID   { return v.id }
func (v wireTargetView) ContractJSON() []byte          { return append([]byte(nil), v.contract...) }
func (v wireTargetView) ContractDigest() string        { return v.digest }
func (v wireTargetView) Sources() []string             { return append([]string(nil), v.sources...) }
func (v wireTargetView) Exposure() generation.Exposure { return v.exposure }

type wireAliasView struct {
	id       generation.CapabilityID
	target   generation.CapabilityID
	digest   string
	exposure generation.Exposure
}

func (v wireAliasView) ID() generation.CapabilityID     { return v.id }
func (v wireAliasView) Target() generation.CapabilityID { return v.target }
func (v wireAliasView) TargetContractDigest() string    { return v.digest }
func (v wireAliasView) Exposure() generation.Exposure   { return v.exposure }
func (wireAliasView) Deprecated() string                { return "" }

func wireTarget(t testing.TB, source string) wireTargetView {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(canonical, &identity); err != nil {
		t.Fatalf("decode identity: %v", err)
	}
	id, err := generation.ParseCapabilityID(identity.ID)
	if err != nil {
		t.Fatalf("ParseCapabilityID: %v", err)
	}
	return wireTargetView{
		id:       id,
		contract: canonical,
		digest:   digest(canonical),
		sources:  []string{"example.com/contracts@v1/" + identity.ID + "/capability.yaml"},
		exposure: generation.Exposure{Go: true, HTTP: true},
	}
}

func wireAlias(t testing.TB, id string, target wireTargetView) wireAliasView {
	t.Helper()
	parsed, err := generation.ParseCapabilityID(id)
	if err != nil {
		t.Fatalf("ParseCapabilityID(Alias): %v", err)
	}
	return wireAliasView{id: parsed, target: target.id, digest: target.digest, exposure: generation.Exposure{HTTP: true}}
}

func wireModel(t testing.TB, enabled bool, targets ...wireTargetView) protobufmodel.Model {
	t.Helper()
	views := make([]protobufmodel.CanonicalTargetView, len(targets))
	for index := range targets {
		views[index] = targets[index]
	}
	model, err := protobufmodel.Build(enabled, views, nil)
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	return model
}

func decodeTestDocument(t testing.TB, data []byte) document {
	t.Helper()
	var result document
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode wire map: %v", err)
	}
	return result
}
