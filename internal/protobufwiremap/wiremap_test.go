package protobufwiremap

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	initial, err := Build(initialModel, emptyInterfaceProjection(t, initialModel), nil, false, "")
	if err != nil || !initial.Valid() {
		t.Fatalf("Build(initial) = %#v, %v", initial, err)
	}
	initialDocument := decodeTestDocument(t, initial.CanonicalJSON())
	entry := initialDocument.LegacyCapabilities["customer.enroll/v1"]
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
	added, err := Build(reordered, emptyInterfaceProjection(t, reordered), initial.CanonicalJSON(), true, initial.Digest())
	if err != nil {
		t.Fatalf("Build(added): %v", err)
	}
	addedEntry := decodeTestDocument(t, added.CanonicalJSON()).LegacyCapabilities["customer.enroll/v1"]
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
	removed, err := Build(removedModel, emptyInterfaceProjection(t, removedModel), added.CanonicalJSON(), true, added.Digest())
	if err != nil {
		t.Fatalf("Build(removed): %v", err)
	}
	removedEntry := decodeTestDocument(t, removed.CanonicalJSON()).LegacyCapabilities["customer.enroll/v1"]
	if removedEntry.Request.Fields["alpha"].Number != 1 || removedEntry.Request.Fields["gamma"].Number != 3 || removedEntry.Request.Fields["delta"].Number != 4 {
		t.Fatalf("post-removal assignments = %#v", removedEntry.Request.Fields)
	}
	if !slices.Equal(removedEntry.Request.ReservedNumbers, []int{2}) || !slices.Equal(removedEntry.Request.ReservedNames, []string{"beta"}) {
		t.Fatalf("reservations = numbers %v names %v", removedEntry.Request.ReservedNumbers, removedEntry.Request.ReservedNames)
	}

	disabledModel := wireModel(t, false, wireTarget(t, `id: ignored.invalid/v1`))
	disabled, err := Build(disabledModel, emptyInterfaceProjection(t, disabledModel), removed.CanonicalJSON(), true, removed.Digest())
	if err != nil {
		t.Fatalf("Build(disabled): %v", err)
	}
	inactive := decodeTestDocument(t, disabled.CanonicalJSON()).LegacyCapabilities["customer.enroll/v1"]
	if inactive.Active || inactive.Request.Fields["delta"].Number != 4 || !slices.Equal(inactive.Request.ReservedNumbers, []int{2}) {
		t.Fatalf("inactive history = %#v", inactive)
	}
	var active activeDocument
	if err := json.Unmarshal(disabled.ActiveJSON(), &active); err != nil || len(active.LegacyCapabilities) != 0 {
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
	if result, err := Build(reusedName, emptyInterfaceProjection(t, reusedName), disabled.CanonicalJSON(), true, disabled.Digest()); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "permanently reserved") {
		t.Fatalf("Build(reused name) = %#v, %v", result, err)
	}
}

func TestActiveCapabilitiesExposeTypedDefensiveProjection(t *testing.T) {
	t.Parallel()

	model := wireModel(t, true, wireTarget(t, `id: delivery.route/v1
request:
  mode: {type: string, enum: [fast, slow]}
  route_id: {type: string}
response:
  accepted: {type: boolean}
`))
	wireMap, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	active := wireMap.ActiveCapabilities()
	if len(active) != 1 || active[0].ID() != "delivery.route/v1" || active[0].ContractDigest() == "" {
		t.Fatalf("ActiveCapabilities = %#v", active)
	}
	request := active[0].Request()
	if request.Name() != "DeliveryRouteV1Request" || len(request.Fields()) != 2 || len(request.Enums()) != 1 {
		t.Fatalf("Request = %#v", request)
	}
	fields := request.Fields()
	enums := request.Enums()
	members := enums[0].Members()
	canonical := members[0].CanonicalJSON()
	reservedNumbers := request.ReservedNumbers()
	active[0] = CapabilityProjection{}
	fields[0] = FieldProjection{}
	enums[0] = EnumProjection{}
	members[0] = EnumValueProjection{}
	if len(canonical) != 0 {
		canonical[0] = 'x'
	}
	reservedNumbers = append(reservedNumbers, 99)
	if len(reservedNumbers) != 1 {
		t.Fatalf("mutated reservation copy = %v", reservedNumbers)
	}
	fresh := wireMap.ActiveCapabilities()[0]
	if fresh.ID() != "delivery.route/v1" || fresh.Request().Fields()[0].CanonicalName() != "mode" || fresh.Request().Enums()[0].Members()[0].Name() == "" || len(fresh.Request().ReservedNumbers()) != 0 {
		t.Fatal("ActiveCapabilities exposed mutable projection storage")
	}
}

func TestBuildAllocatesAndPreservesCanonicalEnumHistory(t *testing.T) {
	t.Parallel()

	initialModel := wireModel(t, true, wireTarget(t, `id: delivery.route/v1
request:
  mode: {type: string, enum: [slow, fast]}
response: {}
errors: []
`))
	initial, err := Build(initialModel, emptyInterfaceProjection(t, initialModel), nil, false, "")
	if err != nil {
		t.Fatalf("Build(initial): %v", err)
	}
	initialAssignment := decodeTestDocument(t, initial.CanonicalJSON()).LegacyCapabilities["delivery.route/v1"].Request.Enums["mode"]
	const identity = "plystra.generated.delivery.route.v1.DeliveryRouteV1RequestModeEnum"
	const sentinel = "DELIVERYROUTEV1REQUESTMODEENUM_UNSPECIFIED"
	if !initialAssignment.Active || initialAssignment.Identity != identity || initialAssignment.Kind != "string" || initialAssignment.Sentinel != (enumSymbol{Name: sentinel, Number: 0}) {
		t.Fatalf("initial enum identity = %#v", initialAssignment)
	}
	if got := enumNumbers(initialAssignment); !equalStringIntMap(got, map[string]int{`"fast"`: 1, `"slow"`: 2}) {
		t.Fatalf("initial enum numbers = %v", got)
	}
	for _, member := range initialAssignment.Members {
		sum := sha256.Sum256(member.Canonical)
		want := "DELIVERYROUTEV1REQUESTMODEENUM_VALUE_" + fmt.Sprintf("%X", sum)
		if member.Name != want || member.Number <= 0 {
			t.Fatalf("member assignment = %#v; want name %s and positive number", member, want)
		}
	}
	kindChanged := wireModel(t, true, wireTarget(t, `id: delivery.route/v1
request:
  mode: {type: integer, enum: [1, 2]}
response: {}
errors: []
`))
	if result, err := Build(kindChanged, emptyInterfaceProjection(t, kindChanged), initial.CanonicalJSON(), true, initial.Digest()); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "enum kind changed") {
		t.Fatalf("Build(changed enum kind) = %#v, %v", result, err)
	}

	addedModel := wireModel(t, true, wireTarget(t, `id: delivery.route/v1
request:
  mode: {type: string, enum: [express, fast, slow]}
response: {}
errors: []
`))
	added, err := Build(addedModel, emptyInterfaceProjection(t, addedModel), initial.CanonicalJSON(), true, initial.Digest())
	if err != nil {
		t.Fatalf("Build(added): %v", err)
	}
	addedAssignment := decodeTestDocument(t, added.CanonicalJSON()).LegacyCapabilities["delivery.route/v1"].Request.Enums["mode"]
	if got := enumNumbers(addedAssignment); !equalStringIntMap(got, map[string]int{`"express"`: 3, `"fast"`: 1, `"slow"`: 2}) {
		t.Fatalf("added enum numbers = %v", got)
	}

	removedModel := wireModel(t, true, wireTarget(t, `id: delivery.route/v1
request:
  mode: {type: string, enum: [later, slow, express]}
response: {}
errors: []
`))
	removed, err := Build(removedModel, emptyInterfaceProjection(t, removedModel), added.CanonicalJSON(), true, added.Digest())
	if err != nil {
		t.Fatalf("Build(removed): %v", err)
	}
	removedAssignment := decodeTestDocument(t, removed.CanonicalJSON()).LegacyCapabilities["delivery.route/v1"].Request.Enums["mode"]
	if got := enumNumbers(removedAssignment); !equalStringIntMap(got, map[string]int{`"express"`: 3, `"later"`: 4, `"slow"`: 2}) {
		t.Fatalf("post-removal enum numbers = %v", got)
	}
	fastName := enumMemberName(identity, json.RawMessage(`"fast"`))
	if !slices.Equal(removedAssignment.ReservedNumbers, []int{1}) || !slices.Equal(removedAssignment.ReservedNames, []string{fastName}) {
		t.Fatalf("enum reservations = numbers %v names %v", removedAssignment.ReservedNumbers, removedAssignment.ReservedNames)
	}

	readdedModel := wireModel(t, true, wireTarget(t, `id: delivery.route/v1
request:
  mode: {type: string, enum: [fast, later, slow, express]}
response: {}
errors: []
`))
	if result, err := Build(readdedModel, emptyInterfaceProjection(t, readdedModel), removed.CanonicalJSON(), true, removed.Digest()); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "permanently occupied generated name") {
		t.Fatalf("Build(re-added member) = %#v, %v", result, err)
	}
}

func TestBuildSupportsEveryCanonicalScalarEnumKind(t *testing.T) {
	t.Parallel()

	model := wireModel(t, true, wireTarget(t, `id: scalar.enum/v1
request:
  text: {type: string, enum: [alpha, beta]}
  count: {type: integer, enum: [-1, 0, 9223372036854775807]}
  ratio: {type: number, enum: [-2.5, 1, 1.25]}
  enabled: {type: boolean, enum: [false, true]}
response: {}
errors: []
`))
	result, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	enums := decodeTestDocument(t, result.CanonicalJSON()).LegacyCapabilities["scalar.enum/v1"].Request.Enums
	for field, kind := range map[string]string{"text": "string", "count": "integer", "ratio": "number", "enabled": "boolean"} {
		assignment := enums[field]
		if !assignment.Active || string(assignment.Kind) != kind || assignment.Sentinel.Number != 0 || len(assignment.Members) < 2 {
			t.Fatalf("enum %s = %#v", field, assignment)
		}
		for _, member := range assignment.Members {
			if member.Number <= 0 {
				t.Fatalf("enum %s member = %#v", field, member)
			}
		}
	}
	if got := enumNumbers(enums["count"])["9223372036854775807"]; got == 0 {
		t.Fatalf("signed 64-bit integer enum was not retained: %#v", enums["count"])
	}
}

func TestBuildRetainsInactiveEnumHistoryOutsideActiveProjection(t *testing.T) {
	t.Parallel()

	withEnum := wireModel(t, true, wireTarget(t, `id: account.state/v1
request: {state: {type: string, enum: [active, disabled]}}
response: {}
errors: []
`))
	initial, err := Build(withEnum, emptyInterfaceProjection(t, withEnum), nil, false, "")
	if err != nil {
		t.Fatalf("Build(initial): %v", err)
	}
	initialAssignment := decodeTestDocument(t, initial.CanonicalJSON()).LegacyCapabilities["account.state/v1"].Request.Enums["state"]

	withoutEnum := wireModel(t, true, wireTarget(t, `id: account.state/v1
request: {state: {type: string}}
response: {}
errors: []
`))
	inactive, err := Build(withoutEnum, emptyInterfaceProjection(t, withoutEnum), initial.CanonicalJSON(), true, initial.Digest())
	if err != nil {
		t.Fatalf("Build(without enum): %v", err)
	}
	inactiveAssignment := decodeTestDocument(t, inactive.CanonicalJSON()).LegacyCapabilities["account.state/v1"].Request.Enums["state"]
	if inactiveAssignment.Active || !equalStringIntMap(enumNumbers(inactiveAssignment), enumNumbers(initialAssignment)) {
		t.Fatalf("inactive enum history = %#v", inactiveAssignment)
	}
	var active activeDocument
	if err := json.Unmarshal(inactive.ActiveJSON(), &active); err != nil {
		t.Fatalf("decode ActiveJSON: %v", err)
	}
	if enums := active.LegacyCapabilities["account.state/v1"].Request.Enums; len(enums) != 0 {
		t.Fatalf("active projection retained inactive enum history: %#v", enums)
	}

	reactivated, err := Build(withEnum, emptyInterfaceProjection(t, withEnum), inactive.CanonicalJSON(), true, inactive.Digest())
	if err != nil {
		t.Fatalf("Build(reactivated): %v", err)
	}
	reactivatedAssignment := decodeTestDocument(t, reactivated.CanonicalJSON()).LegacyCapabilities["account.state/v1"].Request.Enums["state"]
	if !reactivatedAssignment.Active || !equalStringIntMap(enumNumbers(reactivatedAssignment), enumNumbers(initialAssignment)) {
		t.Fatalf("reactivated enum history = %#v", reactivatedAssignment)
	}
}

func TestBuildIsDeterministicDefensiveAndAllocatesNoAliasLedger(t *testing.T) {
	t.Parallel()
	target := wireTarget(t, `id: customer.profile.get/v1
request:
  profile_id: {type: string}
  view: {type: string, enum: [compact, full]}
response: {found: {type: boolean}}
errors: []
`)
	alias := wireAlias(t, "account.profile/v1", target)
	model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, []protobufmodel.AliasView{alias})
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	first, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
	if err != nil || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() || first.ActiveDigest() != second.ActiveDigest() {
		t.Fatalf("Build(second) = %s, %v", second.CanonicalJSON(), err)
	}
	document := decodeTestDocument(t, first.CanonicalJSON())
	if len(document.LegacyCapabilities) != 1 {
		t.Fatalf("LegacyCapabilities = %#v", document.LegacyCapabilities)
	}
	if _, allocated := document.LegacyCapabilities["account.profile/v1"]; allocated {
		t.Fatal("Alias allocated a field-number ledger")
	}
	if enums := document.LegacyCapabilities["customer.profile.get/v1"].Request.Enums; len(enums) != 1 || len(enums["view"].Members) != 2 {
		t.Fatalf("canonical target enum ledger = %#v", enums)
	}
	canonical := first.CanonicalJSON()
	active := first.ActiveJSON()
	canonical[0] = 'x'
	active[0] = 'x'
	if first.CanonicalJSON()[0] != '{' ||
		first.ActiveJSON()[0] != '{' ||
		!first.Matches(model, emptyInterfaceProjection(t, model)) {
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
	valid, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
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
			result, err := Build(model, emptyInterfaceProjection(t, model), test.data, test.exists, test.digest)
			if !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Build = %#v, %v", result, err)
			}
		})
	}

	inconsistent := decodeTestDocument(t, valid.CanonicalJSON())
	record := inconsistent.LegacyCapabilities["email.send/v1"]
	record.Request.Message = "WrongRequest"
	inconsistent.LegacyCapabilities["email.send/v1"] = record
	data, err := encode(inconsistent, true)
	if err != nil {
		t.Fatalf("encode inconsistent: %v", err)
	}
	if result, err := Build(model, emptyInterfaceProjection(t, model), data, true, digest(data)); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "message identities") {
		t.Fatalf("Build(inconsistent) = %#v, %v", result, err)
	}
	oldSchema := bytes.Replace(valid.CanonicalJSON(), []byte(ProjectionSchema), []byte("plystra.proto-wire-map/v3"), 1)
	if result, err := Build(model, emptyInterfaceProjection(t, model), oldSchema, true, digest(oldSchema)); !errors.Is(err, ErrHistory) || result.Valid() || !strings.Contains(err.Error(), "projection_schema") {
		t.Fatalf("Build(old schema) = %#v, %v", result, err)
	}
}

func TestBuildRejectsCorruptEnumHistory(t *testing.T) {
	t.Parallel()

	model := wireModel(t, true, wireTarget(t, `id: email.send/v1
request: {priority: {type: string, enum: [normal, urgent]}}
response: {}
errors: []
`))
	valid, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
	if err != nil {
		t.Fatalf("Build(valid): %v", err)
	}
	tests := []struct {
		name     string
		mutate   func(*enumAssignment)
		contains string
	}{
		{name: "identity", mutate: func(value *enumAssignment) { value.Identity += "Wrong" }, contains: "identity"},
		{name: "kind", mutate: func(value *enumAssignment) { value.Kind = "object" }, contains: "supported canonical scalar"},
		{name: "sentinel name", mutate: func(value *enumAssignment) { value.Sentinel.Name = "WRONG_UNSPECIFIED" }, contains: "sentinel must be"},
		{name: "sentinel number", mutate: func(value *enumAssignment) { value.Sentinel.Number = 1 }, contains: "numeric value 0"},
		{name: "member canonical kind", mutate: func(value *enumAssignment) { value.Members[0].Canonical = json.RawMessage("1") }, contains: "must be a JSON string"},
		{name: "member generated name", mutate: func(value *enumAssignment) { value.Members[0].Name += "WRONG" }, contains: "generated name"},
		{name: "member zero number", mutate: func(value *enumAssignment) { value.Members[0].Number = 0 }, contains: "invalid positive number"},
		{name: "duplicate member number", mutate: func(value *enumAssignment) { value.Members[1].Number = value.Members[0].Number }, contains: "duplicate number"},
		{name: "reserved number collision", mutate: func(value *enumAssignment) { value.ReservedNumbers = []int{value.Members[0].Number} }, contains: "reserved enum number"},
		{name: "invalid reserved name", mutate: func(value *enumAssignment) { value.ReservedNames = []string{"WRONG"} }, contains: "reserved enum name"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := decodeTestDocument(t, valid.CanonicalJSON())
			record := document.LegacyCapabilities["email.send/v1"]
			assignment := record.Request.Enums["priority"]
			test.mutate(&assignment)
			record.Request.Enums["priority"] = assignment
			document.LegacyCapabilities["email.send/v1"] = record
			data, encodeErr := encode(document, true)
			if encodeErr != nil {
				t.Fatalf("encode corrupt history: %v", encodeErr)
			}
			result, buildErr := Build(model, emptyInterfaceProjection(t, model), data, true, digest(data))
			if !errors.Is(buildErr, ErrHistory) || result.Valid() || !strings.Contains(buildErr.Error(), test.contains) {
				t.Fatalf("Build(corrupt enum) = %#v, %v; want %q", result, buildErr, test.contains)
			}
		})
	}
}

func TestEnumHistoryCloningIsDefensive(t *testing.T) {
	t.Parallel()

	model := wireModel(t, true, wireTarget(t, `id: records.create/v1
request: {kind: {type: string, enum: [primary, secondary]}}
response: {}
errors: []
`))
	result, err := Build(model, emptyInterfaceProjection(t, model), nil, false, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	document := decodeTestDocument(t, result.CanonicalJSON())
	cloned := cloneDocument(document)
	record := cloned.LegacyCapabilities["records.create/v1"]
	assignment := record.Request.Enums["kind"]
	assignment.Members[0].Canonical[0] = 'x'
	assignment.Members[0].Name = "changed"
	assignment.ReservedNumbers = append(assignment.ReservedNumbers, 99)
	assignment.ReservedNames = append(assignment.ReservedNames, "changed")
	record.Request.Enums["kind"] = assignment
	cloned.LegacyCapabilities["records.create/v1"] = record

	original := document.LegacyCapabilities["records.create/v1"].Request.Enums["kind"]
	if original.Members[0].Canonical[0] != '"' || original.Members[0].Name == "changed" || len(original.ReservedNumbers) != 0 || len(original.ReservedNames) != 0 {
		t.Fatalf("clone mutated source enum history: %#v", original)
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

func TestNextEnumNumberUsesOnlyUnusedPositiveValues(t *testing.T) {
	t.Parallel()
	if number, err := nextEnumNumber(map[int]string{0: "sentinel", 1: "member", 2: "reserved"}); err != nil || number != 3 {
		t.Fatalf("nextEnumNumber = %d, %v", number, err)
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
	source = withWireQuerySemantics(source)
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

func withWireQuerySemantics(source string) string {
	if strings.Contains(source, "\nsemantics:") {
		return source
	}
	if !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return source + `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
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

func emptyInterfaceProjection(t testing.TB, legacy protobufmodel.Model) protobufmodel.InterfaceModel {
	t.Helper()
	model, err := protobufmodel.BuildInterfaces(legacy.Enabled(), nil)
	if err != nil {
		t.Fatalf("protobufmodel.BuildInterfaces: %v", err)
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

func enumNumbers(value enumAssignment) map[string]int {
	result := make(map[string]int, len(value.Members))
	for _, member := range value.Members {
		result[string(member.Canonical)] = member.Number
	}
	return result
}

func equalStringIntMap(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
